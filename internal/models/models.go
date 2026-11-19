// Package models contains the shared data types used across the notifier.
package models

import (
	"strconv"
	"strings"
	"time"
)

// FreeScout conversation statuses (conversations.status).
const (
	TicketStatusActive  = 1
	TicketStatusPending = 2
	TicketStatusClosed  = 3
	TicketStatusSpam    = 4
)

// FreeScout conversation states (conversations.state).
const (
	TicketStateDraft     = 1
	TicketStatePublished = 2
	TicketStateDeleted   = 3
)

// Ticket is a denormalized view of a FreeScout conversation.
type Ticket struct {
	ID               int       `json:"id"`
	Number           int       `json:"number"`
	Subject          string    `json:"subject"`
	CustomerEmail    string    `json:"customer_email,omitempty"`
	CustomerName     string    `json:"customer_name,omitempty"`
	AssignedUserName string    `json:"assigned_user_name,omitempty"`
	MailboxID        int       `json:"mailbox_id"`
	MailboxName      string    `json:"mailbox_name,omitempty"`
	Status           int       `json:"status"`
	CreatedAt        time.Time `json:"created_at"`
	// WaitingSince is the timestamp the current wait started at: the
	// conversation creation for new tickets, the message time for customer
	// replies, and the last relevant activity for the aging checks.
	WaitingSince   time.Time `json:"waiting_since"`
	MinutesWaiting int       `json:"minutes_waiting"`
}

// URL returns the absolute URL of the conversation in FreeScout.
func (t Ticket) URL(baseURL string) string {
	return strings.TrimRight(baseURL, "/") + "/conversation/" + strconv.Itoa(t.ID)
}

// DisplayCustomer renders the best available customer identity.
func (t Ticket) DisplayCustomer() string {
	switch {
	case t.CustomerName != "" && t.CustomerEmail != "":
		return t.CustomerName + " (" + t.CustomerEmail + ")"
	case t.CustomerName != "":
		return t.CustomerName
	case t.CustomerEmail != "":
		return t.CustomerEmail
	default:
		return "Unknown"
	}
}

// DisplayAssignee renders the assigned agent or a placeholder.
func (t Ticket) DisplayAssignee() string {
	if strings.TrimSpace(t.AssignedUserName) == "" {
		return "Unassigned"
	}
	return t.AssignedUserName
}

// DisplayMailbox renders the mailbox name, falling back to its ID.
func (t Ticket) DisplayMailbox() string {
	if t.MailboxName != "" {
		return t.MailboxName
	}
	return strconv.Itoa(t.MailboxID)
}

// NotificationType identifies the kind of alert being sent.
type NotificationType string

const (
	// NewTicket fires when a brand-new conversation appears.
	NewTicket NotificationType = "new_ticket"
	// CustomerReply fires when a customer replies to an existing conversation.
	CustomerReply NotificationType = "customer_reply"
	// UnansweredActive fires when an active ticket waits too long for an agent.
	UnansweredActive NotificationType = "unanswered_active"
	// AgingPending fires when a pending ticket waits too long for the customer.
	AgingPending NotificationType = "aging_pending"
)

func (n NotificationType) String() string { return string(n) }

// IsReminderCapable reports whether alerts of this type repeat while the
// underlying condition persists.
func (n NotificationType) IsReminderCapable() bool {
	return n == UnansweredActive || n == AgingPending
}

// Alert is a fully-rendered notification ready to be sent or queued.
type Alert struct {
	Type   NotificationType `json:"type"`
	Ticket Ticket           `json:"ticket"`
	// ReminderNumber is 1 for the first alert and increments for every
	// reminder sent while the condition persists.
	ReminderNumber int `json:"reminder_number"`
}

// CustomerReplyEvent pairs a customer thread with its conversation.
type CustomerReplyEvent struct {
	ThreadID int    `json:"thread_id"`
	Ticket   Ticket `json:"ticket"`
}

// AlertState is the reminder bookkeeping for one ticket + alert type.
type AlertState struct {
	AlertCount  int
	LastAlertAt *time.Time
}

// Notification log statuses.
const (
	LogSent   = "sent"
	LogQueued = "queued"
	LogFailed = "failed"
)

// CycleStats summarises one monitoring cycle.
type CycleStats struct {
	NewTickets      int
	CustomerReplies int
	Sent            int
	Queued          int
	Reminders       int
	Skipped         int
	Errors          int
	Duration        time.Duration
}

// IsEmpty reports whether nothing noteworthy happened during the cycle.
func (c *CycleStats) IsEmpty() bool {
	return c.NewTickets == 0 && c.CustomerReplies == 0 && c.Sent == 0 &&
		c.Queued == 0 && c.Reminders == 0 && c.Skipped == 0 && c.Errors == 0
}

// Merge adds the counters of another CycleStats into this one.
func (c *CycleStats) Merge(o *CycleStats) {
	c.NewTickets += o.NewTickets
	c.CustomerReplies += o.CustomerReplies
	c.Sent += o.Sent
	c.Queued += o.Queued
	c.Reminders += o.Reminders
	c.Skipped += o.Skipped
	c.Errors += o.Errors
}

// Stats is the persisted notification statistics snapshot.
type Stats struct {
	TotalSent       int
	SentLast24h     int
	FailedLast24h   int
	QueuedNow       int
	ByType          map[string]int
	ActiveReminders map[string]int
}
