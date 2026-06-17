package bot

import (
	"testing"
	"time"

	"github.com/nikitasomusev/kehrwoche/pkg/schedule"
)

func TestCleaningWindow(t *testing.T) {
	cases := []struct {
		name string
		date string
		want string
	}{
		{"thursday mid-month", "2026-06-18", "19.06 – 21.06"},
		{"monday same week", "2026-06-15", "19.06 – 21.06"},
		{"sunday same week", "2026-06-21", "19.06 – 21.06"},
		{"month boundary jan-feb", "2026-01-29", "30.01 – 01.02"},
		{"month boundary mar-apr", "2026-03-26", "27.03 – 29.03"},
		{"year boundary", "2026-12-31", "01.01 – 03.01"},
		{"friday in window", "2026-06-19", "19.06 – 21.06"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := time.Parse("2006-01-02", tc.date)
			if got := schedule.CleaningWindow(d); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}