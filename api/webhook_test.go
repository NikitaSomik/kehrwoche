package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
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

// fakeRows implements pgx.Rows for plan() tests; empty, since these tests
// only need the "no rows" path — GetUpcoming's own logic is covered in
// pkg/schedule/repo_test.go.
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

// fakeQuerier implements schedule.Querier without a live Postgres connection.
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
	// ": —" is the empty-room placeholder; the header also contains a bare
	// "—" as a stylistic dash, so counting that alone would overcount.
	if strings.Count(got, ": —") != schedule.DutyTypeToilet1.PlanCount() {
		t.Errorf("got %q, want %d empty-room placeholders", got, schedule.DutyTypeToilet1.PlanCount())
	}
}

// commandUpdateJSON builds a minimal Telegram update JSON body containing a
// single bot_command message, which is the shape Message.Command() requires
// (a bot_command entity at offset 0).
func commandUpdateJSON(command string) string {
	text := "/" + command
	return `{"update_id":1,"message":{"message_id":1,"date":1,` +
		`"chat":{"id":123,"type":"group"},"text":"` + text + `",` +
		`"entities":[{"type":"bot_command","offset":0,"length":` +
		strconv.Itoa(len(text)) + `}]}}`
}

func TestWebhook_MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/webhook", nil)
	rec := httptest.NewRecorder()

	Webhook(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("got status %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestWebhook_Unauthorized(t *testing.T) {
	cases := []struct {
		name      string
		secretEnv string
		header    string
	}{
		{"secret not configured, no header", "", ""},
		{"secret not configured, header sent anyway", "", "s3cret"},
		{"secret configured, no header", "s3cret", ""},
		{"secret configured, wrong header", "s3cret", "wrong"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WEBHOOK_SECRET", tc.secretEnv)
			req := httptest.NewRequest(http.MethodPost, "/api/webhook", strings.NewReader(""))
			if tc.header != "" {
				req.Header.Set("X-Telegram-Bot-Api-Secret-Token", tc.header)
			}
			rec := httptest.NewRecorder()

			Webhook(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("got status %d, want %d", rec.Code, http.StatusUnauthorized)
			}
		})
	}
}

// TestWebhook_AuthorizedRequests covers request bodies that must not panic
// and must always answer 200 once authorized — Telegram retries any non-200
// response, so a transient error must never surface as an HTTP error here.
func TestWebhook_AuthorizedRequests(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"malformed JSON", "not json"},
		{"no message field", `{"update_id":1}`},
		{"unknown command", commandUpdateJSON("nope")},
		{"known command, no DB available", commandUpdateJSON("toilette1")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WEBHOOK_SECRET", "s3cret")
			t.Setenv("DATABASE_URL", "")
			req := httptest.NewRequest(http.MethodPost, "/api/webhook", strings.NewReader(tc.body))
			req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "s3cret")
			rec := httptest.NewRecorder()

			Webhook(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("got status %d, want %d", rec.Code, http.StatusOK)
			}
		})
	}
}
