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
| `task vuln` | `govulncheck ./...` — known vulnerabilities in dependencies, pinned version |
| `task fmt` | `gofmt -w .` |
| `task build` | `go build ./...` |
| `task tidy` | `go mod tidy` |
| `task mcp` | run the MCP server over stdio (normally started by an MCP client, not by hand) |
| `task migrate` | apply DB migrations — **hits the DB in `.env`, run manually only** |
| `task seed -- <flags>` | seed / regenerate the schedule — **writes to the DB in `.env`, run manually only** |
| `task setcommands` | push the bot command menu to Telegram (`setMyCommands`); `-- -show` prints the current one — **hits the Telegram API, run manually only** |
| `task test:integration` | integration tests against a throwaway `postgres:17` (needs Docker) |
| `task smoke:cron` | send a real failure notice to `ADMIN_CHAT_ID` — **hits the Telegram API, run manually only** |

Before committing: `task fmt && task vet && task lint && task test`. `task vuln` runs in CI too and fails the build on a reachable vulnerability; it complements Dependabot, which reports new versions rather than reachable holes.

## Architecture

- `api/webhook.go` — Vercel function (`func Webhook`), handles Telegram slash commands. Auth: `X-Telegram-Bot-Api-Secret-Token` header, constant-time compare, fail-closed. Always returns 200 (Telegram retries non-200 → duplicate messages); errors are logged only. Also records the sender in `users` (`recordUser` on the dispatch path, which already holds a connection; `recordSender` opens one for `/start`, the only static reply that touches the DB).
- `api/cron.go` — Vercel function (`func Cron`), sends the scheduled reminder. Auth: `Authorization: Bearer <CRON_SECRET>`. Cron cadence is in `vercel.json` (two entries covering summer/winter local time). A run that can't do its job reports to `ADMIN_CHAT_ID` privately via `notifyAdmin` — one message per run, skipped when unset, never sent to the group. The webhook deliberately does not: it answers a person who notices the silence, while a failed cron is invisible.
- `pkg/schedule` — domain logic: duty types, recurrence rules, date math, message formatting. No I/O except `repo.go`.
  - `pkg/schedule/repo.go` — all SQL for the schedule. `Querier` interface is satisfied by a pgx connection; tests use fakes.
