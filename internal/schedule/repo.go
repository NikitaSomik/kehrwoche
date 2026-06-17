package schedule

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// GetOnDuty returns the room assigned to dutyType for the week containing now.
func GetOnDuty(ctx context.Context, conn *pgx.Conn, dutyType string, now time.Time) (string, bool, error) {
	var room string
	err := conn.QueryRow(ctx,
		`SELECT room FROM schedules WHERE week_start = $1 AND duty_type = $2`,
		mondayOf(now), dutyType,
	).Scan(&room)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return room, true, nil
}

// GetUpcoming returns n consecutive weekly entries for dutyType starting from the week of from.
// Weeks with no DB row get an empty Room field.
func GetUpcoming(ctx context.Context, conn *pgx.Conn, dutyType string, from time.Time, n int) ([]Entry, error) {
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
