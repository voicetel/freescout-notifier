package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/voicetel/freescout-notifier/internal/config"
	"github.com/voicetel/freescout-notifier/internal/models"
)

// FreeScoutDB wraps the connection to the FreeScout MySQL database.
type FreeScoutDB struct {
	db *sql.DB
	// hasLastCustomerReply indicates whether the conversations table has
	// the last_customer_reply_at column (added in newer FreeScout
	// versions). Older installations fall back to last_reply_at.
	hasLastCustomerReply bool
}

// ConnectFreeScout opens, verifies and introspects the FreeScout database.
func ConnectFreeScout(ctx context.Context, cfg config.FreeScoutConfig) (*FreeScoutDB, error) {
	db, err := sql.Open("mysql", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	f := &FreeScoutDB{db: db}
	if err := f.detectSchema(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return f, nil
}

// detectSchema checks for optional columns so queries work across FreeScout
// versions.
func (f *FreeScoutDB) detectSchema(ctx context.Context) error {
	var count int
	err := f.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE()
			AND TABLE_NAME = 'conversations'
			AND COLUMN_NAME = 'last_customer_reply_at'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("schema detection failed: %w", err)
	}
	f.hasLastCustomerReply = count > 0
	return nil
}

// HasLastCustomerReply reports whether the connected FreeScout version tracks
// the last customer reply timestamp separately.
func (f *FreeScoutDB) HasLastCustomerReply() bool { return f.hasLastCustomerReply }

// Close closes the underlying connection pool.
func (f *FreeScoutDB) Close() error { return f.db.Close() }

// Ping verifies the connection is still alive.
func (f *FreeScoutDB) Ping(ctx context.Context) error { return f.db.PingContext(ctx) }

// ticketColumns is the shared SELECT list for conversation queries. The
// column order must match scanTicket; every query appends waiting_since and
// minutes_waiting as the last two columns.
const ticketColumns = `
	c.id,
	c.number,
	c.subject,
	COALESCE(c.customer_email, '') AS customer_email,
	TRIM(CONCAT(COALESCE(cust.first_name, ''), ' ', COALESCE(cust.last_name, ''))) AS customer_name,
	TRIM(CONCAT(COALESCE(u.first_name, ''), ' ', COALESCE(u.last_name, ''))) AS assigned_user_name,
	c.mailbox_id,
	COALESCE(m.name, '') AS mailbox_name,
	c.status,
	c.created_at`

const ticketJoins = `
	FROM conversations c
	LEFT JOIN customers cust ON c.customer_id = cust.id
	LEFT JOIN users u ON c.user_id = u.id
	LEFT JOIN mailboxes m ON c.mailbox_id = m.id`

// FetchNewConversations returns published, non-spam conversations created
// within the lookback window, oldest first.
func (f *FreeScoutDB) FetchNewConversations(ctx context.Context, lookback time.Duration) ([]models.Ticket, error) {
	query := fmt.Sprintf(`
		SELECT %s,
			c.created_at AS waiting_since,
			TIMESTAMPDIFF(MINUTE, c.created_at, NOW()) AS minutes_waiting
		%s
		WHERE c.state = %d
			AND c.status != %d
			AND c.created_at >= DATE_SUB(NOW(), INTERVAL ? MINUTE)
		ORDER BY c.id ASC`,
		ticketColumns, ticketJoins, models.TicketStatePublished, models.TicketStatusSpam)

	rows, err := f.db.QueryContext(ctx, query, windowMinutes(lookback))
	if err != nil {
		return nil, fmt.Errorf("new conversations query failed: %w", err)
	}
	return scanTickets(rows)
}

// FetchCustomerReplies returns published customer messages posted within the
// lookback window, excluding the first message of each conversation (that is
// the new-ticket event). Oldest first.
func (f *FreeScoutDB) FetchCustomerReplies(ctx context.Context, lookback time.Duration) ([]models.CustomerReplyEvent, error) {
	query := fmt.Sprintf(`
		SELECT t.id AS thread_id,
			%s,
			t.created_at AS waiting_since,
			TIMESTAMPDIFF(MINUTE, t.created_at, NOW()) AS minutes_waiting
		FROM threads t
		JOIN conversations c ON c.id = t.conversation_id
		LEFT JOIN customers cust ON c.customer_id = cust.id
		LEFT JOIN users u ON c.user_id = u.id
		LEFT JOIN mailboxes m ON c.mailbox_id = m.id
		WHERE t.type = 1            -- customer message
			AND t.state = 2           -- published
			AND t.created_at >= DATE_SUB(NOW(), INTERVAL ? MINUTE)
			AND c.state = %d
			AND c.status != %d
			AND t.id <> (SELECT MIN(t2.id) FROM threads t2 WHERE t2.conversation_id = c.id)
		ORDER BY t.id ASC`,
		ticketColumns, models.TicketStatePublished, models.TicketStatusSpam)

	rows, err := f.db.QueryContext(ctx, query, windowMinutes(lookback))
	if err != nil {
		return nil, fmt.Errorf("customer replies query failed: %w", err)
	}
	defer rows.Close()

	var replies []models.CustomerReplyEvent
	for rows.Next() {
		var (
			r             models.CustomerReplyEvent
			subject       sql.NullString
			customerEmail sql.NullString
			customerName  sql.NullString
			assignedUser  sql.NullString
			mailboxName   sql.NullString
		)
		args := append([]any{&r.ThreadID},
			ticketScanArgs(&r.Ticket, &subject, &customerEmail, &customerName, &assignedUser, &mailboxName)...)
		if err := rows.Scan(args...); err != nil {
			return nil, fmt.Errorf("scan failed: %w", err)
		}
		applyTicketStrings(&r.Ticket, subject, customerEmail, customerName, assignedUser, mailboxName)
		replies = append(replies, r)
	}
	return replies, rows.Err()
}

// FetchUnansweredActive returns active tickets whose last word came from the
// customer (or that never received any reply) and that have been waiting for
// at least the given threshold. Longest wait first.
func (f *FreeScoutDB) FetchUnansweredActive(ctx context.Context, threshold time.Duration) ([]models.Ticket, error) {
	wait := f.unansweredWaitingExpr()
	query := fmt.Sprintf(`
		SELECT %s,
			%s AS waiting_since,
			TIMESTAMPDIFF(MINUTE, %s, NOW()) AS minutes_waiting
		%s
		WHERE c.status = %d
			AND c.state = %d
			AND (c.last_reply_from = 1 OR c.last_reply_from IS NULL)
			AND %s < DATE_SUB(NOW(), INTERVAL ? MINUTE)
			AND %s > DATE_SUB(NOW(), INTERVAL 30 DAY)
		ORDER BY waiting_since ASC`,
		ticketColumns, wait, wait, ticketJoins,
		models.TicketStatusActive, models.TicketStatePublished, wait, wait)

	rows, err := f.db.QueryContext(ctx, query, windowMinutes(threshold))
	if err != nil {
		return nil, fmt.Errorf("unanswered active query failed: %w", err)
	}
	return scanTickets(rows)
}

// FetchAgingPending returns pending tickets that have been waiting for the
// customer for at least the given threshold. Longest wait first.
func (f *FreeScoutDB) FetchAgingPending(ctx context.Context, threshold time.Duration) ([]models.Ticket, error) {
	const wait = "COALESCE(c.last_reply_at, c.updated_at, c.created_at)"
	query := fmt.Sprintf(`
		SELECT %s,
			%s AS waiting_since,
			TIMESTAMPDIFF(MINUTE, %s, NOW()) AS minutes_waiting
		%s
		WHERE c.status = %d
			AND c.state = %d
			AND %s < DATE_SUB(NOW(), INTERVAL ? MINUTE)
			AND %s > DATE_SUB(NOW(), INTERVAL 90 DAY)
		ORDER BY waiting_since ASC`,
		ticketColumns, wait, wait, ticketJoins,
		models.TicketStatusPending, models.TicketStatePublished, wait, wait)

	rows, err := f.db.QueryContext(ctx, query, windowMinutes(threshold))
	if err != nil {
		return nil, fmt.Errorf("aging pending query failed: %w", err)
	}
	return scanTickets(rows)
}

// unansweredWaitingExpr is the timestamp an active ticket has been waiting
// since: the last customer reply when the column exists (it is immune to the
// "waiting since = first unanswered message" FreeScout setting), otherwise
// the last reply or creation time.
func (f *FreeScoutDB) unansweredWaitingExpr() string {
	if f.hasLastCustomerReply {
		return "COALESCE(c.last_customer_reply_at, c.last_reply_at, c.created_at)"
	}
	return "COALESCE(c.last_reply_at, c.created_at)"
}

func windowMinutes(d time.Duration) int {
	m := int(d.Minutes())
	if m < 1 {
		return 1
	}
	return m
}

func scanTickets(rows *sql.Rows) ([]models.Ticket, error) {
	defer rows.Close()

	var tickets []models.Ticket
	for rows.Next() {
		var (
			t             models.Ticket
			subject       sql.NullString
			customerEmail sql.NullString
			customerName  sql.NullString
			assignedUser  sql.NullString
			mailboxName   sql.NullString
		)
		args := ticketScanArgs(&t, &subject, &customerEmail, &customerName, &assignedUser, &mailboxName)
		if err := rows.Scan(args...); err != nil {
			return nil, fmt.Errorf("scan failed: %w", err)
		}
		applyTicketStrings(&t, subject, customerEmail, customerName, assignedUser, mailboxName)
		tickets = append(tickets, t)
	}
	return tickets, rows.Err()
}

// ticketScanArgs returns the scan destinations for the shared ticket column
// list plus the trailing waiting_since and minutes_waiting columns.
func ticketScanArgs(t *models.Ticket, subject, customerEmail, customerName, assignedUser, mailboxName *sql.NullString) []any {
	return []any{
		&t.ID,
		&t.Number,
		subject,
		customerEmail,
		customerName,
		assignedUser,
		&t.MailboxID,
		mailboxName,
		&t.Status,
		&t.CreatedAt,
		&t.WaitingSince,
		&t.MinutesWaiting,
	}
}

func applyTicketStrings(t *models.Ticket, subject, customerEmail, customerName, assignedUser, mailboxName sql.NullString) {
	t.Subject = subject.String
	t.CustomerEmail = customerEmail.String
	t.CustomerName = customerName.String
	t.AssignedUserName = assignedUser.String
	t.MailboxName = mailboxName.String
}
