// Command seed generates future rows for the four per-floor duties (toilet1,
// toilet2, Etage, Waschküche) and writes them to DATABASE_URL. Treppenhaus is
// left manual. It's a local dev tool, not a Vercel function.
//
//	go run ./cmd/seed -start 2026-07-14      # seed/continue all four
//	go run ./cmd/seed -vacant 5 -regen -start 2026-09-01
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
	"github.com/nikitasomusev/kehrwoche/pkg/db"
	"github.com/nikitasomusev/kehrwoche/pkg/schedule"
)

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

func main() {
	dutyStr := flag.String("duty", "", "comma-separated duties to target: toilet1,toilet2,floor,laundry (default: all four)")
	nPeriods := flag.Int("n", 26, "number of periods to generate per duty")
	startStr := flag.String("start", "", "start date YYYY-MM-DD (required for a duty with no rows yet, or with -regen)")
	vacantStr := flag.String("vacant", "", "comma-separated vacant room numbers (omit to be prompted)")
	regen := flag.Bool("regen", false, "delete existing rows from -start forward, then regenerate (use after a move-out)")
	dry := flag.Bool("dry", false, "print planned rows without writing")
	flag.Parse()

	if err := run(context.Background(), *dutyStr, *nPeriods, *startStr, *vacantStr, *regen, *dry); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, dutyStr string, nPeriods int, startStr, vacantStr string, regen, dry bool) error {
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

	conn, err := db.Connect(ctx)
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
		rows, err := planDuty(ctx, tx, duty, active, nPeriods, startStr, regen)
		if err != nil {
			return err
		}
		for _, r := range rows {
			room := schedule.RoomNo(r.room)
			fmt.Printf("%-12s %s  %s\n", duty, r.date.Format("2006-01-02"), room)
			if dry {
				continue
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO schedules (duty_type, duty_date, room) VALUES ($1, $2, $3)`,
				duty, r.date, room,
			); err != nil {
				return fmt.Errorf("insert %s %s: %w", duty, r.date.Format("2006-01-02"), err)
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

func planDuty(ctx context.Context, tx pgx.Tx, duty schedule.DutyType, active []int, n int, startStr string, regen bool) ([]plannedRow, error) {
	last, hasLast, err := lastRow(ctx, tx, duty)
	if err != nil {
		return nil, err
	}

	var date time.Time
	var idx int
	switch {
	case hasLast && !regen:
		date = duty.NextEventDate(last.date)
		idx = indexAfter(active, last.room)
	default:
		if startStr == "" {
			return nil, fmt.Errorf("%s: no existing rows, -start required", duty)
		}
		parsed, err := time.Parse("2006-01-02", startStr)
		if err != nil {
			return nil, fmt.Errorf("invalid -start: %w", err)
		}
		date = duty.EventDate(parsed)
		idx = 0
		if regen {
			if _, err := tx.Exec(ctx,
				`DELETE FROM schedules WHERE duty_type = $1 AND duty_date >= $2`,
				duty, date,
			); err != nil {
				return nil, fmt.Errorf("regen delete %s: %w", duty, err)
			}
		}
	}

	rows := make([]plannedRow, n)
	for i := 0; i < n; i++ {
		rows[i] = plannedRow{date: date, room: active[(idx+i)%len(active)]}
		date = duty.NextEventDate(date)
	}
	return rows, nil
}

type dbRow struct {
	date time.Time
	room int
}

func lastRow(ctx context.Context, tx pgx.Tx, duty schedule.DutyType) (dbRow, bool, error) {
	var date time.Time
	var name string
	err := tx.QueryRow(ctx,
		`SELECT duty_date, room FROM schedules
		 WHERE duty_type = $1 ORDER BY duty_date DESC LIMIT 1`,
		duty,
	).Scan(&date, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return dbRow{}, false, nil
	}
	if err != nil {
		return dbRow{}, false, err
	}
	num, err := roomNumber(name)
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

func indexAfter(active []int, room int) int {
	for i, r := range active {
		if r == room {
			return (i + 1) % len(active)
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

func roomNumber(name string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(name, "Zimmer")))
	if err != nil {
		return 0, fmt.Errorf("unexpected room name %q", name)
	}
	return n, nil
}
