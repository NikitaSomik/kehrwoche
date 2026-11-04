# Architecture: who calls what

Every arrow means "calls" or "reads/writes". Read it top to bottom: something
outside starts a run, an entry point handles it, and the run ends in the
database or the Telegram Bot API.

```mermaid
flowchart TB
  subgraph triggers["Who starts a run"]
    direction LR
    TG(["Telegram<br/>messages, buttons, join requests"])
    VC(["Vercel Cron<br/>Tue, Thu, Fri morning"])
    DEV(["Developer<br/>task seed / migrate / set*"])
    AI(["MCP client<br/>e.g. Claude"])
  end

  subgraph entry["Entry points"]
    direction LR
    webhook["api/webhook.go"]
    cron["api/cron.go"]
    tools["cmd/seed · cmd/migrate<br/>cmd/setcommands · cmd/setwebhook"]
    mcp["cmd/mcp<br/>read-only"]
  end

  subgraph logic["Use cases"]
    direction LR
    botcmd["pkg/botcmd<br/>slash commands"]
    onboard["pkg/onboard<br/>joining the group"]
  end

  subgraph data["Data"]
    users["pkg/users<br/>users SQL<br/><i>webhook writes here too</i>"]
    schedule["pkg/schedule<br/>date rules + schedule SQL"]
    PG[("Postgres · Neon")]
  end

  subgraph integ["Telegram"]
    telegram["pkg/telegram<br/>Bot API client<br/><i>used by webhook, cron,<br/>onboard and cmd/set*</i>"]
    TGAPI[("Telegram Bot API")]
  end

  TG --> webhook
  VC --> cron
  DEV --> tools
  AI --> mcp

  webhook --> botcmd
  webhook --> onboard
  cron --> schedule
  tools --> schedule
  mcp --> schedule

  botcmd --> schedule
  onboard --> users

  schedule --> PG
  users --> PG
  telegram --> TGAPI
  botcmd ~~~ telegram
```

Two edges are written into the boxes instead of drawn, because as arrows they
cross half the picture:

- `pkg/telegram` is called by `api/webhook.go`, `api/cron.go`, `pkg/onboard`
  and `cmd/setcommands` / `cmd/setwebhook`. It is the only code that talks to
  the Bot API.
- `api/webhook.go` writes to `pkg/users` directly: `recordUser` notes whoever
  runs a duty command.

What the picture doesn't show:

- `pkg/config` (read once by every entry point) and `pkg/db` (`db.Connect`).
- Each file in `api/` is its own Vercel function: they share code only through
  `pkg/`.
- Only `pkg/schedule/repo.go` and `pkg/users` hold SQL.
