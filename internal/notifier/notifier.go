package notifier

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/voicetel/freescout-notifier/internal/config"
	"github.com/voicetel/freescout-notifier/internal/database"
	"github.com/voicetel/freescout-notifier/internal/models"
	"github.com/voicetel/freescout-notifier/internal/slack"
)

type Notifier struct {
	fsDB     *sql.DB
	localDB  *database.DB
	config   *config.Config
	slack    *slack.Client
	bizHours *BusinessHours
}

func New(fsDB *sql.DB, localDB *database.DB, cfg *config.Config) *Notifier {
	return &Notifier{
		fsDB:     fsDB,
		localDB:  localDB,
		config:   cfg,
		slack:    slack.NewClient(cfg.Slack),
		bizHours: NewBusinessHours(cfg.BusinessHours),
	}
}

func (n *Notifier) Run() (*models.RunStats, error) {
	start := time.Now()
	stats := &models.RunStats{}

	now := time.Now()
	isBusinessHours := n.bizHours.IsBusinessHours(now)
	isStartOfDay := n.bizHours.IsStartOfBusinessDay(now)

	if n.config.Verbose {
		log.Printf("Current time: %s", now.Format("2006-01-02 15:04:05"))
		log.Printf("Is business hours: %t", isBusinessHours)
		log.Printf("Is start of day: %t", isStartOfDay)
	}

	// If start of business day, process queued notifications first
	if isStartOfDay {
		sent, err := n.sendQueuedNotifications()
		if err != nil {
			log.Printf("Error sending queued notifications: %v", err)
			stats.Errors++
		} else {
			stats.NotificationsSent += sent
		}
	}

	// Get open tickets needing attention
	openTickets, err := database.GetOpenTicketsNeedingAttention(n.fsDB, n.config.OpenThreshold)
	if err != nil {
		return stats, fmt.Errorf("failed to get open tickets: %w", err)
	}
	stats.TicketsChecked += len(openTickets)

	// Get pending tickets needing attention
	pendingTickets, err := database.GetPendingTicketsNeedingAttention(n.fsDB, n.config.PendingThreshold)
	if err != nil {
		return stats, fmt.Errorf("failed to get pending tickets: %w", err)
	}
	stats.TicketsChecked += len(pendingTickets)

	// Process all tickets
	allTickets := append(openTickets, pendingTickets...)

	for _, ticket := range allTickets {
		if err := n.processTicket(ticket, isBusinessHours, stats); err != nil {
			log.Printf("Error processing ticket %d: %v", ticket.ID, err)
			stats.Errors++
		}
	}

	stats.Duration = time.Since(start)
	return stats, nil
}

func (n *Notifier) processTicket(ticket models.Ticket, isBusinessHours bool, stats *models.RunStats) error {
	// Check if we should skip this ticket
	shouldSkip, err := n.shouldSkipTicket(ticket)
	if err != nil {
		return err
	}
	if shouldSkip {
		return nil
	}

	if isBusinessHours {
		// Send immediately
		if !n.config.DryRun {
			if err := n.sendNotification(ticket); err != nil {
				return err
			}
		}
		if err := n.recordNotification(ticket, models.StatusSent); err != nil {
			return err
		}
		stats.NotificationsSent++

		if n.config.Verbose {
			log.Printf("Sent notification for ticket #%d", ticket.Number)
		}
	} else {
		// Queue for later
		if err := n.recordNotification(ticket, models.StatusQueued); err != nil {
			return err
		}
		stats.NotificationsQueued++

		if n.config.Verbose {
			log.Printf("Queued notification for ticket #%d", ticket.Number)
		}
	}

	return nil
}

func (n *Notifier) shouldSkipTicket(ticket models.Ticket) (bool, error) {
	query := `
		SELECT sent_at, notification_status
		FROM notifications
		WHERE ticket_id = ? AND notification_type = ?
		ORDER BY COALESCE(sent_at, queued_at, first_eligible_at) DESC
		LIMIT 1
	`

	var sentAt sql.NullTime
	var status string

	err := n.localDB.QueryRow(query, ticket.ID, ticket.NotificationType).Scan(&sentAt, &status)
	if err == sql.ErrNoRows {
		return false, nil // No previous notification
	}
	if err != nil {
		return false, err
	}

	// If already queued, skip
	if status == string(models.StatusQueued) {
		return true, nil
	}

	// Check cooldown
	if sentAt.Valid {
		cooldownExpiry := sentAt.Time.Add(n.config.CooldownPeriod)
		if time.Now().Before(cooldownExpiry) {
			return true, nil // Still in cooldown
		}
	}

	return false, nil
}

