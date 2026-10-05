# Claude Code time tracking ("timebase"): design

Status: **design for approval, no code**. Written 2026-10-04 on this MacBook. Eight
read-only investigators verified the building blocks (~550 tool calls, file:line
evidence); four adversarial reviewers attacked the first draft (66 findings) and two
more re-checked the revision (27 findings); all are folded in. *Verified* = read or
run here. *Unconfirmed* items are collected in §13 and never stated as fact elsewhere
without a "(§13)" tag.

Segment lifecycle, used everywhere below: **provisional** (an agent of the thread may
still be running) → **settled** (closed, reconciled, no open agent) → **final**
(settled and: session ended, or ≥ idle limit of silence in every source, or for a
crashed session: process dead + silence, at most 24 h after the close). Grunf sees
settled segments; Wakapi sees final ones (so Wakapi can lag up to 24 h after a crash);
the band shows everything, labelled.

## 0. Decisions

| Question | Decision |
| --- | --- |
| Canonical record | Local append-only ledger per Mac (`~/.timebase/`), machine id + session id in every row. Wakapi and Grunf are projections, never the record. |
| Live recorder | The mod (only it sees interrupts, prompt origin, typing bursts, phone attach/detach, agent completions). Writes write-once chunk files. |
| Witness | Classic command hooks in the same plugin, out of process, O_APPEND rows, own timestamps. Survives a hooks-worker crash and the remote mod kill switch. |
| History / reconciliation | Transcript parsing for every session (nightly and on demand); also the only recorder when hooks and mods are both off. |
| Reconciliation | Marks merged **per turn**, not totals per session: facts several sources see → most conservative wins, disagreement flagged; facts one source sees → taken and labelled. One segmentation feeds all consumers. |
| Wakapi | Synthetic heartbeats from **final** segments (`type=file`, 120 s spacing; Claude = `ai coding`, you = `code reviewing`), posted by an out-of-process sync job with the key from `~/.wakatime.cfg`. Only heartbeats reach summaries → Odoo (verified). |
| Wakapi per-project number | Wakapi credits each gap to the previous heartbeat's group, so parallel threads on different projects get a **wall-clock split** per project. That is what Odoo bills. Full per-thread billing per client would need a non-heartbeat path (not in v1; §1.8). |
| Grunf | Same per-thread numbers, **settled** only. One existing AI/Codex log per thread whose cumulative = **Claude time + my time** (decided 2026-10-05); the audit note carries the split. No server change. |
| Mod UI | Band `Claude 1h12m · You 34m · Total 1h46m` + state; **Report to Grunf** button prefills `/logtime <id>` (the API requires Croatian notes the model writes). Phone: pane on attach, unverified. |
| Reuse | Patterns from codex-worklog, not its code; `time` script replaced by `timebase session --json`, a superset of its output (`taskId`, `cumulativeDurationMs` unchanged). |
| Distribution | New private GitHub repo `igbenic/timebase` = marketplace `igor-tools` with one plugin `timebase` (mod + witness + CLI + launchd job). (`claude-time` is a reserved plugin-name prefix; validate refuses it, verified.) |
| Surface | No key in the plugin. The mod has no side effect outside its own ledger directory and its UI, except `$.prompt.fill` on your button press (full call list in §10). |

## 1. Decisions taken (Igor, 2026-10-05) and defaults

Answered by Igor:

1. **Prvi Maj / Tehnoline work runs on the Macs in Claude Code** from go-live; this base covers it. Paperclip needs no export.
2. **Gap 2026-09-23 → go-live is billed manually** from Paperclip's issue history; nothing to automate.
3. **Grunf gets one log per thread, as today (AI/Codex), whose cumulative = Claude time + my time.** No separate human log, no server change; the audit note shows the split.
4. **Idle rule: Wakapi rule, 10 min** — pauses ≥ 10 min count nothing; frozen at first sync.

Defaults chosen by me (technical; say so if any is wrong):

5. Repo `igbenic/timebase`, private; ledger at `~/.timebase/`; scratch (no-repo) sessions are tracked but never synced.
6. Odoo bills one project total (Claude + you), as today.
7. Button prefills `/logtime <id>`; permission waits are measured by hooking `tool.call` for every tool, observe-only.
8. Routing: a thread on an Odoo-billed project (Prvi Maj, Tehnoline) is **never offered to Grunf** (`deferred: odoo-billed`); Grunf tickets are Onix work. Parallel threads on two client projects get Wakapi's wall-clock split.
9. Claude heartbeats use the same Wakapi machine name as VS Code (`igbenic.local`); phase-2 test 7 checks whether Grunf's own Wakapi sync picks them up and, if it does, the sync switches to a separate machine name.
10. Wakapi heartbeat timeout: assumed 10 min; test 6 proves it by posting test heartbeats and reading the summary back before any real sync (a 2-min legacy default exists in the code, `models/user.go:20`).
11. The 20 stray `claude-code/2.1.286` heartbeats of 2026-10-02: the sync warns if any non-timebase `claude-code` UA appears on a day it syncs.
12. The Grunf user behind the MCP key, its policy and any `WakaMachines` allowlist are read off the first real preview in test 7, not asked.

## 2. Verified facts that shaped the design

