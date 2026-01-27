# GitHub commits integration — remaining work (January 27, 2026)

Context: commit syncing & WakaTime-compatible commit endpoints are partially implemented (data models, basic PAT link API, on-demand sync). The UI and background sync are missing, causing “project keeps spinning” when no link exists.

## Must-have (ship to users)
1. **Settings UI to link a project to GitHub**  
   - Form in Settings → Integrations: select Wakapi project, enter `owner/repo`, fine‑grained PAT (Contents: Read), optional branch override.  
   - Show existing links (project → repo, branch, status, last sync).  
   - POST to the new API/handler; surface errors inline (invalid repo/token, 404, rate limits).
2. **Background sync scheduler**  
   - Cron job (e.g., every 10–15 min) to sync stale `project_repository_links` (no `last_synced_at` or older than threshold) instead of only on-demand.  
   - Respect per-link status (`ok` / `error`), store last error, and back off on repeated failures.
3. **Better API responses for unlinked projects**  
   - Already added: 404 when project is not linked. Clients should now stop spinning.

## Should-have (robustness & UX)
4. **Unlink/update flow**  
   - Allow replacing PAT/branch and removing a link (cleanup `project_repository_links` + maybe `scm_account` if unused).
5. **Rate-limit & error surfacing**  
   - Capture GitHub rate-limit headers; set `sync_status=error` + `sync_error` for 403/429 and expose in UI.
6. **Test coverage**  
   - Unit tests for Settings handler action (link success/failure).  
   - Integration-ish test for cron sync selecting stale links.

## Nice-to-have (later)
7. **GitHub App / OAuth**  
   - Alternative auth to reduce PAT friction and scope overreach.
8. **Multiple repos per project & branch-aware commits**  
   - Branch/ref join table to avoid duplicating commits across branches; UI picker for branch list from GitHub.
9. **Telemetry & observability**  
   - Metrics for sync duration/failures and queue depth.

## Decision defaults
- Auth: fine-grained PAT with Contents: Read (minimum), user-provided.  
- Sync cadence: every 10 minutes; stale threshold 15 minutes (matches on-demand check).  
- Max page size: 100 commits per GitHub call; stop at first known hash.  
- Time attribution: “time between commits” (already implemented).
