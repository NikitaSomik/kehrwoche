// Command seed generates future rows for the cleaning duties and writes them
// to DATABASE_URL. It's a local dev tool, not a Vercel function.
//
// The four in-flat duties (toilet1, toilet2, Etage, Waschküche) run on our own
// weekly cycle, so they're seeded together for months ahead. Treppenhaus is
// different: whose turn it is on the staircase is decided per floor by the
// house, not by us, so it's only ever seeded explicitly with -duty hall, one
// block at a time, once the date of our next turn is known. A block is one
// week per occupied room, in order, starting again from the first — so its
// length is not a choice and -weeks does not apply to it.
//
// Run it through Task so it picks up DATABASE_URL from .env; flags go after --:
//
//	task seed -- -start 2026-07-14                     # seed/continue the four
//	task seed -- -vacant 1,6 -regen -start 2026-08-28  # regenerate after a move-out
//	task seed -- -vacant 1,6 -regen -start 2026-08-28 -dry
//	task seed -- -duty laundry -weeks 12 -regen -start 2026-09-04  # one duty, shorter horizon
//	task seed -- -duty hall -regen -start 2026-11-06                # our floor's next staircase block
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/NikitaSomik/kehrwoche/pkg/config"
	"github.com/NikitaSomik/kehrwoche/pkg/db"
	"github.com/NikitaSomik/kehrwoche/pkg/schedule"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// txQuerier is the subset of pgx.Tx that planDuty/lastRow actually use —
// narrower than the full transaction interface so the room-rotation logic
// is testable with a fake, without a real DB transaction.
type txQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// rotations is the order rooms take their turn at each duty. Position matters,
// not membership: planDuty walks the slice, so the room after the last one
// assigned is simply the next entry. Where a cycle starts is decided at seed
// time (by the previous row, or by the first occupied room), not here.
var rotations = map[schedule.DutyType][]int{
	schedule.DutyTypeToilet1: {4, 3, 7},
	schedule.DutyTypeToilet2: {1, 2, 5, 6, 8},
	schedule.DutyTypeHall:    {1, 2, 3, 4, 5, 6, 7, 8},
	schedule.DutyTypeFloor:   {1, 2, 3, 4, 5, 6, 7, 8},
	schedule.DutyTypeLaundry: {8, 1, 2, 3, 4, 5, 6, 7},
}

// generatable is every duty -duty accepts: the ones rotations knows a room
// order for, in the domain's own order. Derived rather than listed, so
// rotations stays the only place a duty has to be registered.
func generatable() []schedule.DutyType {
	out := make([]schedule.DutyType, 0, len(rotations))
	for _, d := range schedule.AllDutyTypes() {
		if rotations[d] != nil {
			out = append(out, d)
		}
	}
	return out
}

// defaultDuties is what runs when -duty is omitted: everything generatable
// that isn't a block duty. Treppenhaus is left out on purpose — its weeks are
// handed to us by the house a block at a time, so extending it by the same
// horizon as the rest would invent dates we don't own. It has to be asked for
// by name.
func defaultDuties() []schedule.DutyType {
	out := make([]schedule.DutyType, 0, len(rotations))
	for _, d := range generatable() {
		if !isBlock(d) {
			out = append(out, d)
		}
	}
	return out
}

// isBlock reports whether a duty is seeded one complete pass at a time instead
// of on a rolling horizon. The staircase rotates between the floors of the
// house: when our turn comes round, every occupied room takes one week in
// order, and then the next floor takes over. So the block is exactly as long
// as there are occupied rooms, and it always restarts at the first of them —
// -weeks and the carry-over from the previous block don't apply.
func isBlock(d schedule.DutyType) bool {
	return d == schedule.DutyTypeHall
}

const dateLayout = "2006-01-02"

// regenFrom is the date -regen deletes from for a duty: the flag's date moved
// onto that duty's own event day. Counting what will be deleted and generating
// what replaces it have to agree on this boundary exactly, so they read it
// from here rather than each computing it.
func regenFrom(duty schedule.DutyType, startStr string) (time.Time, error) {
	parsed, err := parseStart(startStr)
	if err != nil {
		return time.Time{}, err
	}
	return duty.EventDate(parsed), nil
}

func parseStart(s string) (time.Time, error) {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid -start: %w", err)
	}
	return t, nil
}

