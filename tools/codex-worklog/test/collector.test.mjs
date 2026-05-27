import assert from "node:assert/strict";
import {mkdtemp, mkdir, readFile, readdir, rm, writeFile} from "node:fs/promises";
import {tmpdir} from "node:os";
import path from "node:path";
import test from "node:test";

import {handleHook, sweep} from "../src/collector.mjs";

const now = new Date("2026-05-14T09:00:00.000Z");

async function withWorklogHome(fn) {
  const dir = await mkdtemp(path.join(tmpdir(), "codex-worklog-"));
  try {
    await fn(dir);
  } finally {
    await rm(dir, {recursive: true, force: true});
  }
}

function testEnv(home) {
  return {
    CODEX_WORKLOG_HOME: home,
    CODEX_WORKLOG_INSTALLATION_ID: "local",
    CODEX_WORKLOG_CONFIG: path.join(home, "missing-config.json"),
    CODEX_WORKLOG_SUMMARY_ENABLED: "0",
  };
}

test("UserPromptSubmit starts a task keyed by session and turn", async () => {
  await withWorklogHome(async (home) => {
    const result = await handleHook(
      {
        hook_event_name: "UserPromptSubmit",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/OnixServer",
        prompt: "implement codex task worklogs",
      },
      testEnv(home),
      {now: () => now, resolveWorkspace: async (cwd) => cwd},
    );

    assert.equal(result.action, "started");

    const taskPath = path.join(home, "tasks", "thread-1__turn-1.json");
    const task = JSON.parse(await readFile(taskPath, "utf8"));
    assert.equal(task.external_key, "codex:local:thread-1:turn-1");
    assert.equal(task.project, "OnixServer");
    assert.equal(task.prompt, "implement codex task worklogs");
    assert.equal(task.started_at, "2026-05-14T09:00:00.000Z");
  });
});

test("hook ignores events from the summary subprocess", async () => {
  await withWorklogHome(async (home) => {
    const result = await handleHook(
      {
        hook_event_name: "UserPromptSubmit",
        session_id: "summary-session",
        turn_id: "summary-turn",
        cwd: "/Users/igbenic/Projects/wakapi",
        prompt: "summarize another task",
      },
      {
        ...testEnv(home),
        CODEX_WORKLOG_SUMMARY_RUNNING: "1",
      },
      {now: () => now, resolveWorkspace: async (cwd) => cwd},
    );

    assert.equal(result.action, "ignored");
    assert.deepEqual(await readdir(path.join(home, "tasks")), []);
  });
});

test("PostToolUse records file evidence from apply_patch", async () => {
  await withWorklogHome(async (home) => {
    const env = testEnv(home);
    const deps = {now: () => now, resolveWorkspace: async (cwd) => cwd};

    await handleHook(
      {
        hook_event_name: "UserPromptSubmit",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/wakapi",
        prompt: "add wakapi api",
      },
      env,
      deps,
    );

    await handleHook(
      {
        hook_event_name: "PostToolUse",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/wakapi",
        tool_name: "apply_patch",
        tool_input: {
          command: "*** Begin Patch\n*** Update File: routes/api/codex_tasks.go\n@@\n+test\n*** End Patch\n",
        },
      },
      env,
      deps,
    );

    const task = JSON.parse(await readFile(path.join(home, "tasks", "thread-1__turn-1.json"), "utf8"));
    assert.deepEqual(task.evidence, ["routes/api/codex_tasks.go"]);
    assert.equal(task.events[0].tool_name, "apply_patch");
  });
});

test("Stop closes and queues a session when Wakapi credentials are missing", async () => {
  await withWorklogHome(async (home) => {
    const env = testEnv(home);
    let current = now;
    const deps = {
      now: () => current,
      resolveWorkspace: async (cwd) => cwd,
    };

    await handleHook(
      {
        hook_event_name: "UserPromptSubmit",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/OnixServer",
        prompt: "sync codex worklogs",
      },
      env,
      deps,
    );

    current = new Date("2026-05-14T09:20:00.000Z");
    const result = await handleHook(
      {
        hook_event_name: "Stop",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/OnixServer",
        last_assistant_message: "Implemented Codex task worklogs.",
      },
      env,
      deps,
    );

    assert.equal(result.action, "queued");

    const queued = await readdir(path.join(home, "queue"));
    assert.equal(queued.length, 1);
    const payload = JSON.parse(await readFile(path.join(home, "queue", queued[0]), "utf8"));
    assert.equal(payload.sessions[0].external_key, "codex:local:thread-1:turn-1");
    assert.equal(payload.sessions[0].duration_seconds, 1200);
    assert.equal(payload.sessions[0].summary_hr, "Planiranje implementacije za projekt OnixServer.");
    assert.equal(payload.sessions[0].summary_hr_original, "Planiranje implementacije za projekt OnixServer.");
    assert.equal(payload.sessions[0].summary_hr_normalized, "Planiranje implementacije za projekt OnixServer.");
    assert.equal(payload.sessions[0].summary_source, "evidence");
    assert.equal(payload.sessions[0].summary_confidence, 0.44);
    assert.equal(payload.sessions[0].client_message_hr, "Planiranje implementacije za projekt OnixServer.");
    assert.equal(payload.sessions[0].review_status, "needs_grouping");
    assert.match(payload.sessions[0].internal_message_hr, /Predloženi sažetak/);
    assert.doesNotMatch(payload.sessions[0].summary_hr, /sync codex worklogs/i);
    assert.equal(payload.sessions[0].last_assistant_message, "Implemented Codex task worklogs.");
  });
});