1. **Engines.** Desktop sessions run engine **2.1.286** (type file header "EARLY ACCESS"); terminal CLI and the Remote Control supervisor run **2.1.289**. 2.1.286 loads mods by default (rollout flag defaults on; the env var only overrides a served-off state — read from the binary). All 11 sessions on this Mac are desktop; no RC session has run here.
2. **Only heartbeats are time in Wakapi.** Codex task sessions are a separate table exposed only via `routes/api/codex_tasks.go:41` (worklogs) and the review queue; durations read heartbeats only (`services/duration.go:191`). Odoo's `billing_external_wakapi` reads project-filtered day summaries and the fork's commits endpoint, nothing else (`wakapi_handler.py:179-247`). **`odoo-addons/docs/wakapi/*.md` describe `wakapi_billing`, deleted 2026-02-02; the live path is `billing_external_wakapi` + `billing_external_base`.**
3. **Wakapi's algorithm is one timeline per user** (`services/duration.go:188-267`): each gap shorter than the timeout (code default 10 min, `models/user.go:19`; production value inferred; §1.10) is credited to the *previous* heartbeat's group; a gap ≥ timeout counts 0. Dedup hashes entity, type, category, project, branch, language, is_write, **time**; inserts `ON CONFLICT DO NOTHING`; no per-heartbeat delete. Production runs pre-merge `ba6f547`; master's `type=app + ai coding` exclusion is not deployed and not needed here (our heartbeats are `type=file`). Production's UA parser is a single regex requiring a trailing `<name>-wakatime/<ver>` token.
4. **Turn intervals are not Claude time.** 829 of 1,397 turn-minutes on this Mac were Claude waiting for you inside the turn (one overnight 790-min `ExitPlanMode`); 143 of 327 agent-minutes lie outside any main turn. Permission waits are observable only live, and not via `classic.PermissionRequest` (its `next(e)` returns the hooks' decision, not your answer).
5. **Events carry no timestamps.** `turn.start` = `{text, turnId}`, not raised for agents; `turn.complete` has `durationMs`, `isAborted`, `reason`, `agentId`. `prompt.edit` fires per burst/paste, origin `composer` only. `prompt.submit` origin `sdk` = "the SDK host's own turn"; the desktop app is an SDK host. In desktop transcripts `promptSource: sdk` and `origin.kind: human` are **distinct fields**; typed prompts carry `origin.kind: human` (verified).
6. **Mods API**: `$.ui.status` renders `<plugin>: <text>` (binary; web docs add ⚠ — §13); no 50 ms `prompt.edit` budget (10 s own-time; `session.end` chain shares 1.5 s); `$.fs` has no append, no `~` expansion, 4 MiB cap; `$.store` is one machine-shared JSON file (web docs: non-atomic, purged after `cleanupPeriodDays` — §13); `$.session.repo().root` is the *main* worktree's root; in-session `/resume` ends the session like `/clear` (no `session.start`); `ui.press` has no `origin` field, but `$.ui` has no press op, so a press is a person's act by construction; one `hooks.json` may carry both `modules` and classic `hooks` (validate accepted it).
7. **Remote Control**: AbovePrompt renders on terminal + desktop only; `Pane` is raised on every surface incl. `mobile` (Button yes, Input no), placement by the phone unverified; docs say drawings appear only in the Mac terminal; a pre-GA report says mod `/commands` are denied over RC.
8. **Grunf** `ai-time-log`: checkpoint keyed `(tenant, CodexTaskId, CodexCumulativeDurationMs)`, serializable re-preview + SHA-256 fingerprint; preview statuses `READY`, `NO_NEW_TIME` (cumulative = checkpoint), `CUMULATIVE_DURATION_REGRESSED` (cumulative < checkpoint — the checkpoint is the maximum ever stored, so this is sticky); Type `AI/Codex` and `Invoice=false` forced; **Billable from policy, no request flag** (when billable: `BillableTime` = ceil 15 min, minimum 15 min per log); `Date = SnapshotAtUtc`; `claude:<lowercase GUID>` ids; Croatian `note`/`technicalNote` **required**; no human-time field. The `logtime` "user-typed" rule is prose only; `ostalo` = its monthly catch-all ticket. Grunf's `WakaTimeSyncService` also pulls your Wakapi heartbeats per mapped project into attachable, invoiceable worklogs, subtracting only Codex task seconds.
9. **Paperclip** = upstream open-source agent orchestrator, self-hosted in k3s, running **Codex** agents in pods for exactly the Prvi Maj / Tehnoline projects since 2026-09-25; its billing router stamps issue codes for Paperclip's internal token-cost ledger and exports nothing. Wakapi shows **0 h on those projects since 2026-09-23**.
10. **This Mac's `~/.claude` dates from 2026-10-02**; default transcript retention 30 days (desktop sessions exempt while in the app). The `time` script counts finished sessions as still running and ignores agent files; `codex-worklog` turns agent turns into 4-hour-stale overlapping worklogs (1.77× over-count today). Neither is reusable as code. `/logtime` and `time` are not loaded in Claude Code on this Mac (stale "My Uploads" sync).

## 3. Architecture

```
 Claude Code session (terminal 2.1.289 / desktop 2.1.286 / RC 2.1.289)
 ├─ mod (hooks worker)  → ~/.timebase/inbox/<session>/<chunk>.json   (write-once)
 └─ witness (classic hooks, one process per event) → ~/.timebase/witness/<session>.jsonl (O_APPEND)
 ~/.claude/projects/**/*.jsonl ─┐
                                ▼
 timebase CLI (node, no deps; stable copy outside the plugin cache)
   compact   inbox (cursor) + witness (byte offset) + transcript (re-parsed, deduped by uuid)
             → events/<session>.jsonl (canonical, append-only)
   derive    marks → segments → sessions → rollups (rebuildable)
   reconcile per-turn merge, flags · sync  final segments → Wakapi heartbeats
   session   per-thread JSON for logtime / band · summary · verify · backfill · install
        │                       │                        │
        ▼                       ▼                        ▼
 Wakapi summaries + commits   Grunf ai-time-log        band / pane
 → Odoo billing_external_     via /logtime (skill,     (mod reads derived/
   wakapi (unchanged)         MCP tools, checkpoint)   sessions + live state)
```

## 4. Definitions

**Thread** = session id (`$.session.id()` = transcript file name). `claude --resume
<id>` keeps it; in-session `/resume` and `/clear` end the session (reasons `resume` /
`clear`) and continue under another id without a new `session.start`; forks mint a
new id. Grunf's `claude:<session>` task id means the same thread.

**Marks** are timestamped facts; **segments** are derived from marks; raw marks are
kept so a rule change is a recompute (subject to the projection freeze, §7.1).

### 4.1 Claude time (per thread)

Union of:

- **Main turns** `[prompt accepted → turn.complete]`, any origin, **minus waits**
  (§4.3). Interrupted turns end at the interrupt (`turn.complete{isAborted}`).
- **Agent runs**: end = `turn.complete{agentId}`; start = the latest of first
  `turn.step{agentId}`, `classic.SubagentStart`, `complete − durationMs` (disagreement
  beyond tolerance flagged); **minus waits keyed to that `agent_id`** (background
  agents raise permission dialogs too); unioned with main turns. Work outside any main
  turn still counts.
- Compaction inside a turn is Claude time.

### 4.2 My time (per thread)

Wakapi rule (decided, §1.4): consecutive **activity marks** closer than the idle
limit (10 min, config) are joined; a gap ≥ limit contributes nothing. (Capped
alternative: each gap contributes `min(gap, limit)`.) Then **clip**: minus the union of
the thread's Claude segments, so non-overlap is derived. Segments are *provisional*
while any agent of the thread is running.

| Mark | Source |
| --- | --- |
| main turn completed | `turn.complete` / `Stop` / transcript |
| wait started, wait ended | §4.3 |
| typing burst (start, end) | `prompt.edit` — terminal and desktop composer (verified on 2.1.286, §14); phone unconfirmed (§13) |
| prompt accepted with human origin | `prompt.submit` (rule below); transcript `origin.kind: human`; classic `UserPromptSubmit.source: user` when present |
| `auto-continuation` submit instant | a person's UI action, not typed: the turn is non-human, the instant is a mark |
| phone / app client joined or left | `session.attach` / `session.detach` |
| you pressed / typed into the mod's own elements | `ui.press` / `ui.input` on this plugin (never the text) |
| session started by you | `session.start` only when `isInteractive` and surface terminal/desktop; bridge/SDK/scheduled sessions start at the first attach or human prompt |

**Human origin rule**: a prompt is yours unless its origin is one of
`task-notification, scheduled-trigger, auto-continuation, plugin, peer,
peer-send-message, projects-relay, channel, coordinator, observer, observer-activity,
slack-ping`; `composer` and `bridge` are yours; `sdk` is yours only when
`$.session.surfaces()` includes `desktop`; `unclassified` when surfaces include
`desktop` or `isInteractive` was true. Human-ness is reconciled against the
transcript's `origin.kind` (authoritative; verified `human` for desktop-typed prompts)
and the witness's `source`. §12 test 1 settles what the desktop sends live.

**Why the Wakapi rule.** This Mac's 92 gaps: uncapped 2,282 min; Wakapi rule 244 min;
capped 424 min. The 180-minute difference is time *assumed* after your last signal.
The Wakapi rule credits the whole gap between two marks closer than the limit (as
Wakapi does for editor heartbeats) and nothing beyond the last mark; it materializes
exactly as heartbeats; the capped rule mismatches Wakapi for gaps of 10–20 min.

### 4.3 Waits (Claude waiting for you inside a turn or agent run)

| Wait | Start | End |
| --- | --- | --- |
| `AskUserQuestion`, `ExitPlanMode`, `$.ui.ask` | `tool.call` seen | tool result (`await next(e)`, recorded in `finally`; on `next.signal` abort → `interrupted`) |
| permission dialog | `classic.PermissionRequest` (primary) or `classic.Notification` whose type names a permission prompt (name unconfirmed on 2.1.286, §13) | end of the enclosing `tool.call` span **minus** `classic.PostToolUse.duration_ms` (execution excludes prompt time per the type file); without `duration_ms` the whole span is a wait, flag `waits: approximate` |
| rejected plan/question | `PreToolUse` | witness: no `PostToolUse` on rejection → closed at the next `PreToolUse`/`Stop`, `closed_by: inferred`; mod and transcript see the real end |

A wait is not Claude time; its start and end are my-time marks. Example: 40-min
turn, question open from minute 5 to 35 → Claude 10 min; you 0 (Wakapi rule) or 10
(capped); heartbeats (epoch origin at minute 0) `0,2,4,5 | 35,36,38,40` → 10 min.
Transcripts show only rejected dialogs, so backfilled sessions carry `waits: partial`.

### 4.4 What cannot be known

Reading/thinking without a signal for ≥ idle limit; typing that produces no
`prompt.edit` (phone; the desktop composer does produce it, §14); permission waits in
backfilled sessions; anything after a crashed session's last
evidence; work outside Claude Code (editor time is Wakapi's own; Paperclip runs are
not tracked).

### 4.5 Totals and which number goes where

- Per thread: `claude_ms`, `human_ms`, `wait_ms` (informational), `total = claude + human`.
- Rollups (project, client, day, machine, overall): **sum of thread totals** and
  **interval union**, side by side, so parallel runs stay visible.
- **Wakapi/Odoo**: heartbeats → Wakapi's per-user union, split per project by
  interleaving (§7.1 example). Odoo bills that per-project number.
- **Grunf**: the per-thread sum (Claude + you) of the reported thread. On this Mac the overlap with
  other threads on the same ticket is visible at report time (`session --json` lists
  them; the preview shows the overlap minutes); cross-Mac overlaps only via `verify
  --all-machines` after a merge.
- **Band**: the thread's sum and today's union across this Mac's threads.

### 4.6 Clock

Each mark stores wall time, a monotonic reading and a **clock domain** (hooks-worker
instance for the mod; boot id for witness/CLI). Within a domain, wall-vs-monotonic
divergence > 30 s = `clock.jump`: the spanned segment is split and the span excluded
and flagged. Across a domain change (reload, worker respawn, reboot) nothing is
excluded. Sleep/hang: a missed 60-s tick (> 3 min) while a turn or agent runs (tick
behaviour across sleep unconfirmed, §13; fallback: the boot-id/wall-vs-mono check).

## 5. Project, branch, client

- Session tree: `$.session.root()` (follows worktree moves); branch from
  `<root>/.git` — a file `gitdir: <path>` for worktrees → `<path>/HEAD`, else
  `<root>/.git/HEAD`; re-read on `classic.CwdChanged` / `WorktreeCreate`. Repo for the
  project name: `$.session.repo().root`; CLI: `git rev-parse`. `derive` rewrites
  `<repo>/.claude/worktrees/<name>` to the base repo as a string (raw `cwd` stays in
  the event row) and reads `.wakatime-project` from the base repo.
- **Project name** = `.wakatime-project` in the base repo if present (the convention
  your `~/.wakatime.cfg` already uses), else the repo basename → same names as the
  Odoo maps. `verify` flags projects seen on one machine only.
- A my-time segment takes the project of the turn it follows.
- **Client map** `clients.json`: `project → client` plus Paperclip codes
  (`PRVI-MAJ:ODOO`, `PRVI-MAJ:WASTEWISE`, `PRVI-MAJ:WW-ODOO`, `TEHNOLINE:EVCHARGE`).
  It drives **routing** (§1.8): a thread on an Odoo-billed project is never offered
  to Grunf (`session --json` answers `deferred: odoo-billed`; the AI endpoint has no
  billable flag, so "not at all" is the only enforceable rule). The decision is shown
  in the preview and stored with the report.
- No git repo (desktop scratch workspaces): `scratch:<name>`, not synced, listed as
  unassigned. Reassignment = append to `assignments.jsonl`; reassigning an
  already-synced session is `correction-needed` (§7.1).

## 6. Record schema

```
~/.timebase/
  machine.json        {machine_id, hostname, created_at}
  config.json         idle_limit_s 600, idle_rule, heartbeat_spacing_s 120, projection_version,
                      wakapi_user (checked on every sync), sync.unassigned skip
  clients.json
  inbox/<session>/<seq>-<ts>-<nonce>.json   mod chunks, write-once ($.fs.exists first)
  witness/<session>.jsonl                   concurrent O_APPEND, one write ≤ 4 KiB per row, no seq
  events/<session>.jsonl                    canonical, append-only
  compact/<session>.cursor.json             consumed chunk names + witness byte offset (tmp+rename)
  derived/sessions/<session>.json, derived/days/<day>.json   rebuildable
  assignments.jsonl   reports.jsonl   sync/queue/   sync/dead/
```

Event row `{v, ts, mono, domain, machine, session, src: mod|witness|transcript, seq?,
pid?, kind, turn?, agent?, data}`; dedupe key `(src, session, kind, ts, turn|agent|pid)`.
Kinds: `session.start` (`cwd, surface, isInteractive` from the event; `source` from
`classic.SessionStart`; `root, repo, project, branch, version` computed),
`session.end{reason}`, `prompt.submit{origin,human,queued_ts?}`,
`prompt.edit{start,end,edits}` (never text), `turn.start`, `turn.complete{duration_ms,
aborted,reason}`, `agent.start`, `agent.complete{duration_ms}`, `wait.start{kind,tool,
agent?}`, `wait.end{outcome,exec_ms?}`, `attach`, `detach`, `compact`, `idle_prompt`
(diagnostic only), `tick`, `clock.jump`, `cwd`, `ticket.seen{id,how}`,
`witness.health{mode}`, `reload`.

Segment `{session, machine, id, kind: claude|human|wait, start, end, turn?, agent?,
project, branch, basis, closed_by: event|sweeper|transcript|inferred, provisional,
settled, final, synced: {at, start, end, instants}}`. Later evidence may **extend** a
final segment (additive heartbeats, new Grunf delta; report row `extended` keyed by
segment id), never shrink it.

Session (derived): identity, totals per source where observable, chosen totals,
coverage per source (`full | partial from <ts> | none`), counts, flags (§7.4),
reported state.

Report row `{ts, consumer, session, segment_ids, boundaries, ticket?, series:
ai|human, cumulative_ms, delta_ms, projection_version, idle_rule, sources, remote_id?,
fingerprint?, status, routing}`.

`$.store` holds nothing billing-related (at most `{ledger_dir}`); per-session seq
lives in `$.state` (CAS) and is re-seeded from `$.fs.list(inbox/<session>)`.

## 7. Capture and consumers

### 7.1 Wakapi (→ Odoo)

Sync job (launchd, `StartInterval 300` + `RunAtLoad` + a calendar minute array so it
fires after wake; backfill in a separate job). For every **final** `claude`/`human`
segment with a project: heartbeats at the exact start, every absolute epoch multiple
of 120 s inside, and the exact end; `type: file`, `category: ai coding | code
reviewing`, `project`, `branch`, `entity: <repo>/.claude/sessions/<session-id>`,
`language: Claude Code`, `is_write: false`; UA
`wakatime/v1.0.0 (darwin-<kernel>-arm64) go1.0 claude-code/<engine> claude-code-wakatime/<timebase-v>`
(parses on the deployed regex **and** on master — tested against both parsers);
`X-Machine-Name` = `hostname` (`igbenic.local`, as vscode-wakatime) unless test 7 says
separate (§1.9). Before each run: `GET /users/current` username must equal
`config.wakapi_user`, else refuse + dead-letter + `summary`/`$.ui.status`. The bulk
endpoint answers 201 if *any* item was accepted, with per-item statuses in the body:
a segment is synced only when every instant returned 201; failures are dead-lettered
individually.

**Interplay** (verified): 120 s < timeout, so a segment is credited in full; the tail
after our last heartbeat is credited to our group if any heartbeat (VS Code, other
Mac, other thread) follows within the timeout — the same tail credit the editor plugin
gets. Two threads on different projects at once: A at 0,2,4,6, B at 1,3,5 → project
A 3 min, project B 3 min — the total is the union, each project a **split**. IDE
heartbeats on the same project while Claude works are not double counted.
Idempotency: `time` is in the hash and interior instants are absolute multiples, so a
boundary moved by seconds re-inserts only boundary rows.

**Projection freeze and corrections.** `idle_rule`, `idle_limit_s` and spacing form
`projection_version`, recorded per report; the sync refuses to run under a different
version than last synced for the machine. No per-heartbeat delete exists: a
project/branch/category change or shrink after sync is `correction-needed` and
surfaced; planned remedy: fork-only `DELETE /api/heartbeats?entity=…` (optional
server change), until then manual.

**Odoo** requests project-filtered summaries, computed live from heartbeats, so late
sync is safe for Odoo; the dashboard and `machine=` summaries are cached/aggregated and
are not the billing number (verified 2026-10-05: unfiltered views omit or under-count
backfilled heartbeats entirely — fork duration cache, §14). Odoo sums all categories
per project (§1.6). Max heartbeat age 4320 h: older backfill stays ledger-only
(`skipped.too_old`).

**`timebase verify <day>`**: pulls the day's heartbeats, replays Wakapi's algorithm
locally (the investigator's replay matched production exactly), compares with
`/api/summary?from=D&to=D+1&project=&recompute=true` (`to` is exclusive and `total`
is seconds on production, §14), reports the tail credit separately, and reads
one synced heartbeat back to assert editor/OS parsed.

### 7.2 Grunf (`logtime`)

- `timebase session <id> --json`: `taskId: claude:<session>`, `aiCumulativeDurationMs`
  (settled `claude_ms`), `humanCumulativeDurationMs` (settled `human_ms`),
  **`cumulativeDurationMs` = their sum** (what `logtime` sends, per §1.3), audit list
  (turns, agent runs, waits, my-time segments, flags), overlapping threads on the same
  ticket, routing, projection version, and `report: ready | deferred(reason)`.
  `logtime` refuses to create on `deferred` unless `--force`; its audit note states
  "Claude Xh Ym · Igor Xh Ym". The `time` skill becomes a wrapper.
- **Settled only, never a running turn.** `REGRESSED` is sticky and an over-report is
  irreversible without a manual Grunf edit, so the command offers
  `max(last_reported, settled_now)` only if the delta is positive and no `disagree`
  flag is open; it stores rule and version per report and answers `rule-changed`
  under another version unless `--rebase` (writes an explicit correction note, no
  delta).
- **Billable floor** (when policy makes the log billable: ≥ 15 billable minutes per
  log, ceil 15): `deferred` unless the thread has ended or the delta ≥ 15 min, and at
  most one report per thread per ticket per day. Billable/BillableTime are in the
  preview DTO and the fingerprint and are shown. `Date = SnapshotAtUtc`: a thread
  spanning days is logged on the report date (stated in the audit note).
- **No server change.** The existing endpoint takes one cumulative; it now carries
  Claude + my time. Only the `logtime` SKILL and the `time` wrapper change (§10.4).
- **Not through two doors**: server checkpoint + `reports.jsonl`; and because Grunf's
  `WakaTimeSyncService` would surface our heartbeats as attachable worklogs for mapped
  projects, test 7 checks whether it does; if so the sync switches to a separate
  machine name outside your `WakaMachines` allowlist (§1.9). `verify` lists worklogs
  overlapping `claude:` logs.
- **Ticket inference**: explicit id/URL in the request wins; else the thread's most
  recent `ticket.seen` (literal Grunf URL in a human prompt, or a successful ticket
  read); never customer name, cwd or similarity; ambiguity → ask; `ostalo` as today.

### 7.3 Mod

All event hooks are `record(e); return next(e)`:

| Hook | Records |
| --- | --- |
| `session.start` | `session.start`, `cwd`; restores the id's totals from the ledger; re-arms timers; self-test (write + read one chunk, else `$.ui.status`) |
| any event whose `$.session.id()` differs from the last known id | closes the old id (after `session.end{clear|resume}`), loads the new id's ledger |
| `prompt.submit` | `prompt.submit{origin, human}`; `ticket.seen` when the text holds `/support/edit?id=` or a bare ticket per logtime rules (text not stored) |
| `prompt.edit` | burst accumulator (`Date.now()`/`performance.now()` only; no awaited `$` call on the keystroke path); one row per burst |
| `turn.start` / `turn.step` (agent first step) / `turn.complete` | `turn.*`, `agent.start`, `agent.complete`; flush |
| `tool.call` — all tools (§1.7) | span start/end; `wait.*`; `ticket.seen` from `mcp__plugin_onix-support_onix-support-ticketing__onix_support_get_ticket` / `…resolve_ui_url` results; end in `finally`/abort |
| `classic.PermissionRequest`, `classic.Notification`, `classic.PostToolUse{duration_ms}`, `classic.SubagentStart/Stop` | permission wait start, execution time, agent type |
| `session.attach` / `session.detach` | `attach` / `detach` (`detach{reason:end}` is buffered into the `session.end` chunk) |
| `session.compact` | `compact` |
| `session.end` | `session.end{reason}` with `e.sessionId`; exactly one small chunk write |
| `$.clock.every(60 s)` while a turn or agent runs | `tick`, `clock.jump` |
| `ui.render` (AbovePrompt, Pane), `ui.press`, `ui.input` (`{plugin: timebase, element: ticket}`), `command.run` | display; marks; `$.prompt.fill` |

Flush: a new chunk on `turn.start`, `turn.complete`, `agent.*`, `wait.*`,
`session.end`, and at most once per tick; a torn chunk is quarantined; names cannot
repeat (`exists` + nonce).

**UI.** (1) Band (AbovePrompt, terminal + desktop): `Claude 1h12m · You 34m · Total
1h46m · ● working 3m12s | ◌ waiting for you | ◔ agent running | ⏸ idle`, then `today
3h05m across 3 threads (union)` and flags; `$.clock.every(30 s)` + `$.ui.invalidate`.
(2) **Report to Grunf**: shows the settled unreported delta, the inferred ticket (or
an Input for id / URL), then `await $.prompt.fill({ text: '/logtime <id>' })` (toast
if `!isFilled`); you press Enter; `logtime` runs preview → create. Reason: the API
requires Croatian `note`/`technicalNote`; a code-only button would invent invoice
text. This keeps the human-typed rule and the guard and keeps `$.mcp.call` out of the
mod (§1.7). (3) Phone: a `Pane` opened on `session.attach{surface: mobile}`, closed on
detach, with readout + Button; `isPlaced: false` → no phone UI; `/timebase` command on
terminal/desktop; all phone behaviour unverified (§13); the guaranteed phone path is
typing `/logtime <id>` once onix-support with `logtime`/`time` is installed on both
Macs (§10.4). (4) `$.ui.status` only for flags.

### 7.4 Witness, transcript, reconciliation

**Witness** (classic hooks in the same `hooks.json`): `SessionStart`, `UserPromptSubmit`
(records `source` when present, never the prompt), `Stop`, `SubagentStart`,
`SubagentStop`, `SessionEnd`, `PreToolUse`/`PostToolUse`/`PostToolUseFailure`
(matcher `AskUserQuestion|ExitPlanMode`), `PermissionRequest`, `Notification`,
`PreCompact`/`PostCompact`, `CwdChanged`; exec form (`"command": "<wrapper>", "args":
["${CLAUDE_PLUGIN_ROOT}/bin/witness"]`). The wrapper stamps the time first, resolves
node in a fixed order (`TIMEBASE_NODE`, the data-dir copy, `/opt/homebrew/bin`,
`/usr/local/bin`, newest `~/.nvm/versions/node/*`), else writes a pure-bash row `{ts,
event, session_id, cwd}` plus `witness.health{mode: bash}` so the reconciler never
treats that session as a full witness source. One write per row, ≤ 4 KiB. The witness
cannot see interrupts (Stop does not fire) or rejected-dialog ends.

**Transcript** (`timebase backfill`, and the third source in every nightly reconcile):
sort by `timestamp` (files are not chronological); turn start = `user` with
`turnPosition` and not `isCompactSummary`, human iff `origin.kind == human`; prompt
accepted = the prompt record, typing/submit mark = the matching `queue-operation
enqueue`; turn end = `system/stop_hook_summary` if present, else last assistant/
tool_result, else the interrupt row; waits = `tool_use → tool_result` spans of
`AskUserQuestion`, `ExitPlanMode`, rejected tools; agent segments from
`<session>/subagents/**/agent-*.jsonl`; project/branch per turn from `cwd` +
`gitBranch`; dedupe by `uuid` across files. `cost-state.totalAPIDuration` ≤ sum of
Claude segments and union ≤ wall time, else flag. Retention: `cleanupPeriodDays: 400`
on this Mac (≈45 MB/day; Mac mini unconfirmed, §13); the ledger is the long-term
evidence.

**Reconciliation** per turn, agent run and wait: for a fact several sources observe
(prompt accepted, turn end, agent start/end) take the most conservative boundary
(latest start, earliest end), flag `disagree:<src>` beyond max(2 min, 5 %); for a fact
one source observes (typing, attach, interrupts, permission waits, rejected-dialog
ends) take it, label `single-source:<src>`. A source that stops mid-session gets
`coverage: partial from <ts>` and is ignored after that point. Turn existence:
transcript authoritative. Flags: `witness-silent`, `mod-silent`, `transcript-only`,
`sweeper`, `inferred`, `clock`, `waits: partial|approximate`, `unassigned`,
`non-human-turns: n`, `interrupted: n`, `agents: n`, `plugin-version-skew`.

## 8. Rollups: `timebase summary`

`timebase today | yesterday | week | month | --from --to`, `--by thread|project|
client|day|machine`; rows show `claude`, `you`, `sum`, `union`, `threads`, `flags`.
`--all-machines` merges the other Mac's ledger (one-way rsync/ssh pull set up by
`install`; sessions never span machines, so the merge is a union by `(machine,
session)`), else reads Wakapi per-machine summaries and says so. `verify
--all-machines` lists machines seen in Wakapi vs the ledger with last-heartbeat age.

## 9. Edge cases

| Case | Handling |
| --- | --- |
| Crashed / killed session | Chunks and witness rows are on disk; sweeper closes at the last evidence across sources (`closed_by: sweeper`); final per the lifecycle box once the process is dead (pid from `~/.claude/sessions/<pid>.json`, verified by `ps -o command=` containing `claude`; file survival after a crash unconfirmed, §13) and silent. |
| Interrupted turn | Mod `turn.complete{aborted}`; transcript interrupt row; witness lacks it. |
| `/clear`, in-session `/resume` | `session.end{clear|resume}` closes the old id; next event under the new id loads that id's ledger (resume) or starts fresh (clear). |
| `claude --resume`, `--continue` | Same id, `session.start` fires, totals restored. |
| Fork / `/branch` | New id, new Grunf task id; transcript dedupe by `uuid` (copying unverified). |
| Remote Control | Supervisor runs 2.1.289 with the same `~/.claude` → user-scope plugin loads (inferred); origin `bridge`; no typing marks; presence from attach/detach; no `session.start` mark; zero-turn sessions dropped at compaction. |
| Compaction | `compact` mark; Claude time; same id. |
| Sleep / clock change | §4.6. |
| Hooks failing silently | `witness-silent` / `mod-silent` / `coverage: partial`; self-test at `session.start`; `witness.health`. |
| Mods off (`--safe-mode`, `--bare`, `disableAllHooks`, `allowManagedHooksOnly`, untrusted workspace, diskless cloud, remote kill switch, worker crashed 3×) | Witness continues where only the mod is off; where both are off, nightly transcript parsing yields `transcript-only` (`waits: partial`); diskless sessions are not recorded at all. |
| Agent / background time | `agent.start`/`agent.complete`; agent-keyed waits; tick while agents run; "agent running" in the band; agent files for backfill. |
| Parallel sessions, two Macs | Per-thread sums separate; rollups show sum and union; Wakapi splits wall-clock per project; `verify --all-machines`. |
| No project folder | §5. |
| Wakapi offline / wrong user | Queue with backoff; 4xx and per-item failures dead-lettered; user check before every sync. |
| 1.5 s exit budget | Every boundary already flushed; `session.end` = one chunk; witness `SessionEnd` = one append. |
| Same thread to Grunf twice | Server checkpoint + `reports.jsonl`; settled delta only; `deferred` gate. |
| Non-human prompts | Origin recorded; Claude time; `non-human-turns: n` keeps loops and scheduled runs visible. |
| Plugin version skew (RC listeners keep the old plugin until the supervisor restarts; `/reload-plugins` is terminal-only) | Compactor accepts any `v` ≤ current; `summary` warns on two versions per machine; `install --refresh` + supervisor restart documented. |

## 10. Distribution, security, phase-2 work

1. **Repo** `igbenic/timebase` (§1.5): `.claude-plugin/marketplace.json` (marketplace
   `igor-tools`), `plugins/timebase/{.claude-plugin/plugin.json, hooks/hooks.json,
   hooks/register.tsx, types/index.d.ts, bin/, lib/, tests/, launchd/, README.md}`.
   Per Mac: `claude plugin marketplace add …`, `claude plugin install timebase@igor-tools`
   (user scope), then `timebase install` (copies the CLI to `${CLAUDE_PLUGIN_DATA}` +
   `~/.local/bin/timebase` — the plugin cache path changes per version — writes the
   launchd plist with an absolute node path and `EnvironmentVariables.PATH`, and
   `config.json`). After `claude plugin update`: `timebase install --refresh` and
   restart the RC supervisor; the mod's self-test compares plugin and CLI versions.
2. **Observe-only proof.** Expected `claude plugin validate` hooks: exactly §7.3;
   expected calls: `$.clock.{now,every}`, `$.session.{id,root,repo,surfaces,version}`,
   `$.env.get` (reads `HOME` only), `$.fs.{read,write,list,exists}`, `$.state.*`,
   `$.ui.{resolve,status,invalidate,open,close,toast}`, `$.command.register`,
   `$.prompt.fill`. Forbidden and absent: `$.prompt.submit`, `$.tool.call`,
   `$.session.append`, `$.http`, `$.process`, `$.mcp.call`, `$.env.set`. The classic
   `hooks` block is shape-checked but not listed by validate; both outputs go in the
   README. A test asserts every hook returns the engine's result unchanged. The mod
   reads only `.git/HEAD`/`gitdir`, `.wakatime-project` and its own ledger.
3. `userConfig`: none required (`ledger_dir` optional absolute path). Nothing
   third-party is installed.
4. **Work breakdown**: (a) `timebase` CLI (node ≥ 22 ESM, no deps); (b) witness
   wrapper + classic block; (c) mod + `types/` + `claude plugin test` suite + validate
   output; (d) `time` wrapper and `logtime` SKILL (cumulative = Claude + my time,
   audit split, `deferred` gate; PR in onix-mcp), and publishing onix-support with
   `logtime`/`time` to both Macs (verify `/logtime` loads in an RC session);
   (e) Odoo `commit_author_filter` pass-through if wanted (§11), optional fork
   `DELETE /api/heartbeats?entity=`; (f) docs: README, this file, refresh
   `odoo-addons/docs/wakapi/`, Odoo cut-over runbook.

## 11. Migrating Prvi Maj and Tehnoline off Sheets; the Paperclip gap

Verified: the Odoo Wakapi path is functional in code and CI; billing is config-driven
(`contract.report.block` + one active `wakapi.project.map` per Odoo project;
`tracking_valid_from` rejects earlier periods); the Sheets provider has immutable
month claims; the Wakapi provider is `mutable_latest` (drafts recompute). Live config
(Tehnoline contract 28 / line 40 formula, Prvi Maj contract 3 fixed EUR 300
`specification_only`, draft 300) is not visible from the repos.

1. On a billing-period boundary, per client: deactivate Sheets blocks and bindings;
   re-activate Wakapi blocks per real project; one active map per project;
   `tracking_valid_from` = cut-over period start; Tehnoline variable line → Wakapi
   metric; Prvi Maj fixed line + Wakapi notes if that is the contract. Check the first
   draft's generated lines before posting; no cross-provider claim exists.
2. **Commits**: `commit_author_filter` is a bare field nobody reads (the fork's
   commits endpoint does accept `author`). Paperclip Builder commits
   (`noreply@paperclip.ing`) appear on work reports unless a small handler/client
   change passes the filter or `include_commits_in_report` is off for the transition.
   The handler **raises** when a project has commits but 0 Wakapi seconds in the
   period — the Paperclip situation — so such a draft fails until there is tracked
   time or commits are excluded.
3. **Deploying the fork's master is not required**; it would stop counting every
   `type=app + ai coding` heartbeat (all Mac mini Codex activity) and recompute open
   drafts. Separate decision, after fixing the failed `docker.yml` build.
4. **Paperclip gap** (decided, §1.1–1.2): work returns to the Macs and is covered from
   go-live; 2026-09-23 → go-live is billed manually from Paperclip's issue history.
   The billing router can stay (it exports nothing).

## 12. Verification plan

Phase 2, before any claim (status per item in §14):

1. **Desktop, 2.1.286**: `prompt.submit.origin`, `session.start.isInteractive`,
   `prompt.edit` in the composer, and the transcript's `origin.kind` for the same
   typed prompt. **Verified (§14)**: origin `composer`, `human: true`, `prompt.edit`
   bursts; desktop `session.start` reports `surface: null`, `isInteractive: false` —
   the desktop surface arrives with `session.attach`.
2. Unit fixtures from real event streams (both engines): interrupt, question wait,
   overnight plan wait, rejected plan, permission wait (with/without `duration_ms`),
   background agent finishing after the turn, agent permission prompt,
   task-notification turn, compaction, `/clear`, in-session `/resume`, `claude
   --resume`, fork, sleep gap, worker respawn (domain change), `kill -9`.
3. `claude plugin validate` (both sections in README) and `claude plugin test`: band on
   four surfaces, numbers advance with the mock clock, button prefills, every hook
   returns the engine's result unchanged.
4. Three-way check on 5 sessions per engine; broken witness → `witness-silent`;
   `--bare` → `transcript-only`; `allowManagedHooksOnly` and an untrusted directory
   likewise; mid-session worker crash → `coverage: partial`.
5. RC from the phone: origin `bridge`, attach/detach, pane placement, `/timebase`,
   `/logtime`.
6. Wakapi: sync a test project; read back heartbeats (editor/OS parsed on the
   production image); replay the algorithm; compare with
   `/api/summary?from=D&to=D+1&project=&recompute=true` (≤ 2 min); sync twice → row
   count unchanged; one real day across three threads on two Macs; per-item 400s
   dead-lettered. **Verified (§14)** except the three-thread/two-Mac day and the
   per-item 400s: project-filtered totals equal the ledger union to the second.
7. Grunf: preview on a ticket you name; create once; re-run → `NO_NEW_TIME`; Type,
   Invoice, Billable as policy says; logged minutes = Claude + my time; `deferred`
   gate; check whether Grunf's Wakapi sync lists the test project's Claude heartbeats.
   **Preview verified (§14)**; create, the `NO_NEW_TIME` re-run and the
   `WakaTimeSyncService` check have not been run.
8. Backfill of this Mac's 11 sessions vs `cost-state` (sum ≥ API time, union ≤ wall).
9. `claude plugin update` on both Macs + `install --refresh` + supervisor restart;
   version-skew warning.

Phase 3: a separate read-only reviewer re-runs 4–7 against real data and what Wakapi
and Grunf actually show, and exercises the band on terminal, desktop Code tab and the
phone; discrepancies reported as found. Run on 2026-10-05 (three reviewers: numbers,
servers, mod + terminal); findings and the verified facts are in §14. The desktop
Code tab band is verified; the terminal PTY run and the phone are still open.

## 13. Unconfirmed

(Confirmed on 2026-10-05 and moved to §14: desktop `prompt.submit` origin and
composer `prompt.edit`; the source of the 20 `claude-code` heartbeats;
`UserPromptSubmit.source`, which neither engine populates.)

Anything on the phone / Remote Control (pane placement, `/commands`, origin `bridge`,
attach/detach); terminal rendering of the band (the PTY run stopped at the
workspace-trust dialog); ⚠ glyph on `$.ui.status`; `$.store` atomicity and
`cleanupPeriodDays` purge (web docs only); `$.clock.every` across sleep; resume/fork
transcript copying; `~/.claude/sessions/<pid>.json` after a crash; production
heartbeat timeout (inferred 10 min; the local replay with 600 s matched production
on three days including an 18 484-s VS Code day, §14, but the value was not read off
the server); the notification type name for permission prompts on 2.1.286
(`permission_prompt` seen in the 2.1.289 binary); the Mac mini (versions, supervisor,
`~/.wakatime.cfg`, `cleanupPeriodDays`, whether it is `homeserver.local`, why that
machine fell from 52.6 h to 0.4 h since 09-25); Odoo live config for contracts 28 and
3; whether any invoice covered the Paperclip gap; the Grunf user behind the MCP key,
its `WakaMachines` allowlist and project maps (both read off test 7), existing
`claude:*` logs (none on ticket 56830 as of 2026-10-05); whether the launchd jobs run
(rendered, not loaded).

## 14. Phase 2/3 status (2026-10-05)

Source: the three-part phase-3 review of 2026-10-05 (numbers; servers; mod +
terminal), run read-only against the real ledger on this Mac, the production Wakapi
and a Grunf preview. Everything below was verified there unless marked otherwise.

**Desktop 2.1.286 (test 1, phase-3 band on the Code tab).** Desktop sessions deliver
`prompt.submit` with origin `composer` and `human: true`, and `prompt.edit` bursts:
session `01dcc649` recorded bursts of 67, 4, 61, 13, 15 and 77 edits, two `composer`
prompts, `ticket.seen` (`url`, `read`) and 60-s ticks while the turn ran. A band press
was recorded in session `d3bcd175`: `press:report` → `input:ticket` + `ticket.seen
56830 (id)` → `/logtime` prefill → `prompt.submit` origin `composer`. The band
therefore renders and works on the desktop Code tab. Desktop `session.start` reports
`surface: null`, `isInteractive: false`; the desktop surface only appears via
`session.attach` (`desktop`, client `desktop-2`), which the `unclassified` fallback in
§4.2 must take into account. The message "`/logtime` isn't a command here" is not a
mod or band failure: the `onix-support` plugin Claude Code loads on this Mac is the
stale claude.ai "My Uploads" copy without the `logtime` skill (§2.10, §10.4).

**Witness.** The classic `UserPromptSubmit` payload carries no `source` field on
either engine (2.1.286 desktop, 2.1.289 CLI), so a witness prompt row is
`human: null` (unknown origin) and reconciliation takes the mod's or the
transcript's verdict. Headless `claude -p` (session `09b29ffa`) produced consistent
mod and witness rows: origin `sdk`, `human: false`, Claude 1157 ms, human 0.

**The 20 `claude-code` heartbeats of 2026-10-02** (§13, resolved): UA
`wakatime/v2.22.2 (darwin-27.0.0-arm64) go1.26.5 [opus/5-5|sonnet/5-5|] claude-code/2.1.286`,
parsed by the server as editor Vscode / OS Macos, project `onix-mcp`, branch `master`,
machine `igbenic.local`, entity `plugins/onix-support/skills/logtime/SKILL.md`,
19 reads + 1 write, 07:40–10:57Z. They are VS Code's WakaTime extension attributing
AI edits made in the editor — not a second Claude Code sync and not timebase. Nothing
of ours exists on that day; the ledger's only `onix-mcp` thread (`511f6c3c`, 25 s)
overlaps one of them, so a future `onix-mcp` sync of 10-02 would interleave the two
sources knowingly. `verify` must not count a `… vscode-wakatime/<v>` UA as stray.

**Wakapi production (test 6).** All 63 ledger instants of the synced days exist on
the server exactly once with the designed UA (parsed `Claude-code/Macos`), machine
`igbenic.local`, entity, branch and categories; two `sync --dry-run` passes posted
nothing and heartbeat counts were identical across two reads. `/api/summary` treats
`to` as **exclusive**: `from=D&to=D` is an empty window, `from=D&to=D+1` is the day;
`total` and the category values are **seconds** (not Go nanoseconds). With those two
forms the project-filtered summaries — the ones Odoo's
`wakapi_task_logs/services/wakapi_client.py` reads via
`/users/current/summaries?start=D&end=D&project=P` — equal the ledger's day union to
the second: `odoo-addons` 2026-10-03 **413 s** (ledger 413 367 ms; 222 / 191 s
reviewing / ai coding), 2026-10-04 **1174 s** (ledger 1 174 855 ms; 902 / 272 s),
`timebase-test` 2026-10-05 1 s (1406 ms); tail credit 0. The local replay matched
production on those days and on an 18 484-s VS Code day. **Unfiltered / dashboard
summaries lag or omit backfilled heartbeats**: the fork's `services/duration.go`
recomputes live only under a project filter and otherwise fills only the span after
the last cached duration, so 10-04 shows 0 s for `odoo-addons` and 10-03 shows 308 s
instead of 413 s. Remedy: a fork change (`recompute=true` bypassing the duration
cache, or regenerating durations after a backfill) or a nightly regeneration; until
then every comparison is project-filtered and Grunf's `WakaTimeSyncService` endpoint
remains to be identified. `timebase verify` as built before the review requested
`to=D` and divided by 1e6, so it reported `wakapi 0m` for every project; fixed to
`to=D+1` and seconds (SPEC, README). Still open from test 6: one real day across
three threads on two Macs; a per-item 400 against production.

**Grunf (test 7, preview only).** `onix_support_preview_ai_time_log` for the test
thread `claude:3c0cda17-…` (cumulative 1406 ms, settled, ended, `report: ready`) on
ticket 56830 "Ostalo 10.2026." returned `checkpointStatus READY`, `canCreate true`,
`previousCheckpoint null`, `durationMs 1406`, 1 logged minute (`time 00:01:00`),
`billableTime 00:15:00`, `billable true`, `invoice false`,
`workOutsideWorkingHours false`, type `AI/Codex`, date = `snapshotAtUtc`, agent Igor.
No create was called; the `NO_NEW_TIME` re-run, the per-policy billing on a real log
and the `WakaTimeSyncService` check are still open. The ticket holds six Codex logs
and no `claude:*` log. Consequence for §7.2: a 1.4-s ended thread was offered at 15
billable minutes, so `session --json` now defers an ended thread with under 1 min
settled (`too-short`) and a second report of a thread on one day (`reported-today`;
`last.ticket` names the ticket), besides `billable-floor` for a running thread under
15 min of delta; `rule-changed` is never lifted by `logtime --force`, only by
`timebase report-grunf --rebase` run by hand (a correction row with no delta);
`report-grunf` stores the preview fingerprint with the row.

**Numbers (tests 4 and 8).** An independent lib-free recomputation matched
`timebase session --json` to the millisecond for the transcript-only sessions and
every delta on the live session `d3bcd175` was traced to a design rule (rejected
permission wait, enqueue mark, typing marks). The §7.4 sanity check needs the
overlapping sum of turn and agent runs, not the disjoint union: parallel agents' API
time exceeds wall clock (`d3bcd175` 514.8 API-min vs 391.3 union-min). Flags
`api-time-exceeds-segments` and `union-exceeds-wall` carry that check; desktop
attach/detach are presence, not marks (`attach-marks: n` shows how much rests on
them); `final_after_s` defaults to `idle_limit_s`.

**Terminal (2.1.289, after the fixes).** A scripted PTY session in `~/Projects/wakapi`
(a trusted folder; the review's run had stopped at the trust dialog of the scratch
repo) rendered the band on boot: `Claude 0m · You 0m · Total 0m · ⏸ idle [Report to
Grunf] ticket id or URL [-] nothing unreported`; during the turn `● working 0m00s`
and `today 1h52m across 11 threads (union)`; `/timebase` printed the thread readout
(`thread ea0ebe3b · wakapi · Claude 0m · You 1m · Total 1m`); no `timebase:` error or
refusal line appeared. After the review, two more changes landed: the transcript's
trailing cost-state now travels on `session.end{cost}` so the §7.4 check works on the
ledger path, and a mod-emitted `clock.jump` row is advisory — exclusions are decided
only from the (wall, mono) pairs of consecutive stamped rows, so a recorder bug can
never drop real time (one pre-fix row existed, in a scratch thread). Suite: 183 node
tests, `claude plugin test` 21, validate unchanged.

**Not run / still open.** The phone / Remote Control surface (test 5); the launchd
jobs are rendered by `timebase install` but not loaded (`launchctl list` shows no
timebase jobs) — the syncs so far were started by hand and limited to `timebase-test`
and `odoo-addons` (229 further final segments are pending for the first full post);
`claude plugin update` on both Macs (test 9); the Mac mini.
