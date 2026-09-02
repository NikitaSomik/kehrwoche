// Command migrate applies pending migrations/*.sql (in filename order) to the
// database in DATABASE_URL, recording each in schema_migrations. Run from the
// repo root with `task migrate`.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/nikitasomusev/kehrwoche/internal/migrate"
	"github.com/nikitasomusev/kehrwoche/pkg/config"
	"github.com/nikitasomusev/kehrwoche/pkg/db"
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	conn, err := db.Connect(ctx, config.Load().DatabaseURL)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()

	applied, err := migrate.Apply(ctx, conn, "migrations")
	if err != nil {
		return err
	}
	for _, v := range applied {
		fmt.Println("applied", v)
	}
	if len(applied) == 0 {
		fmt.Println("migrate: already up to date")
	}
	return nil
}
