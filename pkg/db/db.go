package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Connect opens a single connection to url (see config.Config.DatabaseURL).
// In serverless each invocation is short-lived, so a pool would only waste
// Neon's connection slots. Use the Neon pooler endpoint in DATABASE_URL
// (e.g. ep-xxx-pooler.eu-central-1.aws.neon.tech) to stay within free-tier limits.
func Connect(ctx context.Context, url string) (*pgx.Conn, error) {
	if url == "" {
		return nil, fmt.Errorf("DATABASE_URL is not set")
	}
	return pgx.Connect(ctx, url)
}
