package notifier

import (
	"strings"
	"testing"
	"time"

	"github.com/voicetel/freescout-notifier/internal/models"
)

func baseTicket() models.Ticket {
	return models.Ticket{
		ID:               7,
		Number:           42,
		Subject:          "Cannot connect",
		CustomerName:     "Jane Doe",
		CustomerEmail:    "jane@example.com",
		AssignedUserName: "Agent Smith",
		MailboxID:        1,
		MailboxName:      "Support",
		Status:           models.TicketStatusActive,
		CreatedAt:        time.Now(),
		WaitingSince:     time.Now(),
		MinutesWaiting:   5,
	}
}

func TestFormatAlertNewTicket(t *testing.T) {
	msg := FormatAlert(models.Alert{
		Type:           models.NewTicket,
		Ticket:         baseTicket(),
		ReminderNumber: 1,
	}, "https://help.example.com/")

	for _, want := range []string{
		"New ticket #42",
		"Cannot connect",
		"Jane Doe (jane@example.com)",
		"Support",
		"5 minutes ago",
		"<https://help.example.com/conversation/7|Open #42 in FreeScout>",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "Reminder") {
		t.Errorf("first alert should not mention reminders:\n%s", msg)
	}
}

func TestFormatAlertCustomerReply(t *testing.T) {
	msg := FormatAlert(models.Alert{
		Type:           models.CustomerReply,
		Ticket:         baseTicket(),
		ReminderNumber: 1,
	}, "https://help.example.com")

	if !strings.Contains(msg, "New customer reply on #42") {
		t.Errorf("unexpected message:\n%s", msg)
	}
	// Trailing slash must be trimmed so links are well-formed.
	if strings.Contains(msg, "help.example.com//conversation") {
		t.Errorf("double slash in ticket URL:\n%s", msg)
	}
}

func TestFormatAlertUnansweredActiveReminder(t *testing.T) {
	ticket := baseTicket()
	ticket.MinutesWaiting = 200
	msg := FormatAlert(models.Alert{
		Type:           models.UnansweredActive,
		Ticket:         ticket,
		ReminderNumber: 3,
	}, "https://help.example.com")

	for _, want := range []string{
		"waiting for an agent reply",
		"3 hours 20 minutes",
		"Reminder #3",
		"Agent Smith",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
}

func TestFormatAlertAgingPendingUnassigned(t *testing.T) {
	ticket := baseTicket()
	ticket.AssignedUserName = "  "
	ticket.MinutesWaiting = 60 * 50 // 50 hours
	msg := FormatAlert(models.Alert{
		Type:           models.AgingPending,
		Ticket:         ticket,
		ReminderNumber: 2,
	}, "https://help.example.com")

	for _, want := range []string{
		"still pending",
		"2 days 2 hours",
		"Unassigned",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
}

func TestFormatPush(t *testing.T) {
	ticket := baseTicket()
	ticket.MinutesWaiting = 132

	cases := []struct {
		alert     models.Alert
		wantTitle string
		wantBody  string
	}{
		{
			models.Alert{Type: models.NewTicket, Ticket: ticket, ReminderNumber: 1},
			"🆕 New ticket #42",
			"Cannot connect — Jane Doe (jane@example.com)",
		},
		{
			models.Alert{Type: models.CustomerReply, Ticket: ticket, ReminderNumber: 1},
			"💬 Customer reply on #42",
			"Cannot connect — Jane Doe (jane@example.com)",
		},
		{
			models.Alert{Type: models.UnansweredActive, Ticket: ticket, ReminderNumber: 1},
			"🚨 Ticket #42 unanswered for 2 hours 12 minutes",
			"Cannot connect — assigned: Agent Smith",
		},
		{
			models.Alert{Type: models.AgingPending, Ticket: ticket, ReminderNumber: 3},
			"⏳ Ticket #42 pending for 2 hours 12 minutes (reminder #3)",
			"Cannot connect — waiting on customer",
		},
	}

	for _, c := range cases {
		title, body := FormatPush(c.alert)
		if title != c.wantTitle {
			t.Errorf("title = %q, want %q", title, c.wantTitle)
		}
		if body != c.wantBody {
			t.Errorf("body = %q, want %q", body, c.wantBody)
		}
	}
}

func TestTimeAgoJustNow(t *testing.T) {
	ticket := baseTicket()
	ticket.MinutesWaiting = 0
	msg := FormatAlert(models.Alert{
		Type:           models.NewTicket,
		Ticket:         ticket,
		ReminderNumber: 1,
	}, "https://help.example.com")

	if !strings.Contains(msg, "*Received:* just now") {
		t.Errorf("expected 'just now' without trailing 'ago':\n%s", msg)
	}
}

func TestHumanizeMinutes(t *testing.T) {
	cases := []struct {
		minutes int
		want    string
	}{
		{0, "just now"},
		{1, "1 minute"},
		{45, "45 minutes"},
		{60, "1 hour"},
		{90, "1 hour 30 minutes"},
		{120, "2 hours"},
		{1440, "1 day"},
		{1500, "1 day 1 hour"},
		{2880, "2 days"},
		{2940, "2 days 1 hour"},
	}
	for _, c := range cases {
		if got := HumanizeMinutes(c.minutes); got != c.want {
			t.Errorf("HumanizeMinutes(%d) = %q, want %q", c.minutes, got, c.want)
		}
	}
}
