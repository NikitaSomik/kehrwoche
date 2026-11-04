# Docs

The README keeps the setup and day-to-day instructions; what only comes up now
and then lives here.

Diagrams (Mermaid, so GitHub draws them):

- [Architecture](architecture.md): who calls what, from the triggers down to
  Postgres and the Bot API.
- [Onboarding flow](onboarding.md): every branch after `/start`, up to the
  approved join request.
- [Database](database.md): the `schedules` and `users` tables.

Runbooks:

- [Regenerating the schedule](schedule.md): `task seed`, `-regen` after a
  move-out, the staircase (`hall`) one block at a time.
- [Secrets](secrets.md): where they live, the gitleaks scan, and how to rotate
  each one after a leak.