test("Stop falls back to changed file evidence instead of vague generated text", async () => {
  await withWorklogHome(async (home) => {
    const env = {
      ...testEnv(home),
      CODEX_WORKLOG_SUMMARY_ENABLED: "1",
    };
    let current = now;
    const deps = {
      now: () => current,
      resolveWorkspace: async (cwd) => cwd,
      summarizeTask: async () => "Checked and patched it.",
    };

    await handleHook(
      {
        hook_event_name: "UserPromptSubmit",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/wakapi",
        prompt: "raw user message should never become the visible summary",
      },
      env,
      deps,
    );

    await handleHook(
      {
        hook_event_name: "PostToolUse",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/wakapi",
        tool_name: "apply_patch",
        tool_input: {
          command: "*** Begin Patch\n*** Update File: routes/api/codex_tasks.go\n@@\n+test\n*** End Patch\n",
        },
      },
      env,
      deps,
    );

    current = new Date("2026-05-14T09:20:00.000Z");
    await handleHook(
      {
        hook_event_name: "Stop",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/wakapi",
        last_assistant_message: "Checked and patched it.",
      },
      env,
      deps,
    );

    const queued = await readdir(path.join(home, "queue"));
    const payload = JSON.parse(await readFile(path.join(home, "queue", queued[0]), "utf8"));
    assert.equal(payload.sessions[0].summary_hr, "Rad na Codex worklog integraciji u Wakapiju.");
    assert.doesNotMatch(payload.sessions[0].summary_hr, /checked and patched it/i);
    assert.doesNotMatch(payload.sessions[0].summary_hr, /raw user message/i);
  });
});

test("Stop falls back to work intent instead of changed file breadcrumb", async () => {
  await withWorklogHome(async (home) => {
    const env = {
      ...testEnv(home),
      CODEX_WORKLOG_SUMMARY_ENABLED: "1",
    };
    let current = now;
    const deps = {
      now: () => current,
      resolveWorkspace: async (cwd) => cwd,
      summarizeTask: async () => "Ažurirano routes/api/codex_tasks.go.",
    };

    await handleHook(
      {
        hook_event_name: "UserPromptSubmit",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/wakapi",
        prompt: "make Wakapi Codex worklog messages smarter",
      },
      env,
      deps,
    );

    await handleHook(
      {
        hook_event_name: "PostToolUse",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/wakapi",
        tool_name: "apply_patch",
        tool_input: {
          command: "*** Begin Patch\n*** Update File: routes/api/codex_tasks.go\n@@\n+test\n*** End Patch\n",
        },
      },
      env,
      deps,
    );

    current = new Date("2026-05-14T09:20:00.000Z");
    await handleHook(
      {
        hook_event_name: "Stop",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/wakapi",
        last_assistant_message: "Added grouped Codex worklog summary logic in Wakapi.",
      },
      env,
      deps,
    );

    const queued = await readdir(path.join(home, "queue"));
    const payload = JSON.parse(await readFile(path.join(home, "queue", queued[0]), "utf8"));
    assert.equal(payload.sessions[0].summary_hr, "Rad na Codex worklog integraciji u Wakapiju.");
    assert.doesNotMatch(payload.sessions[0].summary_hr, /Ažurirano/);
    assert.doesNotMatch(payload.sessions[0].summary_hr, /routes\/api\/codex_tasks\.go/);
  });
});

