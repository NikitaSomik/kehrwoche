//go:build integration

package users_test

import (
	"context"
	"testing"
	"time"

	"github.com/NikitaSomik/kehrwoche/internal/pgtest"
	"github.com/NikitaSomik/kehrwoche/pkg/users"
)

// The point of the table: a person is written once however often they use the
// bot, and each later sighting only moves updated_at.
func TestRecordUpsertsOneRow(t *testing.T) {
	conn := pgtest.Connect(t)
	ctx := context.Background()
	const id = int64(987654321)

	read := func() (created, updated time.Time) {
		t.Helper()
		if err := conn.QueryRow(ctx,
			`SELECT created_at, updated_at FROM users WHERE tg_user_id = $1`, id,
		).Scan(&created, &updated); err != nil {
			t.Fatalf("read row: %v", err)
		}
		return created, updated
	}

	if err := users.Record(ctx, conn, id); err != nil {
		t.Fatalf("first record: %v", err)
	}
	created1, updated1 := read()

	if err := users.Record(ctx, conn, id); err != nil {
		t.Fatalf("second record: %v", err)
	}
	created2, updated2 := read()

	var rows int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Errorf("got %d rows, want 1 — the repeat inserted instead of updating", rows)
	}
	if !created2.Equal(created1) {
		t.Errorf("created_at moved from %s to %s; it must record the first sighting", created1, created2)
	}
	if !updated2.After(updated1) {
		t.Errorf("updated_at did not advance: %s then %s", updated1, updated2)
	}
}

// The onboarding's transitions, against the real constraints: a request is
// filed once, decided once, a room needs approval, and the CHECKs hold.
func TestOnboardingTransitions(t *testing.T) {
	conn := pgtest.Connect(t)
	ctx := context.Background()
	const id = int64(555)

	mustGet := func(want users.User) {
		t.Helper()
		got, found, err := users.Get(ctx, conn, id)
		if err != nil || !found {
			t.Fatalf("get: found=%v err=%v", found, err)
		}
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	}

	if _, found, err := users.Get(ctx, conn, id); err != nil || found {
		t.Fatalf("unknown user: found=%v err=%v", found, err)
	}

	// Seen before the onboarding: the request still goes through.
	if err := users.Record(ctx, conn, id); err != nil {
		t.Fatal(err)
	}
	mustGet(users.User{})

	if ok, err := users.SetApprovedRoom(ctx, conn, id, 3); err != nil || ok {
		t.Fatalf("room before approval: ok=%v err=%v", ok, err)
	}
	if ok, err := users.RequestJoin(ctx, conn, id); err != nil || !ok {
		t.Fatalf("first request: ok=%v err=%v", ok, err)
	}
	if ok, err := users.RequestJoin(ctx, conn, id); err != nil || ok {
		t.Fatalf("second request: ok=%v err=%v", ok, err)
	}
	mustGet(users.User{Status: users.StatusPending})

	if err := users.CancelRequest(ctx, conn, id); err != nil {
		t.Fatal(err)
	}
	mustGet(users.User{})
	if ok, err := users.RequestJoin(ctx, conn, id); err != nil || !ok {
		t.Fatalf("request after cancel: ok=%v err=%v", ok, err)
	}

	if ok, err := users.Decide(ctx, conn, id, true); err != nil || !ok {
		t.Fatalf("approve: ok=%v err=%v", ok, err)
	}
	if ok, err := users.Decide(ctx, conn, id, false); err != nil || ok {
		t.Fatalf("second decision: ok=%v err=%v", ok, err)
	}
	if ok, err := users.SetApprovedRoom(ctx, conn, id, 3); err != nil || !ok {
		t.Fatalf("room: ok=%v err=%v", ok, err)
	}
	mustGet(users.User{Status: users.StatusApproved, Room: 3})

	if _, err := users.SetApprovedRoom(ctx, conn, id, 9); err == nil {
		t.Error("room 9 passed the CHECK")
	}
	if err := users.SetMemberRoom(ctx, conn, 666, 0); err == nil {
		t.Error("room 0 passed the CHECK")
	}

	// A rejected user stays rejected: a new /start files nothing.
	const other = int64(556)
	if _, err := users.RequestJoin(ctx, conn, other); err != nil {
		t.Fatal(err)
	}
	if _, err := users.Decide(ctx, conn, other, false); err != nil {
		t.Fatal(err)
	}
	if ok, err := users.RequestJoin(ctx, conn, other); err != nil || ok {
		t.Errorf("request after rejection: ok=%v err=%v", ok, err)
	}

	// A member picks a room whatever their status, including none.
	if err := users.SetMemberRoom(ctx, conn, 777, 5); err != nil {
		t.Fatal(err)
	}
	got, _, err := users.Get(ctx, conn, 777)
	if err != nil || got != (users.User{Room: 5}) {
		t.Errorf("member room: %+v, %v", got, err)
	}
}
