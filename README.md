# Kehrwoche

A Telegram bot that sends weekly toilet cleaning reminders to a shared flat (WG) group chat.

Every Thursday at 09:00 (Europe/Berlin) the bot posts who is on duty this week. Rooms rotate in a round-robin order defined by the `ROOMS` environment variable.

## Features

- Weekly reminder every Thursday at 09:00 Berlin time
- Round-robin rotation across rooms
- Auto-extends schedule on startup — adding a new room just requires updating `ROOMS` and restarting
- Graceful shutdown on SIGTERM/SIGINT
- `/wer` — who is on duty this week
- `/plan` — cleaning schedule for the next 4 weeks

## Requirements

- Go 1.26+
- Telegram bot token from [@BotFather](https://t.me/BotFather)
- Telegram group chat ID

## Getting started

```bash
cp .env.example .env   # fill in token and chat_id
make run               # start the bot (polling)
```

## Configuration

| Variable | Required | Description |
|----------|----------|-------------|
| `TELEGRAM_BOT_TOKEN` | yes | Bot token from @BotFather |
| `ROOMS` | yes | Comma-separated room names in rotation order |
| `CHAT_ID` | yes | Telegram group chat ID |
| `SCHEDULE_WEEKS` | no | Weeks to generate ahead (default: `8`) |
| `SCHEDULE_PATH` | no | Path to schedule file (default: `schedule.json`) |
| `TZ` | no | Timezone (default: `Europe/Berlin`) |
| `SEND_NOW` | no | Set to `1` to send reminder immediately and exit |

Example `.env`:
```
TELEGRAM_BOT_TOKEN=1234567890:AAF...
ROOMS=Zimmer 1,Zimmer 2,Zimmer 6,Zimmer 8
CHAT_ID=-1001234567890
```

## Telegram commands

| Command | Description |
|---------|-------------|
| `/wer` | Who is on duty this week |
| `/plan` | Cleaning schedule for the next 4 weeks |

## Development

```bash
make run    # run with polling
make send   # send reminder once and exit
make build  # build binary to out/main
make test   # run tests
make vet    # run go vet
make fmt    # format code
make tidy   # tidy dependencies
```

## Deployment (JustRunMy.App)

Git push → platform builds Docker image → runs it.

1. Add the deploy remote:
```bash
git remote add deploy <url from JustRunMy.App>
```
2. Set env vars in their dashboard
3. Push:
```bash
git push deploy main
```

On first start `schedule.json` is created automatically. No `.env` file needed on the server.

## Project structure

```
main.go                  entry point, Thursday scheduler, schedule bootstrap
internal/
  config/config.go       loads and validates configuration from env vars
  schedule/schedule.go   schedule logic: generate, extend, query
  bot/bot.go             Telegram polling, commands, message formatting
Dockerfile               multi-stage build (alpine, non-root user)
```

## Notes

**`schedule.json` is not in git** — generated on the server at startup. If the disk is ephemeral (reset on redeploy), the file is recreated automatically and rotation continues correctly.

**Adding a room** — update `ROOMS` and restart the bot. `Extend()` reads the last entry and appends new weeks using the updated room list, so the new room is picked up without losing history.

**ISO week format** — schedule is stored as `2026-W25`. The year is explicit to avoid ambiguity at year boundaries (W52 2026 ≠ W52 2027).

**Polling, not webhook** — the bot polls Telegram every 60 seconds. Sufficient for a weekly reminder.
