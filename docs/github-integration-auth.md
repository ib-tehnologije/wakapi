# GitHub Integration: "Connect once" auth and repo picker

Status: draft (January 27, 2026)

This document describes how to remove the "paste a PAT per repo" requirement and replace it with an account-level GitHub connection plus a repo picker that maps Wakapi projects to GitHub repositories.

## Goals
- Users connect GitHub once (App, OAuth App, or PAT) and re-use that token for all links.
- Show the user the repos the token can access and let them pick one to link to a Wakapi project.
- Keep the existing PAT-per-link flow working so current users are not broken.

## Auth modes
We support three modes; the UI should prefer them in this order:
1) GitHub App (recommended): read-only Contents + metadata; user installs and selects repos. Store `installation_id`; exchange for installation tokens per sync.
2) OAuth App: scopes `read:user`, `read:org`, and `repo` (or `public_repo` if we restrict to public). Store access + refresh; handle refresh.
3) Fine-grained PAT (fallback / self-host): user pastes once; scope `contents:read` for selected repos. Keep legacy per-link PAT for compatibility.

## Data model additions
- `scm_accounts`
  - add `installation_id` (string) for GitHub App installs.
  - keep `auth_type`, `access_token_enc`, `refresh_token_enc`, `expires_at`, `provider_login`, `provider_user_id`, `avatar_url`.
- `project_repository_links`
  - unchanged; still the project↔repo mapping used by commit sync.

## API surface (new)
- `POST /api/integrations/github/app/start` → redirect user to GitHub App install/consent (stores state).
- `GET  /api/integrations/github/app/callback` → exchange code, store `scm_account` with `auth_type=github_app` and `installation_id`.
- `POST /api/integrations/github/oauth/start` and `/callback` → standard OAuth flow; store `auth_type=oauth_app`, tokens, expiry.
- `POST /api/integrations/github/pat` → save a PAT once to `scm_accounts` (`auth_type=pat`).
- `GET  /api/integrations/github/repos` → list repos accessible to the stored account; supports paging + search; responds with `id`, `full_name`, `html_url`, `default_branch`, visibility, owner.
- `POST /api/integrations/github/links` → body `{ project, repository_id, branch_override? }`; creates/updates `project_repository_links` and kicks off initial sync.
- `PUT  /api/integrations/github/links/:id` → change branch or relink.
- `DELETE /api/integrations/github/links/:id` → unlink (optional purge flag).
- `POST /api/integrations/github/links/:id/sync` → manual sync trigger.

## Backend behavior changes
- `LinkProject` should accept an optional token; if missing, pull the user’s stored `scm_account` and use/refresh it. Keep the token parameter to support the old compat endpoint.
- Extend `GitHubClient`:
  - GitHub App: `/user/installations` then `/user/installations/{id}/repositories`.
  - OAuth/PAT: `/user/repos` with pagination and `affiliation=owner,collaborator,organization_member`.
- Add token refresh helpers for GitHub App installations and OAuth App access tokens; call them before sync and before listing repos.
- Sync scheduler remains the same; it now uses the stored account token or a fresh installation token per link.

## UI/UX (Settings → Integrations → GitHub)
- If not connected: show “Connect GitHub” (App/OAuth) and “Paste PAT once” fallback.
- After connect: display connected account (login + avatar), token type, scopes, and rate-limit info if available.
- Repo picker: search/list accessible repos → pick repo → pick branch (default branch prefilled) → pick Wakapi project → “Link”.
- Existing links table stays, but actions now call the new link endpoints; hide per-link PAT inputs unless no account is stored.

## Migration and compatibility
- Migration: add `installation_id` column to `scm_accounts` (nullable). No change needed for existing `project_repository_links`.
- Backwards compatibility: keep the current POST body with `token` in `routes/compat/wakatime/v1/commits.go` and the legacy Settings form path; they should still work.
- Existing links created with per-link PAT continue to sync; users can switch to account-level auth by saving a PAT once or connecting the App.

## Rollout checklist
- Ship migrations.
- Implement new endpoints and repo listing in the service layer.
- Update settings UI to the new flow; keep a small “Advanced: link with PAT + owner/repo” form for self-hosters.
- Add tests for: account storage, repo listing (App and OAuth), link creation without explicit token, and legacy link creation.

## Open questions
- Should OAuth mode allow only public repos by default (`public_repo`) to avoid broad `repo` scope? (safer default).
- Do we want to surface GitHub rate-limit headers in the UI? Useful for debugging sync stalls.
