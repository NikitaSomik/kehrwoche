// Command mcp is a local MCP (Model Context Protocol) server exposing the
// cleaning schedule over stdio, so an MCP client (Claude Code, Claude Desktop)
// can query it. Read-only for now: list_duties, on_duty, upcoming.
//
// It's registered in .mcp.json as `task mcp`, which is how it gets DATABASE_URL
// from .env. The client starts it — don't run it by hand.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	_ "time/tzdata"

	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nikitasomusev/kehrwoche/pkg/config"
	"github.com/nikitasomusev/kehrwoche/pkg/db"
	"github.com/nikitasomusev/kehrwoche/pkg/schedule"
)

const (
	dateLayout = "2006-01-02"
	// maxUpcomingWeeks caps the `upcoming` look-ahead so a large `weeks` value
	// can't blow up the query (GetUpcoming builds one date per event day).
	maxUpcomingWeeks = 52
)

func main() {
	if err := run(context.Background()); err != nil && !isClientDisconnect(err) {
		fmt.Fprintln(os.Stderr, "mcp:", err)
		os.Exit(1)
	}
}

// isClientDisconnect reports whether err is just the MCP client closing stdin
// (the normal way a client stops the server), not a real failure. The string
// check is a fallback for the SDK's wrapped shutdown error; io.EOF is the
// reliable signal.
func isClientDisconnect(err error) bool {
	return errors.Is(err, io.EOF) || strings.Contains(err.Error(), "server is closing")
}

func run(ctx context.Context) error {
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		return fmt.Errorf("load location: %w", err)
	}

	s := mcp.NewServer(&mcp.Implementation{
		Name:    "kehrwoche",
		Version: "0.1.0",
	}, nil)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_duties",
		Description: "List every cleaning duty: key, German label, event weekdays, how many days each assignment stays current, and the last date generated so far.",
	}, listDuties)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "on_duty",
		Description: "Who is on duty on a given date. Omit `duty` to get every duty for that date; omit `date` for today (Europe/Berlin).",
	}, onDutyHandler(loc))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "upcoming",
		Description: "The next assignments per duty, one row per event day, gaps shown as an empty room. Omit `duty` for every duty.",
	}, upcomingHandler(loc))

	return s.Run(ctx, &mcp.StdioTransport{})
}

// withConn opens a short-lived connection per call, matching the api/ handlers
// — a *pgx.Conn is not safe for the concurrent calls an MCP client can make.
func withConn(ctx context.Context, fn func(context.Context, *pgx.Conn) error) error {
	conn, err := db.Connect(ctx, config.Load().DatabaseURL)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	return fn(ctx, conn)
}

func parseDuty(s string) (schedule.DutyType, error) {
	for _, d := range schedule.AllDutyTypes() {
		if string(d) == s {
			return d, nil
		}
	}
	return "", fmt.Errorf("unknown duty %q (valid: toilet1, toilet2, hall, floor, laundry)", s)
}

// selectDuties resolves the optional `duty` argument shared by on_duty and
// upcoming: a specific duty, or every duty when the argument is empty.
func selectDuties(dutyArg string) ([]schedule.DutyType, error) {
	if dutyArg == "" {
		return schedule.AllDutyTypes(), nil
	}
	d, err := parseDuty(dutyArg)
	if err != nil {
		return nil, err
	}
	return []schedule.DutyType{d}, nil
}

func resolveDate(s string, loc *time.Location) (time.Time, error) {
	if s == "" {
		return time.Now().In(loc), nil
	}
	t, err := time.ParseInLocation(dateLayout, s, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q, want YYYY-MM-DD", s)
	}
	return t, nil
}

// --- list_duties ---

type listDutiesIn struct{}

type dutyInfo struct {
	Key           string   `json:"key"`
	Label         string   `json:"label"`
	EventWeekdays []string `json:"event_weekdays"`
	WindowDays    int      `json:"window_days"`
	LastGenerated string   `json:"last_generated,omitempty"`
}

type listDutiesOut struct {
	Duties []dutyInfo `json:"duties"`
}

