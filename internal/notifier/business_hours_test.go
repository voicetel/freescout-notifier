package notifier

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/voicetel/freescout-notifier/internal/config"
)

// Wednesday 2026-11-18 is a weekday; Saturday 2026-11-21 is not.
var (
	weekdayNoon    = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	weekdayEarly   = time.Date(2026, 11, 18, 7, 30, 0, 0, time.UTC)
	weekdayLate    = time.Date(2026, 11, 18, 17, 0, 0, 0, time.UTC)
	weekdayStart   = time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	saturdayNoon   = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	holidayNoon    = time.Date(2026, 12, 25, 12, 0, 0, 0, time.UTC)
	weekdayNoonCHI = time.Date(2026, 11, 18, 12, 0, 0, 0, mustLoc("America/Chicago"))
)

func mustLoc(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

func testConfig() config.BusinessHoursConfig {
	return config.BusinessHoursConfig{
		Enabled:   true,
		StartHour: 9,
		EndHour:   17,
		Timezone:  "UTC",
		WorkDays:  []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday},
	}
}

func TestIsBusinessHours(t *testing.T) {
	bh := NewBusinessHours(testConfig())

	cases := []struct {
		name string
		t    time.Time
		want bool
	}{
		{"weekday noon", weekdayNoon, true},
		{"before start", weekdayEarly, false},
		{"at end hour (exclusive)", weekdayLate, false},
		{"at start hour (inclusive)", weekdayStart, true},
		{"weekend", saturdayNoon, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := bh.IsBusinessHours(c.t); got != c.want {
				t.Errorf("IsBusinessHours(%v) = %v, want %v", c.t, got, c.want)
			}
		})
	}
}

func TestBusinessHoursDisabled(t *testing.T) {
	cfg := testConfig()
	cfg.Enabled = false
	bh := NewBusinessHours(cfg)

	if !bh.IsBusinessHours(saturdayNoon) {
		t.Error("disabled business hours should treat every moment as open")
	}
}

func TestBusinessHoursTimezone(t *testing.T) {
	cfg := testConfig()
	cfg.Timezone = "America/Chicago"
	bh := NewBusinessHours(cfg)

	// 12:00 in Chicago is business hours regardless of the wall clock elsewhere.
	if !bh.IsBusinessHours(weekdayNoonCHI) {
		t.Error("expected Chicago noon on a Wednesday to be business hours")
	}
	// 12:00 UTC is 06:00 in Chicago (CST) — before the 9:00 start.
	if bh.IsBusinessHours(weekdayNoon) {
		t.Error("expected 12:00 UTC to be before business hours in Chicago")
	}
}

func TestBusinessHoursHolidays(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "holidays.json")
	if err := os.WriteFile(file, []byte(`{"holidays":["2026-12-25"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	cfg.HolidaysFile = file
	bh := NewBusinessHours(cfg)

	if bh.IsBusinessHours(holidayNoon) {
		t.Error("expected a holiday to be outside business hours")
	}
	if !bh.IsBusinessHours(weekdayNoon) {
		t.Error("expected a normal weekday to be inside business hours")
	}
}

func TestBusinessHoursBadHolidaysFile(t *testing.T) {
	cfg := testConfig()
	cfg.HolidaysFile = "/nonexistent/holidays.json"

	// Must not panic; the schedule keeps working without holidays.
	bh := NewBusinessHours(cfg)
	if !bh.IsBusinessHours(weekdayNoon) {
		t.Error("expected weekday noon to remain business hours")
	}
}
