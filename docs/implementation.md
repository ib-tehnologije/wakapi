# Wakapi GitHub Commits Integration

## WakaTime-compatible “Commit / Commits” API + automatic time attribution

### Quick check: does Wakapi already do this?

Wakapi’s README explicitly positions it as a smaller subset of WakaTime and calls out missing **“Additional Integrations (with GitLab, etc.)”** and a **“Richer API”**. ([GitHub][1])
In practice, that lines up with what you’re asking for: **there is no first-class GitHub repo linkage + “time per commit” feature baked in today**, and there are no documented WakaTime-compatible `commits` endpoints in Wakapi’s public docs.

So: assume **this needs to be implemented**.

---

## 1) Goal

Add a GitHub integration and project↔repo mapping to Wakapi, and then expose commit data (with time totals) through **the exact same API interface as WakaTime’s “Commit” and “Commits” resources**. ([WakaTime][2])

Concretely:

* Users can connect GitHub to Wakapi.
* A **Wakapi Project** can be linked to a **GitHub Repository** (plus optional branch override).
* Wakapi periodically syncs commits from GitHub.
* Wakapi computes **time spent coding on each commit** from Wakapi’s own heartbeat-derived durations.
* Wakapi exposes WakaTime-compatible endpoints under Wakapi’s compat base path:

  * `GET /api/compat/wakatime/v1/users/current/projects/:project/commits`
  * `GET /api/compat/wakatime/v1/users/current/projects/:project/commits/:hash`

The API shapes / fields must match WakaTime docs **exactly** for these endpoints (field names, top-level keys, nullability). ([WakaTime][2])

---

## 2) Key design decision: how to attribute time to commits

WakaTime’s commit feature is commonly understood to attribute time by **measuring the tracked coding time between commits** (not by inspecting diffs per file). There’s even an OSS tool noting that WakaTime compares “time between commits” and that their approach differs. ([GitHub][3])

So, Wakapi’s baseline algorithm should be:

> For a given repo+branch, attribute tracked coding time in the interval **(previous commit time, current commit time]** to the current commit.

That gets you the “commit message ↔ time bucket” association automatically without requiring the editor plugins to send commit hashes.

---

## 3) GitHub integration: auth options and recommendation

### Recommended: GitHub App (best security + repo-scoped)

Use a GitHub App with read-only permissions. GitHub’s “List commits” endpoint documentation states that fine-grained tokens (including GitHub App installation tokens) require **Contents repository permission (read)**. ([GitHub Docs][4])
This is ideal because users can grant access to **only selected repos**.

### Also useful: Fine-grained PAT (self-host convenience)

Let users paste a fine-grained PAT restricted to specific repos and read-only contents permissions. Same minimal permission requirement (Contents read) for commit listing. ([GitHub Docs][4])

### OAuth App (optional; private repo scopes are coarse)

OAuth scopes for repos are broad; `repo` grants broad access (including write). ([GitHub Docs][5])
Use this only if you accept the tradeoff or restrict to public repos with `public_repo`.

### Rate limits you must design around

Authenticated REST calls are typically limited to **5,000 requests/hour**. ([GitHub Docs][6])
So syncing must be incremental, cached, and backoff-aware.

---

## 4) Data model additions

Design it “provider-ready” (GitHub first), because Wakapi’s README hints future integrations would be valuable. ([GitHub][1])

### 4.1 `scm_accounts` (per user)

Stores the user’s GitHub connection.

Fields:

* `id` (UUID)
* `user_id` (FK)
* `provider` (`github`)
* `auth_type` (`github_app` | `pat` | `oauth_app`)
* `access_token_enc` (nullable; encrypt at rest)
* `refresh_token_enc`, `expires_at` (OAuth only)
* `provider_user_id`, `provider_login`, `avatar_url`
* timestamps: `created_at`, `updated_at`, `revoked_at`

Indexes:

* unique `(user_id, provider)`