// periods is how many rows to generate for one duty: `weeks` of calendar time
// times how many times a week that duty runs (laundry twice, the rest once).
func periods(weeks int, d schedule.DutyType) int {
	return weeks * len(d.EventWeekdays())
}

func main() {
	dutyStr := flag.String("duty", "", "comma-separated duties to target (default: every duty except hall, which must be named explicitly)")
	weeks := flag.Int("weeks", 26, "weeks of schedule to generate per duty (laundry runs twice a week, so it gets twice the rows; ignored for hall, whose block length is the number of occupied rooms)")
	startStr := flag.String("start", "", "start date YYYY-MM-DD (required for a duty with no rows yet, or with -regen)")
	vacantStr := flag.String("vacant", "", "comma-separated vacant room numbers (omit to be prompted)")
	regen := flag.Bool("regen", false, "delete existing rows from -start forward, then regenerate (use after a move-out)")
	dry := flag.Bool("dry", false, "print planned rows without writing")
	flag.Parse()

	// Which flags were actually typed, as opposed to left at their default.
	// -weeks 26 and an untouched -weeks look identical in the value alone.
	given := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) { given[f.Name] = true })

	f := cliFlags{
		duty:   *dutyStr,
		weeks:  *weeks,
		start:  *startStr,
		vacant: *vacantStr,
		regen:  *regen,
		dry:    *dry,
		given:  given,
	}
	if err := run(context.Background(), newAsker("seed"), f); err != nil {
		// A cancelled run has already said so inside the frame; anything
		// else is a failure, and stderr is where a failure belongs.
		if !errors.Is(err, errCancelled) {
			fmt.Fprintln(os.Stderr, "seed:", err)
		}
		os.Exit(1)
	}
}

// cliFlags is what the command line carried, plus which of it was actually
// typed — a value alone can't tell a default from a deliberate one.
type cliFlags struct {
	duty   string
	weeks  int
	start  string
	vacant string
	regen  bool
	dry    bool
	given  map[string]bool
}

// run wires the CLI flags to seed, asking for whatever was left out, and opens
// the connection from DATABASE_URL.
//
// -regen is deliberately never prompted for. It deletes every row from -start
// forward with no upper bound, and a question with a default is the wrong
// shape for that: destroying months of schedule should take typing the flag,
// not pressing a key at the wrong moment.
func run(ctx context.Context, a *asker, f cliFlags) error {
	a.intro("seed · Kehrwoche")

	duties, err := chooseDuties(a, f)
	if err != nil {
		return a.stop(err)
	}
	if autoRegen(duties, f, a.interactive) {
		f.regen = true
	}
	// Before anything else is asked or connected to: a selection the run can't
	// honour should cost one question, not all of them.
	if err := checkBlockDuties(duties, f.regen); err != nil {
		return err
	}

	if f.regen && f.start == "" {
		f.start, err = a.requireValue("Start date YYYY-MM-DD (rows from here on are deleted and rewritten)", "-start")
		if err != nil {
			return a.stop(err)
		}
	}

	// -weeks has no say over a block duty, so don't ask about it when that's
	// all we're seeding — the horizon there is the number of occupied rooms.
	if !f.given["weeks"] && !allBlock(duties) {
		f.weeks = a.intVal("Weeks of schedule per duty", f.weeks)
	}

	vacant, err := chooseVacant(a, f, duties)
	if err != nil {
		return a.stop(err)
	}

	conn, err := db.Connect(ctx, config.Load().DatabaseURL)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()

	return seed(ctx, conn, seedParams{
		duties: duties,
		weeks:  f.weeks,
		start:  f.start,
		vacant: vacant,
		regen:  f.regen,
		dry:    f.dry,
		style:  a.st,
		out:    a.out,
		confirm: func() bool {
			return a.confirm("Write this to the " + a.st.danger("production") + " database in .env?")
		},
		finish: a.outro,
	})
}

// autoRegen reports whether the run should supply -regen itself.
//
// A block duty has no other mode: without -regen there is nothing for it to
// generate, so demanding the flag on top of choosing the duty is a toll with
// no decision behind it. What -regen actually costs — rows deleted from -start
// forward — is put in front of the confirmation instead, counted, which also
// catches the mistake typing the flag never did: the wrong -start.
//
// Only for a selection made in the list, though. On the command line `-duty
// hall` has to keep failing loudly, or a script that has always run it would
// quietly start deleting.
func autoRegen(duties []schedule.DutyType, f cliFlags, interactive bool) bool {
	return interactive && !f.given["duty"] && !f.regen && allBlock(duties)
}

