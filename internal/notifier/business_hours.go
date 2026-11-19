package notifier

import (
	"encoding/json"
	"log/slog"
	"os"
	"time"

	"github.com/voicetel/freescout-notifier/internal/config"
)

// BusinessHours decides whether notifications are delivered immediately or
// queued until the next business period.
type BusinessHours struct {
	enabled   bool
	startHour int
	endHour   int
	timezone  *time.Location
	workDays  map[time.Weekday]bool
	holidays  map[string]bool
}

// HolidaysFile is the JSON layout of the holidays configuration.
type HolidaysFile struct {
	Holidays []string `json:"holidays"`
}

// NewBusinessHours builds the schedule from configuration. An invalid
// timezone falls back to UTC, and an unusable holidays file is logged and
// ignored.
func NewBusinessHours(cfg config.BusinessHoursConfig) *BusinessHours {
	bh := &BusinessHours{
		enabled:   cfg.Enabled,
		startHour: cfg.StartHour,
		endHour:   cfg.EndHour,
		workDays:  make(map[time.Weekday]bool, len(cfg.WorkDays)),
		holidays:  make(map[string]bool),
	}

	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		slog.Warn("invalid business hours timezone, falling back to UTC",
			"timezone", cfg.Timezone, "error", err)
		loc = time.UTC
	}
	bh.timezone = loc

	for _, day := range cfg.WorkDays {
		bh.workDays[day] = true
	}

	if cfg.HolidaysFile != "" {
		if err := bh.loadHolidays(cfg.HolidaysFile); err != nil {
			slog.Warn("failed to load holidays file",
				"file", cfg.HolidaysFile, "error", err)
		}
	}

	return bh
}

// IsBusinessHours reports whether t falls inside the configured schedule.
// When business hours are disabled, every moment counts as business hours.
func (bh *BusinessHours) IsBusinessHours(t time.Time) bool {
	if !bh.enabled {
		return true
	}

	localTime := t.In(bh.timezone)

	if bh.holidays[localTime.Format("2006-01-02")] {
		return false
	}
	if !bh.workDays[localTime.Weekday()] {
		return false
	}

	hour := localTime.Hour()
	return hour >= bh.startHour && hour < bh.endHour
}

func (bh *BusinessHours) loadHolidays(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}

	var hf HolidaysFile
	if err := json.Unmarshal(data, &hf); err != nil {
		return err
	}

	for _, holiday := range hf.Holidays {
		bh.holidays[holiday] = true
	}
	return nil
}
