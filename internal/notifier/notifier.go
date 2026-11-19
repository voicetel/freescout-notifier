// Package notifier contains the monitoring loops and alert dispatch logic.
package notifier

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/voicetel/freescout-notifier/internal/config"
	"github.com/voicetel/freescout-notifier/internal/database"
	"github.com/voicetel/freescout-notifier/internal/logging"
	"github.com/voicetel/freescout-notifier/internal/models"
	"github.com/voicetel/freescout-notifier/internal/push"
	"github.com/voicetel/freescout-notifier/internal/slack"
)

// Notifier monitors a FreeScout installation and dispatches Slack alerts:
//
//   - new tickets and new customer replies are detected by a fast poller
//     (near real time) and deduplicated via the local state store;
//   - active tickets waiting too long for an agent and pending tickets
//     aging too long are detected by a slower sweep and re-alerted on a
//     reminder interval until the condition clears;
//   - outside business hours alerts are queued and flushed when the
//     business day is open.
type Notifier struct {
	fs       *database.FreeScoutDB
	store    *database.Store
	cfg      *config.Config
	slack    *slack.Client
	push     *push.Client // nil when mobile push is disabled or failed to init
	bizHours *BusinessHours
	log      *logging.Logger
}

// New builds a Notifier.
func New(fs *database.FreeScoutDB, store *database.Store, cfg *config.Config, logger *logging.Logger) *Notifier {
	n := &Notifier{
		fs:       fs,
		store:    store,
		cfg:      cfg,
		slack:    slack.NewClient(cfg.Slack),
		bizHours: NewBusinessHours(cfg.BusinessHours),
		log:      logger,
	}
	if cfg.Push.Enabled {
		client, err := push.NewClient(context.Background(), cfg.Push)
		if err != nil {
			logger.LogError("mobile push disabled: failed to initialize FCM client", err)
		} else {
			n.push = client
			logger.Info("mobile push enabled",
				"topic", client.Topic(), "alert_types", cfg.Push.AlertTypes)
		}
	}
	return n
}

// ConversationEventKey is the dedup key for a new conversation.
func ConversationEventKey(conversationID int) string {
	return fmt.Sprintf("conversation:%d", conversationID)
}

// ThreadEventKey is the dedup key for a customer reply thread.
func ThreadEventKey(threadID int) string {
	return fmt.Sprintf("thread:%d", threadID)
}

// RunDaemon blocks, running the monitoring loops until ctx is cancelled.
func (n *Notifier) RunDaemon(ctx context.Context) error {
	n.log.Info("starting notifier daemon",
		"poll_interval", n.cfg.PollInterval.String(),
		"check_interval", n.cfg.CheckInterval.String(),
		"lookback_window", n.cfg.LookbackWindow.String(),
		"unanswered_threshold", n.cfg.UnansweredThreshold.String(),
		"pending_threshold", n.cfg.PendingThreshold.String(),
		"reminder_interval", n.cfg.ReminderInterval.String(),
		"business_hours", n.cfg.BusinessHours.Enabled,
		"last_customer_reply_at_column", n.fs.HasLastCustomerReply(),
		"dry_run", n.cfg.DryRun,
	)

	n.seedSeenEvents(ctx)

	// Run a full cycle immediately so state is fresh after a restart.
	startup := &models.CycleStats{}
	start := time.Now()
	n.pollNewEvents(ctx, startup)
	n.checkAging(ctx, startup)
	n.flushQueue(ctx)
	startup.Duration = time.Since(start)
	n.logCycle("startup", startup)

	pollTicker := time.NewTicker(n.cfg.PollInterval.Duration)
	checkTicker := time.NewTicker(n.cfg.CheckInterval.Duration)
	defer pollTicker.Stop()
	defer checkTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			n.log.Info("shutdown requested; stopping notifier daemon")
			return nil

		case <-pollTicker.C:
			stats := &models.CycleStats{}
			start := time.Now()
			n.pollNewEvents(ctx, stats)
			n.flushQueue(ctx)
			stats.Duration = time.Since(start)
			n.logCycle("poll", stats)

		case <-checkTicker.C:
			stats := &models.CycleStats{}
			start := time.Now()
			n.checkAging(ctx, stats)
			n.flushQueue(ctx)
			stats.Duration = time.Since(start)
			n.logCycle("aging_check", stats)
		}
	}
}

// RunOnce executes a single full monitoring pass and returns. Intended for
// cron deployments and testing.
func (n *Notifier) RunOnce(ctx context.Context) (*models.CycleStats, error) {
	stats := &models.CycleStats{}
	start := time.Now()

	n.seedSeenEvents(ctx)
	n.pollNewEvents(ctx, stats)
	n.checkAging(ctx, stats)
	n.flushQueue(ctx)

	stats.Duration = time.Since(start)
	if stats.Errors > 0 {
		return stats, fmt.Errorf("%d error(s) occurred during the run", stats.Errors)
	}
	return stats, nil
}