test("Stop falls back to inspected file evidence instead of assistant reply text", async () => {
  await withWorklogHome(async (home) => {
    const env = testEnv(home);
    let current = now;
    const deps = {
      now: () => current,
      resolveWorkspace: async (cwd) => cwd,
    };

    await handleHook(
      {
        hook_event_name: "UserPromptSubmit",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/IBTechK3SFleetRepo",
        prompt: "raw user message should never become the visible summary",
      },
      env,
      deps,
    );

    await handleHook(
      {
        hook_event_name: "PostToolUse",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/IBTechK3SFleetRepo",
        tool_name: "Bash",
        tool_input: {
          command: "sed -n '1,140p' 02-fleet/05-apps/zerotier-client-gateway/zerotier-client-gateway-configmap.yaml",
        },
      },
      env,
      deps,
    );

    await handleHook(
      {
        hook_event_name: "PostToolUse",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/IBTechK3SFleetRepo",
        tool_name: "Bash",
        tool_input: {
          command: "sed -n '1,120p' 02-fleet/05-apps/zerotier-client-gateway/zerotier-client-gateway-service.yaml",
        },
      },
      env,
      deps,
    );

    current = new Date("2026-05-14T09:20:00.000Z");
    await handleHook(
      {
        hook_event_name: "Stop",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/IBTechK3SFleetRepo",
        last_assistant_message: "You use it as a Kubernetes TCP gateway, not as a ZeroTier interface inside Grunf/onix-api.",
      },
      env,
      deps,
    );

    const queued = await readdir(path.join(home, "queue"));
    const payload = JSON.parse(await readFile(path.join(home, "queue", queued[0]), "utf8"));
    assert.equal(payload.sessions[0].summary_hr, "Codex aktivnost zahtijeva ručni pregled.");
    assert.equal(payload.sessions[0].summary_hr_original, "Rad na deployu i Kubernetes konfiguraciji projekta IBTechK3SFleetRepo.");
    assert.equal(payload.sessions[0].summary_hr_normalized, "Rad na deployu i Kubernetes konfiguraciji projekta IBTechK3SFleetRepo.");
    assert.equal(payload.sessions[0].summary_source, "evidence");
    assert.equal(payload.sessions[0].client_message_hr, null);
    assert.equal(payload.sessions[0].review_status, "needs_review");
    assert.doesNotMatch(payload.sessions[0].summary_hr, /you use it as/i);
    assert.doesNotMatch(payload.sessions[0].summary_hr, /raw user message/i);
  });
});

test("Stop falls back to command category evidence when no files are captured", async () => {
  await withWorklogHome(async (home) => {
    const env = testEnv(home);
    let current = now;
    const deps = {
      now: () => current,
      resolveWorkspace: async (cwd) => cwd,
    };

    await handleHook(
      {
        hook_event_name: "UserPromptSubmit",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/IBTechK3SFleetRepo",
        prompt: "is wakapi deployed",
      },
      env,
      deps,
    );

    await handleHook(
      {
        hook_event_name: "PostToolUse",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/IBTechK3SFleetRepo",
        tool_name: "Bash",
        tool_input: {
          command: "kubectl -n wakapi-system get deploy wakapi-backend-deployment",
        },
      },
      env,
      deps,
    );

    current = new Date("2026-05-14T09:20:00.000Z");
    await handleHook(
      {
        hook_event_name: "Stop",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/IBTechK3SFleetRepo",
        last_assistant_message: "Patch applied successfully.",
      },
      env,
      deps,
    );

    const queued = await readdir(path.join(home, "queue"));
    const payload = JSON.parse(await readFile(path.join(home, "queue", queued[0]), "utf8"));
    assert.equal(payload.sessions[0].summary_hr, "Codex aktivnost zahtijeva ručni pregled.");
    assert.equal(payload.sessions[0].summary_hr_original, "Rad na deployu i Kubernetes konfiguraciji projekta IBTechK3SFleetRepo.");
    assert.equal(payload.sessions[0].summary_hr_normalized, "Rad na deployu i Kubernetes konfiguraciji projekta IBTechK3SFleetRepo.");
    assert.equal(payload.sessions[0].summary_source, "evidence");
    assert.equal(payload.sessions[0].client_message_hr, null);
    assert.equal(payload.sessions[0].review_status, "needs_review");
    assert.doesNotMatch(payload.sessions[0].summary_hr, /rad s codexom|patch applied/i);
  });
});