- `pkg/users` — the `users` table, the only SQL outside `pkg/schedule`. `Record` is a single `INSERT ... ON CONFLICT DO NOTHING`; `Execer` is the write half of a pgx connection so tests can fake it.
- `pkg/telegram` — the Bot API. Outbound: `Send` / `SendPlain` (messages) and `SetCommands` / `GetCommands` (the command menu), all over one `call` helper that keeps the bot token out of errors — the only outbound HTTP in the project. Inbound: `update.go` holds the slice of the webhook payload the bot reads (`Update`, `Message`, `Chat`, `User`, `MessageEntity`) plus `Message.Command` and `Chat.IsPrivate`. `Command` finds the end of the command in the text rather than indexing by the entity's `Length`, which is counted in UTF-16 units and arrives from outside.
- `pkg/botcmd` — the bot's slash commands. The `duties` slice (name + German description + handler) is the single source of truth; `Lookup` (webhook dispatch), `Menu` (`setMyCommands` payload), `StaticReply` (`/help` any chat, `/start` private only). `wer`/`plan` handlers live here. Kept out of `api/` because Vercel builds every `api/*.go` as its own function.
- `pkg/db` — `db.Connect`.
- `pkg/config` — `config.Load()` reads every env var once into `Config`. Add new env vars here, not scattered `os.Getenv`.
- `cmd/migrate`, `cmd/seed` — one-shot CLIs. `cmd/seed` asks for anything left off the command line (`cmd/seed/prompt.go`), prints the plan and confirms before committing — the transaction is already staged by then, so declining just rolls back. A flag that was typed is never asked about (`flag.Visit`), and a non-terminal stdin skips every prompt so scripts don't hang — `isTerminal` asks the terminal driver (`term.IsTerminal`) rather than reading the file mode, because `/dev/null` is a character device too and a mode check would take `seed < /dev/null` for somebody at a keyboard. `-regen` is deliberately flag-only: it deletes rows with no upper bound. The run is drawn as one framed block hanging off a rail (`┌ │ ├ └`, the shape `@clack/prompts` and Laravel Prompts made familiar): a question per step, the plan under it, a totals table, then the confirmation — the totals are last because with hundreds of rows scrolled past they are what the yes is actually answered against. Duties and vacant rooms are `multiselect` lists driven by the arrow keys (`cmd/seed/term.go` — raw mode via `golang.org/x/term`, its only dependency `x/sys` already in the graph); Ctrl+C arrives there as a byte rather than a signal, so it is handled as a key, and `rawMode` restores the terminal from the deferred call, a panic, or SIGTERM/SIGHUP. Raw mode also switches off ONLCR, the driver's `\n` → `\r\n` translation, so everything printed while it is on goes through `crlfWriter` — without it a bare newline drops a line without returning to column 0 and the list walks off the right of the screen a step at a time. A list falls back to typing the same keys the flags take whenever raw mode isn't available. `cmd/seed/style.go` colours the prompts and the plan; it keys off stdout (not stdin, which decides the prompting) and honours `NO_COLOR`, so a redirected plan file stays free of escape sequences, and `wrap` reopens the outer colour after a nested one so a styled word can't leave the rest of its line plain. `cmd/seed` also regenerates the rotation after a move-out (`-vacant`, `-regen -start`). `hall` is generatable but excluded from `defaultDuties`: the staircase rotates between floors of the house, so its dates come from outside and it is seeded one block at a time with `-duty hall -regen -start` (refused without `-regen`). A block is one week per occupied room, restarting at the first room each time — `isBlock` in `cmd/seed/main.go` marks that, so `-weeks` and the carry-over from the previous block don't apply. `checkBlockDuties` holds both of `hall`'s rules in one place and is called twice: by `run` before it asks anything else or connects, and by `seed` whoever called it. Besides refusing `hall` without `-regen`, it refuses `hall` in the same run as any other duty — `-regen` deletes from `-start` forward for *every* duty selected, so the combination would silently drop months of the in-flat schedule. The interactive list keeps that selection from being made at all: `hall`'s row is `exclusive`, so picking it clears the others and picking any other clears it (`toggle`/`selectAll` in `cmd/seed/prompt.go`) — cleared rather than greyed out, which needs no disabled state and leaves every selection one key press away. `checkBlockDuties` stays as the backstop for the paths the list doesn't cover: a typed `-duty`, and the non-terminal fallback. `autoRegen` then supplies `-regen` for a block duty chosen from the list, since `hall` has no other mode — but never for a typed `-duty hall`, where a script that has always run it must keep failing rather than start deleting. What `-regen` costs is named instead of charged for: `countReplaced` counts the rows and their span *before* the `DELETE`, and `printDeletions` puts them above the confirmation. That catches the mistake typing the flag never did — a `-start` a year off looks like months deleted, not one block. `regenFrom` is the single definition of where the deletion begins, so the count and the generation can't disagree. `cmd/migrate` is a thin wrapper over `internal/migrate.Apply`.
- `cmd/setcommands` — one-shot CLI, pushes the Telegram command menu (`setMyCommands`) from `botcmd.Menu()` to every scope (default + `all_private_chats` + `all_group_chats` + `all_chat_administrators`), so an old scope-specific list can't shadow it; `-show` prints each scope's current menu.
- `cmd/mcp` — local MCP server (stdio) exposing the schedule read-only: `list_duties`, `on_duty`, `upcoming`. Thin adapter over `pkg/schedule`; opens a DB connection per call. Wired up in `.mcp.json` via `task mcp` (so it inherits `DATABASE_URL` from `.env`). Smoke-test with `scripts/mcp-smoke.sh`.
- `internal/migrate` — `Apply(ctx, conn, dir)`, the migration runner shared by `cmd/migrate` and the integration tests.
- `internal/pgtest` — integration-test helpers (`Raw` / `Connect` / `MigrationsDir`); `t.Skip` when `TEST_DATABASE_URL` is unset. `Raw` drops every application table by name — add new ones there or the second run fails re-applying their migration.
- `migrations/*.sql` — plain SQL, applied in order by `cmd/migrate`.