### 4.2 `scm_repositories` (repo metadata)

Fields:

* `id` (UUID)
* `provider` (`github`)
* `external_id` (GitHub repo id)
* `full_name` (`owner/repo`)
* `name`, `owner`
* `html_url`, `api_url`
* `description`, `homepage`
* `default_branch`
* `is_private`, `is_fork`
* `star_count`, `fork_count`, `watch_count`
* timestamps

Indexes:

* unique `(provider, full_name)`
* unique `(provider, external_id)`

### 4.3 `project_repository_links` (project ↔ repo mapping)

Fields:

* `id` (UUID)
* `user_id`
* `project_id` or `project_name` (depending on Wakapi internals)
* `repository_id`
* `branch_override` (nullable)
* `last_synced_at`
* `sync_status` (string: `ok` | `pending` | `error` | `disabled`)
* `sync_error` (nullable)
* timestamps

Indexes:

* unique `(user_id, project_id)` (MVP: one repo per project)

### 4.4 `scm_commits` (commit metadata)

Fields:

* `id` (UUID) — WakaTime returns a separate `id` and `hash`. ([WakaTime][2])
* `repository_id`
* `hash` (SHA)
* `truncated_hash` (first 7 chars)
* `message`
* `html_url` (provider commit page)
* author fields: `author_name`, `author_email`, `author_date`, `author_username`, `author_avatar_url`, `author_html_url`, `author_url`
* committer fields: `committer_name`, `committer_email`, `committer_date`, `committer_username`, `committer_avatar_url`, `committer_html_url`, `committer_url`
* `ref` (ex `refs/heads/main`)
* `branch` (the branch used during sync, MVP)
* `created_at` (when **synced into Wakapi**, not commit authored time) ([WakaTime][2])

Indexes:

* unique `(repository_id, hash)`
* index `(repository_id, branch, committer_date)`

> Note: A commit can exist on multiple branches. If you want correctness later, add a join table `commit_refs(commit_id, branch, ref)` and treat `scm_commits` as branch-agnostic.

### 4.5 `commit_stats` (user/project-specific time totals)

Fields:

* `id` (UUID)
* `user_id`
* `project_id`
* `repository_id`
* `branch`
* `commit_hash`
* `total_seconds` (float) ([WakaTime][2])
* `human_readable_total` (string) ([WakaTime][2])
* `human_readable_total_with_seconds` (string) ([WakaTime][2])
* `calculated_at`
* `algo_version` (int, start at 1)
* `dirty` (bool)

Indexes:

* unique `(user_id, project_id, branch, commit_hash)`
* index `(user_id, project_id, branch, dirty)`

---

## 5) GitHub sync: what to call and how

### 5.1 Commit listing endpoint

Use GitHub REST “List commits”:
`GET /repos/{owner}/{repo}/commits`

Important parameters include:

* `sha` (branch name or SHA)
* `author`
* `since`, `until`
* `per_page` (max 100)
* `page` ([GitHub Docs][4])

Permissions:

* Requires “Contents” repository permission (read) for fine-grained tokens. ([GitHub Docs][4])

### 5.2 Sync strategy (incremental)

Per link `(user_id, project_id, repo_id, branch)`:

1. Determine branch:

   * if `branch_override` set, use it
   * else use repo `default_branch`
2. Fetch commits newest-first with `per_page=100`
3. Stop early when you encounter a commit hash already stored (fast cutoff)
4. Upsert commit metadata into `scm_commits`
5. Update:

   * `project_repository_links.last_synced_at = now()`
   * `sync_status` / `sync_error`

### 5.3 When to sync

* Immediately after linking a repo to a project.
* On the commits API call if `last_synced_at` is stale (e.g., older than 10–15 minutes).
* Periodically via a cron/scheduler.

### 5.4 Rate-limit behavior

Track response headers and backoff on `403/429`. GitHub’s docs describe rate limits and secondary limits and provide headers. ([GitHub Docs][6])

---

