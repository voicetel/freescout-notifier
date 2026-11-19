package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/voicetel/freescout-notifier/internal/models"
)

// Store persists notifier state in a local SQLite database: event
// deduplication, reminder bookkeeping, the business-hours queue and the
// notification log.
type Store struct {
	db *sql.DB
}

// NewStore opens the SQLite database, applies pragmas and creates the schema
// if needed.
func NewStore(dbPath string) (*Store, error) {
	if dir := filepath.Dir(dbPath); dir != "" && dir != "." && dir != "/" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open SQLite: %w", err)
	}
	// SQLite supports a single writer; one connection avoids SQLITE_BUSY.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to open SQLite at %s: %w", dbPath, err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

const schema = `
CREATE TABLE IF NOT EXISTS seen_events (
	event_key TEXT PRIMARY KEY,
	ticket_id INTEGER NOT NULL,
	seen_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_seen_events_seen_at ON seen_events(seen_at);

CREATE TABLE IF NOT EXISTS ticket_alerts (
	ticket_id       INTEGER NOT NULL,
	alert_type      TEXT NOT NULL,
	alert_count     INTEGER NOT NULL DEFAULT 0,
	condition_since TIMESTAMP,
	first_alert_at  TIMESTAMP,
	last_alert_at   TIMESTAMP,
	ticket_subject  TEXT,
	customer_name   TEXT,
	assigned_user   TEXT,
	minutes_waiting INTEGER,
	PRIMARY KEY (ticket_id, alert_type)
);

CREATE TABLE IF NOT EXISTS queued_notifications (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	ticket_id         INTEGER NOT NULL,
	notification_type TEXT NOT NULL,
	ticket_subject    TEXT,
	payload           TEXT NOT NULL,
	queued_at         TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE (ticket_id, notification_type)
);

CREATE TABLE IF NOT EXISTS notification_log (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	ticket_id         INTEGER NOT NULL,
	ticket_number     INTEGER,
	notification_type TEXT NOT NULL,
	status            TEXT NOT NULL,
	minutes_waiting   INTEGER,
	reminder_number   INTEGER NOT NULL DEFAULT 1,
	created_at        TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_notification_log_created_at ON notification_log(created_at);
`

func (s *Store) migrate() error {
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}
	return nil
}

// --- Event deduplication -------------------------------------------------

// HasSeenEvents reports whether any event has been recorded yet. Used to
// detect the very first run so history is not replayed as alerts.
func (s *Store) HasSeenEvents(ctx context.Context) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM seen_events)`).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists == 1, nil
}

// IsEventSeen reports whether a dedup key was already processed.
func (s *Store) IsEventSeen(ctx context.Context, key string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM seen_events WHERE event_key = ?`, key).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// MarkEventSeen records a dedup key as processed.
func (s *Store) MarkEventSeen(ctx context.Context, key string, ticketID int) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO seen_events (event_key, ticket_id) VALUES (?, ?)`,
		key, ticketID)
	return err
}

// --- Reminder bookkeeping ------------------------------------------------

// GetAlertState returns the reminder state for a ticket, or nil when the
// ticket is not currently tracked.
func (s *Store) GetAlertState(ctx context.Context, ticketID int, alertType models.NotificationType) (*models.AlertState, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT alert_count, last_alert_at FROM ticket_alerts WHERE ticket_id = ? AND alert_type = ?`,
		ticketID, string(alertType))

	var state models.AlertState
	var lastAlertAt sql.NullTime
	err := row.Scan(&state.AlertCount, &lastAlertAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if lastAlertAt.Valid {
		state.LastAlertAt = &lastAlertAt.Time
	}
	return &state, nil
}

// RecordAlert inserts or increments the reminder state for an alert that was
// delivered (sent or queued).
func (s *Store) RecordAlert(ctx context.Context, alert models.Alert) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO ticket_alerts (
			ticket_id, alert_type, alert_count, condition_since,
			first_alert_at, last_alert_at,
			ticket_subject, customer_name, assigned_user, minutes_waiting
		)
		VALUES (?, ?, 1, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, ?, ?, ?, ?)
		ON CONFLICT(ticket_id, alert_type) DO UPDATE SET
			alert_count = alert_count + 1,
			condition_since = excluded.condition_since,
			last_alert_at = CURRENT_TIMESTAMP,
			ticket_subject = excluded.ticket_subject,
			customer_name = excluded.customer_name,
			assigned_user = excluded.assigned_user,
			minutes_waiting = excluded.minutes_waiting`,
		alert.Ticket.ID,
		string(alert.Type),
		alert.Ticket.WaitingSince.UTC(),
		alert.Ticket.Subject,
		alert.Ticket.CustomerName,
		alert.Ticket.AssignedUserName,
		alert.Ticket.MinutesWaiting,
	)
	return err
}

// ResolveAlerts drops the reminder state of tickets that no longer match the
// condition, so a future occurrence starts a fresh alert sequence. It returns
// the number of cleared tickets.
func (s *Store) ResolveAlerts(ctx context.Context, alertType models.NotificationType, activeIDs []int) (int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT ticket_id FROM ticket_alerts WHERE alert_type = ?`, string(alertType))
	if err != nil {
		return 0, err
	}
	var tracked []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		tracked = append(tracked, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	active := make(map[int]struct{}, len(activeIDs))
	for _, id := range activeIDs {
		active[id] = struct{}{}
	}

	var stale []int
	for _, id := range tracked {
		if _, ok := active[id]; !ok {
			stale = append(stale, id)
		}
	}
	if len(stale) == 0 {
		return 0, nil
	}

	var deleted int64
	const chunkSize = 400 // stay well below SQLite's variable limit
	for i := 0; i < len(stale); i += chunkSize {
		end := min(i+chunkSize, len(stale))
		batch := stale[i:end]

		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		query := fmt.Sprintf(
			`DELETE FROM ticket_alerts WHERE alert_type = ? AND ticket_id IN (%s)`, placeholders)

		args := make([]any, 0, len(batch)+1)
		args = append(args, string(alertType))
		for _, id := range batch {
			args = append(args, id)
		}

		res, err := s.db.ExecContext(ctx, query, args...)
		if err != nil {
			return deleted, err
		}
		n, _ := res.RowsAffected()
		deleted += n
	}
	return deleted, nil
}