func listDuties(ctx context.Context, _ *mcp.CallToolRequest, _ listDutiesIn) (*mcp.CallToolResult, listDutiesOut, error) {
	var out listDutiesOut
	err := withConn(ctx, func(ctx context.Context, conn *pgx.Conn) error {
		for _, d := range schedule.AllDutyTypes() {
			last, ok, err := schedule.LastGenerated(ctx, conn, d)
			if err != nil {
				return fmt.Errorf("last generated for %s: %w", d, err)
			}
			days := d.EventWeekdays()
			weekdays := make([]string, len(days))
			for i, w := range days {
				weekdays[i] = w.String()
			}
			info := dutyInfo{
				Key:           string(d),
				Label:         d.Label(),
				EventWeekdays: weekdays,
				WindowDays:    d.WindowDays(),
			}
			if ok {
				info.LastGenerated = last.Format(dateLayout)
			}
			out.Duties = append(out.Duties, info)
		}
		return nil
	})
	if err != nil {
		return nil, listDutiesOut{}, err
	}
	return nil, out, nil
}

// --- on_duty ---

type onDutyIn struct {
	Duty string `json:"duty,omitempty" jsonschema:"duty key (toilet1, toilet2, hall, floor, laundry); omit for all duties"`
	Date string `json:"date,omitempty" jsonschema:"date YYYY-MM-DD; defaults to today (Europe/Berlin)"`
}

type assignment struct {
	Duty    string `json:"duty"`
	Label   string `json:"label"`
	Window  string `json:"window"`
	Room    string `json:"room,omitempty"`
	Planned bool   `json:"planned"`
}

type onDutyOut struct {
	Date        string       `json:"date"`
	Assignments []assignment `json:"assignments"`
}

func onDutyHandler(loc *time.Location) mcp.ToolHandlerFor[onDutyIn, onDutyOut] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in onDutyIn) (*mcp.CallToolResult, onDutyOut, error) {
		date, err := resolveDate(in.Date, loc)
		if err != nil {
			return nil, onDutyOut{}, err
		}
		duties, err := selectDuties(in.Duty)
		if err != nil {
			return nil, onDutyOut{}, err
		}

		out := onDutyOut{Date: date.Format(dateLayout)}
		err = withConn(ctx, func(ctx context.Context, conn *pgx.Conn) error {
			for _, d := range duties {
				res, err := schedule.GetOnDuty(ctx, conn, d, date)
				if err != nil {
					return fmt.Errorf("on duty for %s: %w", d, err)
				}
				out.Assignments = append(out.Assignments, assignment{
					Duty:    string(d),
					Label:   d.Label(),
					Window:  d.Window(date),
					Room:    res.Room,
					Planned: res.Room != "",
				})
			}
			return nil
		})
		if err != nil {
			return nil, onDutyOut{}, err
		}
		return nil, out, nil
	}
}

// --- upcoming ---

type upcomingIn struct {
	Duty  string `json:"duty,omitempty" jsonschema:"duty key (toilet1, toilet2, hall, floor, laundry); omit for every duty"`
	Weeks int    `json:"weeks,omitempty" jsonschema:"weeks to look ahead (default 4)"`
}

type upcomingSlot struct {
	Date    string `json:"date"`
	Weekday string `json:"weekday"`
	Room    string `json:"room,omitempty"`
}

type dutyPlan struct {
	Duty  string         `json:"duty"`
	Label string         `json:"label"`
	Slots []upcomingSlot `json:"slots"`
}

type upcomingOut struct {
	Weeks int        `json:"weeks"`
	Plans []dutyPlan `json:"plans"`
}

func upcomingHandler(loc *time.Location) mcp.ToolHandlerFor[upcomingIn, upcomingOut] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in upcomingIn) (*mcp.CallToolResult, upcomingOut, error) {
		weeks := in.Weeks
		switch {
		case weeks <= 0:
			weeks = schedule.PlanWeeks
		case weeks > maxUpcomingWeeks:
			weeks = maxUpcomingWeeks
		}
		duties, err := selectDuties(in.Duty)
		if err != nil {
			return nil, upcomingOut{}, err
		}
		now := time.Now().In(loc)

		out := upcomingOut{Weeks: weeks}
		err = withConn(ctx, func(ctx context.Context, conn *pgx.Conn) error {
			for _, d := range duties {
				n := weeks * len(d.EventWeekdays())
				entries, err := schedule.GetUpcoming(ctx, conn, d, now, n)
				if err != nil {
					return fmt.Errorf("upcoming for %s: %w", d, err)
				}
				plan := dutyPlan{Duty: string(d), Label: d.Label()}
				for _, e := range entries {
					plan.Slots = append(plan.Slots, upcomingSlot{
						Date:    e.Date.Format(dateLayout),
						Weekday: e.Date.Weekday().String(),
						Room:    e.Room,
					})
				}
				out.Plans = append(out.Plans, plan)
			}
			return nil
		})
		if err != nil {
			return nil, upcomingOut{}, err
		}
		return nil, out, nil
	}
}
