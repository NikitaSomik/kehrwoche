# Regenerating the schedule

`cmd/seed` fills the `schedules` table. `-weeks` is the horizon per duty
(laundry runs twice a week, so it gets twice the rows), defaulting to 8.
Always dry-run first.

The cron warns with `schedule.HorizonWeeks` (4) left, so a top-up of 8 keeps
roughly one to three months planned ahead. That is deliberately short: which
rooms are occupied is passed in on every run and stored nowhere, so a stale
`-vacant` can only mislead until the next top-up.

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
task seed -- -dry                                            # continue every duty, 8 weeks
task seed -- -vacant 1,6 -regen -start 2026-08-28 -dry       # rewrite the future after a move-out
task seed -- -duty laundry -weeks 12 -regen -start 2026-09-04 -dry
```

`-regen` deletes rows from `-start` forward and rewrites them, continuing the
rotation from the last surviving row. Without `-regen` it only appends.

You don't have to remember to do this: once a week the cron checks how far
ahead each rolling-horizon duty is planned and, with `ADMIN_CHAT_ID` set,
messages that chat privately when one is within `schedule.HorizonWeeks` of
running out. Unset, the notice is simply skipped — it never falls back to the
group chat, since only whoever runs `cmd/seed` can act on it.

## Treppenhaus

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
