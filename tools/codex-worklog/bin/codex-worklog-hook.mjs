#!/usr/bin/env node
import {readFile} from "node:fs/promises";

import {handleHook, sweep} from "../src/collector.mjs";

async function readStdin() {
  const chunks = [];
  for await (const chunk of process.stdin) {
    chunks.push(chunk);
  }
  return Buffer.concat(chunks).toString("utf8").trim();
}

async function main() {
  if (process.argv.includes("sweep")) {
    await sweep();
    return;
  }

  if (process.argv.includes("flush")) {
    await sweep({...process.env, CODEX_WORKLOG_STALE_MINUTES: "999999"});
    return;
  }

  const input = await readStdin();
  if (!input) {
    return;
  }

  const payload = JSON.parse(input);
  const result = await handleHook(payload);

  if (payload.hook_event_name === "Stop") {
    process.stdout.write(JSON.stringify({continue: true}));
    return;
  }

  if (process.argv.includes("--debug")) {
    await readFile(new URL("../package.json", import.meta.url), "utf8");
    process.stderr.write(`codex-worklog: ${result.action}\n`);
  }
}

main().catch((err) => {
  process.stderr.write(`codex-worklog failed: ${err?.stack || err}\n`);
  if (process.env.CODEX_WORKLOG_STRICT === "1") {
    process.exit(1);
  }
  if (process.argv.includes("Stop")) {
    process.stdout.write(JSON.stringify({continue: true}));
  }
});
