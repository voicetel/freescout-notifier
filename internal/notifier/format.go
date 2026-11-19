package notifier

import (
	"fmt"
	"strings"
	"time"

	"github.com/voicetel/freescout-notifier/internal/models"
)

// FormatAlert renders an alert as a Slack message (mrkdwn).
func FormatAlert(alert models.Alert, baseURL string) string {
	t := alert.Ticket
	var b strings.Builder

	switch alert.Type {
	case models.NewTicket:
		fmt.Fprintf(&b, "🆕 *New ticket #%d* — %s\n", t.Number, t.Subject)
		fmt.Fprintf(&b, "*Customer:* %s\n", t.DisplayCustomer())
		fmt.Fprintf(&b, "*Mailbox:* %s\n", t.DisplayMailbox())
		fmt.Fprintf(&b, "*Received:* %s\n", timeAgo(t.MinutesWaiting))

	case models.CustomerReply:
		fmt.Fprintf(&b, "💬 *New customer reply on #%d* — %s\n", t.Number, t.Subject)
		fmt.Fprintf(&b, "*Customer:* %s\n", t.DisplayCustomer())
		fmt.Fprintf(&b, "*Assigned:* %s\n", t.DisplayAssignee())
		fmt.Fprintf(&b, "*Received:* %s\n", timeAgo(t.MinutesWaiting))

	case models.UnansweredActive:
		fmt.Fprintf(&b, "🚨 *Ticket #%d is waiting for an agent reply* — %s\n", t.Number, t.Subject)
		fmt.Fprintf(&b, "*Customer:* %s\n", t.DisplayCustomer())
		fmt.Fprintf(&b, "*Assigned:* %s\n", t.DisplayAssignee())
		fmt.Fprintf(&b, "*Waiting:* %s\n", HumanizeMinutes(t.MinutesWaiting))

	case models.AgingPending:
		fmt.Fprintf(&b, "⏳ *Ticket #%d is still pending* — %s\n", t.Number, t.Subject)
		fmt.Fprintf(&b, "*Customer:* %s\n", t.DisplayCustomer())
		fmt.Fprintf(&b, "*Assigned:* %s\n", t.DisplayAssignee())
		fmt.Fprintf(&b, "*Waiting on customer for:* %s\n", HumanizeMinutes(t.MinutesWaiting))

	default:
		fmt.Fprintf(&b, "ℹ️ *Ticket #%d* — %s\n", t.Number, t.Subject)
	}

	if alert.ReminderNumber > 1 {
		fmt.Fprintf(&b, "🔁 _Reminder #%d — still unhandled_\n", alert.ReminderNumber)
	}

	fmt.Fprintf(&b, "*View ticket:* <%s|Open #%d in FreeScout>", t.URL(baseURL), t.Number)
	return b.String()
}

// FormatPush renders an alert as a mobile push title and body. The ticket URL
// is passed separately as the notification's deep link.
func FormatPush(alert models.Alert) (title, body string) {
	t := alert.Ticket
	reminder := ""
	if alert.ReminderNumber > 1 {
		reminder = fmt.Sprintf(" (reminder #%d)", alert.ReminderNumber)
	}

	switch alert.Type {
	case models.NewTicket:
		title = fmt.Sprintf("🆕 New ticket #%d%s", t.Number, reminder)
		body = fmt.Sprintf("%s — %s", t.Subject, t.DisplayCustomer())

	case models.CustomerReply:
		title = fmt.Sprintf("💬 Customer reply on #%d%s", t.Number, reminder)
		body = fmt.Sprintf("%s — %s", t.Subject, t.DisplayCustomer())

	case models.UnansweredActive:
		title = fmt.Sprintf("🚨 Ticket #%d unanswered for %s%s", t.Number, HumanizeMinutes(t.MinutesWaiting), reminder)
		body = fmt.Sprintf("%s — assigned: %s", t.Subject, t.DisplayAssignee())

	case models.AgingPending:
		title = fmt.Sprintf("⏳ Ticket #%d pending for %s%s", t.Number, HumanizeMinutes(t.MinutesWaiting), reminder)
		body = fmt.Sprintf("%s — waiting on customer", t.Subject)

	default:
		title = fmt.Sprintf("Ticket #%d", t.Number)
		body = t.Subject
	}
	return title, body
}

// timeAgo renders a minute count as "X ago", or "just now" for fresh events.
func timeAgo(minutes int) string {
	if minutes < 1 {
		return "just now"
	}
	return HumanizeMinutes(minutes) + " ago"
}

// HumanizeMinutes renders a minute count as a compact human duration.
func HumanizeMinutes(minutes int) string {
	if minutes < 1 {
		return "just now"
	}

	d := time.Duration(minutes) * time.Minute
	switch {
	case d < time.Hour:
		if minutes == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", minutes)

	case d < 24*time.Hour:
		hours := int(d.Hours())
		mins := minutes % 60
		hourStr := "hours"
		if hours == 1 {
			hourStr = "hour"
		}
		if mins == 0 {
			return fmt.Sprintf("%d %s", hours, hourStr)
		}
		return fmt.Sprintf("%d %s %d minutes", hours, hourStr, mins)

	default:
		days := int(d.Hours()) / 24
		hours := int(d.Hours()) % 24
		dayStr := "days"
		if days == 1 {
			dayStr = "day"
		}
		if hours == 0 {
			return fmt.Sprintf("%d %s", days, dayStr)
		}
		hourStr := "hours"
		if hours == 1 {
			hourStr = "hour"
		}
		return fmt.Sprintf("%d %s %d %s", days, dayStr, hours, hourStr)
	}
}
