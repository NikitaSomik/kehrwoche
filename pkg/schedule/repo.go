package schedule

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Querier is the subset of *pgx.Conn this package needs, so tests can pass a
// fake implementation instead of a live Postgres connection.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// GetOnDuty returns the on-duty result for dutyType for the week containing now.
// OnDutyResult.Room is empty when no schedule entry exists for the week.
func GetOnDuty(ctx context.Context, conn Querier, dutyType DutyType, now time.Time) (OnDutyResult, error) {
	var room string
	err := conn.QueryRow(ctx,
		`SELECT room FROM schedules WHERE week_start = $1 AND duty_type = $2`,
		mondayOf(now), dutyType,
	).Scan(&room)
	if errors.Is(err, pgx.ErrNoRows) {
		return OnDutyResult{}, nil
	}
	if err != nil {
		return OnDutyResult{}, err
	}
	return OnDutyResult{Room: room}, nil
}

// GetUpcoming returns n consecutive weekly entries for dutyType starting from the week of from.
// Weeks with no DB row get an empty Room field.
func GetUpcoming(ctx context.Context, conn Querier, dutyType DutyType, from time.Time, n int) ([]Entry, error) {
	weekStart := mondayOf(from)
	rows, err := conn.Query(ctx,
		`SELECT week_start, room FROM schedules
		 WHERE duty_type = $1 AND week_start >= $2
		 ORDER BY week_start LIMIT $3`,
		dutyType, weekStart, n,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	index := make(map[string]string, n)
	for rows.Next() {
		var ws time.Time
		var room string
		if err := rows.Scan(&ws, &room); err != nil {
			return nil, err
		}
		index[WeekKey(ws)] = room
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	result := make([]Entry, n)
	for i := range n {
		t := weekStart.AddDate(0, 0, i*7)
		key := WeekKey(t)
		result[i] = Entry{Week: key, Room: index[key]}
	}
	return result, nil
}

// mondayOf returns midnight UTC of the Monday for the week containing t.
func mondayOf(t time.Time) time.Time {
	weekday := int(t.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	d := t.AddDate(0, 0, -(weekday - 1))
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
}
