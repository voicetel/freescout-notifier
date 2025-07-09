package notifier

import (
	"log"
	"time"

	"github.com/voicetel/freescout-notifier/internal/database"
)

// CleanupOldNotifications removes old notification records to prevent database bloat
func CleanupOldNotifications(db *database.DB, retentionDays int) error {
	if retentionDays <= 0 {
		retentionDays = 90 // Default to 90 days
	}

	query := `
		DELETE FROM notifications
		WHERE (sent_at IS NOT NULL AND sent_at < datetime('now', '-' || ? || ' days'))
		   OR (sent_at IS NULL AND first_eligible_at < datetime('now', '-' || ? || ' days'))
	`

	result, err := db.Exec(query, retentionDays, retentionDays)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err == nil && rowsAffected > 0 {
		log.Printf("Cleaned up %d old notification records", rowsAffected)
	}

	// Also cleanup old business hours log entries
	logQuery := `
		DELETE FROM business_hours_log
		WHERE event_time < datetime('now', '-' || ? || ' days')
	`

	result, err = db.Exec(logQuery, retentionDays)
	if err != nil {
		return err
	}

	rowsAffected, err = result.RowsAffected()
	if err == nil && rowsAffected > 0 {
		log.Printf("Cleaned up %d old business hours log entries", rowsAffected)
	}

	return nil
}

// VacuumDatabase performs SQLite VACUUM to reclaim disk space
func VacuumDatabase(db *database.DB) error {
	log.Printf("Performing database vacuum...")
	start := time.Now()

	_, err := db.Exec("VACUUM")
	if err != nil {
		return err
	}

	log.Printf("Database vacuum completed in %s", time.Since(start))
	return nil
}
