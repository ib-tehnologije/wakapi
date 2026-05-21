import {execFile} from "node:child_process";
import {createHash, randomUUID} from "node:crypto";
import {existsSync} from "node:fs";
import {mkdir, readFile, readdir, rename, rm, writeFile} from "node:fs/promises";
import {homedir} from "node:os";
import path from "node:path";
import {promisify} from "node:util";

const execFileAsync = promisify(execFile);
const fallbackSummaryMaxChars = 180;
const fillerSummaries = new Set(["yes", "yep", "ok", "okay", "done", "sure", "youreright", "youareright"]);
const evidenceFilePattern = /(?:^|[\s"'=:(])((?:\.{1,2}\/)?[A-Za-z0-9._@~+-][A-Za-z0-9._@~+/-]*\.(?:cs|go|mjs|cjs|js|jsx|ts|tsx|json|ya?ml|toml|sql|pas|dfm|dart|md|sh|bash|zsh|ps1|csproj|sln|props|targets|graphql|proto|rs|py|rb|php|java|kt|swift|css|scss|html|xml|txt|ini|conf|env|service))(?:[:#]\d+)?(?=$|[\s"'`,);])/gi;
const croatianSummaryPattern = /[čćđšž]|(?:^|[^\p{L}\p{N}])(?:rad|ažuriran|azuriran|pregledan|provjeren|dodan|dodana|dodano|dodane|popravljen|popravljena|popravljeno|uklonjen|uklonjena|obrisan|obrisani|istražen|istrazen|pokrenut|generiran|implementiran|integracij|validacij|provjerama|obradi|deployu|sinkronizacij|sesija|sažetak|sazetak|stanje|baze|podataka|resursi|repozitorij|repozitorija|migracij|tijek|skrivan|commitan|pushan)(?=$|[^\p{L}\p{N}])/iu;

export async function handleHook(payload, env = process.env, deps = {}) {
  const now = deps.now ?? (() => new Date());
  const resolveWorkspace = deps.resolveWorkspace ?? resolveGitWorkspace;
  const worklogHome = env.CODEX_WORKLOG_HOME || path.join(homedir(), ".codex", "worklog");
  const dirs = await ensureDirs(worklogHome);
  const eventName = payload.hook_event_name;

  if (env.CODEX_WORKLOG_SUMMARY_RUNNING === "1") {
    return {action: "ignored"};
  }

  if (eventName === "SessionStart") {
    await sweep(env, deps);
    return {action: "session_seen"};
  }

  if (eventName === "UserPromptSubmit") {
    const task = await createTask(payload, env, now(), resolveWorkspace);
    await saveTask(dirs, task);
    await flushQueue(dirs, env, deps);
    return {action: "started", task};
  }

  if (eventName === "PreToolUse" || eventName === "PostToolUse" || eventName === "PermissionRequest") {
    const task = await loadOrCreateTask(dirs, payload, env, now(), resolveWorkspace);
    recordToolEvent(task, payload, now());
    await saveTask(dirs, task);
    return {action: "recorded", task};
  }

  if (eventName === "Stop") {
    const task = await loadOrCreateTask(dirs, payload, env, now(), resolveWorkspace);
    closeTask(task, payload, now());
    await addHumanSummary(task, env, deps);
    await saveTask(dirs, task);

    const payloadBody = {sessions: [taskToSessionPayload(task)]};
    const submitted = await submitOrQueue(dirs, payloadBody, env, deps);
    if (submitted) {
      await removeTask(dirs, task);
      await flushQueue(dirs, env, deps);
      return {action: "submitted", task};
    }

    await removeTask(dirs, task);
    return {action: "queued", task};
  }

  return {action: "ignored"};
}

export async function sweep(env = process.env, deps = {}) {
  const now = deps.now ?? (() => new Date());
  const resolveWorkspace = deps.resolveWorkspace ?? resolveGitWorkspace;
  const worklogHome = env.CODEX_WORKLOG_HOME || path.join(homedir(), ".codex", "worklog");
  const dirs = await ensureDirs(worklogHome);
  const staleAfterMinutes = Number(env.CODEX_WORKLOG_STALE_MINUTES || 240);
  const staleBefore = now().getTime() - staleAfterMinutes * 60 * 1000;
  let closed = 0;

  for (const file of await safeReaddir(dirs.tasks)) {
    if (!file.endsWith(".json")) {
      continue;
    }
    const task = await readJsonOrQuarantine(dirs, path.join(dirs.tasks, file));
    if (!task) {
      continue;
    }
    const updatedAt = Date.parse(task.updated_at || task.started_at);
    if (!Number.isFinite(updatedAt) || updatedAt >= staleBefore) {
      continue;
    }
    closeTask(task, {last_assistant_message: task.last_assistant_message}, new Date(updatedAt));
    task.status = "stale";
    await submitOrQueue(dirs, {sessions: [taskToSessionPayload(task)]}, env, deps);
    await removeTask(dirs, task);
    closed += 1;
  }

  await flushQueue(dirs, env, {...deps, resolveWorkspace});
  return {closed};
}

export async function flushQueue(dirs, env = process.env, deps = {}) {
  if (!await getSubmitConfig(env)) {
    return {submitted: 0};
  }

  let submitted = 0;
  for (const file of await safeReaddir(dirs.queue)) {
    if (!file.endsWith(".json")) {
      continue;
    }

    const filePath = path.join(dirs.queue, file);
    const payload = await readJsonOrQuarantine(dirs, filePath);
    if (!payload) {
      continue;
    }
    if (await submitPayload(payload, env, deps)) {
      await rm(filePath, {force: true});
      submitted += 1;
    }
  }
  return {submitted};
}

async function createTask(payload, env, startedAt, resolveWorkspace) {
  const cwd = payload.cwd || process.cwd();
  const workspaceRoot = await resolveWorkspace(cwd);
  const installationId = await getInstallationId(env);
  const sessionId = String(payload.session_id || "no-session");
  const turnId = String(payload.turn_id || "no-turn");

  return {
    id: taskId(payload),
    external_key: `codex:${installationId}:${sessionId}:${turnId}`,
    session_id: sessionId,
    turn_id: turnId,
    project: projectName(workspaceRoot),
    workspace_root: workspaceRoot,
    repository: projectName(workspaceRoot),
    branch: await gitBranch(workspaceRoot),
    started_at: startedAt.toISOString(),
    updated_at: startedAt.toISOString(),
    status: "open",
    prompt: payload.prompt || "",
    last_assistant_message: "",
    evidence: [],
    events: [],
  };
}

async function loadOrCreateTask(dirs, payload, env, startedAt, resolveWorkspace) {
  const existing = await loadTask(dirs, taskId(payload));
  if (existing) {
    return existing;
  }
  return createTask(payload, env, startedAt, resolveWorkspace);
}

function recordToolEvent(task, payload, eventTime) {
  const event = {
    at: eventTime.toISOString(),
    hook_event_name: payload.hook_event_name,
    tool_name: payload.tool_name,
  };
  const command = payload.tool_input?.command || payload.tool_input?.cmd;
  if (typeof command === "string" && command.trim()) {
    event.command = command.trim().slice(0, 2000);
  }
  task.events.push(event);

  for (const item of extractEvidence(payload)) {
    if (!task.evidence.includes(item)) {
      task.evidence.push(item);
    }
  }
  task.updated_at = eventTime.toISOString();
}

function closeTask(task, payload, endedAt) {
  task.ended_at = endedAt.toISOString();
  task.updated_at = task.ended_at;
  task.status = task.status === "stale" ? "stale" : "closed";
  task.last_assistant_message = payload.last_assistant_message || task.last_assistant_message || "";
  const durationSeconds = Math.max(0, Math.round((Date.parse(task.ended_at) - Date.parse(task.started_at)) / 1000));
  task.duration_seconds = durationSeconds;
}

function taskToSessionPayload(task) {
  return {
    external_key: task.external_key,
    project: task.project,
    workspace_root: task.workspace_root,
    repository: task.repository,
    branch: task.branch,
    started_at: task.started_at,
    ended_at: task.ended_at,
    duration_seconds: task.duration_seconds || 0,
    status: task.status,
    summary_hr: task.summary_hr || fallbackSummary(task),
    prompt: task.prompt,
    last_assistant_message: task.last_assistant_message,
    evidence: task.evidence || [],
    technical_evidence: {
      events: task.events || [],
      session_id: task.session_id,
      turn_id: task.turn_id,
    },
  };
}

function fallbackSummary(task) {
  const evidenceSummary = evidenceFallbackSummary(task);
  if (evidenceSummary) {
    return evidenceSummary;
  }

  const assistantSummary = assistantFallbackSummary(task.last_assistant_message);
  if (assistantSummary) {
    return assistantSummary;
  }

  return "Codex sesija bez zabilježenog konteksta.";
}

function assistantFallbackSummary(value) {
  const raw = String(value || "").trim();
  if (!raw) {
    return "";
  }

  const jsonSummary = summaryFromJson(raw);
  if (jsonSummary) {
    return usefulSummary(ensureSentence(jsonSummary), fallbackSummaryMaxChars);
  }

  return "";
}

function evidenceFallbackSummary(task) {
  const changedFiles = [];
  const inspectedFiles = [];
  const commands = [];

  for (const item of task.evidence || []) {
    const evidence = String(item || "").trim();
    if (!evidence) {
      continue;
    }
    if (evidence.startsWith("command:")) {
      const command = evidence.slice("command:".length);
      commands.push(command);
      addUnique(inspectedFiles, extractFilesFromCommand(command));
      continue;
    }
    addUnique(changedFiles, [evidence]);
  }

  for (const event of task.events || []) {
    const command = String(event?.command || "").trim();
    const signal = `${event?.tool_name || ""} ${command}`.trim();
    if (signal) {
      commands.push(signal);
    }
    const patchFiles = extractPatchFiles(command);
    if (patchFiles.length > 0 || event?.tool_name === "apply_patch") {
      addUnique(changedFiles, patchFiles);
      continue;
    }
    if (!command) {
      continue;
    }
    addUnique(inspectedFiles, extractFilesFromCommand(command));
  }

  if (changedFiles.length > 0 || inspectedFiles.length > 0 || commands.length > 0) {
    const intentSummary = workIntentSummary(task, changedFiles, inspectedFiles, commands);
    if (intentSummary) {
      return intentSummary;
    }
  }

  if (changedFiles.length > 0) {
    return fileSummary("Ažurirano", changedFiles.slice(0, 1), fallbackSummaryMaxChars);
  }
  if (inspectedFiles.length > 0) {
    return fileSummary("Pregledano", inspectedFiles.slice(0, 2), fallbackSummaryMaxChars);
  }
  if (commands.length > 0) {
    return commandCategorySummary(commands);
  }
  return "";
}

function workIntentSummary(task, changedFiles = [], inspectedFiles = [], commands = []) {
  const project = String(task?.project || "").trim();
  const projectLower = project.toLowerCase();
  const context = [
    project,
    task?.workspace_root,
    task?.repository,
    task?.branch,
    task?.prompt,
    task?.last_assistant_message,
    ...(task?.evidence || []),
    ...(task?.events || []).map((event) => `${event?.tool_name || ""} ${event?.command || ""}`),
    ...changedFiles,
    ...inspectedFiles,
    ...commands,
  ].join("\n").toLowerCase();

  if (containsAny(context, ["kubectl", "kubernetes", "fleet", "deployment", "helm", "ghcr.io", "rollout"]) &&
    !containsAny(context, ["codex_task", "codex task", "codex worklog", "codex-worklog"])) {
    return `Rad na deployu i Kubernetes konfiguraciji projekta ${projectLabel(project)}.`;
  }

  if (containsAny(context, [
    "sqlcmd",
    "execute_sql",
    "mcp__mssql",
    "db_query",
    "database_query",
    "company_db_query",
    "select ",
  ])) {
    return `Analiza podataka u bazi za projekt ${projectLabel(project)}.`;
  }

  if (containsAny(context, ["codex worklog", "codex-worklog", "codex_task", "codex task", "wakatime"]) ||
    (context.includes("wakapi") && containsAny(context, ["worklog", "wakatime", "codex_task", "codex task"]))) {
    return "Rad na Codex worklog integraciji u Wakapiju.";
  }

  if (containsAny(context, [
    "delphi-decompiler",
    "delphi decompiler",
    "cli/check.sh",
    "decompiler",
    " idr",
  ])) {
    return "Rad na CLI provjerama i validaciji Delphi decompilera.";
  }

  if (projectLower === "ura" || containsAny(context, [
    "/ura/",
    "ura_",
    "onxpo",
  ])) {
    return "Rad na URA poslovnoj logici, testovima i migracijskim koracima.";
  }

  if (containsAny(context, [
    "onixphone",
    "document_batch",
    "pdf_batch",
    "batch_print",
    "dms ispis",
  ])) {
    return "Rad na OnixPhone DMS ispisu i obradi dokumenata.";
  }

  if (containsAny(context, [
    "test_",
    "_test.",
    "npm test",
    "yarn test",
    "go test",
    "dotnet test",
    "pytest",
  ])) {
    return `Rad na testovima i provjerama projekta ${projectLabel(project)}.`;
  }

  return "";
}

function containsAny(value, needles) {
  return needles.some((needle) => value.includes(String(needle).toLowerCase()));
}

function projectLabel(project) {
  return String(project || "").trim() || "projekt";
}

function commandCategorySummary(commands) {
  const joined = commands.join("\n").toLowerCase();
  if (/\bkubectl\b/.test(joined)) {
    return "Provjereni Kubernetes resursi.";
  }
  if (/\b(psql|sqlcmd|execute_sql|mcp__mssql)\b/.test(joined)) {
    return "Provjereno stanje baze podataka.";
  }
  if (/(?:db_query|database_query|company_db_query)/.test(joined)) {
    return "Provjereno stanje baze podataka.";
  }
  if (/\b(gh\s+(run|workflow|actions?)|git\s+)/.test(joined)) {
    return "Provjereno stanje repozitorija.";
  }
  if (/\b(npm|yarn|pnpm|dotnet|go)\s+(test|build|run)\b/.test(joined)) {
    return "Pokrenute projektne provjere.";
  }
  return "";
}

function extractPatchFiles(command) {
  const files = [];
  const regex = /^\*\*\* (?:Add|Update|Delete) File: (.+)$/gm;
  let match;
  while ((match = regex.exec(command)) !== null) {
    addUnique(files, [match[1]]);
  }
  return files;
}

function extractFilesFromCommand(command) {
  const files = [];
  let match;
  evidenceFilePattern.lastIndex = 0;
  while ((match = evidenceFilePattern.exec(String(command || ""))) !== null) {
    addUnique(files, [match[1]]);
  }
  return files;
}

function addUnique(target, values) {
  for (const value of values || []) {
    const file = cleanEvidenceFile(value);
    if (file && !target.includes(file)) {
      target.push(file);
    }
  }
}

function cleanEvidenceFile(value) {
  const file = String(value || "")
    .trim()
    .replace(/^["'`]+|["'`,.;)]+$/g, "")
    .replace(/^\.\//, "");
  if (!file || file.includes("://") || file.includes("node_modules/") || file.includes("/.git/")) {
    return "";
  }
  return file;
}

function fileSummary(verb, files, maxChars) {
  const cleanFiles = [];
  addUnique(cleanFiles, files);
  if (cleanFiles.length === 0) {
    return "";
  }

  const label = cleanFiles.length === 1 ? cleanFiles[0] : `${cleanFiles[0]} i ${cleanFiles[1]}`;
  const summary = `${verb} ${label}.`;
  return summary.length <= maxChars ? summary : `${verb} ${path.basename(cleanFiles[0])}.`;
}

function summaryFromJson(value) {
  try {
    const parsed = JSON.parse(value);
    if (parsed && typeof parsed.title === "string") {
      return parsed.title.trim();
    }
    if (parsed && typeof parsed.message === "string") {
      return parsed.message.trim();
    }
    if (parsed && typeof parsed.summary === "string") {
      return parsed.summary.trim();
    }
  } catch {
    return "";
  }
  return "";
}

function cleanSummaryText(value) {
  return String(value || "")
    .replace(/\[([^\]]+)\]\([^)]+\)/g, "$1")
    .replace(/`([^`]+)`/g, "$1")
    .split("\n")
    .map((line) => line
      .replace(/^#{1,6}\s+/, "")
      .replace(/^\s*(?:[-*+]|\d+[.)])\s+/, "")
      .trim())
    .filter(Boolean)
    .join(" ")
    .replace(/[*_~]/g, "")
    .replace(/\s+/g, " ")
    .trim();
}

function firstSentence(value) {
  const text = String(value || "").trim();
  const match = text.match(/^(.{1,180}?[.!?])(?:\s|$)/);
  return match ? match[1].trim() : text;
}

function ensureSentence(value) {
  const text = String(value || "").trim();
  return text && !/[.!?]$/.test(text) ? `${text}.` : text;
}

function usefulSummary(value, maxChars) {
  const summary = normalizeSummary(value, maxChars);
  const plain = summary.toLowerCase().replace(/[^\p{L}\p{N}]+/gu, "");
  return plain && !fillerSummaries.has(plain) && isUsefulWorkSummary(summary) && isLikelyCroatianSummary(summary) ? summary : "";
}

function isLikelyCroatianSummary(value) {
  return croatianSummaryPattern.test(String(value || ""));
}

function isUsefulWorkSummary(value) {
  const summary = String(value || "").trim();
  if (!summary) {
    return false;
  }

  const lower = summary.toLowerCase();
  if (lower === "..." || lower.startsWith("rad s codexom na projektu ")) {
    return false;
  }
  if (/^(?:you|you're|you are|your|i|i'm|i am|i've|i have|we|we're|we are)\b/.test(lower)) {
    return false;
  }
  if (/^(?:yes|yep|no|ok|okay|sure|done|right|exactly|correct)\b/.test(lower)) {
    return false;
  }
  if (/^(?:good|great)\b/.test(lower)) {
    return false;
  }
  if (/^(?:checked|patched|fixed|updated|changed|reviewed|worked on|handled|investigated|debugged|cleaned)(?:\s+(?:it|this|that))?[.!?]?$/.test(lower)) {
    return false;
  }
  if (/^(?:checked and patched|checked and fixed|patched and checked|fixed and checked)\s+(?:it|this|that)[.!?]?$/.test(lower)) {
    return false;
  }
  if (/^(?:patch applied successfully|corrective patch is applied(?: and verified)?)[.!?]?$/.test(lower)) {
    return false;
  }
  if (isLowValueWorkSummary(summary)) {
    return false;
  }

  return true;
}

function isLowValueWorkSummary(value) {
  const lower = String(value || "").trim().toLowerCase();
  if (!lower || lower.includes("bez zabilježenog konteksta") || lower.includes("bez zabiljezenog konteksta")) {
    return true;
  }
  return lower.startsWith("ažurirano ") ||
    lower.startsWith("azurirano ") ||
    lower.startsWith("pregledano ") ||
    lower.startsWith("provjereno stanje ") ||
    lower.startsWith("provjereni kubernetes ") ||
    lower.startsWith("pokrenute projektne ") ||
    lower.startsWith("provjereno stanje repozitorija");
}

async function addHumanSummary(task, env, deps = {}) {
  if (task.summary_hr || env.CODEX_WORKLOG_SUMMARY_ENABLED === "0" || env.CODEX_WORKLOG_SUMMARY_RUNNING === "1") {
    return;
  }

  const summarizeTask = deps.summarizeTask ?? generateCodexSummary;
  try {
    const summary = await summarizeTask(task, env, deps);
    const normalized = normalizeSummary(summary, summaryMaxChars(env));
    if (usefulSummary(normalized, summaryMaxChars(env))) {
      task.summary_hr = normalized;
    }
  } catch {
    // Summary generation is best-effort. Submission/queueing must still work.
  }
}

function summaryMaxChars(env) {
  const maxChars = Number(env.CODEX_WORKLOG_SUMMARY_MAX_CHARS || 220);
  return Number.isFinite(maxChars) && maxChars > 0 ? maxChars : 220;
}

async function generateCodexSummary(task, env, deps = {}) {
  const codexBin = env.CODEX_WORKLOG_CODEX_BIN || "codex";
  const model = env.CODEX_WORKLOG_SUMMARY_MODEL || "gpt-5.4-mini";
  const timeout = Number(env.CODEX_WORKLOG_SUMMARY_TIMEOUT_MS || 12000);
  const outputPath = path.join(env.CODEX_WORKLOG_HOME || path.join(homedir(), ".codex", "worklog"), "summary", `${safeFileName(task.id)}.${process.pid}.${randomUUID()}.txt`);
  await mkdir(path.dirname(outputPath), {recursive: true});

  const prompt = summaryPrompt(task);
  const execImpl = deps.execFile ?? execFileAsync;
  const childEnv = {
    ...process.env,
    ...env,
    CODEX_WORKLOG_SUMMARY_RUNNING: "1",
  };

  const args = [
    "exec",
    "--ephemeral",
    "--ignore-user-config",
    "--ignore-rules",
    "--skip-git-repo-check",
    "--model",
    model,
    "--sandbox",
    "read-only",
    "--output-last-message",
    outputPath,
    prompt,
  ];

  await execImpl(codexBin, args, {
    cwd: task.workspace_root || process.cwd(),
    env: childEnv,
    timeout,
    maxBuffer: 128 * 1024,
  });

  return readFile(outputPath, "utf8").finally(() => rm(outputPath, {force: true}));
}

function summaryPrompt(task) {
  const events = (task.events || [])
    .slice(-12)
    .map((event) => {
      const command = event.command ? ` ${event.command}` : "";
      return `- ${event.hook_event_name || "tool"} ${event.tool_name || ""}${command}`.trim().slice(0, 360);
    })
    .join("\n");

  const evidence = (task.evidence || []).slice(0, 12).map((item) => `- ${item}`).join("\n");
  return [
    "Write one concise human worklog summary for Wakapi in Croatian only.",
    "Return only the summary text. No markdown, no bullets, no quotes.",
    "Keep it under 180 characters. Describe the purpose and outcome of the work.",
    "Do not summarize as a file list. Do not start with Ažurirano, Pregledano, or Provjereno.",
    "Avoid raw file paths unless the file is the actual user-facing artifact.",
    "Good style: Rad na Codex worklog integraciji u Wakapiju: grupiranje po chatu i bolji sažeci.",
    "",
    `Project: ${task.project || "unknown"}`,
    `Prompt: ${task.prompt || ""}`.slice(0, 1200),
    `Assistant result: ${task.last_assistant_message || ""}`.slice(0, 1200),
    "Evidence:",
    evidence || "- none",
    "Recent tool events:",
    events || "- none",
  ].join("\n");
}

function normalizeSummary(value, maxChars) {
  const summary = String(value || "")
    .replace(/```[\s\S]*?```/g, "")
    .replace(/^["'`]+|["'`]+$/g, "")
    .replace(/\s+/g, " ")
    .trim();
  if (!summary) {
    return "";
  }
  return summary.length > maxChars ? `${summary.slice(0, Math.max(0, maxChars - 3)).trim()}...` : summary;
}

async function submitOrQueue(dirs, payload, env, deps) {
  if (await submitPayload(payload, env, deps)) {
    return true;
  }

  const key = createHash("sha256").update(JSON.stringify(payload)).digest("hex").slice(0, 24);
  const tmpPath = path.join(dirs.queue, `${key}.${process.pid}.${randomUUID()}.tmp`);
  const finalPath = path.join(dirs.queue, `${key}.json`);
  await writeFile(tmpPath, JSON.stringify(payload, null, 2));
  await rename(tmpPath, finalPath);
  return false;
}

async function submitPayload(payload, env, deps = {}) {
  const submitConfig = await getSubmitConfig(env);
  if (!submitConfig) {
    return false;
  }

  const fetchImpl = deps.fetch ?? globalThis.fetch;
  if (typeof fetchImpl !== "function") {
    return false;
  }

  const baseUrl = submitConfig.url.trim().replace(/\/+$/, "");
  const url = `${baseUrl}/api/integrations/codex/task-sessions`;
  try {
    const response = await fetchImpl(url, {
      method: "POST",
      headers: {
        "Authorization": `Basic ${Buffer.from(submitConfig.apiKey).toString("base64")}`,
        "Content-Type": "application/json",
        "User-Agent": "codex-worklog-hook/0.1",
      },
      body: JSON.stringify(payload),
    });
    return response.ok;
  } catch {
    return false;
  }
}

function hasWakapiCredentials(env) {
  return Boolean((env.CODEX_WORKLOG_WAKAPI_URL || env.WAKAPI_URL) &&
    (env.CODEX_WORKLOG_WAKAPI_API_KEY || env.WAKAPI_API_KEY));
}

async function getSubmitConfig(env) {
  if (hasWakapiCredentials(env)) {
    return {
      url: env.CODEX_WORKLOG_WAKAPI_URL || env.WAKAPI_URL,
      apiKey: env.CODEX_WORKLOG_WAKAPI_API_KEY || env.WAKAPI_API_KEY,
    };
  }

  const configPath = env.CODEX_WORKLOG_CONFIG || path.join(homedir(), ".codex", "worklog", "config.json");
  try {
    const config = JSON.parse(await readFile(configPath, "utf8"));
    const url = config.wakapi_url || config.wakapiUrl || config.url;
    const apiKey = config.wakapi_api_key || config.wakapiApiKey || config.api_key || config.apiKey;
    if (url && apiKey) {
      return {url, apiKey};
    }
  } catch {
    return null;
  }

  return null;
}

function extractEvidence(payload) {
  const evidence = [];
  const command = payload.tool_input?.command || payload.tool_input?.cmd || "";

  if (payload.tool_name === "apply_patch" && typeof command === "string") {
    const regex = /^\*\*\* (?:Add|Update|Delete) File: (.+)$/gm;
    let match;
    while ((match = regex.exec(command)) !== null) {
      evidence.push(match[1].trim());
    }
  }

  if (payload.tool_name === "Bash" && typeof command === "string") {
    evidence.push(`command: ${command.split("\n")[0].trim().slice(0, 160)}`);
  }

  return evidence.filter(Boolean);
}

async function ensureDirs(root) {
  const dirs = {
    root,
    tasks: path.join(root, "tasks"),
    queue: path.join(root, "queue"),
    bad: path.join(root, "bad"),
  };
  await mkdir(dirs.tasks, {recursive: true});
  await mkdir(dirs.queue, {recursive: true});
  await mkdir(dirs.bad, {recursive: true});
  return dirs;
}

async function readJsonOrQuarantine(dirs, filePath) {
  try {
    return JSON.parse(await readFile(filePath, "utf8"));
  } catch {
    await quarantineBadFile(dirs, filePath);
    return null;
  }
}

async function quarantineBadFile(dirs, filePath) {
  const digest = createHash("sha256").update(`${filePath}:${Date.now()}`).digest("hex").slice(0, 12);
  const targetPath = path.join(dirs.bad, `${path.basename(filePath)}.${digest}.bad`);
  await rename(filePath, targetPath);
}

async function saveTask(dirs, task) {
  const tmpPath = path.join(dirs.tasks, `${safeFileName(task.id)}.${process.pid}.${randomUUID()}.tmp`);
  const finalPath = path.join(dirs.tasks, `${safeFileName(task.id)}.json`);
  await writeFile(tmpPath, JSON.stringify(task, null, 2));
  await rename(tmpPath, finalPath);
}

async function loadTask(dirs, id) {
  const filePath = path.join(dirs.tasks, `${safeFileName(id)}.json`);
  if (!existsSync(filePath)) {
    return null;
  }
  return JSON.parse(await readFile(filePath, "utf8"));
}

async function removeTask(dirs, task) {
  await rm(path.join(dirs.tasks, `${safeFileName(task.id)}.json`), {force: true});
}

async function safeReaddir(dir) {
  try {
    return await readdir(dir);
  } catch {
    return [];
  }
}

function taskId(payload) {
  return `${payload.session_id || "no-session"}__${payload.turn_id || "no-turn"}`;
}

function safeFileName(value) {
  return String(value).replace(/[^a-zA-Z0-9._-]/g, "-");
}

function projectName(workspaceRoot) {
  return path.basename(workspaceRoot || process.cwd()) || "unknown";
}

async function getInstallationId(env) {
  if (env.CODEX_WORKLOG_INSTALLATION_ID) {
    return safeExternalKeyPart(env.CODEX_WORKLOG_INSTALLATION_ID);
  }

  try {
    const raw = await readFile(path.join(homedir(), ".codex", "installation_id"), "utf8");
    return safeExternalKeyPart(raw.trim());
  } catch {
    return "local";
  }
}

function safeExternalKeyPart(value) {
  return String(value || "local").replace(/[^a-zA-Z0-9._-]/g, "-");
}

async function resolveGitWorkspace(cwd) {
  try {
    const {stdout} = await execFileAsync("git", ["-C", cwd, "rev-parse", "--show-toplevel"]);
    return stdout.trim() || cwd;
  } catch {
    return cwd;
  }
}

async function gitBranch(cwd) {
  try {
    const {stdout} = await execFileAsync("git", ["-C", cwd, "branch", "--show-current"]);
    return stdout.trim();
  } catch {
    return "";
  }
}