test("Stop falls back to tool category evidence when command text is absent", async () => {
  await withWorklogHome(async (home) => {
    const env = testEnv(home);
    let current = now;
    const deps = {
      now: () => current,
      resolveWorkspace: async (cwd) => cwd,
    };

    await handleHook(
      {
        hook_event_name: "UserPromptSubmit",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/URA",
        prompt: "check URA customer data",
      },
      env,
      deps,
    );

    await handleHook(
      {
        hook_event_name: "PostToolUse",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/URA",
        tool_name: "mcp__onix_support_ticketing__onix_support_company_db_query",
        tool_input: {},
      },
      env,
      deps,
    );

    current = new Date("2026-05-14T09:20:00.000Z");
    await handleHook(
      {
        hook_event_name: "Stop",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/URA",
        last_assistant_message: "Good.",
      },
      env,
      deps,
    );

    const queued = await readdir(path.join(home, "queue"));
    const payload = JSON.parse(await readFile(path.join(home, "queue", queued[0]), "utf8"));
    assert.equal(payload.sessions[0].summary_hr, "Codex aktivnost zahtijeva ručni pregled.");
    assert.equal(payload.sessions[0].summary_hr_original, "Analiza podataka u bazi za projekt URA.");
    assert.equal(payload.sessions[0].summary_hr_normalized, "Analiza podataka u bazi za projekt URA.");
    assert.equal(payload.sessions[0].summary_source, "evidence");
    assert.equal(payload.sessions[0].client_message_hr, null);
    assert.equal(payload.sessions[0].review_status, "needs_review");
    assert.doesNotMatch(payload.sessions[0].summary_hr, /rad s codexom|good/i);
  });
});

test("Stop marks generic test-run summary as needs_review", async () => {
  await withWorklogHome(async (home) => {
    const env = testEnv(home);
    let current = now;
    const deps = {
      now: () => current,
      resolveWorkspace: async (cwd) => cwd,
    };

    await handleHook(
      {
        hook_event_name: "UserPromptSubmit",
        session_id: "thread-test-run",
        turn_id: "turn-test-run",
        cwd: "/Users/igbenic/Projects/OnixServer",
        prompt: "run tests for support service merge",
      },
      env,
      deps,
    );

    await handleHook(
      {
        hook_event_name: "PostToolUse",
        session_id: "thread-test-run",
        turn_id: "turn-test-run",
        cwd: "/Users/igbenic/Projects/OnixServer",
        tool_name: "Bash",
        tool_input: {
          command: "dotnet test OnixWeb.sln --filter FullyQualifiedName~SupportServiceMergeTests",
        },
      },
      env,
      deps,
    );

    current = new Date("2026-05-14T09:20:00.000Z");
    await handleHook(
      {
        hook_event_name: "Stop",
        session_id: "thread-test-run",
        turn_id: "turn-test-run",
        cwd: "/Users/igbenic/Projects/OnixServer",
        last_assistant_message: "Ran tests and confirmed expected behavior.",
      },
      env,
      deps,
    );

    const queued = await readdir(path.join(home, "queue"));
    const payload = JSON.parse(await readFile(path.join(home, "queue", queued[0]), "utf8"));
    assert.equal(payload.sessions[0].summary_hr, "Codex aktivnost zahtijeva ručni pregled.");
    assert.equal(payload.sessions[0].summary_hr_original, "Rad na testovima i provjerama projekta OnixServer.");
    assert.equal(payload.sessions[0].summary_hr_normalized, "Rad na testovima i provjerama projekta OnixServer.");
    assert.equal(payload.sessions[0].review_status, "needs_review");
    assert.equal(payload.sessions[0].client_message_hr, null);
  });
});

test("Stop does not classify generated OnixPhone work as URA", async () => {
  await withWorklogHome(async (home) => {
    const env = testEnv(home);
    let current = now;
    const deps = {
      now: () => current,
      resolveWorkspace: async (cwd) => cwd,
    };

    await handleHook(
      {
        hook_event_name: "UserPromptSubmit",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/OnixPhone",
        prompt: "sredi DMS ispis za OnixPhone dokumente",
      },
      env,
      deps,
    );

    await handleHook(
      {
        hook_event_name: "PostToolUse",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/OnixPhone",
        tool_name: "apply_patch",
        tool_input: {
          command: "*** Begin Patch\n*** Update File: Services/DmsPrintService.cs\n@@\n+test\n*** End Patch\n",
        },
      },
      env,
      deps,
    );

    current = new Date("2026-05-14T09:20:00.000Z");
    await handleHook(
      {
        hook_event_name: "Stop",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/OnixPhone",
        last_assistant_message: "Generiran DMS ispis dokumenata.",
      },
      env,
      deps,
    );

    const queued = await readdir(path.join(home, "queue"));
    const payload = JSON.parse(await readFile(path.join(home, "queue", queued[0]), "utf8"));
    assert.equal(payload.sessions[0].summary_hr, "Rad na OnixPhone DMS ispisu i obradi dokumenata.");
    assert.doesNotMatch(payload.sessions[0].summary_hr, /URA/);
  });
});

