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