## 6) Time attribution service (commit ⇄ time)

### 6.1 Core algorithm (WakaTime-style)

Compute per commit using the **time between commits** approach. ([GitHub][3])

For commits on a branch, ordered by `committer_date` ascending:

For each commit `C[i]`:

* `end = C[i].committer_date`
* `start = C[i-1].committer_date` if `i>0`
* for the oldest commit in the considered set, choose:

  * `start = earliest heartbeat timestamp` for that project/branch, or
  * `start = end - lookback_window` (e.g., 30d), whichever is later

Then:

* Query heartbeat-derived durations for `[start, end]` filtered by `user_id`, `project`, `branch`
* Sum overlap seconds (clip durations at boundaries)

Pseudo:

```text
for each commit in commits_asc:
  start = prev_commit_time or inferred_start
  end = commit_time
  durations = load_durations(user, project, branch, start, end)
  total = sum(overlap(d, [start,end]) for d in durations)
  store commit_stats(total, human_readable_total, ...)
```

### 6.2 Late-arriving heartbeats (offline sync)

WakaTime clients can backfill heartbeats later; Wakapi supports older heartbeats depending on its config. ([GitHub][1])

Implement a “dirty marking” approach:

* When a heartbeat arrives with timestamp `t`:

  * find first commit on that branch where `committer_date >= t`
  * mark that commit’s `commit_stats.dirty=true`
* A background worker recomputes dirty stats

### 6.3 Branch missing/empty in heartbeats

Some clients omit branch. Choose one:

* **Strict**: require exact branch match.
* **Lenient** (better UX): if requested branch == default branch, include heartbeats where branch is empty OR default branch.

Document behavior clearly.

---

## 7) WakaTime-compatible API implementation (this is the contract)

Everything in this section must match WakaTime’s docs for Commit/Commits. ([WakaTime][2])

### 7.1 List commits

**Route (Wakapi compat base):**
`GET /api/compat/wakatime/v1/users/current/projects/:project/commits`

Query params:

* `author` (optional) ([WakaTime][2])
* `branch` (optional; defaults to repo default branch) ([WakaTime][2])
* `page` (optional) ([WakaTime][2])

Response top-level keys (must exist):

* `commits`: array
* `author`: string or null
* `next_page`: int or null
* `next_page_url`: string or null
* `page`: int
* `prev_page`: int or null
* `prev_page_url`: string or null
* `branch`: string
* `project`: object:

  * `id`, `name`, `privacy`
  * `repository`: repo object with fields listed in WakaTime docs ([WakaTime][2])
* `status`: string (project’s sync status)
* `total`: int
* `total_pages`: int ([WakaTime][2])

Each commit object must contain all these fields (names exactly):

* `author_avatar_url`, `author_date`, `author_email`, `author_html_url`, `author_name`, `author_url`, `author_username`
* `committer_avatar_url`, `committer_date`, `committer_email`, `committer_html_url`, `committer_name`, `committer_url`, `committer_username`
* `created_at`
* `hash`, `truncated_hash`
* `html_url`
* `human_readable_total`, `human_readable_total_with_seconds`
* `id`
* `message`
* `ref`
* `total_seconds`
* `url`
* `branch` ([WakaTime][2])

Pagination:

* WakaTime docs do not expose `per_page` here, only `page`. ([WakaTime][2])
* Pick a fixed `PAGE_SIZE` (start with 50) and compute:

  * `total_pages = ceil(total / PAGE_SIZE)`

### 7.2 Single commit

**Route:**
`GET /api/compat/wakatime/v1/users/current/projects/:project/commits/:hash`

Query params:

* `branch` (optional; defaults to repo default branch) ([WakaTime][2])

Response top-level keys:

* `commit`: commit object (same fields as above)
* `branch`: string
* `project`: object with `repository`
* `status`: string ([WakaTime][2])

---

## 8) Internal config + UI (Wakapi-specific)

