package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	*sql.DB
}

func InitSQLite(dbPath string) (*DB, error) {
	// Ensure directory exists
	dir := filepath.Dir(dbPath)
	if dir != "." && dir != "/" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open SQLite: %w", err)
	}

	// Set pragmas for better performance
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	}

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to set pragma %s: %w", pragma, err)
		}
	}

	return &DB{db}, nil
}

func InitSchema(db *DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS notifications (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ticket_id INTEGER NOT NULL,
		notification_type TEXT NOT NULL,
		notification_status TEXT NOT NULL DEFAULT 'pending',
		first_eligible_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		queued_at TIMESTAMP DEFAULT NULL,
		sent_at TIMESTAMP DEFAULT NULL,
		ticket_subject TEXT,
		customer_name TEXT,
		assigned_user TEXT,
		minutes_waiting INTEGER,
		threshold_minutes INTEGER,
		ticket_data TEXT,
		UNIQUE(ticket_id, notification_type)
	);

	CREATE INDEX IF NOT EXISTS idx_notification_queue ON notifications(notification_status, queued_at);

	CREATE TABLE IF NOT EXISTS business_hours_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		event_type TEXT NOT NULL,
		event_time TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		notifications_sent INTEGER DEFAULT 0
	);
	`

	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	return nil
}

// GetNotificationStats returns statistics about notifications
func (db *DB) GetNotificationStats() (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	// Total notifications
	var total int
	err := db.QueryRow("SELECT COUNT(*) FROM notifications").Scan(&total)
	if err != nil {
		return nil, err
	}
	stats["total_notifications"] = total

	// Notifications by status
	statusQuery := `
		SELECT notification_status, COUNT(*)
		FROM notifications
		GROUP BY notification_status
	`
	rows, err := db.Query(statusQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	statusCounts := make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		statusCounts[status] = count
	}
	stats["by_status"] = statusCounts

	// Notifications by type
	typeQuery := `
		SELECT notification_type, COUNT(*)
		FROM notifications
		GROUP BY notification_type
	`
	rows, err = db.Query(typeQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	typeCounts := make(map[string]int)
	for rows.Next() {
		var notifType string
		var count int
		if err := rows.Scan(&notifType, &count); err != nil {
			return nil, err
		}
		typeCounts[notifType] = count
	}
	stats["by_type"] = typeCounts

	// Notifications sent in last 24 hours
	var last24h int
	err = db.QueryRow(`
		SELECT COUNT(*)
		FROM notifications
		WHERE sent_at > datetime('now', '-24 hours')
	`).Scan(&last24h)
	if err != nil {
		return nil, err
	}
	stats["sent_last_24h"] = last24h

	// Current queue size
	var queueSize int
	err = db.QueryRow(`
		SELECT COUNT(*)
		FROM notifications
		WHERE notification_status = 'queued'
	`).Scan(&queueSize)
	if err != nil {
		return nil, err
	}
	stats["current_queue_size"] = queueSize

	// Business hours burst stats
	burstQuery := `
		SELECT COUNT(*), COALESCE(SUM(notifications_sent), 0)
		FROM business_hours_log
		WHERE event_type = 'burst_sent'
		AND event_time > datetime('now', '-7 days')
	`
	var burstEvents, totalBurstSent int
	err = db.QueryRow(burstQuery).Scan(&burstEvents, &totalBurstSent)
	if err != nil {
		return nil, err
	}
	stats["burst_events_7d"] = burstEvents
	stats["burst_notifications_7d"] = totalBurstSent

	// Average response time stats
	avgQuery := `
		SELECT
			AVG(minutes_waiting) as avg_wait,
			MIN(minutes_waiting) as min_wait,
			MAX(minutes_waiting) as max_wait
		FROM notifications
		WHERE sent_at IS NOT NULL
		AND sent_at > datetime('now', '-7 days')
	`
	var avgWait, minWait, maxWait sql.NullFloat64
	err = db.QueryRow(avgQuery).Scan(&avgWait, &minWait, &maxWait)
	if err != nil {
		return nil, err
	}

	waitStats := make(map[string]interface{})
	if avgWait.Valid {
		waitStats["average_minutes"] = avgWait.Float64
	}
	if minWait.Valid {
		waitStats["minimum_minutes"] = minWait.Float64
	}
	if maxWait.Valid {
		waitStats["maximum_minutes"] = maxWait.Float64
	}
	stats["response_times_7d"] = waitStats

	return stats, nil
}