## Domain

Duty types (`schedule.DutyType`): `toilet1`, `toilet2`, `laundry`, `hall`, `floor`.
Recurrence in `pkg/schedule/schedule.go` `configs`:

- **Weekly duties** (`toilet1`, `toilet2`, `hall`, `floor`): event day Friday, window Fri–Sun (3 days).
- **Laundry** (`waschkueche`): event days Tuesday & Friday, 1-day window.

Rooms are labelled `Zimmer N` (`RoomNo`/`ParseRoomNo`). All user-facing text is German; weekday abbreviations `Mo`..`So`. Timezone is always `Europe/Berlin` (`_ "time/tzdata"` is imported for the Vercel runtime).

`/*_plan` commands show `PlanWeeks` (4) weeks ahead. DB: `schedules` `(duty_type, duty_date, room)`, unique on `(duty_type, duty_date)`; `users` `(id, tg_user_id, created_at, updated_at)`, unique on `tg_user_id` — first and last sighting of a Telegram id and nothing else, deliberately unlinked to any room.

Bot commands: `/toilette1`, `/toilette2`, `/treppenhaus` (hall), `/etage` (floor), `/waschkueche` (laundry), each with a `_plan` variant. Plus `/help` (command list, any chat) and `/start` (greeting, private chat only). All defined in `pkg/botcmd` — the `duties` slice there (name + German description + handler) drives the webhook dispatch, `/help`, and `setMyCommands`. After changing it, run `task setcommands` to update the Telegram menu (BotFather is no longer used).

## Testing

- `task test` — unit tests, no DB, fast. The SQL layer is faked.
- `task test:integration` — files tagged `//go:build integration` run against a throwaway `postgres:17` (Docker/OrbStack). Covers the real SQL / pgx paths in `pkg/schedule/repo.go`, `cmd/seed`, `internal/migrate`. Tests `t.Skip` when `TEST_DATABASE_URL` is unset, so a plain `go test ./...` never needs a database.
- CI runs both in the `test` job (integration on a `postgres:17` service container); that job gates `migrate` and `deploy`.
- `task smoke:cron` — `//go:build smoke`, outside both. It runs the real `Cron` against a deliberately unroutable `DATABASE_URL`, so the run fails early and only `ADMIN_CHAT_ID` hears about it; the group is never reached. Proves the seam the unit tests can't: real config, real Bot API.

## Deployment

- `main` → production (Vercel `--prod`), `dev` → preview. Deploy is automatic from CI on push — never run `vercel` locally.
- On push to `main`, CI also runs `task migrate` against the production DB.
- CI scans the git history with gitleaks (`.gitleaks.toml`, own job, doesn't gate deploy — a committed secret has to be rotated, not un-deployed). README has the rotation runbook.
- Secrets live in `.env` locally and the Vercel dashboard: `TELEGRAM_BOT_TOKEN`, `CHAT_ID`, `DATABASE_URL`, `WEBHOOK_SECRET`, `CRON_SECRET`, `ADMIN_CHAT_ID` (optional). Never read, print, or commit `.env`.

## Conventions

- Go 1.27, `golangci-lint` v2 (`errcheck`, `govet`, `ineffassign`, `staticcheck`, `unused`, `gosec`).
- `api/` holds function entrypoints only: every non-test `.go` there must export one `func Name(w http.ResponseWriter, r *http.Request)` — Vercel builds each file as a separate serverless function. Shared logic goes in `pkg/`.
- Keep new date/cadence logic derived from `configs` — don't duplicate weekday literals (see the `EventWeekdays` comment). `schedule.WeeklyReminderDay` / `ReminderWeekdays` feed both `api/cron.go` and the closing line of `/help`. `schedule.ReminderHour` is the one value that can't be derived: it mirrors `vercel.json` by hand, so change both together.
- Commit messages: no `Co-Authored-By` trailers.