// --- Business-hours queue ------------------------------------------------

// QueuedNotification is one entry of the business-hours queue.
type QueuedNotification struct {
	ID       int64
	Type     models.NotificationType
	TicketID int
	Subject  string
	Payload  string
	QueuedAt time.Time
}

// Enqueue adds an alert to the queue, replacing any older queued alert for
// the same ticket and type so only the freshest copy is delivered.
func (s *Store) Enqueue(ctx context.Context, alert models.Alert, payload string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO queued_notifications (ticket_id, notification_type, ticket_subject, payload)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(ticket_id, notification_type) DO UPDATE SET
			ticket_subject = excluded.ticket_subject,
			payload = excluded.payload,
			queued_at = CURRENT_TIMESTAMP`,
		alert.Ticket.ID, string(alert.Type), alert.Ticket.Subject, payload)
	return err
}

// Dequeue returns queued notifications oldest first. A limit of 0 or less
// returns everything.
func (s *Store) Dequeue(ctx context.Context, limit int) ([]QueuedNotification, error) {
	query := `SELECT id, ticket_id, notification_type, COALESCE(ticket_subject, ''), payload, queued_at
		FROM queued_notifications ORDER BY queued_at ASC, id ASC`
	args := []any{}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []QueuedNotification
	for rows.Next() {
		var item QueuedNotification
		var notifType string
		if err := rows.Scan(&item.ID, &item.TicketID, &notifType, &item.Subject, &item.Payload, &item.QueuedAt); err != nil {
			return nil, err
		}
		item.Type = models.NotificationType(notifType)
		items = append(items, item)
	}
	return items, rows.Err()
}

// RemoveQueued deletes a queue entry after successful delivery.
func (s *Store) RemoveQueued(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM queued_notifications WHERE id = ?`, id)
	return err
}

// QueueSize returns the number of queued notifications.
func (s *Store) QueueSize(ctx context.Context) (int, error) {
	var size int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM queued_notifications`).Scan(&size)
	return size, err
}

// --- Notification log ----------------------------------------------------

// LogNotification appends an entry to the immutable notification history.
func (s *Store) LogNotification(ctx context.Context, alert models.Alert, status string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO notification_log (ticket_id, ticket_number, notification_type, status, minutes_waiting, reminder_number)
		VALUES (?, ?, ?, ?, ?, ?)`,
		alert.Ticket.ID, alert.Ticket.Number, string(alert.Type), status,
		alert.Ticket.MinutesWaiting, max(alert.ReminderNumber, 1))
	return err
}

// Stats aggregates the notification history for reporting.
func (s *Store) Stats(ctx context.Context) (*models.Stats, error) {
	stats := &models.Stats{
		ByType:          map[string]int{},
		ActiveReminders: map[string]int{},
	}

	row := s.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(status = 'sent'), 0),
			COALESCE(SUM(status = 'sent' AND created_at > datetime('now', '-24 hours')), 0),
			COALESCE(SUM(status = 'failed' AND created_at > datetime('now', '-24 hours')), 0)
		FROM notification_log`)
	if err := row.Scan(&stats.TotalSent, &stats.SentLast24h, &stats.FailedLast24h); err != nil {
		return nil, err
	}

	size, err := s.QueueSize(ctx)
	if err != nil {
		return nil, err
	}
	stats.QueuedNow = size

	rows, err := s.db.QueryContext(ctx, `
		SELECT notification_type, COUNT(*)
		FROM notification_log
		WHERE status = 'sent'
		GROUP BY notification_type`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var notifType string
		var count int
		if err := rows.Scan(&notifType, &count); err != nil {
			rows.Close()
			return nil, err
		}
		stats.ByType[notifType] = count
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = s.db.QueryContext(ctx, `
		SELECT alert_type, COUNT(*)
		FROM ticket_alerts
		GROUP BY alert_type`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var alertType string
		var count int
		if err := rows.Scan(&alertType, &count); err != nil {
			rows.Close()
			return nil, err
		}
		stats.ActiveReminders[alertType] = count
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return stats, nil
}

// --- Maintenance ---------------------------------------------------------

// Cleanup deletes state older than the retention period and reports how many
// rows were removed per table.
func (s *Store) Cleanup(ctx context.Context, retentionDays int) (map[string]int64, error) {
	if retentionDays <= 0 {
		retentionDays = 90
	}
	cutoff := fmt.Sprintf("-%d days", retentionDays)

	tables := []struct {
		name   string
		column string
	}{
		{"seen_events", "seen_at"},
		{"notification_log", "created_at"},
		{"queued_notifications", "queued_at"},
	}

	deleted := make(map[string]int64, len(tables))
	for _, table := range tables {
		query := fmt.Sprintf(`DELETE FROM %s WHERE %s < datetime('now', ?)`, table.name, table.column)
		res, err := s.db.ExecContext(ctx, query, cutoff)
		if err != nil {
			return deleted, fmt.Errorf("cleanup of %s failed: %w", table.name, err)
		}
		n, _ := res.RowsAffected()
		deleted[table.name] = n
	}
	return deleted, nil
}

// Vacuum reclaims disk space in the SQLite file.
func (s *Store) Vacuum(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `VACUUM`)
	return err
}
