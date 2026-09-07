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

## Secrets

`TELEGRAM_BOT_TOKEN`, `WEBHOOK_SECRET`, `CRON_SECRET` and `DATABASE_URL` live in
`.env` locally and in the Vercel dashboard. `CHAT_ID` and `ADMIN_CHAT_ID` are
chat identifiers, not secrets — a leak there costs nothing.

CI scans the git history with [gitleaks](https://github.com/gitleaks/gitleaks)
on every push and pull request. `.gitleaks.toml` extends the default rules,
which already cover Telegram bot tokens, with one for a Postgres URL carrying
an inline password — the shape `DATABASE_URL` takes. Placeholders and the
throwaway test databases are allowlisted by value, not by file, so a real
credential added to one of those same files is still caught.

The scan runs as its own job and does not gate `deploy`: by the time it fires
the secret is already in the history, and refusing to deploy would not take it
back out. It is there to tell you to rotate.

### If one leaks

Rotating is the only fix — a secret that reached a commit is public even after
the commit is gone, since forks and caches keep it.

| Secret | How to rotate |
|---|---|
| `TELEGRAM_BOT_TOKEN` | `/revoke` in [@BotFather](https://t.me/BotFather), which issues a new token; put it in Vercel and `.env` |
| `WEBHOOK_SECRET` | new random string in Vercel, then re-register the webhook — Telegram only sends the header it was given at `setWebhook` time, so the bot goes deaf until you do |
| `CRON_SECRET` | new value in Vercel, then redeploy; Vercel sends it itself, nothing else to update |
| `DATABASE_URL` | reset the role's password in the Neon dashboard, then update Vercel and the `DATABASE_URL` secret in GitHub Actions, which `task migrate` uses |

## Regenerating the schedule

`cmd/seed` fills the `schedules` table. `-weeks` is the horizon per duty
(laundry runs twice a week, so it gets twice the rows). Always dry-run first.

Run it with no flags and it asks: which duties, how many weeks, which rooms are
empty — then prints the plan and waits for a yes before writing. Any flag you
do pass is taken as given and not asked about, so the commands below still work
unchanged, and a piped or redirected stdin skips every question rather than
hanging a script.

The duties and the empty rooms are picked from a list rather than typed: arrow
keys move, space toggles, `a` selects everything, enter confirms. Listing the
rooms is the point of asking this way — a wrong `-vacant` produces a schedule
that looks entirely plausible and calls the wrong people, and eight numbers are
easier to check on screen than from memory. Where a list can't be drawn — a
redirected stdout, a terminal that won't go into raw mode — the same question
is answered by typing the same keys the flags take (`2,6`, `laundry`).

Ctrl+C is the way out: nothing is written, and the command leaves with 130,
the code a shell reports for an interrupted program. Esc does nothing, on
purpose — over a slow connection the three bytes of an arrow key can arrive
separately and be indistinguishable from it, and losing a key press is better
than losing the run.

`-regen` is the exception: it is never offered as a question. It deletes every
row from `-start` forward with no upper bound, and that should cost typing a
flag rather than a keystroke at the wrong moment.

Whatever `-regen` is about to remove is counted before it runs and printed
above the confirmation — how many rows, and between which dates. Typing the
flag never guarded against the mistake that actually costs something, a
`-start` earlier than intended, because the flag says nothing about which rows
it reaches; a count and a span do. This is for every `-regen`, a move-out among
the in-flat duties included, not only for the staircase.

Treppenhaus is the exception to the exception, because `-regen` is the only
mode it has — see below. It also cannot share a run: `-regen` would delete the
other duties' rows from `-start` too, behind a plan that looked perfectly
ordinary. So the list holds it apart — picking Treppenhaus clears the in-flat
duties, and picking any of them clears Treppenhaus — and choosing it supplies
the flag it can't do without.

```bash
task seed                                                    # ask for everything
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