test("Stop skips English title from assistant JSON", async () => {
  await withWorklogHome(async (home) => {
    const env = testEnv(home);
    let current = now;
    const deps = {
      now: () => current,
      resolveWorkspace: async (cwd) => cwd,
    };

    await handleHook(
      {
        hook_event_name: "UserPromptSubmit",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/URA",
        prompt: "raw user message should never become the visible summary",
      },
      env,
      deps,
    );

    current = new Date("2026-05-14T09:20:00.000Z");
    await handleHook(
      {
        hook_event_name: "Stop",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/URA",
        last_assistant_message: "{\"title\":\"Review URA migration flow\"}",
      },
      env,
      deps,
    );

    const queued = await readdir(path.join(home, "queue"));
    const payload = JSON.parse(await readFile(path.join(home, "queue", queued[0]), "utf8"));
    assert.equal(payload.sessions[0].summary_hr, "Pregled i verifikacija rješenja za projekt URA.");
    assert.equal(payload.sessions[0].summary_source, "evidence");
    assert.equal(payload.sessions[0].review_status, "needs_grouping");
    assert.doesNotMatch(payload.sessions[0].summary_hr, /raw user message/i);
    assert.doesNotMatch(payload.sessions[0].summary_hr, /title/i);
  });
});

test("Stop skips English message from assistant JSON", async () => {
  await withWorklogHome(async (home) => {
    const env = testEnv(home);
    let current = now;
    const deps = {
      now: () => current,
      resolveWorkspace: async (cwd) => cwd,
    };

    await handleHook(
      {
        hook_event_name: "UserPromptSubmit",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/OnixServer",
        prompt: "raw user message should never become the visible summary",
      },
      env,
      deps,
    );

    current = new Date("2026-05-14T09:20:00.000Z");
    await handleHook(
      {
        hook_event_name: "Stop",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/OnixServer",
        last_assistant_message: "{\"message\":\"Add hide action for TeamViewer sessions\"}",
      },
      env,
      deps,
    );

    const queued = await readdir(path.join(home, "queue"));
    const payload = JSON.parse(await readFile(path.join(home, "queue", queued[0]), "utf8"));
    assert.equal(payload.sessions[0].summary_hr, "Codex aktivnost zahtijeva ručni pregled.");
    assert.equal(payload.sessions[0].summary_hr_normalized, "Codex aktivnost bez dovoljno konteksta za opis.");
    assert.equal(payload.sessions[0].summary_source, "fallback");
    assert.equal(payload.sessions[0].client_message_hr, null);
    assert.equal(payload.sessions[0].review_status, "needs_review");
    assert.doesNotMatch(payload.sessions[0].summary_hr, /raw user message/i);
    assert.doesNotMatch(payload.sessions[0].summary_hr, /message/i);
  });
});

test("Stop accepts Croatian generated summaries only", async () => {
  await withWorklogHome(async (home) => {
    const env = {
      ...testEnv(home),
      CODEX_WORKLOG_SUMMARY_ENABLED: "1",
    };
    let current = now;
    const deps = {
      now: () => current,
      resolveWorkspace: async (cwd) => cwd,
      summarizeTask: async () => "Generated via Codex summary.",
    };

    await handleHook(
      {
        hook_event_name: "UserPromptSubmit",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/wakapi",
        prompt: "summarize Codex worklogs",
      },
      env,
      deps,
    );

    current = new Date("2026-05-14T09:20:00.000Z");
    await handleHook(
      {
        hook_event_name: "Stop",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/wakapi",
        last_assistant_message: "",
      },
      env,
      deps,
    );

    const queued = await readdir(path.join(home, "queue"));
    const payload = JSON.parse(await readFile(path.join(home, "queue", queued[0]), "utf8"));
    assert.equal(payload.sessions[0].summary_hr, "Codex aktivnost zahtijeva ručni pregled.");
    assert.equal(payload.sessions[0].summary_hr_normalized, "Codex aktivnost bez dovoljno konteksta za opis.");
    assert.equal(payload.sessions[0].summary_source, "fallback");
    assert.equal(payload.sessions[0].client_message_hr, null);
    assert.equal(payload.sessions[0].review_status, "needs_review");
    assert.doesNotMatch(payload.sessions[0].summary_hr, /generated via/i);
  });
});

