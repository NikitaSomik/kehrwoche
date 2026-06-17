package db

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
)

// Connect opens a single connection using DATABASE_URL.
// In serverless each invocation is short-lived, so a pool would only waste
// Neon's connection slots. Use the Neon pooler endpoint in DATABASE_URL
// (e.g. ep-xxx-pooler.eu-central-1.aws.neon.tech) to stay within free-tier limits.
func Connect(ctx context.Context) (*pgx.Conn, error) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return nil, fmt.Errorf("DATABASE_URL is not set")
	}
	return pgx.Connect(ctx, url)
}
