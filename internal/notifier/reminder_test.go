package notifier

import (
	"testing"
	"time"

	"github.com/voicetel/freescout-notifier/internal/models"
)

func TestNeedsReminder(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	interval := 4 * time.Hour

	recent := now.Add(-1 * time.Hour)
	exact := now.Add(-interval)
	old := now.Add(-interval - time.Minute)

	cases := []struct {
		name  string
		state models.AlertState
		want  bool
	}{
		{"never alerted", models.AlertState{AlertCount: 0, LastAlertAt: nil}, true},
		{"alerted recently", models.AlertState{AlertCount: 1, LastAlertAt: &recent}, false},
		{"alerted exactly one interval ago", models.AlertState{AlertCount: 1, LastAlertAt: &exact}, true},
		{"alerted longer than interval ago", models.AlertState{AlertCount: 5, LastAlertAt: &old}, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NeedsReminder(c.state, interval, now); got != c.want {
				t.Errorf("NeedsReminder() = %v, want %v", got, c.want)
			}
		})
	}
}
