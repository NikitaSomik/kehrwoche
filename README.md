# Kehrwoche

![CI](https://github.com/NikitaSomik/kehrwoche/actions/workflows/ci.yml/badge.svg)
![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)
![Vercel](https://img.shields.io/badge/Vercel-serverless-000000?logo=vercel&logoColor=white)
![golangci-lint](https://img.shields.io/badge/golangci--lint-enabled-brightgreen)

A Telegram bot that reminds a shared flat (WG) group chat who's on cleaning duty this week.

Runs as serverless Go functions on Vercel, with the rotation schedule stored in a Postgres database (Neon).

Diagrams (architecture, onboarding, database) and the runbooks (schedule, secrets) are in [docs/](docs/README.md).

## Bot commands

`/toilette1`, `/toilette2`, `/treppenhaus`, `/etage`, `/waschkueche` — who's on
duty this week — each with a `_plan` variant for the next 4 weeks, plus `/help`
and `/start`. All defined in `pkg/botcmd`.

In the WG group (`CHAT_ID`) the commands answer everyone; other groups get
nothing. In private they answer only admins of the group (`creator` /
`administrator`, checked with `getChatMember` on every command). An admin's
`/start` shows the command list and sets the command menu for that chat. A
member's `/start` gets a greeting, a banned user's nothing, anybody else's
starts the onboarding. Anything else such a person writes is answered with a
pointer to `/start` while the onboarding is on.

On Tuesday, Thursday and Friday around 10:00 Berlin time the bot posts the
reminder to the group by itself.

## Onboarding

A new resident opens the bot and sends `/start`:

1. `ADMIN_CHAT_ID` gets a card with their name, id and ✅ / ❌ buttons; only an
   admin of the WG group can press them. A 👤 button opens their real profile
   (names are anyone's to pick); it is left off when their privacy settings
   refuse it.
2. ✅ — they pick their room from buttons 1–8. ❌ — they are told once; after
   that the bot ignores their `/start`.
3. The bot sends them a personal join-request link (valid 24 h; another
   `/start` sends the same one until it is nearly expired). Until they join,
   a „Zimmer ändern“ button under it lets them pick the room again. It approves
   their request only if it comes from them through that link, revokes the
   link, sets their member tag to the room number (`3`) and greets them in the
   group. Any other join request is left to you.

Someone already in the group who sends `/start` just gets a greeting.

When a member leaves the group or is removed, the bot tells `ADMIN_CHAT_ID`,
with their room if it knows it, so a move-out isn't missed in the rotation.
Someone who was in the group before and asks again goes through the same
steps, room choice included: the card says „War schon Mitglied der WG“ (with
the date they left, when known) and the group hears „👋 … ist wieder da!“
instead of the newcomer welcome. A banned user stays silent until the ban is
lifted in the group's removed users.

The whole flow, branch by branch, is drawn in [docs/onboarding.md](docs/onboarding.md);
where its state is kept, in [docs/database.md](docs/database.md).

## Setup

| Variable | Description |
|---|---|
| `TELEGRAM_BOT_TOKEN` | bot token from @BotFather |
| `CHAT_ID` | group chat the weekly reminder is sent to |
| `DATABASE_URL` | Postgres connection string (use Neon's pooler endpoint) |
| `WEBHOOK_SECRET` | secret Telegram must send with every webhook call |
| `CRON_SECRET` | bearer token Vercel Cron must send |
| `ADMIN_CHAT_ID` | your private chat with the bot (= your Telegram id) or a small group with the bot in it: failure notices and onboarding approvals. Only admins of the WG group can approve or reject |
| `ONBOARDING_ENABLED` | `true` turns the onboarding on; unset or anything else leaves it off, and strangers' `/start` gets a plain greeting |

First run, in this order:

1. `cp .env.example .env` and fill in the values. Set the same variables in the
   Vercel project dashboard.
2. `task migrate` creates the tables.
3. `task seed` fills the schedule; it asks for everything it needs (see
   [docs/schedule.md](docs/schedule.md)).
4. Deploy by pushing to `main` (see [Deployment](#deployment)).
5. Make the bot an admin of the WG group with **Add Users**
   (`can_invite_users`) and **Edit Member Tags** (`can_manage_tags`) — the
   onboarding needs both.
6. `task setwebhook -- -url https://<project>.vercel.app/api/webhook` registers
   the webhook with `WEBHOOK_SECRET` and the update kinds the bot handles
   (`callback_query`, `chat_join_request` and `chat_member` among them; the
   default registration leaves `chat_member` out). Later runs keep the URL
   Telegram has; `task setwebhook -- -show` prints the current registration.
7. `task setcommands` sets the command menu for the group and `/start` for
   private chats.
8. To turn the onboarding on, set `ONBOARDING_ENABLED=true` in the Vercel
   dashboard and redeploy: Vercel applies a changed variable only to new
   deployments. `false` turns it off the same way; a request already in flight
   stays where it is.

## Deployment

CI deploys; nothing is deployed by hand.

- Push to `main` → tests, then `task migrate` against the production database,
  then a production deploy.
- Push to `dev` → tests, then a preview deploy.
- The GitHub repository needs the secrets `VERCEL_TOKEN`, `VERCEL_ORG_ID`,
  `VERCEL_PROJECT_ID` and `DATABASE_URL`.

## Regenerating the schedule

`task seed` fills the `schedules` table: run it with no flags and it asks for
everything, prints the plan and waits for a yes. The cron tells `ADMIN_CHAT_ID`
when a duty is about to run out. Flags, `-regen` after a move-out and the
staircase blocks: [docs/schedule.md](docs/schedule.md).

## Secrets

`TELEGRAM_BOT_TOKEN`, `WEBHOOK_SECRET`, `CRON_SECRET` and `DATABASE_URL` live in
`.env` locally and in the Vercel dashboard; never commit them. CI scans the git
history with gitleaks. How the scan works and how to rotate each secret after a
leak: [docs/secrets.md](docs/secrets.md).

## Development

Install [Task](https://taskfile.dev):

```bash
brew install go-task/tap/go-task   # macOS
```

```bash
task test              # unit tests (no database)
task test:integration  # integration tests against a throwaway Postgres (needs Docker)
task build             # compile all packages
task vet               # go vet
task lint              # golangci-lint
task fmt               # gofmt
task tidy              # tidy dependencies
task migrate           # apply database migrations
task seed -- -dry      # seed / regenerate the schedule (flags after --)
task setcommands       # command menu for the group, /start in private (manual)
task setwebhook        # re-register the webhook with the update kinds the bot handles (manual)
task mcp               # run the MCP server (started by an MCP client, not by hand)
```

`task test:integration` starts a disposable `postgres:17` container, applies the
migrations, and runs the tests tagged `//go:build integration` against it — the
SQL and pgx paths (`pkg/schedule/repo.go`, `cmd/seed`, `internal/migrate`) that
the unit tests fake. Plain `task test` needs no database. CI runs both.

`task setcommands -- -show` prints the command menu of each broad scope.

### MCP server

`cmd/mcp` is a local [MCP](https://modelcontextprotocol.io) server (stdio) that
exposes the schedule read-only, so an MCP client (Claude Code, Claude Desktop)
can answer questions about it:

| Tool | Purpose |
|---|---|
| `list_duties` | every duty: label, event weekdays, window, last date generated |
| `on_duty` | who is on duty on a date (all duties, or one) |
| `upcoming` | the next assignments for one duty, or every duty |

It's registered in `.mcp.json` and reads `DATABASE_URL` from `.env` (via `task mcp`).
