# Kehrwoche

Telegram bot that reminds a shared flat (WG) group chat who is on cleaning duty.
Serverless Go functions on Vercel; rotation schedule in Postgres (Neon).

## Commands

Always use `task` (CI runs these exact tasks — don't call `go test`/`go vet` directly):

| Command | Purpose |
|---|---|
| `task test` | `go test ./...` |
| `task vet` | `go vet ./...` |
| `task lint` | `golangci-lint run ./...` |
| `task fmt` | `gofmt -w .` |
| `task build` | `go build ./...` |
| `task tidy` | `go mod tidy` |
| `task mcp` | run the MCP server over stdio (normally started by an MCP client, not by hand) |
| `task migrate` | apply DB migrations — **hits the DB in `.env`, run manually only** |
| `task seed -- <flags>` | seed / regenerate the schedule — **writes to the DB in `.env`, run manually only** |
| `task setcommands` | push the bot command menu to Telegram (`setMyCommands`); `-- -show` prints the current one — **hits the Telegram API, run manually only** |
| `task test:integration` | integration tests against a throwaway `postgres:17` (needs Docker) |

Before committing: `task fmt && task vet && task lint && task test`.

## Architecture

- `api/webhook.go` — Vercel function (`func Webhook`), handles Telegram slash commands. Auth: `X-Telegram-Bot-Api-Secret-Token` header, constant-time compare, fail-closed. Always returns 200 (Telegram retries non-200 → duplicate messages); errors are logged only.
- `api/cron.go` — Vercel function (`func Cron`), sends the scheduled reminder. Auth: `Authorization: Bearer <CRON_SECRET>`. Cron cadence is in `vercel.json` (two entries covering summer/winter local time).
- `pkg/schedule` — domain logic: duty types, recurrence rules, date math, message formatting. No I/O except `repo.go`.
  - `pkg/schedule/repo.go` — the only place with SQL. `Querier` interface is satisfied by a pgx connection; tests use fakes.
- `pkg/telegram` — `telegram.Send`, the only outbound HTTP.
- `pkg/db` — `db.Connect`.
- `pkg/config` — `config.Load()` reads every env var once into `Config`. Add new env vars here, not scattered `os.Getenv`.
- `cmd/migrate`, `cmd/seed` — one-shot CLIs. `cmd/seed` also regenerates the rotation after a move-out (`-vacant`, `-regen -start`). `cmd/migrate` is a thin wrapper over `internal/migrate.Apply`.
- `cmd/setcommands` — one-shot CLI, pushes the Telegram command menu (`setMyCommands`) from `botcmd.Menu()` to every scope (default + `all_private_chats` + `all_group_chats` + `all_chat_administrators`), so an old scope-specific list can't shadow it; `-show` prints each scope's current menu.
- `pkg/botcmd` — the bot's slash commands: `duties` (name + German description + handler) is the single source of truth; `Lookup` (webhook dispatch), `Menu` (setMyCommands), `StaticReply` (`/help` any chat, `/start` private only). `wer`/`plan` handlers live here. Kept out of `api/` because Vercel builds every `api/*.go` as its own function.
- `cmd/mcp` — local MCP server (stdio) exposing the schedule read-only: `list_duties`, `on_duty`, `upcoming`. Thin adapter over `pkg/schedule`; opens a DB connection per call. Wired up in `.mcp.json` via `task mcp` (so it inherits `DATABASE_URL` from `.env`). Smoke-test with `scripts/mcp-smoke.sh`.
- `migrations/*.sql` — plain SQL, applied in order by `cmd/migrate`.

## Domain

Duty types (`schedule.DutyType`): `toilet1`, `toilet2`, `laundry`, `hall`, `floor`.
Recurrence in `pkg/schedule/schedule.go` `configs`:

- **Weekly duties** (`toilet1`, `toilet2`, `hall`, `floor`): event day Friday, window Fri–Sun (3 days).
- **Laundry** (`waschkueche`): event days Tuesday & Friday, 1-day window.

Rooms are labelled `Zimmer N` (`RoomNo`/`ParseRoomNo`). All user-facing text is German; weekday abbreviations `Mo`..`So`. Timezone is always `Europe/Berlin` (`_ "time/tzdata"` is imported for the Vercel runtime).

`/*_plan` commands show `PlanWeeks` (4) weeks ahead. `DB schemas` table: `(duty_type, duty_date, room)` unique on `(duty_type, duty_date)`.

Bot commands: `/toilette1`, `/toilette2`, `/treppenhaus` (hall), `/etage` (floor), `/waschkueche` (laundry), each with a `_plan` variant. Plus `/help` (command list, any chat) and `/start` (greeting, private chat only). All defined in `pkg/botcmd` — the `duties` slice there (name + German description + handler) drives the webhook dispatch, `/help`, and `setMyCommands`. After changing it, run `task setcommands` to update the Telegram menu (BotFather is no longer used).

## Deployment

- `main` → production (Vercel `--prod`), `dev` → preview. Deploy is automatic from CI on push — never run `vercel` locally.
- On push to `main`, CI also runs `task migrate` against the production DB.
- Secrets live in `.env` locally and the Vercel dashboard: `TELEGRAM_BOT_TOKEN`, `CHAT_ID`, `DATABASE_URL`, `WEBHOOK_SECRET`, `CRON_SECRET`. Never read, print, or commit `.env`.

## Conventions

- Go 1.27, `golangci-lint` v2 (`errcheck`, `govet`, `ineffassign`, `staticcheck`, `unused`, `gosec`).
- `api/` holds function entrypoints only: every non-test `.go` there must export one `func Name(w http.ResponseWriter, r *http.Request)` — Vercel builds each file as a separate serverless function. Shared logic goes in `pkg/`.
- Keep new date/cadence logic derived from `configs` — don't duplicate weekday literals (see the `EventWeekdays` comment).
- Commit messages: no `Co-Authored-By` trailers.