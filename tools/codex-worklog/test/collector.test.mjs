import assert from "node:assert/strict";
import {mkdtemp, readFile, readdir, rm} from "node:fs/promises";
import {tmpdir} from "node:os";
import path from "node:path";
import test from "node:test";

import {handleHook} from "../src/collector.mjs";

const now = new Date("2026-05-14T09:00:00.000Z");

async function withWorklogHome(fn) {
  const dir = await mkdtemp(path.join(tmpdir(), "codex-worklog-"));
  try {
    await fn(dir);
  } finally {
    await rm(dir, {recursive: true, force: true});
  }
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
      {CODEX_WORKLOG_HOME: home, CODEX_WORKLOG_INSTALLATION_ID: "local"},
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

test("PostToolUse records file evidence from apply_patch", async () => {
  await withWorklogHome(async (home) => {
    const env = {CODEX_WORKLOG_HOME: home, CODEX_WORKLOG_INSTALLATION_ID: "local"};
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
    const env = {CODEX_WORKLOG_HOME: home, CODEX_WORKLOG_INSTALLATION_ID: "local"};
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
    assert.equal(payload.sessions[0].last_assistant_message, "Implemented Codex task worklogs.");
  });
});
