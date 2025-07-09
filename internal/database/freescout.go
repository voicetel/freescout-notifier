package database

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/voicetel/freescout-notifier/internal/config"
	"github.com/voicetel/freescout-notifier/internal/models"
)

func ConnectFreeScout(cfg config.FreeScoutConfig) (*sql.DB, error) {
	// Use the DSN directly
	db, err := sql.Open("mysql", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Set connection pool settings
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	return db, nil
}

// Rest of the file remains the same...
func GetOpenTicketsNeedingAttention(db *sql.DB, threshold time.Duration) ([]models.Ticket, error) {
	query := `
		SELECT DISTINCT
			c.id AS ticket_id,
			c.number AS ticket_number,
			c.subject,
			c.customer_email,
			CONCAT(COALESCE(cust.first_name, ''), ' ', COALESCE(cust.last_name, '')) AS customer_name,
			c.user_id AS assigned_user_id,
			CONCAT(COALESCE(u.first_name, ''), ' ', COALESCE(u.last_name, '')) AS assigned_user_name,
			c.last_reply_at,
			TIMESTAMPDIFF(MINUTE, c.last_reply_at, NOW()) AS minutes_since_reply,
			c.mailbox_id
		FROM conversations c
		LEFT JOIN customers cust ON c.customer_id = cust.id
		LEFT JOIN users u ON c.user_id = u.id
		WHERE c.status = 1  -- Active/Open status
			AND c.state = 1  -- Published state
			AND c.last_reply_from = 1  -- Last reply was from customer
			AND c.last_reply_at < DATE_SUB(NOW(), INTERVAL ? MINUTE)
			AND c.last_reply_at > DATE_SUB(NOW(), INTERVAL 7 DAY)  -- Limit to recent tickets
		ORDER BY c.last_reply_at ASC
	`

	rows, err := db.Query(query, int(threshold.Minutes()))
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	return scanTickets(rows, models.OpenNoAgentResponse)
}

func GetPendingTicketsNeedingAttention(db *sql.DB, threshold time.Duration) ([]models.Ticket, error) {
	query := `
		SELECT DISTINCT
			c.id AS ticket_id,
			c.number AS ticket_number,
			c.subject,
			c.customer_email,
			CONCAT(COALESCE(cust.first_name, ''), ' ', COALESCE(cust.last_name, '')) AS customer_name,
			c.user_id AS assigned_user_id,
			CONCAT(COALESCE(u.first_name, ''), ' ', COALESCE(u.last_name, '')) AS assigned_user_name,
			c.last_reply_at,
			TIMESTAMPDIFF(MINUTE, c.last_reply_at, NOW()) AS minutes_since_reply,
			c.mailbox_id
		FROM conversations c
		LEFT JOIN customers cust ON c.customer_id = cust.id
		LEFT JOIN users u ON c.user_id = u.id
		WHERE c.status = 2  -- Pending status
			AND c.state = 1  -- Published state
			AND c.last_reply_from = 2  -- Last reply was from user/agent
			AND c.last_reply_at < DATE_SUB(NOW(), INTERVAL ? MINUTE)
			AND c.last_reply_at > DATE_SUB(NOW(), INTERVAL 30 DAY)  -- Limit to recent tickets
		ORDER BY c.last_reply_at ASC
	`

	rows, err := db.Query(query, int(threshold.Minutes()))
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	return scanTickets(rows, models.PendingNoCustomerResponse)
}

func scanTickets(rows *sql.Rows, notificationType models.NotificationType) ([]models.Ticket, error) {
	var tickets []models.Ticket

	for rows.Next() {
		var t models.Ticket
		t.NotificationType = notificationType

		err := rows.Scan(
			&t.ID,
			&t.Number,
			&t.Subject,
			&t.CustomerEmail,
			&t.CustomerName,
			&t.AssignedUserID,
			&t.AssignedUserName,
			&t.LastReplyAt,
			&t.MinutesSinceReply,
			&t.MailboxID,
		)
		if err != nil {
			return nil, fmt.Errorf("scan failed: %w", err)
		}

		tickets = append(tickets, t)
	}

	return tickets, rows.Err()
}
