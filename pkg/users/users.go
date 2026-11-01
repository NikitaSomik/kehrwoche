// Package users is the users table: Telegram ids the bot has seen, plus the
// onboarding's status and room. No names are stored.
package users

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Execer is the write side of a pgx connection, so tests can use a fake.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Querier adds single-row reads to Execer.
type Querier interface {
	Execer
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Status is a user's place in the onboarding. StatusNone means no request is
// open: never asked, already joined, or cancelled.
type Status string

const (
	StatusNone     Status = ""
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusRejected Status = "rejected"
)

// User is a row as the onboarding reads it. Room is 0, InviteLink empty and
// InviteExpires zero when unset.
type User struct {
	Status        Status
	Room          int
	InviteLink    string
	InviteExpires time.Time
}

// Record stores tgUserID on first sight and moves updated_at on every later
// one, in a single upsert.
func Record(ctx context.Context, conn Execer, tgUserID int64) error {
	_, err := conn.Exec(ctx,
		`INSERT INTO users (tg_user_id) VALUES ($1)
		 ON CONFLICT (tg_user_id) DO UPDATE SET updated_at = now()`,
		tgUserID,
	)
	return err
}

// Get returns the user's row; found is false for an unknown id.
func Get(ctx context.Context, conn Querier, tgUserID int64) (u User, found bool, err error) {
	var status string
	var expires *time.Time
	err = conn.QueryRow(ctx,
		`SELECT COALESCE(status, ''), COALESCE(room, 0), COALESCE(invite_link, ''), invite_expires_at
		 FROM users WHERE tg_user_id = $1`,
		tgUserID,
	).Scan(&status, &u.Room, &u.InviteLink, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, err
	}
	u.Status = Status(status)
	if expires != nil {
		u.InviteExpires = *expires
	}
	return u, true, nil
}

// The transitions below are each one conditional statement. changed reports
// whether it applied, so a double press or a race can't act twice.

// RequestJoin moves a user with no status to pending. changed is true only
// for the call that filed the request.
func RequestJoin(ctx context.Context, conn Execer, tgUserID int64) (changed bool, err error) {
	return applied(conn.Exec(ctx,
		`INSERT INTO users (tg_user_id, status) VALUES ($1, 'pending')
		 ON CONFLICT (tg_user_id) DO UPDATE SET status = 'pending', updated_at = now()
		 WHERE users.status IS NULL`,
		tgUserID,
	))
}

// CancelRequest moves a pending user back to no status.
func CancelRequest(ctx context.Context, conn Execer, tgUserID int64) error {
	_, err := conn.Exec(ctx,
		`UPDATE users SET status = NULL, updated_at = now()
		 WHERE tg_user_id = $1 AND status = 'pending'`,
		tgUserID,
	)
	return err
}

// Decide moves a pending user to approved or rejected.
func Decide(ctx context.Context, conn Execer, tgUserID int64, approve bool) (changed bool, err error) {
	status := StatusRejected
	if approve {
		status = StatusApproved
	}
	return applied(conn.Exec(ctx,
		`UPDATE users SET status = $2, updated_at = now()
		 WHERE tg_user_id = $1 AND status = 'pending'`,
		tgUserID, string(status),
	))
}

// SetApprovedRoom stores the room of an approved user.
func SetApprovedRoom(ctx context.Context, conn Execer, tgUserID int64, room int) (changed bool, err error) {
	return applied(conn.Exec(ctx,
		`UPDATE users SET room = $2, updated_at = now()
		 WHERE tg_user_id = $1 AND status = 'approved'`,
		tgUserID, room,
	))
}

// SetInviteLink stores the join-request link issued to an approved user and
// when it expires.
func SetInviteLink(ctx context.Context, conn Execer, tgUserID int64, link string, expires time.Time) (changed bool, err error) {
	return applied(conn.Exec(ctx,
		`UPDATE users SET invite_link = $2, invite_expires_at = $3, updated_at = now()
		 WHERE tg_user_id = $1 AND status = 'approved'`,
		tgUserID, link, expires,
	))
}

// MarkJoined closes an approved user's request once they are in the group.
// The approval and the link are spent: getting back in needs a new request.
func MarkJoined(ctx context.Context, conn Execer, tgUserID int64) (changed bool, err error) {
	return applied(conn.Exec(ctx,
		`UPDATE users SET status = NULL, invite_link = NULL, invite_expires_at = NULL, updated_at = now()
		 WHERE tg_user_id = $1 AND status = 'approved'`,
		tgUserID,
	))
}

func applied(tag pgconn.CommandTag, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
