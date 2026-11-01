package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/NikitaSomik/kehrwoche/pkg/config"
	"github.com/NikitaSomik/kehrwoche/pkg/schedule"
)

func TestDutiesFor(t *testing.T) {
	cases := []struct {
		weekday time.Weekday
		want    []schedule.DutyType
	}{
		{time.Monday, nil},
		{time.Tuesday, []schedule.DutyType{schedule.DutyTypeLaundry}},
		{time.Wednesday, nil},
		{time.Thursday, weeklyDuties},
		{time.Friday, []schedule.DutyType{schedule.DutyTypeLaundry}},
		{time.Saturday, nil},
		{time.Sunday, nil},
	}
	for _, tc := range cases {
		t.Run(tc.weekday.String(), func(t *testing.T) {
			got := dutiesFor(tc.weekday)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestWeeklyReminderDayIsThursday(t *testing.T) {
	// Weekly duties fire on Friday; the reminder must land on Thursday.
	if weeklyReminderDay != time.Thursday {
		t.Errorf("got %s, want Thursday", weeklyReminderDay)
	}
}

func TestCron_Unauthorized(t *testing.T) {
	cases := []struct {
		name       string
		secretEnv  string
		authHeader string
	}{
		{"secret not configured, no header", "", ""},
		{"secret not configured, header sent anyway", "", "Bearer x"},
		{"secret configured, no header", "abc", ""},
		{"secret configured, wrong header", "abc", "Bearer wrong"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CRON_SECRET", tc.secretEnv)
			req := httptest.NewRequest(http.MethodPost, "/api/cron", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rec := httptest.NewRecorder()

			Cron(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("got status %d, want %d", rec.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestCron_ValidSecret_DBError(t *testing.T) {
	// No live DB in this test — db.Connect fails fast on an empty
	// DATABASE_URL, exercising the post-auth error path without a network call.
	setClock(t, berlinTime(t, 2026, time.November, 5, schedule.ReminderHour, 7))
	t.Setenv("CRON_SECRET", "abc")
	t.Setenv("DATABASE_URL", "")
	req := httptest.NewRequest(http.MethodPost, "/api/cron", nil)
	req.Header.Set("Authorization", "Bearer abc")
	rec := httptest.NewRecorder()

	Cron(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("got status %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// The run vercel.json fires an hour off ReminderHour must end before it
// touches the database. DATABASE_URL is empty, so a run that got as far as
// connecting would answer 500 instead.
func TestCron_OffHourRunDoesNothing(t *testing.T) {
	t.Setenv("CRON_SECRET", "abc")
	t.Setenv("DATABASE_URL", "")
	for _, hour := range []int{schedule.ReminderHour - 1, schedule.ReminderHour + 1} {
		setClock(t, berlinTime(t, 2026, time.November, 5, hour, 30))
		req := httptest.NewRequest(http.MethodGet, "/api/cron", nil)
		req.Header.Set("Authorization", "Bearer abc")
		rec := httptest.NewRecorder()

		Cron(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("%02d:30 Berlin: got status %d, want %d", hour, rec.Code, http.StatusOK)
		}
	}
}

// weeklyDuties sets the order the reminder lists the weekly duties in, so it
// stays a literal. The set still has to match the cadence in pkg/schedule, or
// a weekly duty added there would be silently left out of the reminder.
func TestWeeklyDutiesMatchSchedule(t *testing.T) {
	got := slices.Clone(weeklyDuties)
	want := schedule.WeeklyDuties()
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("weeklyDuties = %v, but the weekly duties in pkg/schedule are %v: update the list in api/cron.go",
			weeklyDuties, schedule.WeeklyDuties())
	}
}

// vercel.json is the one place the cadence lives outside Go. Check it against
// the code: every entry fires on the reminder weekdays, and on every day of
// the year — summer time, winter time and the weeks they change — exactly one
// entry lands in ReminderHour in Berlin, wherever in its hour Vercel starts it
// (on Hobby that is anywhere from :00 to :59).
func TestVercelCronMatchesReminderHour(t *testing.T) {
	raw, err := os.ReadFile("../vercel.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Crons []struct {
			Path     string `json:"path"`
			Schedule string `json:"schedule"`
		} `json:"crons"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Crons) == 0 {
		t.Fatal("vercel.json has no crons")
	}

	var wantDays []string
	for _, d := range schedule.ReminderWeekdays() {
		wantDays = append(wantDays, strconv.Itoa(int(d)))
	}
	var hours []int
	for _, c := range cfg.Crons {
		f := strings.Fields(c.Schedule)
		if len(f) != 5 {
			t.Fatalf("%q: want 5 fields", c.Schedule)
		}
		if c.Path != "/api/cron" || f[0] != "0" || f[2] != "*" || f[3] != "*" {
			t.Errorf("%s %q: want path /api/cron, minute 0, every day of month, every month", c.Path, c.Schedule)
		}
		if f[4] != strings.Join(wantDays, ",") {
			t.Errorf("%q fires on weekdays %s, the code announces on %s", c.Schedule, f[4], strings.Join(wantDays, ","))
		}
		h, err := strconv.Atoi(f[1])
		if err != nil {
			t.Fatalf("%q: hour %q is not a number", c.Schedule, f[1])
		}
		hours = append(hours, h)
	}

	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	for day := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC); day.Year() < 2028; day = day.AddDate(0, 0, 1) {
		hits := 0
		for _, h := range hours {
			start := day.Add(time.Duration(h) * time.Hour)
			end := start.Add(59 * time.Minute)
			startHit := start.In(loc).Hour() == schedule.ReminderHour
			if startHit != (end.In(loc).Hour() == schedule.ReminderHour) {
				t.Errorf("%s: the %02d:00 UTC run straddles ReminderHour", day.Format("2006-01-02"), h)
			}
			if startHit {
				hits++
			}
		}
		if hits != 1 {
			t.Errorf("%s: %d cron runs land in %02d:xx Berlin, want exactly 1", day.Format("2006-01-02"), hits, schedule.ReminderHour)
		}
	}
}

// setClock makes Cron see now as the current time for the rest of the test.
func setClock(t *testing.T, now time.Time) {
	t.Helper()
	prev := clock
	clock = func() time.Time { return now }
	t.Cleanup(func() { clock = prev })
}

func berlinTime(t *testing.T, year int, month time.Month, day, hour, minute int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	return time.Date(year, month, day, hour, minute, 0, 0, loc)
}

// stubSendPlain replaces the Bot API call for the duration of a test and
// records what would have been sent.
func stubSendPlain(t *testing.T) *[]string {
	t.Helper()
	var sent []string
	prev := sendPlain
	sendPlain = func(ctx context.Context, c *http.Client, token string, chatID int64, text string) error {
		sent = append(sent, text)
		return nil
	}
	t.Cleanup(func() { sendPlain = prev })
	return &sent
}

func TestReportProblems(t *testing.T) {
	t.Run("reaches the admin as one message", func(t *testing.T) {
		sent := stubSendPlain(t)
		cfg := config.Config{AdminChatID: "456"}

		reportProblems(context.Background(), cfg, "Etage: Plan nicht lesbar", "Erinnerung nicht zustellbar")

		if len(*sent) != 1 {
			t.Fatalf("got %d messages, want 1 covering both problems", len(*sent))
		}
		for _, want := range []string{"Etage: Plan nicht lesbar", "Erinnerung nicht zustellbar"} {
			if !strings.Contains((*sent)[0], want) {
				t.Errorf("message is missing %q:\n%s", want, (*sent)[0])
			}
		}
	})

	// The notice is opt-in; without a chat to send it to there is nothing to
	// do, and it must never fall back to the group.
	t.Run("no admin chat, nothing sent", func(t *testing.T) {
		sent := stubSendPlain(t)

		reportProblems(context.Background(), config.Config{}, "something broke")

		if len(*sent) != 0 {
			t.Errorf("sent %v, want nothing", *sent)
		}
	})

	t.Run("nothing wrong, nothing sent", func(t *testing.T) {
		sent := stubSendPlain(t)

		reportProblems(context.Background(), config.Config{AdminChatID: "456"})

		if len(*sent) != 0 {
			t.Errorf("sent %v, want nothing", *sent)
		}
	})

	t.Run("unparseable admin chat sends nothing", func(t *testing.T) {
		sent := stubSendPlain(t)
		cfg := config.Config{AdminChatID: "not-a-number"}

		reportProblems(context.Background(), cfg, "something broke")

		if len(*sent) != 0 {
			t.Errorf("sent %v, want nothing", *sent)
		}
	})
}

// horizonQuerier answers every max(duty_date) with the same date — enough for
// warnHorizon, which only cares whether a gap exists at all. It counts calls so
// a test can assert the DB is never touched.
type horizonQuerier struct {
	t     *testing.T
	last  *time.Time
	err   error
	calls int
}

func (q *horizonQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	q.calls++
	return horizonRow{last: q.last, err: q.err}
}

func (q *horizonQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	q.t.Helper()
	q.t.Error("warnHorizon must not run a multi-row query")
	return nil, errors.New("horizonQuerier: Query not used")
}

type horizonRow struct {
	last *time.Time
	err  error
}

func (r horizonRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(**time.Time) = r.last
	return nil
}

func TestHorizonProblems(t *testing.T) {
	thursday := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	friday := thursday.AddDate(0, 0, 1)
	near := thursday.AddDate(0, 0, 7)
	cfg := config.Config{AdminChatID: "123"}

	t.Run("reminder day checks and warns", func(t *testing.T) {
		sent := stubSendPlain(t)
		q := &horizonQuerier{t: t, last: &near}

		if got := horizonProblems(context.Background(), q, cfg, thursday); got != nil {
			t.Errorf("got problems %v, want none", got)
		}
		if len(*sent) != 1 {
			t.Errorf("got %d messages, want 1", len(*sent))
		}
	})

	t.Run("any other day leaves the DB alone", func(t *testing.T) {
		sent := stubSendPlain(t)
		q := &horizonQuerier{t: t, last: &near}

		if got := horizonProblems(context.Background(), q, cfg, friday); got != nil {
			t.Errorf("got problems %v, want none", got)
		}
		if q.calls != 0 || len(*sent) != 0 {
			t.Errorf("got %d queries and %d messages, want neither", q.calls, len(*sent))
		}
	})

	// A failed check must surface: staying quiet here looks exactly like a
	// schedule with plenty of weeks left.
	t.Run("failed check is reported", func(t *testing.T) {
		q := &horizonQuerier{t: t, err: errors.New("connection reset")}

		got := horizonProblems(context.Background(), q, cfg, thursday)
		if len(got) != 1 || !strings.Contains(got[0], "connection reset") {
			t.Errorf("got %v, want one problem naming the cause", got)
		}
	})
}

func TestWarnHorizon(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	far := now.AddDate(0, 0, 100)
	near := now.AddDate(0, 0, 7)

	t.Run("no admin chat, no query", func(t *testing.T) {
		// The notice is off, so it must not cost a round-trip either.
		q := &horizonQuerier{t: t, last: &near}

		if err := warnHorizon(context.Background(), q, config.Config{}, now); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if q.calls != 0 {
			t.Errorf("queried the DB %d times, want 0", q.calls)
		}
	})

	t.Run("nothing running out, nothing sent", func(t *testing.T) {
		q := &horizonQuerier{t: t, last: &far}
		cfg := config.Config{AdminChatID: "123"}

		if err := warnHorizon(context.Background(), q, cfg, now); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if q.calls != len(schedule.HorizonDuties()) {
			t.Errorf("got %d queries, want one per horizon duty (%d)", q.calls, len(schedule.HorizonDuties()))
		}
	})

	t.Run("running out reaches the admin", func(t *testing.T) {
		sent := stubSendPlain(t)
		q := &horizonQuerier{t: t, last: &near}
		cfg := config.Config{AdminChatID: "123"}

		if err := warnHorizon(context.Background(), q, cfg, now); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(*sent) != 1 {
			t.Fatalf("got %d messages, want 1 covering every duty", len(*sent))
		}
	})

	t.Run("schedule already over still warns", func(t *testing.T) {
		sent := stubSendPlain(t)
		past := now.AddDate(0, 0, -3)
		q := &horizonQuerier{t: t, last: &past}
		cfg := config.Config{AdminChatID: "123"}

		if err := warnHorizon(context.Background(), q, cfg, now); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(*sent) != 1 {
			t.Fatalf("got %d messages, want 1", len(*sent))
		}
	})

	t.Run("failed send is an error", func(t *testing.T) {
		wantErr := errors.New("telegram down")
		prev := sendPlain
		sendPlain = func(ctx context.Context, c *http.Client, token string, chatID int64, text string) error {
			return wantErr
		}
		t.Cleanup(func() { sendPlain = prev })
		q := &horizonQuerier{t: t, last: &near}
		cfg := config.Config{AdminChatID: "123"}

		if err := warnHorizon(context.Background(), q, cfg, now); !errors.Is(err, wantErr) {
			t.Errorf("got %v, want %v", err, wantErr)
		}
	})

	t.Run("unparseable admin chat is an error", func(t *testing.T) {
		q := &horizonQuerier{t: t, last: &near}
		cfg := config.Config{AdminChatID: "not-a-number"}

		err := warnHorizon(context.Background(), q, cfg, now)
		if err == nil {
			t.Fatal("got nil, want an error naming ADMIN_CHAT_ID")
		}
		if !strings.Contains(err.Error(), "ADMIN_CHAT_ID") {
			t.Errorf("got %v, want it to name ADMIN_CHAT_ID", err)
		}
	})

	t.Run("query error is propagated", func(t *testing.T) {
		wantErr := errors.New("connection reset")
		q := &horizonQuerier{t: t, err: wantErr}
		cfg := config.Config{AdminChatID: "123"}

		if err := warnHorizon(context.Background(), q, cfg, now); !errors.Is(err, wantErr) {
			t.Errorf("got %v, want %v", err, wantErr)
		}
	})
}
