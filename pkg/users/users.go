// Package users records the people the bot has seen and where each stands in
// the onboarding. A row holds a Telegram id, when it was first and last seen,
// and — only for somebody who went through the onboarding or picked a room —
// an onboarding status and a room number. No names are stored.
package users

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Execer is the write side of a pgx connection, so tests can use a fake.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Querier is Execer plus single-row reads — what the onboarding needs.
type Querier interface {
	Execer
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Status is a user's place in the onboarding. The zero value means the bot has
// only seen the user: they never asked to join, or were in the group already.
type Status string

const (
	StatusNone     Status = ""
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusRejected Status = "rejected"
)

// User is one row of the table as the onboarding reads it. Room is 0 when no
// room has been chosen.
type User struct {
	Status Status
	Room   int
}

// Record stores tgUserID the first time it is seen and moves updated_at on
// every sighting after that, so the pair reads as first seen / last seen. The
// unique constraint sorts out which of the two happens, so this costs one
// statement rather than a read followed by a write.
func Record(ctx context.Context, conn Execer, tgUserID int64) error {
	_, err := conn.Exec(ctx,
		`INSERT INTO users (tg_user_id) VALUES ($1)
		 ON CONFLICT (tg_user_id) DO UPDATE SET updated_at = now()`,
		tgUserID,
	)
	return err
}

// Get returns the user's row. found is false when the bot has never seen them.
func Get(ctx context.Context, conn Querier, tgUserID int64) (u User, found bool, err error) {
	var status string
	err = conn.QueryRow(ctx,
		`SELECT COALESCE(status, ''), COALESCE(room, 0) FROM users WHERE tg_user_id = $1`,
		tgUserID,
	).Scan(&status, &u.Room)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, err
	}
	u.Status = Status(status)
	return u, true, nil
}

// RequestJoin marks the user as waiting for the admin. It changes only a user
// with no status yet, so a pending, approved or rejected user stays where they
// are; created reports whether this call is the one that made the request, and
// so whether the admin should hear about it.
func RequestJoin(ctx context.Context, conn Execer, tgUserID int64) (created bool, err error) {
	tag, err := conn.Exec(ctx,
		`INSERT INTO users (tg_user_id, status) VALUES ($1, 'pending')
		 ON CONFLICT (tg_user_id) DO UPDATE SET status = 'pending', updated_at = now()
		 WHERE users.status IS NULL`,
		tgUserID,
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// CancelRequest undoes RequestJoin while the request is still pending — for
// when the admin could not be told about it, so the user can simply try again.
func CancelRequest(ctx context.Context, conn Execer, tgUserID int64) error {
	_, err := conn.Exec(ctx,
		`UPDATE users SET status = NULL, updated_at = now()
		 WHERE tg_user_id = $1 AND status = 'pending'`,
		tgUserID,
	)
	return err
}

// Decide records the admin's answer to a pending request. changed is false
// when the request was no longer pending — a second press of the same button,
// or of the other one — so the caller doesn't act on it twice.
func Decide(ctx context.Context, conn Execer, tgUserID int64, approve bool) (changed bool, err error) {
	status := StatusRejected
	if approve {
		status = StatusApproved
	}
	tag, err := conn.Exec(ctx,
		`UPDATE users SET status = $2, updated_at = now()
		 WHERE tg_user_id = $1 AND status = 'pending'`,
		tgUserID, string(status),
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// SetApprovedRoom stores the room of an approved user who has not joined yet.
// changed is false unless the user is approved, so a room button can't skip
// the admin.
func SetApprovedRoom(ctx context.Context, conn Execer, tgUserID int64, room int) (changed bool, err error) {
	tag, err := conn.Exec(ctx,
		`UPDATE users SET room = $2, updated_at = now()
		 WHERE tg_user_id = $1 AND status = 'approved'`,
		tgUserID, room,
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// SetMemberRoom stores the room of somebody already in the group, whatever
// their status: being a member is the permission.
func SetMemberRoom(ctx context.Context, conn Execer, tgUserID int64, room int) error {
	_, err := conn.Exec(ctx,
		`INSERT INTO users (tg_user_id, room) VALUES ($1, $2)
		 ON CONFLICT (tg_user_id) DO UPDATE SET room = $2, updated_at = now()`,
		tgUserID, room,
	)
	return err
}
