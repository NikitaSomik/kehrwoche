package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// fakeRow implements pgx.Row for lastRow tests. lastRow scans (duty_date, room).
type fakeRow struct {
	room string
	err  error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*time.Time) = time.Time{}
	*dest[1].(*string) = r.room
	return nil
}

// fakeTx implements txQuerier without a real DB transaction.
type fakeTx struct{ row fakeRow }

func (f fakeTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row { return f.row }
func (f fakeTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func TestLastRow(t *testing.T) {
	t.Run("parses the room from the label", func(t *testing.T) {
		tx := fakeTx{row: fakeRow{room: "Zimmer 3"}}
		got, ok, err := lastRow(context.Background(), tx, "toilet1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !ok || got.room != 3 {
			t.Errorf("got %+v, %v, want room 3, true", got, ok)
		}
	})

	t.Run("no rows yet", func(t *testing.T) {
		tx := fakeTx{row: fakeRow{err: pgx.ErrNoRows}}
		_, ok, err := lastRow(context.Background(), tx, "toilet1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ok {
			t.Error("got ok=true, want false")
		}
	})

	t.Run("unparseable room label", func(t *testing.T) {
		tx := fakeTx{row: fakeRow{room: "not a room"}}
		_, _, err := lastRow(context.Background(), tx, "toilet1")
		if err == nil {
			t.Error("expected an error")
		}
	})

	t.Run("query error", func(t *testing.T) {
		wantErr := errors.New("connection reset")
		tx := fakeTx{row: fakeRow{err: wantErr}}
		_, _, err := lastRow(context.Background(), tx, "toilet1")
		if !errors.Is(err, wantErr) {
			t.Errorf("got %v, want %v", err, wantErr)
		}
	})
}

func TestActiveRooms(t *testing.T) {
	got := activeRooms([]int{1, 2, 3, 4}, map[int]bool{2: true})
	want := []int{1, 3, 4}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v, want %v", got, want)
		}
	}
}

func TestIndexAfter(t *testing.T) {
	active := []int{1, 2, 3}
	if got := indexAfter(active, 2); got != 2 {
		t.Errorf("got %d, want 2", got)
	}
	if got := indexAfter(active, 3); got != 0 {
		t.Errorf("got %d, want 0 (wraps around)", got)
	}
	if got := indexAfter(active, 99); got != 0 {
		t.Errorf("got %d, want 0 (room not found)", got)
	}
}
