// Command seed generates future rows for the four per-floor duties (toilet1,
// toilet2, Etage, Waschküche) and writes them to DATABASE_URL. Treppenhaus is
// left manual. It's a local dev tool, not a Vercel function.
//
// Run it through Task so it picks up DATABASE_URL from .env; flags go after --:
//
//	task seed -- -start 2026-07-14                     # seed/continue all four
//	task seed -- -vacant 1,6 -regen -start 2026-08-28  # regenerate after a move-out
//	task seed -- -vacant 1,6 -regen -start 2026-08-28 -dry
//	task seed -- -duty laundry -weeks 12 -regen -start 2026-09-04  # one duty, shorter horizon
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nikitasomusev/kehrwoche/pkg/config"
	"github.com/nikitasomusev/kehrwoche/pkg/db"
	"github.com/nikitasomusev/kehrwoche/pkg/schedule"
)

// txQuerier is the subset of pgx.Tx that planDuty/lastRow actually use —
// narrower than the full transaction interface so the room-rotation logic
// is testable with a fake, without a real DB transaction.
type txQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

var rotations = map[schedule.DutyType][]int{
	schedule.DutyTypeToilet1: {4, 3, 7},
	schedule.DutyTypeToilet2: {1, 2, 5, 6, 8},
	schedule.DutyTypeFloor:   {1, 2, 3, 4, 5, 6, 7, 8},
	schedule.DutyTypeLaundry: {8, 1, 2, 3, 4, 5, 6, 7},
}

var dutyOrder = []schedule.DutyType{
	schedule.DutyTypeToilet1, schedule.DutyTypeToilet2,
	schedule.DutyTypeFloor, schedule.DutyTypeLaundry,
}

const dateLayout = "2006-01-02"

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
	dutyStr := flag.String("duty", "", "comma-separated duties to target: toilet1,toilet2,floor,laundry (default: all four)")
	weeks := flag.Int("weeks", 26, "weeks of schedule to generate per duty (laundry runs twice a week, so it gets twice the rows)")
	startStr := flag.String("start", "", "start date YYYY-MM-DD (required for a duty with no rows yet, or with -regen)")
	vacantStr := flag.String("vacant", "", "comma-separated vacant room numbers (omit to be prompted)")
	regen := flag.Bool("regen", false, "delete existing rows from -start forward, then regenerate (use after a move-out)")
	dry := flag.Bool("dry", false, "print planned rows without writing")
	flag.Parse()

	if err := run(context.Background(), *dutyStr, *weeks, *startStr, *vacantStr, *regen, *dry); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, dutyStr string, weeks int, startStr, vacantStr string, regen, dry bool) error {
	if regen && startStr == "" {
		return fmt.Errorf("-regen requires -start")
	}

	duties, err := selectedDuties(dutyStr)
	if err != nil {
		return err
	}

	vacant, err := resolveVacant(vacantStr)
	if err != nil {
		return err
	}

	conn, err := db.Connect(ctx, config.Load().DatabaseURL)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, duty := range duties {
		active := activeRooms(rotations[duty], vacant)
		if len(active) == 0 {
			fmt.Printf("%s: no occupied rooms, skipped\n", duty.Label())
			continue
		}
		rows, err := planDuty(ctx, tx, duty, rotations[duty], active, periods(weeks, duty), startStr, regen)
		if err != nil {
			return err
		}
		for _, r := range rows {
			room := schedule.RoomNo(r.room)
			fmt.Printf("%-12s %s  %s\n", duty, r.date.Format(dateLayout), room)
			if dry {
				continue
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO schedules (duty_type, duty_date, room) VALUES ($1, $2, $3)`,
				duty, r.date, room,
			); err != nil {
				return fmt.Errorf("insert %s %s: %w", duty, r.date.Format(dateLayout), err)
			}
		}
	}

	if dry {
		fmt.Println("seed: dry run, nothing written")
		return nil
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	fmt.Println("seed: done")
	return nil
}

type plannedRow struct {
	date time.Time
	room int
}

func planDuty(ctx context.Context, tx txQuerier, duty schedule.DutyType, rotation, active []int, n int, startStr string, regen bool) ([]plannedRow, error) {
	var date time.Time
	var idx int

	switch {
	case regen:
		// startStr is guaranteed non-empty here (run rejects -regen without -start).
		parsed, err := parseStart(startStr)
		if err != nil {
			return nil, err
		}
		date = duty.EventDate(parsed)

		// Continue the rotation from the last assignment that survives the
		// delete, so dropping a room closes the cycle up instead of restarting
		// it at the first room.
		prev, hasPrev, err := lastRow(ctx, tx, duty, date)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM schedules WHERE duty_type = $1 AND duty_date >= $2`,
			duty, date,
		); err != nil {
			return nil, fmt.Errorf("regen delete %s: %w", duty, err)
		}
		if hasPrev {
			idx = nextActiveIndex(rotation, active, prev.room)
		}

	default:
		last, hasLast, err := lastRow(ctx, tx, duty, time.Time{})
		if err != nil {
			return nil, err
		}
		switch {
		case hasLast:
			date = duty.NextEventDate(last.date)
			idx = nextActiveIndex(rotation, active, last.room)
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
		return dutyOrder, nil
	}
	want := make(map[schedule.DutyType]bool)
	for _, part := range strings.Split(flagVal, ",") {
		want[schedule.DutyType(strings.TrimSpace(part))] = true
	}
	var out []schedule.DutyType
	for _, d := range dutyOrder {
		if want[d] {
			out = append(out, d)
			delete(want, d)
		}
	}
	for d := range want {
		return nil, fmt.Errorf("unknown or non-generatable duty %q (valid: toilet1, toilet2, floor, laundry)", d)
	}
	return out, nil
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

func resolveVacant(flagVal string) (map[int]bool, error) {
	raw := flagVal
	if raw == "" {
		fmt.Print("Vacant room numbers (comma-separated, empty = all occupied): ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		raw = strings.TrimSpace(line)
	}
	vacant := make(map[int]bool)
	if raw == "" {
		return vacant, nil
	}
	for _, part := range strings.Split(raw, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			return nil, fmt.Errorf("invalid vacant room %q", part)
		}
		vacant[n] = true
	}
	return vacant, nil
}