These can be “Wakapi-style” — they don’t need to match WakaTime.

### 8.1 Settings → Integrations → GitHub

* Connect/disconnect
* Auth mode (GitHub App vs PAT vs OAuth)
* Show which repos are accessible
* Show rate limit status / last error (optional)

### 8.2 Project settings

* “Linked repository” (provider + `owner/repo`)
* Branch override
* Last synced timestamp / sync status
* “Sync now” button

Suggested internal endpoints:

* `POST /api/integrations/github/pat` (save PAT)
* `POST /api/integrations/github/oauth/start` (start OAuth)
* `GET /api/integrations/github/oauth/callback` (finish OAuth)
* `GET /api/integrations/github/repos` (list repos for picker)
* `PUT /api/projects/:project/repository` (link repo)
* `DELETE /api/projects/:project/repository` (unlink repo)

---

## 9) Implementation plan (Codex-friendly)

### Phase A — Schema + primitives

1. Add migrations for:

   * `scm_accounts`
   * `scm_repositories`
   * `project_repository_links`
   * `scm_commits`
   * `commit_stats`
2. Add encryption-at-rest helper for tokens (reuse Wakapi’s existing secrets/config patterns).

### Phase B — GitHub client

Implement a small client wrapper around GitHub REST:

* `GetRepo(full_name)`
* `ListCommits(full_name, branch, author, page, per_page)`
  Use GitHub “List commits” parameters and permissions requirements. ([GitHub Docs][4])

### Phase C — Repo linking

* Add project↔repo link CRUD (service layer + storage).
* Validate that repo is accessible with the user’s token.

### Phase D — Commit sync worker

* On link creation/update: sync commits for default branch.
* Add scheduled sync (cron):

  * only for “active” projects or recently accessed ones
  * incremental based on “already have this sha”

### Phase E — Commit stats computation

* Implement `CommitTimeService`:

  * compute stats for commits lacking `commit_stats`
  * recompute dirty stats
* Mark dirty stats on late heartbeats.

### Phase F — WakaTime compat endpoints

Add two handlers under compat routing:

* `/users/current/projects/:project/commits`
* `/users/current/projects/:project/commits/:hash`

**Must** output exactly the WakaTime fields described in their docs. ([WakaTime][2])

### Phase G — Tests

Add contract tests that:

* Assert the JSON keys exist and are spelled exactly as WakaTime docs.
* Validate nullability (`next_page` null on last page, etc.).
* Validate that totals match sum of durations between commit boundaries.

---

## 10) Edge cases to explicitly decide

* **Merge commits**: compute normally vs force `0s`. (WakaTime behavior isn’t documented.)
* **Author filter**: safest is:

  * compute windows using full commit history
  * then filter commits for output
* **Force pushes/rebases**: keep commits by hash; don’t try to “delete history”.
* **Project renamed**: store links by stable project id if possible.

---

## 11) Acceptance criteria

1. User can connect GitHub and link a repo to a Wakapi project.
2. Commits are synced and `last_synced_at` updates.
3. Compat endpoints exist and return WakaTime-shaped payloads:

   * list commits returns `commits`, pagination fields, `project.repository`, `status`, totals ([WakaTime][2])
   * single commit returns `commit`, `project.repository`, `status` ([WakaTime][2])
4. `total_seconds` per commit equals tracked duration between previous commit and that commit (best-effort).
5. External system can swap WakaTime base URL with Wakapi compat base URL and keep working for commit fetching.

---

[1]: https://github.com/muety/wakapi "https://github.com/muety/wakapi"
[2]: https://wakatime.com/developers "https://wakatime.com/developers"
[3]: https://github.com/rposborne/gitwakatime "https://github.com/rposborne/gitwakatime"
[4]: https://docs.github.com/en/rest/commits/commits "https://docs.github.com/en/rest/commits/commits"
[5]: https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/scopes-for-oauth-apps "https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/scopes-for-oauth-apps"
[6]: https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api "https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api"
