import {execFile} from "node:child_process";
import {createHash} from "node:crypto";
import {existsSync} from "node:fs";
import {mkdir, readFile, readdir, rename, rm, writeFile} from "node:fs/promises";
import {homedir} from "node:os";
import path from "node:path";
import {promisify} from "node:util";

const execFileAsync = promisify(execFile);

export async function handleHook(payload, env = process.env, deps = {}) {
  const now = deps.now ?? (() => new Date());
  const resolveWorkspace = deps.resolveWorkspace ?? resolveGitWorkspace;
  const worklogHome = env.CODEX_WORKLOG_HOME || path.join(homedir(), ".codex", "worklog");
  const dirs = await ensureDirs(worklogHome);
  const eventName = payload.hook_event_name;

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

async function submitOrQueue(dirs, payload, env, deps) {
  if (await submitPayload(payload, env, deps)) {
    return true;
  }

  const key = createHash("sha256").update(JSON.stringify(payload)).digest("hex").slice(0, 24);
  const tmpPath = path.join(dirs.queue, `${key}.tmp`);
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
  const tmpPath = path.join(dirs.tasks, `${safeFileName(task.id)}.tmp`);
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