test("Stop skips useless assistant fallback text", async () => {
  await withWorklogHome(async (home) => {
    const env = testEnv(home);
    let current = now;
    const deps = {
      now: () => current,
      resolveWorkspace: async (cwd) => cwd,
    };

    await handleHook(
      {
        hook_event_name: "UserPromptSubmit",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/URA",
        prompt: "raw user message should never become the visible summary",
      },
      env,
      deps,
    );

    current = new Date("2026-05-14T09:20:00.000Z");
    await handleHook(
      {
        hook_event_name: "Stop",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/URA",
        last_assistant_message: "...",
      },
      env,
      deps,
    );

    const queued = await readdir(path.join(home, "queue"));
    const payload = JSON.parse(await readFile(path.join(home, "queue", queued[0]), "utf8"));
    assert.equal(payload.sessions[0].summary_hr, "Codex aktivnost zahtijeva ručni pregled.");
    assert.equal(payload.sessions[0].summary_hr_normalized, "Codex aktivnost bez dovoljno konteksta za opis.");
    assert.equal(payload.sessions[0].summary_source, "fallback");
    assert.equal(payload.sessions[0].client_message_hr, null);
    assert.equal(payload.sessions[0].review_status, "needs_review");
    assert.doesNotMatch(payload.sessions[0].summary_hr, /^\\.\\.\\.$/);
    assert.doesNotMatch(payload.sessions[0].summary_hr, /raw user message/i);
  });
});

test("Stop skips filler assistant acknowledgements", async () => {
  await withWorklogHome(async (home) => {
    const env = testEnv(home);
    let current = now;
    const deps = {
      now: () => current,
      resolveWorkspace: async (cwd) => cwd,
    };

    await handleHook(
      {
        hook_event_name: "UserPromptSubmit",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/OnixServer",
        prompt: "raw user message should never become the visible summary",
      },
      env,
      deps,
    );

    current = new Date("2026-05-14T09:20:00.000Z");
    await handleHook(
      {
        hook_event_name: "Stop",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/OnixServer",
        last_assistant_message: "You're right.",
      },
      env,
      deps,
    );

    const queued = await readdir(path.join(home, "queue"));
    const payload = JSON.parse(await readFile(path.join(home, "queue", queued[0]), "utf8"));
    assert.equal(payload.sessions[0].summary_hr, "Codex aktivnost zahtijeva ručni pregled.");
    assert.equal(payload.sessions[0].summary_hr_normalized, "Codex aktivnost bez dovoljno konteksta za opis.");
    assert.equal(payload.sessions[0].summary_source, "fallback");
    assert.equal(payload.sessions[0].client_message_hr, null);
    assert.equal(payload.sessions[0].review_status, "needs_review");
    assert.notEqual(payload.sessions[0].summary_hr, "You're right.");
    assert.doesNotMatch(payload.sessions[0].summary_hr, /raw user message/i);
  });
});

test("Stop includes a generated human summary in the queued session", async () => {
  await withWorklogHome(async (home) => {
    const env = {
      ...testEnv(home),
      CODEX_WORKLOG_SUMMARY_ENABLED: "1",
    };
    let current = now;
    const deps = {
      now: () => current,
      resolveWorkspace: async (cwd) => cwd,
      summarizeTask: async (task) => {
        assert.equal(task.project, "wakapi");
        assert.equal(task.prompt, "add LLM summaries to Codex worklogs");
        return "Dodan LLM sažetak Codex workloga.";
      },
    };

    await handleHook(
      {
        hook_event_name: "UserPromptSubmit",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/wakapi",
        prompt: "add LLM summaries to Codex worklogs",
      },
      env,
      deps,
    );

    current = new Date("2026-05-14T09:03:00.000Z");
    await handleHook(
      {
        hook_event_name: "Stop",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/wakapi",
        last_assistant_message: "Implemented summary generation.",
      },
      env,
      deps,
    );

    const queued = await readdir(path.join(home, "queue"));
    assert.equal(queued.length, 1);
    const payload = JSON.parse(await readFile(path.join(home, "queue", queued[0]), "utf8"));
    assert.equal(payload.sessions[0].summary_hr, "Dodan LLM sažetak Codex workloga.");
  });
});

