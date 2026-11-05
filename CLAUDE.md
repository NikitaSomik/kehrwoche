# Kehrwoche

Telegram bot that reminds a shared flat (WG) group chat who is on cleaning duty.
Serverless Go functions on Vercel; rotation schedule in Postgres (Neon).

## Commands

Always use `task` (CI runs these exact tasks — don't call `go test`/`go vet` directly):

| Command | Purpose |
|---|---|
| `task test` | `go test ./...` — unit tests, no DB, the SQL layer is faked |
| `task vet` | `go vet ./...` |
| `task lint` | `golangci-lint run ./...` |
| `task vuln` | `govulncheck ./...` — fails CI on a reachable vulnerability |
| `task fmt` | `gofmt -w .` |
| `task build` / `task tidy` | `go build ./...` / `go mod tidy` |
| `task test:integration` | `//go:build integration` tests against a throwaway `postgres:17` (needs Docker) |
| `task mcp` | the MCP server over stdio — started by the MCP client (`.mcp.json`), not by hand |
| `task migrate` | apply DB migrations — **hits the DB in `.env`, run manually only** |
| `task seed -- <flags>` | seed / regenerate the schedule — **writes to the DB in `.env`, run manually only** |
| `task setwebhook` | re-register the webhook — **hits the Telegram API, run manually only** |
| `task setcommands` | push the command menus — **hits the Telegram API, run manually only** |
| `task smoke:cron` | send a real failure notice to `ADMIN_CHAT_ID` — **hits the Telegram API, run manually only** |

Before committing: `task fmt && task vet && task lint && task test`.

## Where things are

Every package's doc comment says how it works — read it (`go doc ./pkg/<name>`, or the top of the file) before changing the package. Function comments carry the reasoning behind the non-obvious choices; keep them current rather than restating them here.

- `api/` — the two Vercel functions: `webhook.go` (Telegram updates) and `cron.go` (the daily reminder). Who gets which command is on `Webhook`'s doc comment.
- `pkg/schedule` — domain: duty types, recurrence (`configs`), rooms, date math, message texts; `repo.go` holds its SQL.
- `pkg/users` — the `users` table, the only other SQL.
- `pkg/onboard` — `/start` → admin approval → room → join-request link → member.
- `pkg/telegram` — the Bot API client and the webhook payload types.
- `pkg/botcmd` — the slash commands; the `duties` slice drives dispatch, `/help` and the menus.
- `pkg/config` — every env var, read once by `config.Load()`. `pkg/db` — `db.Connect`.
- `cmd/seed`, `cmd/migrate`, `cmd/setcommands`, `cmd/setwebhook`, `cmd/mcp` — one-shot CLIs and the local MCP server.
- `internal/migrate` (the migration runner), `internal/pgtest` (integration-test helpers), `migrations/*.sql` (plain SQL, applied in order).
- `docs/` — Mermaid diagrams (`architecture.md`, `onboarding.md`, `database.md`) and runbooks (`schedule.md` for `cmd/seed`, `secrets.md`).

## Rules that span files

These break silently when only one side changes:

- `api/` holds entrypoints only: Vercel builds every non-test `.go` there as its own function, so each must export one `func Name(w http.ResponseWriter, r *http.Request)`. Shared logic goes in `pkg/`.
- `api/webhook.go` always answers 200 once authenticated — Telegram retries anything else and duplicates messages. Both functions' auth is fail-closed.
- New env vars go in `pkg/config`, not scattered `os.Getenv`.
- SQL lives only in `pkg/schedule/repo.go` and `pkg/users`. Each `users` transition is one conditional statement whose `RowsAffected` says whether it applied, so a double press or a race can't act twice.
- `schedule.ReminderHour` mirrors `vercel.json` by hand — `TestVercelCronMatchesReminderHour` fails until they agree. `weeklyDuties` in `api/cron.go` is checked against `schedule.WeeklyDuties()`.
- `schedule.RoomCount` is repeated in the `users_room_check` CHECK of `migrations/0003_onboarding.sql`.
- A new table goes into `internal/pgtest.Raw`'s drop list, or the second integration run fails.
- A new update kind in `api/webhook.go` goes into `allowedUpdates` in `cmd/setwebhook`.
- A change to `pkg/botcmd`'s commands needs `task setcommands` afterwards (admins get theirs with their next `/start`).
- Update the matching `docs/` diagram when a change moves a call between packages, a branch of the onboarding, or a column; a change to `cmd/seed`'s flags or prompts updates `docs/schedule.md`.
- `hall` is seeded one block at a time with `-regen`, never together with other duties: `-regen` deletes forward for every duty selected (`checkBlockDuties`).

## Testing

- Integration tests `t.Skip` when `TEST_DATABASE_URL` is unset, so a plain `go test ./...` never needs a database. CI runs them on a `postgres:17` service container in the `test` job, which gates `migrate` and `deploy`.
- `task smoke:cron` (`//go:build smoke`) runs the real `Cron` against an unroutable `DATABASE_URL`, so only `ADMIN_CHAT_ID` hears about it.

## Deployment

- `main` → production (Vercel `--prod`), `dev` → preview. Deploy is automatic from CI on push — never run `vercel` locally.
- On push to `main`, CI also runs `task migrate` against the production DB.
- CI scans the git history with gitleaks (`.gitleaks.toml`, its own job, doesn't gate deploy — a committed secret has to be rotated, not un-deployed); `docs/secrets.md` has the rotation runbook.
- Secrets live in `.env` locally and the Vercel dashboard: `TELEGRAM_BOT_TOKEN`, `CHAT_ID`, `DATABASE_URL`, `WEBHOOK_SECRET`, `CRON_SECRET`, `ADMIN_CHAT_ID` (optional), `ONBOARDING_ENABLED` (a flag, not a secret). Never read, print, or commit `.env`.

## Conventions

- Go 1.27, `golangci-lint` v2 (`errcheck`, `govet`, `ineffassign`, `staticcheck`, `unused`, `gosec`, `modernize`).
- User-facing text is German; time is always `Europe/Berlin` (`_ "time/tzdata"` is imported for the Vercel runtime).
- Commit messages: no `Co-Authored-By` trailers.
