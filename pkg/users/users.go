// Package users records the people the bot has seen. A row holds nothing but a
// Telegram id and when it was first and last seen, which is deliberate: with no
// names and no link to a room, the table says little on its own.
package users

import (
	"context"

	"github.com/jackc/pgx/v5/pgconn"
)

// Execer is the write side of a pgx connection, so tests can use a fake.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
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