test("Stop invokes Codex summary generation with the low model and recursion guard", async () => {
  await withWorklogHome(async (home) => {
    const env = {
      ...testEnv(home),
      CODEX_WORKLOG_CODEX_BIN: "/bin/codex",
      CODEX_WORKLOG_SUMMARY_ENABLED: "1",
      CODEX_WORKLOG_SUMMARY_MODEL: "tiny-codex",
      CODEX_WORKLOG_SUMMARY_TIMEOUT_MS: "3456",
    };
    let current = now;
    let execCall;
    const deps = {
      now: () => current,
      resolveWorkspace: async (cwd) => cwd,
      execFile: async (command, args, options) => {
        if (command === "git") {
          const action = args.slice(2).join(" ");
          if (action.startsWith("rev-parse --show-toplevel")) {
            return {stdout: "/Users/igbenic/Projects/wakapi\n", stderr: ""};
          }
          if (action.startsWith("branch --show-current")) {
            return {stdout: "codex/test\n", stderr: ""};
          }
          return {stdout: "", stderr: ""};
        }
        execCall = {command, args, options};
        const outputIndex = args.indexOf("--output-last-message");
        await mkdir(path.dirname(args[outputIndex + 1]), {recursive: true});
        await writeFile(args[outputIndex + 1], "Generiran Codex sažetak rada.");
        return {stdout: "", stderr: ""};
      },
    };

    await handleHook(
      {
        hook_event_name: "UserPromptSubmit",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/wakapi",
        prompt: "summarize Codex worklogs",
      },
      env,
      deps,
    );

    current = new Date("2026-05-14T09:04:00.000Z");
    await handleHook(
      {
        hook_event_name: "Stop",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/wakapi",
        last_assistant_message: "Added local summary generation.",
      },
      env,
      deps,
    );

    assert.equal(execCall.command, "/bin/codex");
    assert.equal(execCall.options.cwd, "/Users/igbenic/Projects/wakapi");
    assert.equal(execCall.options.timeout, 3456);
    assert.equal(execCall.options.env.CODEX_WORKLOG_SUMMARY_RUNNING, "1");
    assert.deepEqual(execCall.args.slice(0, 7), [
      "exec",
      "--ephemeral",
      "--ignore-user-config",
      "--ignore-rules",
      "--skip-git-repo-check",
      "--model",
      "tiny-codex",
    ]);
    assert.ok(execCall.args.includes("--sandbox"));
    assert.ok(execCall.args.includes("read-only"));
    assert.match(execCall.args.at(-1), /Croatian only/);

    const queued = await readdir(path.join(home, "queue"));
    const payload = JSON.parse(await readFile(path.join(home, "queue", queued[0]), "utf8"));
    assert.equal(payload.sessions[0].summary_hr, "Generiran Codex sažetak rada.");
  });
});

test("Stop captures git diff/status/log evidence in technical payload", async () => {
  await withWorklogHome(async (home) => {
    const env = testEnv(home);
    let current = now;
    const deps = {
      now: () => current,
      resolveWorkspace: async (cwd) => cwd,
      execFile: async (command, args) => {
        if (command !== "git") {
          throw new Error(`unexpected command ${command}`);
        }
        const action = args.slice(2).join(" ");
        if (action.startsWith("rev-parse --show-toplevel")) {
          return {stdout: "/repo\n", stderr: ""};
        }
        if (action.startsWith("branch --show-current")) {
          return {stdout: "feature/codex-phase1\n", stderr: ""};
        }
        if (action.startsWith("status --porcelain")) {
          return {stdout: " M src/a.go\nA  src/b.go\nM  .env\n", stderr: ""};
        }
        if (action.startsWith("diff --name-status")) {
          return {stdout: "M\tsrc/a.go\nA\tsrc/b.go\nM\t.env\n", stderr: ""};
        }
        if (action.startsWith("diff --numstat")) {
          return {stdout: "5\t2\tsrc/a.go\n10\t0\tsrc/b.go\n1\t1\t.env\n", stderr: ""};
        }
        if (action.startsWith("diff --stat")) {
          return {stdout: " src/a.go | 7 +++++--\n src/b.go | 10 ++++++++++\n .env | 2 +-\n", stderr: ""};
        }
        if (action.startsWith("log --oneline --decorate --since")) {
          return {stdout: "abc1234 feat: update a api_key=topsecret\nbeef567 fix: update b Authorization: Bearer token123\n", stderr: ""};
        }
        throw new Error(`unexpected git action ${action}`);
      },
    };

    await handleHook(
      {
        hook_event_name: "UserPromptSubmit",
        session_id: "thread-git",
        turn_id: "turn-git",
        cwd: "/repo",
        prompt: "collect git evidence on stop",
      },
      env,
      deps,
    );

    current = new Date("2026-05-14T09:05:00.000Z");
    await handleHook(
      {
        hook_event_name: "Stop",
        session_id: "thread-git",
        turn_id: "turn-git",
        cwd: "/repo",
        last_assistant_message: "ok",
      },
      env,
      deps,
    );

    const queued = await readdir(path.join(home, "queue"));
    const payload = JSON.parse(await readFile(path.join(home, "queue", queued[0]), "utf8"));
    assert.equal(payload.sessions[0].technical_evidence.git.workspace_root, "/repo");
    assert.equal(payload.sessions[0].technical_evidence.git.branch, "feature/codex-phase1");
    assert.equal(payload.sessions[0].technical_evidence.git.dirty, true);
    assert.deepEqual(payload.sessions[0].technical_evidence.git.changed_files, [
      {path: "src/a.go", status: "M", added: 5, removed: 2},
      {path: "src/b.go", status: "A", added: 10, removed: 0},
    ]);
    assert.deepEqual(payload.sessions[0].technical_evidence.git.diff_stat, [
      "src/a.go | 7 +++++--",
      "src/b.go | 10 ++++++++++",
    ]);
    assert.deepEqual(payload.sessions[0].technical_evidence.git.recent_commits, [
      {sha: "abc1234", message: "feat: update a api_key=[REDACTED]"},
      {sha: "beef567", message: "fix: update b Authorization=[REDACTED]"},
    ]);
  });
});