// --- Event detection ------------------------------------------------------

// seedSeenEvents marks the events already present in the lookback window as
// seen on the very first run, so history is not replayed as a flood of
// alerts. Later runs rely on the persisted dedup keys, which also gives
// catch-up coverage for events that arrived while the process was down.
func (n *Notifier) seedSeenEvents(ctx context.Context) {
	hasEvents, err := n.store.HasSeenEvents(ctx)
	if err != nil {
		n.log.LogError("failed to check seen-events state", err)
		return
	}
	if hasEvents {
		return
	}

	seeded := 0
	if convs, err := n.fs.FetchNewConversations(ctx, n.cfg.LookbackWindow.Duration); err != nil {
		n.log.LogError("failed to seed existing conversations", err)
	} else {
		for _, t := range convs {
			if err := n.store.MarkEventSeen(ctx, ConversationEventKey(t.ID), t.ID); err != nil {
				n.log.LogError("failed to seed conversation event", err, "ticket", t.Number)
				break
			}
			seeded++
		}
	}

	if replies, err := n.fs.FetchCustomerReplies(ctx, n.cfg.LookbackWindow.Duration); err != nil {
		n.log.LogError("failed to seed existing customer replies", err)
	} else {
		for _, r := range replies {
			if err := n.store.MarkEventSeen(ctx, ThreadEventKey(r.ThreadID), r.Ticket.ID); err != nil {
				n.log.LogError("failed to seed reply event", err, "ticket", r.Ticket.Number)
				break
			}
			seeded++
		}
	}

	n.log.Info("first run: existing events marked as seen without notifying",
		"events", seeded, "lookback_window", n.cfg.LookbackWindow.String())
}

// pollNewEvents detects new conversations and new customer replies.
func (n *Notifier) pollNewEvents(ctx context.Context, stats *models.CycleStats) {
	convs, err := n.fs.FetchNewConversations(ctx, n.cfg.LookbackWindow.Duration)
	if err != nil {
		n.log.LogError("failed to fetch new conversations", err)
		stats.Errors++
	}
	for _, t := range convs {
		alert := models.Alert{Type: models.NewTicket, Ticket: t, ReminderNumber: 1}
		if n.handleNewEvent(ctx, ConversationEventKey(t.ID), alert, stats) {
			stats.NewTickets++
		}
	}

	replies, err := n.fs.FetchCustomerReplies(ctx, n.cfg.LookbackWindow.Duration)
	if err != nil {
		n.log.LogError("failed to fetch customer replies", err)
		stats.Errors++
	}
	for _, r := range replies {
		alert := models.Alert{Type: models.CustomerReply, Ticket: r.Ticket, ReminderNumber: 1}
		if n.handleNewEvent(ctx, ThreadEventKey(r.ThreadID), alert, stats) {
			stats.CustomerReplies++
		}
	}
}

// handleNewEvent dispatches an alert the first time its dedup key is seen.
//
// Events are delivered at-most-once: the key is recorded before sending so a
// failing delivery never produces duplicate alerts on the next poll. Tickets
// that genuinely need attention are still picked up by the aging sweeps.
func (n *Notifier) handleNewEvent(ctx context.Context, key string, alert models.Alert, stats *models.CycleStats) bool {
	seen, err := n.store.IsEventSeen(ctx, key)
	if err != nil {
		n.log.LogError("failed to check event state", err, "event", key)
		stats.Errors++
		return false
	}
	if seen {
		return false
	}

	if err := n.store.MarkEventSeen(ctx, key, alert.Ticket.ID); err != nil {
		n.log.LogError("failed to record event state", err, "event", key)
		stats.Errors++
		return false
	}

	n.dispatch(ctx, alert, stats)
	return true
}

// --- Aging sweeps ---------------------------------------------------------

// checkAging runs both aging sweeps: unanswered active tickets and aging
// pending tickets.
func (n *Notifier) checkAging(ctx context.Context, stats *models.CycleStats) {
	unanswered, err := n.fs.FetchUnansweredActive(ctx, n.cfg.UnansweredThreshold.Duration)
	if err != nil {
		n.log.LogError("failed to fetch unanswered active tickets", err)
		stats.Errors++
	} else {
		n.processAging(ctx, unanswered, models.UnansweredActive, stats)
	}

	pending, err := n.fs.FetchAgingPending(ctx, n.cfg.PendingThreshold.Duration)
	if err != nil {
		n.log.LogError("failed to fetch aging pending tickets", err)
		stats.Errors++
	} else {
		n.processAging(ctx, pending, models.AgingPending, stats)
	}
}

