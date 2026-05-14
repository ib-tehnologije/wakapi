# Codex Worklog Hook

Small local collector for Codex lifecycle hooks.

It stores active task turns under `~/.codex/worklog/tasks`, queues failed submissions under
`~/.codex/worklog/queue`, and submits finished turns to Wakapi:

```sh
export CODEX_WORKLOG_WAKAPI_URL="http://localhost:3000"
export CODEX_WORKLOG_WAKAPI_API_KEY="<wakapi-api-key>"
```

For GUI-launched Codex sessions that do not inherit shell environment variables,
write the same values to `~/.codex/worklog/config.json`:

```json
{
  "wakapi_url": "http://localhost:3000",
  "wakapi_api_key": "<wakapi-api-key>"
}
```

One visible worklog is created for each Codex `UserPromptSubmit` -> `Stop` turn.
If the Wakapi URL or API key is missing, the closed turn is queued and retried by later hooks.

`SessionStart` also runs the stale-task sweeper. The same sweeper can be run from launchd:

```sh
/Users/igbenic/.nvm/versions/node/v22.18.0/bin/node /Users/igbenic/Projects/wakapi/tools/codex-worklog/bin/codex-worklog-hook.mjs sweep
```

By default, tasks with no activity for 240 minutes are closed at their last recorded activity
time and submitted or queued for retry.
