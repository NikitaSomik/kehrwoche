package schedule

import (
	"strings"
	"testing"
	"time"
)

func TestParseDutyType(t *testing.T) {
	for _, d := range AllDutyTypes() {
		got, err := ParseDutyType(string(d))
		if err != nil || got != d {
			t.Errorf("ParseDutyType(%q) = %q, %v; want %q, nil", d, got, err, d)
		}
	}

	_, err := ParseDutyType("kitchen")
	if err == nil {
		t.Fatal("expected an error for an unknown duty")
	}
	// The valid keys come from configs, so a new duty shows up here by itself.
	for _, d := range AllDutyTypes() {
		if !strings.Contains(err.Error(), string(d)) {
			t.Errorf("error %q does not list %q", err, d)
		}
	}
}

func TestGetUpcomingNothingAsked(t *testing.T) {
	// A nil Querier proves no query runs: touching it would panic.
	for _, n := range []int{0, -1} {
		got, err := GetUpcoming(t.Context(), nil, DutyTypeFloor, time.Now(), n)
		if err != nil || got != nil {
			t.Errorf("n=%d: got %v, %v; want nil, nil", n, got, err)
		}
	}
}
