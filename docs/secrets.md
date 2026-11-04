# Secrets

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

## If one leaks

Rotating is the only fix — a secret that reached a commit is public even after
the commit is gone, since forks and caches keep it.

| Secret | How to rotate |
|---|---|
| `TELEGRAM_BOT_TOKEN` | `/revoke` in [@BotFather](https://t.me/BotFather), which issues a new token; put it in Vercel and `.env` |
| `WEBHOOK_SECRET` | new random string in Vercel, then re-register the webhook — Telegram only sends the header it was given at `setWebhook` time, so the bot goes deaf until you do |
| `CRON_SECRET` | new value in Vercel, then redeploy; Vercel sends it itself, nothing else to update |
| `DATABASE_URL` | reset the role's password in the Neon dashboard, then update Vercel and the `DATABASE_URL` secret in GitHub Actions, which `task migrate` uses |