test("SessionStart closes stale open tasks at their last activity time", async () => {
  await withWorklogHome(async (home) => {
    const env = testEnv(home);
    let current = new Date("2026-05-14T09:00:00.000Z");
    const deps = {
      now: () => current,
      resolveWorkspace: async (cwd) => cwd,
    };

    await handleHook(
      {
        hook_event_name: "UserPromptSubmit",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/OnixServer",
        prompt: "work on stale sweeper",
      },
      env,
      deps,
    );

    current = new Date("2026-05-14T09:15:00.000Z");
    await handleHook(
      {
        hook_event_name: "PostToolUse",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/OnixServer",
        tool_name: "Bash",
        tool_input: {command: "npm test"},
      },
      env,
      deps,
    );

    current = new Date("2026-05-14T14:00:00.000Z");
    const result = await handleHook(
      {
        hook_event_name: "SessionStart",
        session_id: "thread-2",
        cwd: "/Users/igbenic/Projects/OnixServer",
      },
      env,
      deps,
    );

    assert.equal(result.action, "session_seen");
    assert.deepEqual(await readdir(path.join(home, "tasks")), []);

    const queued = await readdir(path.join(home, "queue"));
    assert.equal(queued.length, 1);
    const payload = JSON.parse(await readFile(path.join(home, "queue", queued[0]), "utf8"));
    assert.equal(payload.sessions[0].external_key, "codex:local:thread-1:turn-1");
    assert.equal(payload.sessions[0].status, "stale");
    assert.equal(payload.sessions[0].ended_at, "2026-05-14T09:15:00.000Z");
    assert.equal(payload.sessions[0].duration_seconds, 900);
  });
});

test("sweep quarantines malformed task files and continues closing valid stale tasks", async () => {
  await withWorklogHome(async (home) => {
    const env = testEnv(home);
    let current = new Date("2026-05-14T09:00:00.000Z");
    const deps = {
      now: () => current,
      resolveWorkspace: async (cwd) => cwd,
    };

    await handleHook(
      {
        hook_event_name: "UserPromptSubmit",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/OnixServer",
        prompt: "valid stale task",
      },
      env,
      deps,
    );

    await writeFile(path.join(home, "tasks", "broken.json"), "{\"id\":\"broken\"} trailing text");

    current = new Date("2026-05-14T14:00:00.000Z");
    const result = await sweep(env, deps);

    assert.equal(result.closed, 1);
    assert.deepEqual(await readdir(path.join(home, "tasks")), []);
    assert.equal((await readdir(path.join(home, "queue"))).length, 1);

    const badFiles = await readdir(path.join(home, "bad"));
    assert.equal(badFiles.length, 1);
    assert.match(badFiles[0], /^broken\.json\./);
  });
});

test("parallel tool hooks do not corrupt the task file", async () => {
  await withWorklogHome(async (home) => {
    const env = testEnv(home);
    const deps = {now: () => now, resolveWorkspace: async (cwd) => cwd};

    await handleHook(
      {
        hook_event_name: "UserPromptSubmit",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/wakapi",
        prompt: "record parallel tool hooks",
      },
      env,
      deps,
    );

    const hooks = Array.from({length: 40}, (_, index) => handleHook(
      {
        hook_event_name: "PostToolUse",
        session_id: "thread-1",
        turn_id: "turn-1",
        cwd: "/Users/igbenic/Projects/wakapi",
        tool_name: "Bash",
        tool_input: {command: `printf '${"x".repeat(2000)}' # ${index}`},
      },
      env,
      deps,
    ));

    const results = await Promise.allSettled(hooks);
    assert.deepEqual(results.map((result) => result.status), Array(40).fill("fulfilled"));

    const task = JSON.parse(await readFile(path.join(home, "tasks", "thread-1__turn-1.json"), "utf8"));
    assert.ok(task.events.length > 0);
  });
});
