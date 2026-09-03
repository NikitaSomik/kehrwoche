package botcmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nikitasomusev/kehrwoche/pkg/schedule"
)

// fakeRow implements pgx.Row for wer() tests.
type fakeRow struct {
	room string
	err  error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	room, ok := dest[0].(*string)
	if !ok {
		return errors.New("fakeRow: unsupported dest type")
	}
	*room = r.room
	return nil
}

// fakeRows implements pgx.Rows for plan() tests; empty, since these tests only
// need the "no rows" path — GetUpcoming's own logic is covered elsewhere.
type fakeRows struct{}

func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return nil }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Values() ([]any, error)                       { return nil, nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }
func (r *fakeRows) Next() bool                                   { return false }
func (r *fakeRows) Scan(dest ...any) error                       { return nil }

type fakeQuerier struct {
	row fakeRow
}

func (q fakeQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return q.row
}

func (q fakeQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return &fakeRows{}, nil
}

func TestWer(t *testing.T) {
	now, _ := time.Parse("2006-01-02", "2026-06-18")

	t.Run("room assigned", func(t *testing.T) {
		q := fakeQuerier{row: fakeRow{room: "Zimmer 4"}}
		got, err := wer(schedule.DutyTypeToilet1)(context.Background(), q, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got, "Zimmer 4") {
			t.Errorf("got %q, want it to contain %q", got, "Zimmer 4")
		}
	})

	t.Run("no plan", func(t *testing.T) {
		q := fakeQuerier{row: fakeRow{err: pgx.ErrNoRows}}
		got, err := wer(schedule.DutyTypeToilet1)(context.Background(), q, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got, "keine Planung") {
			t.Errorf("got %q, want it to contain %q", got, "keine Planung")
		}
	})

	t.Run("query error", func(t *testing.T) {
		wantErr := errors.New("connection reset")
		q := fakeQuerier{row: fakeRow{err: wantErr}}
		_, err := wer(schedule.DutyTypeToilet1)(context.Background(), q, now)
		if !errors.Is(err, wantErr) {
			t.Errorf("got err %v, want %v", err, wantErr)
		}
	})
}

func TestPlan(t *testing.T) {
	now, _ := time.Parse("2006-01-02", "2026-06-18")
	q := fakeQuerier{}

	got, err := plan(schedule.DutyTypeToilet1)(context.Background(), q, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "Toilette 1") {
		t.Errorf("got %q, want it to contain the duty label", got)
	}
	if strings.Count(got, ": —") != schedule.DutyTypeToilet1.PlanCount() {
		t.Errorf("got %q, want %d empty-room placeholders", got, schedule.DutyTypeToilet1.PlanCount())
	}
}

func TestLookup(t *testing.T) {
	if _, ok := Lookup("toilette1"); !ok {
		t.Error("Lookup(toilette1) not found")
	}
	if _, ok := Lookup("etage_plan"); !ok {
		t.Error("Lookup(etage_plan) not found")
	}
	if _, ok := Lookup("help"); ok {
		t.Error("Lookup(help) should be false — /help is a static reply, not a duty command")
	}
	if _, ok := Lookup("nope"); ok {
		t.Error("Lookup(nope) should be false")
	}
}

func TestStaticReply(t *testing.T) {
	cases := []struct {
		cmd     string
		private bool
		wantOK  bool
	}{
		{"help", true, true},
		{"help", false, true}, // /help works in the group too
		{"start", true, true},
		{"start", false, false}, // no /start noise in the group
		{"toilette1", true, false},
		{"unknown", true, false},
		{"", false, false},
	}
	for _, tc := range cases {
		reply, ok := StaticReply(tc.cmd, tc.private)
		if ok != tc.wantOK {
			t.Errorf("StaticReply(%q, private=%v): ok=%v, want %v", tc.cmd, tc.private, ok, tc.wantOK)
		}
		if ok && reply == "" {
			t.Errorf("StaticReply(%q, private=%v): ok but empty reply", tc.cmd, tc.private)
		}
	}
}

func TestHelpMessageListsEveryDuty(t *testing.T) {
	for _, c := range duties {
		if !strings.Contains(helpMessage, "/"+c.name+" —") {
			t.Errorf("helpMessage has no entry for /%s", c.name)
		}
	}
}

// TestMenuValidForTelegram checks the setMyCommands constraints and logs the
// list to paste into BotFather:
//
//	go test -run TestMenuValidForTelegram -v ./pkg/botcmd/
func TestMenuValidForTelegram(t *testing.T) {
	var list strings.Builder
	for _, c := range Menu() {
		if n := len(c.Command); n < 1 || n > 32 {
			t.Errorf("command %q: length %d, want 1–32", c.Command, n)
		}
		if strings.ToLower(c.Command) != c.Command {
			t.Errorf("command %q: must be lowercase", c.Command)
		}
		if n := len(c.Description); n < 1 || n > 256 {
			t.Errorf("/%s: description length %d, want 1–256", c.Command, n)
		}
		if strings.ContainsAny(c.Description, "\r\n") {
			t.Errorf("/%s: description contains a newline", c.Command)
		}
		fmt.Fprintf(&list, "%s - %s\n", c.Command, c.Description)
	}
	t.Logf("setMyCommands list:\n%s", list.String())
}
