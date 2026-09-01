package schedule

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func GetOnDuty(ctx context.Context, conn Querier, dutyType DutyType, now time.Time) (OnDutyResult, error) {
	var room string
	err := conn.QueryRow(ctx,
		`SELECT room FROM schedules WHERE duty_type = $1 AND duty_date = $2`,
		dutyType, dutyType.EventDate(now),
	).Scan(&room)
	if errors.Is(err, pgx.ErrNoRows) {
		return OnDutyResult{}, nil
	}
	if err != nil {
		return OnDutyResult{}, err
	}
	return OnDutyResult{Room: room}, nil
}

// LastGenerated returns the latest duty_date stored for dutyType. ok is false
// when the schedule has no rows for it yet.
func LastGenerated(ctx context.Context, conn Querier, dutyType DutyType) (time.Time, bool, error) {
	// max() over zero rows yields one NULL row, not pgx.ErrNoRows — scan into a
	// pointer so the empty schedule is a nil, not a scan error.
	var last *time.Time
	if err := conn.QueryRow(ctx,
		`SELECT max(duty_date) FROM schedules WHERE duty_type = $1`,
		dutyType,
	).Scan(&last); err != nil {
		return time.Time{}, false, err
	}
	if last == nil {
		return time.Time{}, false, nil
	}
	return *last, true, nil
}

func GetUpcoming(ctx context.Context, conn Querier, dutyType DutyType, from time.Time, n int) ([]Entry, error) {
	dates := make([]time.Time, n)
	dates[0] = dutyType.UpcomingEventDate(from)
	for i := 1; i < n; i++ {
		dates[i] = dutyType.NextEventDate(dates[i-1])
	}

	// Match the exact computed dates rather than "next n rows from dates[0]":
	// a gap in the schedule (a missing row for one date) would otherwise let
	// LIMIT pull in a row past the display window, silently misaligning the
	// results with the dates being rendered.
	rows, err := conn.Query(ctx,
		`SELECT duty_date, room FROM schedules
		 WHERE duty_type = $1 AND duty_date = ANY($2::date[])`,
		dutyType, dates,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	index := make(map[string]string, n)
	for rows.Next() {
		var dd time.Time
		var room string
		if err := rows.Scan(&dd, &room); err != nil {
			return nil, err
		}
		index[dd.Format("2006-01-02")] = room
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	result := make([]Entry, n)
	for i, dt := range dates {
		result[i] = Entry{Date: dt, Room: index[dt.Format("2006-01-02")]}
	}
	return result, nil
}
