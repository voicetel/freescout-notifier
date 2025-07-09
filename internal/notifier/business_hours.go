package notifier

import (
	"encoding/json"
	"log"
	"os"
	"time"

	"github.com/voicetel/freescout-notifier/internal/config"
)

type BusinessHours struct {
	enabled      bool
	startHour    int
	endHour      int
	timezone     *time.Location
	workDays     map[time.Weekday]bool
	holidays     map[string]bool
	notifyOnOpen bool
}

type HolidaysFile struct {
	Holidays []string `json:"holidays"`
}

func NewBusinessHours(cfg config.BusinessHoursConfig) *BusinessHours {
	bh := &BusinessHours{
		enabled:      cfg.Enabled,
		startHour:    cfg.StartHour,
		endHour:      cfg.EndHour,
		workDays:     make(map[time.Weekday]bool),
		holidays:     make(map[string]bool),
		notifyOnOpen: cfg.NotifyOnOpen,
	}

	// Load timezone
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		loc = time.UTC
	}
	bh.timezone = loc

	// Set work days
	for _, day := range cfg.WorkDays {
		bh.workDays[day] = true
	}

	// Load holidays - FIX: Check error return value
	if cfg.HolidaysFile != "" {
		if err := bh.loadHolidays(cfg.HolidaysFile); err != nil {
			log.Printf("Warning: failed to load holidays file %s: %v", cfg.HolidaysFile, err)
		}
	}

	return bh
}

func (bh *BusinessHours) IsBusinessHours(t time.Time) bool {
	if !bh.enabled {
		return true
	}

	localTime := t.In(bh.timezone)

	// Check if holiday
	dateStr := localTime.Format("2006-01-02")
	if bh.holidays[dateStr] {
		return false
	}

	// Check if work day
	if !bh.workDays[localTime.Weekday()] {
		return false
	}

	// Check if within hours
	hour := localTime.Hour()
	return hour >= bh.startHour && hour < bh.endHour
}

func (bh *BusinessHours) IsStartOfBusinessDay(t time.Time) bool {
	if !bh.enabled || !bh.notifyOnOpen {
		return false
	}

	localTime := t.In(bh.timezone)

	// Must be business hours
	if !bh.IsBusinessHours(t) {
		return false
	}

	// Check if within first 5 minutes of start hour
	return localTime.Hour() == bh.startHour && localTime.Minute() < 5
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