// processAging alerts for every matching ticket that has no reminder state
// yet or whose reminder interval has elapsed, then clears the state of
// tickets whose condition has resolved.
func (n *Notifier) processAging(ctx context.Context, tickets []models.Ticket, alertType models.NotificationType, stats *models.CycleStats) {
	activeIDs := make([]int, 0, len(tickets))

	for _, ticket := range tickets {
		activeIDs = append(activeIDs, ticket.ID)

		state, err := n.store.GetAlertState(ctx, ticket.ID, alertType)
		if err != nil {
			n.log.LogError("failed to load alert state", err, "ticket", ticket.Number)
			stats.Errors++
			continue
		}

		reminderNumber := 1
		if state != nil {
			if !NeedsReminder(*state, n.cfg.ReminderInterval.Duration, time.Now()) {
				continue
			}
			reminderNumber = state.AlertCount + 1
		}

		alert := models.Alert{Type: alertType, Ticket: ticket, ReminderNumber: reminderNumber}
		if !n.dispatch(ctx, alert, stats) {
			// Delivery failed; do not record state so the next sweep retries.
			continue
		}

		if err := n.store.RecordAlert(ctx, alert); err != nil {
			n.log.LogError("failed to record alert state", err, "ticket", ticket.Number)
			stats.Errors++
		}
		if reminderNumber > 1 {
			stats.Reminders++
		}
	}

	resolved, err := n.store.ResolveAlerts(ctx, alertType, activeIDs)
	if err != nil {
		n.log.Warn("failed to clear resolved alert states",
			"error", err, "type", string(alertType))
	} else if resolved > 0 {
		n.log.Info("conditions cleared; reminder state reset",
			"type", string(alertType), "tickets", resolved)
	}
}

// NeedsReminder reports whether enough time has passed since the last alert
// to send another reminder.
func NeedsReminder(state models.AlertState, interval time.Duration, now time.Time) bool {
	if state.LastAlertAt == nil {
		return true
	}
	return !now.Before(state.LastAlertAt.Add(interval))
}

// --- Delivery -------------------------------------------------------------

// dispatch delivers an alert immediately during business hours and queues it
// otherwise. It reports whether the alert was delivered (sent or queued).
func (n *Notifier) dispatch(ctx context.Context, alert models.Alert, stats *models.CycleStats) bool {
	if n.cfg.MaxNotifications > 0 && stats.Sent+stats.Queued >= n.cfg.MaxNotifications {
		n.log.Warn("max notifications per cycle reached; skipping alert",
			"ticket", alert.Ticket.Number, "type", string(alert.Type))
		stats.Skipped++
		return false
	}

	if !n.bizHours.IsBusinessHours(time.Now()) {
		return n.queue(ctx, alert, stats)
	}
	return n.send(ctx, alert, stats)
}

// queue stores an alert for delivery when business hours open. Queued alerts
// count as delivered for reminder bookkeeping.
func (n *Notifier) queue(ctx context.Context, alert models.Alert, stats *models.CycleStats) bool {
	payload, err := json.Marshal(alert)
	if err != nil {
		n.log.LogError("failed to serialize queued alert", err, "ticket", alert.Ticket.Number)
		stats.Errors++
		return false
	}
	if err := n.store.Enqueue(ctx, alert, string(payload)); err != nil {
		n.log.LogError("failed to queue alert", err, "ticket", alert.Ticket.Number)
		stats.Errors++
		return false
	}
	if err := n.store.LogNotification(ctx, alert, models.LogQueued); err != nil {
		n.log.Warn("failed to write notification log", "error", err, "ticket", alert.Ticket.Number)
	}

	stats.Queued++
	n.log.Info("outside business hours; notification queued",
		"ticket", alert.Ticket.Number, "subject", alert.Ticket.Subject,
		"type", string(alert.Type), "reminder", alert.ReminderNumber)
	return true
}

// send delivers an alert to Slack right away.
func (n *Notifier) send(ctx context.Context, alert models.Alert, stats *models.CycleStats) bool {
	message := FormatAlert(alert, n.cfg.FreeScout.URL)

	if n.cfg.DryRun {
		n.log.Info("dry-run: would send notification",
			"ticket", alert.Ticket.Number, "subject", alert.Ticket.Subject,
			"type", string(alert.Type), "reminder", alert.ReminderNumber)
		n.log.Verbose("dry-run message body", "message", message)
		n.pushAlert(ctx, alert)
		if err := n.store.LogNotification(ctx, alert, models.LogSent); err != nil {
			n.log.Warn("failed to write notification log", "error", err, "ticket", alert.Ticket.Number)
		}
		stats.Sent++
		return true
	}

	if err := n.slack.SendMessage(ctx, message); err != nil {
		n.log.LogError("failed to send Slack notification", err,
			"ticket", alert.Ticket.Number, "type", string(alert.Type))
		if logErr := n.store.LogNotification(ctx, alert, models.LogFailed); logErr != nil {
			n.log.Warn("failed to write notification log", "error", logErr)
		}
		stats.Errors++
		return false
	}

	n.pushAlert(ctx, alert)

	if err := n.store.LogNotification(ctx, alert, models.LogSent); err != nil {
		n.log.Warn("failed to write notification log", "error", err, "ticket", alert.Ticket.Number)
	}
	stats.Sent++
	n.log.Info("notification sent",
		"ticket", alert.Ticket.Number, "subject", alert.Ticket.Subject,
		"type", string(alert.Type), "reminder", alert.ReminderNumber)
	return true
}