func (n *Notifier) recordNotification(ticket models.Ticket, status models.NotificationStatus) error {
	ticketJSON, err := json.Marshal(ticket)
	if err != nil {
		return err
	}

	query := `
		INSERT INTO notifications (
			ticket_id,
			notification_type,
			notification_status,
			ticket_subject,
			customer_name,
			assigned_user,
			minutes_waiting,
			threshold_minutes,
			ticket_data,
			queued_at,
			sent_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(ticket_id, notification_type)
		DO UPDATE SET
			notification_status = excluded.notification_status,
			ticket_subject = excluded.ticket_subject,
			customer_name = excluded.customer_name,
			assigned_user = excluded.assigned_user,
			minutes_waiting = excluded.minutes_waiting,
			ticket_data = excluded.ticket_data,
			queued_at = CASE
				WHEN excluded.notification_status = 'queued' THEN CURRENT_TIMESTAMP
				ELSE notifications.queued_at
			END,
			sent_at = CASE
				WHEN excluded.notification_status = 'sent' THEN CURRENT_TIMESTAMP
				ELSE notifications.sent_at
			END
		WHERE notifications.sent_at IS NULL
			OR notifications.sent_at < datetime('now', '-' || ? || ' seconds')
	`

	var queuedAt, sentAt sql.NullTime
	if status == models.StatusQueued {
		queuedAt = sql.NullTime{Time: time.Now(), Valid: true}
	} else if status == models.StatusSent {
		sentAt = sql.NullTime{Time: time.Now(), Valid: true}
	}

	thresholdMinutes := int(n.config.OpenThreshold.Minutes())
	if ticket.NotificationType == models.PendingNoCustomerResponse {
		thresholdMinutes = int(n.config.PendingThreshold.Minutes())
	}

	_, err = n.localDB.Exec(query,
		ticket.ID,
		ticket.NotificationType,
		status,
		ticket.Subject,
		ticket.CustomerName,
		ticket.AssignedUserName,
		ticket.MinutesSinceReply,
		thresholdMinutes,
		string(ticketJSON),
		queuedAt,
		sentAt,
		int(n.config.CooldownPeriod.Seconds()),
	)

	return err
}

func (n *Notifier) sendNotification(ticket models.Ticket) error {
	message := n.formatSlackMessage(ticket)
	return n.slack.SendMessage(message)
}

func (n *Notifier) formatSlackMessage(ticket models.Ticket) string {
	emoji := "🚨"
	action := "needs attention"
	waitingFor := "agent response"

	if ticket.NotificationType == models.PendingNoCustomerResponse {
		emoji = "⏳"
		action = "waiting for customer"
		waitingFor = "customer response"
	}

	timeAgo := formatDuration(time.Duration(ticket.MinutesSinceReply) * time.Minute)
	ticketURL := fmt.Sprintf("%s/conversation/%d", n.config.FreeScout.URL, ticket.Number)

	message := fmt.Sprintf("%s Ticket #%d %s\n", emoji, ticket.Number, action)
	message += fmt.Sprintf("*Subject:* %s\n", ticket.Subject)
	message += fmt.Sprintf("*Customer:* %s\n", ticket.CustomerName)
	message += fmt.Sprintf("*Waiting for:* %s for %s\n", waitingFor, timeAgo)

	if ticket.AssignedUserName != "" {
		message += fmt.Sprintf("*Assigned to:* %s\n", ticket.AssignedUserName)
	} else {
		message += "*Assigned to:* Unassigned\n"
	}

	message += fmt.Sprintf("*View ticket:* <%s|Open in FreeScout>", ticketURL)

	return message
}

func (n *Notifier) sendQueuedNotifications() (int, error) {
	query := `
		SELECT
			ticket_id,
			notification_type,
			ticket_data
		FROM notifications
		WHERE notification_status = 'queued'
		ORDER BY queued_at ASC
		LIMIT ?
	`

	rows, err := n.localDB.Query(query, n.config.MaxNotifications)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	sent := 0
	for rows.Next() {
		var ticketID int
		var notificationType string
		var ticketData string

		if err := rows.Scan(&ticketID, &notificationType, &ticketData); err != nil {
			log.Printf("Error scanning queued notification: %v", err)
			continue
		}

		var ticket models.Ticket
		if err := json.Unmarshal([]byte(ticketData), &ticket); err != nil {
			log.Printf("Error unmarshaling ticket data: %v", err)
			continue
		}

		// Send notification
		if !n.config.DryRun {
			if err := n.sendNotification(ticket); err != nil {
				log.Printf("Error sending queued notification for ticket %d: %v", ticketID, err)
				continue
			}
		}

		// Update status
		updateQuery := `
			UPDATE notifications
			SET notification_status = 'sent', sent_at = CURRENT_TIMESTAMP
			WHERE ticket_id = ? AND notification_type = ?
		`
		if _, err := n.localDB.Exec(updateQuery, ticketID, notificationType); err != nil {
			log.Printf("Error updating notification status: %v", err)
			continue
		}

		sent++

		// Rate limit
		if sent < n.config.MaxNotifications {
			time.Sleep(2 * time.Second)
		}
	}

	// Log business hours event - FIX: Check error return value
	if sent > 0 {
		logQuery := `
			INSERT INTO business_hours_log (event_type, notifications_sent)
			VALUES ('burst_sent', ?)
		`
		if _, err := n.localDB.Exec(logQuery, sent); err != nil {
			log.Printf("Warning: failed to log business hours event: %v", err)
		}
	}

	return sent, nil
}

func formatDuration(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	}

	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60

	if hours == 1 {
		if minutes == 0 {
			return "1 hour"
		}
		return fmt.Sprintf("1 hour %d minutes", minutes)
	}

	if minutes == 0 {
		return fmt.Sprintf("%d hours", hours)
	}
	return fmt.Sprintf("%d hours %d minutes", hours, minutes)
}

func TestSlackWebhook(webhookURL string) error {
	client := slack.NewClient(config.SlackConfig{
		WebhookURL: webhookURL,
		Timeout:    10 * time.Second,
	})

	return client.SendMessage("🔧 FreeScout Notifier test message - connection successful!")
}