// chooseDuties resolves -duty, asking as a list when the flag wasn't given.
// What comes pre-selected is exactly -duty's own default, so answering with
// enter changes nothing.
func chooseDuties(a *asker, f cliFlags) ([]schedule.DutyType, error) {
	if f.given["duty"] {
		return selectedDuties(f.duty)
	}

	all := generatable()
	opts := make([]choice, len(all))
	on := make([]bool, len(all))
	for i, d := range all {
		opts[i] = choice{key: string(d), label: d.Label()}
		if isBlock(d) {
			// It replaces a block rather than extending a horizon, so it can
			// neither share the run nor be seeded without -regen. The list
			// enforces the first by clearing the other side; autoRegen
			// supplies the second, and what that deletes is named before the
			// confirmation instead of being paid for by typing a flag.
			opts[i].exclusive = true
			opts[i].hint = "runs on its own, replaces a block"
		}
		on[i] = !isBlock(d)
	}

	on, err := a.multiselect("Duties to seed", opts, on)
	if err != nil {
		return nil, err
	}

	var out []schedule.DutyType
	for i, d := range all {
		if on[i] {
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no duties selected")
	}
	return out, nil
}

// chooseVacant resolves -vacant. The list is every room that takes a turn at
// one of the duties being seeded, which is the point of asking this way: a
// wrong -vacant produces a schedule that looks entirely plausible and calls
// the wrong people, and the numbers are easier to check on screen than from
// memory.
func chooseVacant(a *asker, f cliFlags, duties []schedule.DutyType) (map[int]bool, error) {
	if f.given["vacant"] {
		return parseVacant(f.vacant)
	}

	rooms := roomsIn(duties)
	opts := make([]choice, len(rooms))
	on := make([]bool, len(rooms))
	for i, r := range rooms {
		opts[i] = choice{key: strconv.Itoa(r), label: schedule.RoomNo(r)}
	}

	on, err := a.multiselect("Vacant rooms", opts, on)
	if err != nil {
		return nil, err
	}

	vacant := make(map[int]bool)
	for i, r := range rooms {
		if on[i] {
			vacant[r] = true
		}
	}
	return vacant, nil
}

// roomsIn is every room that appears in the rotation of any of duties, in
// ascending order. The rotations themselves are ordered by turn rather than by
// number, which is right for generating and wrong for a list to check against.
func roomsIn(duties []schedule.DutyType) []int {
	seen := make(map[int]bool)
	var out []int
	for _, d := range duties {
		for _, r := range rotations[d] {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	slices.Sort(out)
	return out
}

func parseVacant(raw string) (map[int]bool, error) {
	vacant := make(map[int]bool)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("invalid vacant room %q", part)
		}
		vacant[n] = true
	}
	return vacant, nil
}

// seedConn is the slice of *pgx.Conn seed needs — one transaction. It lets
// integration tests pass a real connection while keeping the signature small.
type seedConn interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

type seedParams struct {
	duties []schedule.DutyType
	weeks  int
	start  string // "" unless -start was given
	vacant map[int]bool
	regen  bool
	dry    bool
	style  style
	// out is where the plan is printed; nil means os.Stdout.
	out io.Writer
	// finish prints the closing line — the asker's outro when there is a
	// frame to close, a plain "seed: ..." otherwise.
	finish func(msg string)
	// confirm is asked once the whole plan has been printed and staged in the
	// transaction, but before it is committed. nil means don't ask.
	confirm func() bool
}

// seed generates and writes rows for each duty in one transaction, rolling
// back on any error (and on -dry).
func seed(ctx context.Context, conn seedConn, p seedParams) error {
	if p.regen && p.start == "" {
		return fmt.Errorf("-regen requires -start")
	}
	if err := checkBlockDuties(p.duties, p.regen); err != nil {
		return err
	}

	w := p.out
	if w == nil {
		w = os.Stdout
	}
	finish := p.finish
	if finish == nil {
		finish = func(msg string) { fmt.Fprintln(w, "seed:", msg) }
	}
	st := p.style

	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var totals []dutyTotal
	var doomed []deletion
	for _, duty := range p.duties {
		active := activeRooms(rotations[duty], p.vacant)
		if len(active) == 0 {
			fmt.Fprintf(w, "%s  %s  %s\n", st.muted(symBar), duty.Label(), st.warning("no occupied rooms, skipped"))
			continue
		}
		block := isBlock(duty)
		n := periods(p.weeks, duty)
		note := ""
		if block {
			// Announce it: -weeks has no say here, and silently ignoring a flag
			// the caller passed is how you end up trusting the wrong horizon.
			n = len(active)
			note = "block of one week per occupied room (-weeks does not apply)"
		}
		// Count before planDuty runs the DELETE — afterwards there is nothing
		// left to count, and the confirmation would have nothing to name.
		if p.regen {
			d, err := countReplaced(ctx, tx, duty, p.start)
			if err != nil {
				return err
			}
			doomed = append(doomed, d)
		}

		rows, err := planDuty(ctx, tx, duty, rotations[duty], active, n, p.start, p.regen, block)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			continue
		}

		fmt.Fprintf(w, "%s  %s  %s\n", st.success(symBranch), st.strong(duty.Label()), st.muted(fmt.Sprintf("%d rows", len(rows))))
		if note != "" {
			fmt.Fprintf(w, "%s  %s\n", st.muted(symBar), st.muted(note))
		}
		for _, r := range rows {
			room := schedule.RoomNo(r.room)
			fmt.Fprintf(w, "%s  %s  %s\n", st.muted(symBar), r.date.Format(dateLayout), st.success(room))
			if p.dry {
				continue
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO schedules (duty_type, duty_date, room) VALUES ($1, $2, $3)`,
				duty, r.date, room,
			); err != nil {
				return fmt.Errorf("insert %s %s: %w", duty, r.date.Format(dateLayout), err)
			}
		}
		fmt.Fprintf(w, "%s\n", st.muted(symBar))
		totals = append(totals, dutyTotal{
			duty: duty,
			rows: len(rows),
			from: rows[0].date,
			to:   rows[len(rows)-1].date,
		})
	}

	// The totals go last and stay on screen: with hundreds of rows scrolled
	// past, this is what the confirmation is actually answered against.
	printTotals(w, st, totals)
	// What -regen takes away, next to what it puts back, and before the yes.
	printDeletions(w, st, doomed)

	if p.dry {
		finish("dry run, nothing written")
		return nil
	}
	// Everything above is already staged in the transaction, so declining here
	// costs nothing: the deferred Rollback undoes it. That turns "always run
	// -dry first" from a habit into something the tool enforces.
	if p.confirm != nil && !p.confirm() {
		finish("cancelled, nothing written")
		return nil
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	finish("done")
	return nil
}

// checkBlockDuties enforces the two rules a block duty brings with it. It
// lives in one place so run can apply it before it asks anything else or opens
// a connection, and seed can apply it again whoever called it.
//
// A block duty can't be continued on its own: the week its next block begins
// comes from the house, and without -regen, -start is ignored for a duty that
// already has rows — seeding would run straight on from the last block and
// fill in weeks belonging to the other floors.
//
// It also can't share a run. -regen deletes from -start forward for every duty
// in the run, so pairing the staircase with the in-flat duties would silently
// drop months of their schedule from the block's start date. No run means
// that, which is why this is refused rather than confirmed.
func checkBlockDuties(duties []schedule.DutyType, regen bool) error {
	var block, other []schedule.DutyType
	for _, d := range duties {
		if isBlock(d) {
			block = append(block, d)
		} else {
			other = append(other, d)
		}
	}
	if len(block) == 0 {
		return nil
	}
	if len(other) > 0 {
		return fmt.Errorf("%s is seeded on its own: it runs with -regen, which would delete %s from -start forward too",
			dutyList(block), dutyList(other))
	}
	if !regen {
		return fmt.Errorf("%s: seed it with -regen -start <first Friday of the block>", dutyList(block))
	}
	return nil
}

// deletion is what -regen will remove for one duty, counted before the DELETE
// runs so the confirmation can say so in rows and dates rather than in the
// name of a flag.
type deletion struct {
	duty schedule.DutyType
	rows int
	from time.Time
	to   time.Time
}

const replacedSQL = `SELECT count(*), min(duty_date), max(duty_date) FROM schedules WHERE duty_type = $1 AND duty_date >= $2`

func countReplaced(ctx context.Context, tx txQuerier, duty schedule.DutyType, startStr string) (deletion, error) {
	from, err := regenFrom(duty, startStr)
	if err != nil {
		return deletion{}, err
	}
	// min and max are NULL when nothing matches, so they are scanned through
	// pointers rather than into zero times that would print as real dates.
	var lo, hi *time.Time
	d := deletion{duty: duty}
	if err := tx.QueryRow(ctx, replacedSQL, duty, from).Scan(&d.rows, &lo, &hi); err != nil {
		return deletion{}, fmt.Errorf("count %s rows to replace: %w", duty, err)
	}
	if lo != nil && hi != nil {
		d.from, d.to = *lo, *hi
	}
	return d, nil
}

// printDeletions names what -regen removes. Typing the flag never guarded
// against the mistake that actually costs something — a -start earlier than
// intended — because the flag says nothing about which rows it reaches. A
// count and a span do: one block replaced looks like one block, and a slip of
// a year looks like months.
func printDeletions(w io.Writer, st style, dels []deletion) {
	var real []deletion
	total, width := 0, 0
	for _, d := range dels {
		if d.rows == 0 {
			continue
		}
		real = append(real, d)
		total += d.rows
		if n := len([]rune(d.duty.Label())); n > width {
			width = n
		}
	}
	if len(real) == 0 {
		return
	}

	fmt.Fprintf(w, "%s  %s\n", st.warning(symWarn),
		st.warning(fmt.Sprintf("-regen deletes %d existing rows first", total)))
	for _, d := range real {
		fmt.Fprintf(w, "%s  %s  %4d  %s … %s\n",
			st.muted(symBar),
			st.accent(fmt.Sprintf("%-*s", width, d.duty.Label())),
			d.rows,
			d.from.Format(dateLayout),
			d.to.Format(dateLayout))
	}
	fmt.Fprintf(w, "%s\n", st.muted(symBar))
}

// dutyTotal is one line of the summary: what a duty got, and the span it
// covers.
type dutyTotal struct {
	duty schedule.DutyType
	rows int
	from time.Time
	to   time.Time
}

func printTotals(w io.Writer, st style, totals []dutyTotal) {
	if len(totals) == 0 {
		return
	}
	width := 0
	for _, t := range totals {
		if n := len([]rune(t.duty.Label())); n > width {
			width = n
		}
	}

	fmt.Fprintf(w, "%s  %s\n", st.success(symBranch), st.strong("plan"))
	sum := 0
	for _, t := range totals {
		sum += t.rows
		// Pad before colouring: %-*s counts the escape bytes as width, so
		// colouring first would leave the column short by the sequence.
		fmt.Fprintf(w, "%s  %s  %4d  %s → %s\n",
			st.muted(symBar),
			st.accent(fmt.Sprintf("%-*s", width, t.duty.Label())),
			t.rows,
			t.from.Format(dateLayout),
			t.to.Format(dateLayout))
	}
	fmt.Fprintf(w, "%s  %s  %4d\n%s\n",
		st.muted(symBar),
		st.muted(fmt.Sprintf("%-*s", width, "total")),
		sum,
		st.muted(symBar))
}

type plannedRow struct {
	date time.Time
	room int
}

// restart makes the run begin at the first occupied room instead of carrying
// on from the last assignment — see isBlock.
func planDuty(ctx context.Context, tx txQuerier, duty schedule.DutyType, rotation, active []int, n int, startStr string, regen, restart bool) ([]plannedRow, error) {
	var date time.Time
	var idx int

	switch {
	case regen:
		// startStr is guaranteed non-empty here (run rejects -regen without -start).
		var err error
		date, err = regenFrom(duty, startStr)
		if err != nil {
			return nil, err
		}

		// Continue the rotation from the last assignment that survives the
		// delete, so dropping a room closes the cycle up instead of restarting
		// it at the first room. A block duty opens with its first room whatever
		// came before, so there is nothing to read.
		if !restart {
			prev, hasPrev, err := lastRow(ctx, tx, duty, date)
			if err != nil {
				return nil, err
			}
			if hasPrev {
				idx = nextActiveIndex(rotation, active, prev.room)
			}
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM schedules WHERE duty_type = $1 AND duty_date >= $2`,
			duty, date,
		); err != nil {
			return nil, fmt.Errorf("regen delete %s: %w", duty, err)
		}

	default:
		last, hasLast, err := lastRow(ctx, tx, duty, time.Time{})
		if err != nil {
			return nil, err
		}
		switch {
		case hasLast:
			date = duty.NextEventDate(last.date)
			if !restart {
				idx = nextActiveIndex(rotation, active, last.room)
			}
		case startStr == "":
			return nil, fmt.Errorf("%s: no existing rows, -start required", duty)
		default:
			parsed, err := parseStart(startStr)
			if err != nil {
				return nil, err
			}
			date = duty.EventDate(parsed)
		}
	}

	rows := make([]plannedRow, n)
	for i := range n {
		rows[i] = plannedRow{date: date, room: active[(idx+i)%len(active)]}
		date = duty.NextEventDate(date)
	}
	return rows, nil
}

type dbRow struct {
	date time.Time
	room int
}

const (
	lastRowSQL       = `SELECT duty_date, room FROM schedules WHERE duty_type = $1 ORDER BY duty_date DESC LIMIT 1`
	lastRowBeforeSQL = `SELECT duty_date, room FROM schedules WHERE duty_type = $1 AND duty_date < $2 ORDER BY duty_date DESC LIMIT 1`
)

// lastRow returns the latest stored assignment for duty. When before is
// non-zero only rows earlier than it count, which lets -regen read the row
// that survives its delete and continue the rotation from there.
func lastRow(ctx context.Context, tx txQuerier, duty schedule.DutyType, before time.Time) (dbRow, bool, error) {
	var row pgx.Row
	if before.IsZero() {
		row = tx.QueryRow(ctx, lastRowSQL, duty)
	} else {
		row = tx.QueryRow(ctx, lastRowBeforeSQL, duty, before)
	}

	var date time.Time
	var name string
	err := row.Scan(&date, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return dbRow{}, false, nil
	}
	if err != nil {
		return dbRow{}, false, err
	}
	num, err := schedule.ParseRoomNo(name)
	if err != nil {
		return dbRow{}, false, err
	}
	return dbRow{date: date, room: num}, true, nil
}

func selectedDuties(flagVal string) ([]schedule.DutyType, error) {
	if flagVal == "" {
		return defaultDuties(), nil
	}
	want := make(map[schedule.DutyType]bool)
	for _, part := range strings.Split(flagVal, ",") {
		want[schedule.DutyType(strings.TrimSpace(part))] = true
	}
	var out []schedule.DutyType
	for _, d := range generatable() {
		if want[d] {
			out = append(out, d)
			delete(want, d)
		}
	}
	for d := range want {
		return nil, fmt.Errorf("unknown or non-generatable duty %q (valid: %s)", d, dutyList(generatable()))
	}
	return out, nil
}

func dutyList(duties []schedule.DutyType) string {
	names := make([]string, len(duties))
	for i, d := range duties {
		names[i] = string(d)
	}
	return strings.Join(names, ", ")
}

func activeRooms(rotation []int, vacant map[int]bool) []int {
	active := make([]int, 0, len(rotation))
	for _, r := range rotation {
		if !vacant[r] {
			active = append(active, r)
		}
	}
	return active
}

// nextActiveIndex returns the index within active of the first non-vacant room
// that follows lastRoom in the full rotation order — so removing a room closes
// the cycle up rather than restarting it. Falls back to 0 when lastRoom isn't
// in the rotation (or nothing is active).
func nextActiveIndex(rotation, active []int, lastRoom int) int {
	start := -1
	for i, r := range rotation {
		if r == lastRoom {
			start = i
			break
		}
	}
	if start < 0 || len(active) == 0 {
		return 0
	}

	pos := make(map[int]int, len(active))
	for i, r := range active {
		pos[r] = i
	}
	for step := 1; step <= len(rotation); step++ {
		if i, ok := pos[rotation[(start+step)%len(rotation)]]; ok {
			return i
		}
	}
	return 0
}

// allBlock reports whether every duty selected is seeded a block at a time,
// which is when -weeks stops meaning anything.
func allBlock(duties []schedule.DutyType) bool {
	for _, d := range duties {
		if !isBlock(d) {
			return false
		}
	}
	return len(duties) > 0
}