// pushAlert delivers the mobile push counterpart of an alert. Push is
// best-effort: Slack remains the delivery of record, so a failing push never
// blocks or retries the alert itself.
func (n *Notifier) pushAlert(ctx context.Context, alert models.Alert) {
	if n.push == nil || !n.push.ShouldPush(string(alert.Type)) {
		return
	}

	title, body := FormatPush(alert)
	url := alert.Ticket.URL(n.cfg.FreeScout.URL)

	if n.cfg.DryRun {
		n.log.Verbose("dry-run: would send push notification",
			"title", title, "body", body, "url", url)
		return
	}

	if err := n.push.Send(ctx, title, body, url, string(alert.Type)); err != nil {
		n.log.Warn("push notification failed; Slack delivery unaffected",
			"error", err, "ticket", alert.Ticket.Number, "type", string(alert.Type))
		return
	}
	n.log.Verbose("push notification sent",
		"ticket", alert.Ticket.Number, "type", string(alert.Type))
}

// flushQueue delivers queued notifications while business hours are open.
// Failed deliveries stay queued and are retried on a later cycle.
func (n *Notifier) flushQueue(ctx context.Context) {
	if !n.cfg.BusinessHours.NotifyOnOpen {
		return
	}
	if !n.bizHours.IsBusinessHours(time.Now()) {
		return
	}

	size, err := n.store.QueueSize(ctx)
	if err != nil {
		n.log.LogError("failed to read queue size", err)
		return
	}
	if size == 0 {
		return
	}

	items, err := n.store.Dequeue(ctx, 0)
	if err != nil {
		n.log.LogError("failed to read notification queue", err)
		return
	}

	n.log.Info("business hours open; flushing queued notifications", "count", len(items))
	flushed := 0

	for i, item := range items {
		if ctx.Err() != nil {
			return
		}

		var alert models.Alert
		if err := json.Unmarshal([]byte(item.Payload), &alert); err != nil {
			n.log.LogError("dropping queued notification with corrupt payload", err, "id", item.ID)
			if rmErr := n.store.RemoveQueued(ctx, item.ID); rmErr != nil {
				n.log.Warn("failed to remove corrupt queue entry", "error", rmErr, "id", item.ID)
			}
			continue
		}

		if n.cfg.DryRun {
			n.log.Info("dry-run: would flush queued notification",
				"ticket", alert.Ticket.Number, "type", string(alert.Type))
			n.pushAlert(ctx, alert)
		} else if err := n.slack.SendMessage(ctx, FormatAlert(alert, n.cfg.FreeScout.URL)); err != nil {
			n.log.LogError("failed to flush queued notification; it stays queued for retry", err,
				"ticket", alert.Ticket.Number, "type", string(alert.Type))
			continue
		} else {
			n.pushAlert(ctx, alert)
		}

		if err := n.store.RemoveQueued(ctx, item.ID); err != nil {
			n.log.LogError("failed to remove queued notification", err, "id", item.ID)
		}
		if err := n.store.LogNotification(ctx, alert, models.LogSent); err != nil {
			n.log.Warn("failed to write notification log", "error", err, "ticket", alert.Ticket.Number)
		}
		flushed++

		// Space messages out to stay well within Slack webhook limits.
		if i < len(items)-1 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}

	n.log.Info("queue flush complete", "flushed", flushed, "remaining", len(items)-flushed)
}

// --- Logging --------------------------------------------------------------

func (n *Notifier) logCycle(cycle string, stats *models.CycleStats) {
	if stats.IsEmpty() && !n.cfg.Verbose {
		return
	}
	n.log.Info("cycle complete",
		"cycle", cycle,
		"new_tickets", stats.NewTickets,
		"customer_replies", stats.CustomerReplies,
		"sent", stats.Sent,
		"queued", stats.Queued,
		"reminders", stats.Reminders,
		"skipped", stats.Skipped,
		"errors", stats.Errors,
		"duration", stats.Duration.String(),
	)
}
