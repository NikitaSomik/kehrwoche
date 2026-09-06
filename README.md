# Kehrwoche

![CI](https://github.com/NikitaSomik/kehrwoche/actions/workflows/ci.yml/badge.svg)
![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)
![Vercel](https://img.shields.io/badge/Vercel-serverless-000000?logo=vercel&logoColor=white)
![golangci-lint](https://img.shields.io/badge/golangci--lint-enabled-brightgreen)

A Telegram bot that reminds a shared flat (WG) group chat who's on cleaning duty this week.

Runs as serverless Go functions on Vercel, with the rotation schedule stored in a Postgres database (Neon).

## Setup

```bash
cp .env.example .env   # fill in the values
```

| Variable | Description |
|---|---|
| `TELEGRAM_BOT_TOKEN` | bot token from @BotFather |
| `CHAT_ID` | group chat the weekly reminder is sent to |
| `DATABASE_URL` | Postgres connection string (use Neon's pooler endpoint) |
| `WEBHOOK_SECRET` | secret Telegram must send with every webhook call |

The webhook answers only the `CHAT_ID` group and private chats whose sender is
a member of it, so a stranger who finds the bot by name gets nothing. Residents
keep their private chats without anyone collecting their user ids: membership is
asked of Telegram per message and follows the group as people move in and out.
| `CRON_SECRET` | bearer token Vercel Cron must send |

Set the same variables in the Vercel project dashboard for deployment.

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
task setcommands       # push the bot's command menu to Telegram (manual)
task mcp               # run the MCP server (started by an MCP client, not by hand)
```

## Integration tests

`task test:integration` starts a disposable `postgres:17` container, applies the
migrations, and runs the tests tagged `//go:build integration` against it — the
SQL and pgx paths (`pkg/schedule/repo.go`, `cmd/seed`, `internal/migrate`) that
the unit tests fake. Plain `task test` needs no database. CI runs both.

## Bot commands

`/toilette1`, `/toilette2`, `/treppenhaus`, `/etage`, `/waschkueche` — who's on
duty this week — each with a `_plan` variant for the next 4 weeks, plus `/help`
and `/start`. All defined in `pkg/botcmd`. `task setcommands` pushes the menu to
Telegram (`setMyCommands`); `task setcommands -- -show` prints the current one.

## MCP server

`cmd/mcp` is a local [MCP](https://modelcontextprotocol.io) server (stdio) that
exposes the schedule read-only, so an MCP client (Claude Code, Claude Desktop)
can answer questions about it:

| Tool | Purpose |
|---|---|
| `list_duties` | every duty: label, event weekdays, window, last date generated |
| `on_duty` | who is on duty on a date (all duties, or one) |
| `upcoming` | the next assignments for one duty, or every duty |

It's registered in `.mcp.json` and reads `DATABASE_URL` from `.env` (via `task mcp`).

## Regenerating the schedule

`cmd/seed` fills the `schedules` table. `-weeks` is the horizon per duty
(laundry runs twice a week, so it gets twice the rows). Always dry-run first.

```bash
task seed -- -dry                                            # continue every duty, 26 weeks
task seed -- -vacant 1,6 -regen -start 2026-08-28 -dry       # rewrite the future after a move-out
task seed -- -duty laundry -weeks 12 -regen -start 2026-09-04 -dry
```

`-regen` deletes rows from `-start` forward and rewrites them, continuing the
rotation from the last surviving row. Without `-regen` it only appends.

### Treppenhaus

The staircase rotates between the floors of the house. When our turn comes
round, each occupied room takes one week in order, and once they have all had
their week the next floor takes over. How many rooms the other floors have is
not ours to know, so the week our turn comes back cannot be computed — the
house tells us. `hall` is therefore left out of the default set and seeded one
block at a time:

```bash
task seed -- -duty hall -regen -start 2026-11-06 -dry
```

Two things are decided for you here. The block is exactly as long as there are
occupied rooms — eight rooms, eight consecutive Fridays — so `-weeks` does not
apply. And every block starts again at the first occupied room rather than
carrying on from the last one, so a miscounted block can't leave the rotation
permanently out of step.

`-regen` is the normal mode for `hall`, not the exceptional one: without it
`-start` is ignored for a duty that already has rows, and seeding would run
straight on from the last block into weeks that belong to the other floors.
Seeding `hall` without it is refused.
