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
    assert.equal(payload.sessions[0].summary_hr, "Implemented Codex task worklogs.");
    assert.doesNotMatch(payload.sessions[0].summary_hr, /sync codex worklogs/i);
    assert.equal(payload.sessions[0].last_assistant_message, "Implemented Codex task worklogs.");
  });
});

test("Stop falls back to a clean title from assistant JSON", async () => {
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
    assert.equal(payload.sessions[0].summary_hr, "Review URA migration flow.");
    assert.doesNotMatch(payload.sessions[0].summary_hr, /raw user message/i);
    assert.doesNotMatch(payload.sessions[0].summary_hr, /title/i);
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
        return "Added Codex worklog LLM summaries.";
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
    assert.equal(payload.sessions[0].summary_hr, "Added Codex worklog LLM summaries.");
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
        execCall = {command, args, options};
        const outputIndex = args.indexOf("--output-last-message");
        await mkdir(path.dirname(args[outputIndex + 1]), {recursive: true});
        await writeFile(args[outputIndex + 1], "Generated via Codex summary.");
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

    const queued = await readdir(path.join(home, "queue"));
    const payload = JSON.parse(await readFile(path.join(home, "queue", queued[0]), "utf8"));
    assert.equal(payload.sessions[0].summary_hr, "Generated via Codex summary.");
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
