package models

import "time"

type Ticket struct {
	ID               int
	Number           int
	Subject          string
	CustomerEmail    string
	CustomerName     string
	AssignedUserID   *int
	AssignedUserName string
	LastReplyAt      time.Time
	MinutesSinceReply int
	MailboxID        int
	NotificationType NotificationType
}

type NotificationType string

const (
	OpenNoAgentResponse     NotificationType = "open_no_agent_response"
	PendingNoCustomerResponse NotificationType = "pending_no_customer_response"
)

type Notification struct {
	ID                 int
	TicketID           int
	NotificationType   NotificationType
	NotificationStatus NotificationStatus
	FirstEligibleAt    time.Time
	QueuedAt           *time.Time
	SentAt             *time.Time
	TicketSubject      string
	CustomerName       string
	AssignedUser       string
	MinutesWaiting     int
	ThresholdMinutes   int
	TicketData         string // JSON
}

type NotificationStatus string

const (
	StatusPending NotificationStatus = "pending"
	StatusQueued  NotificationStatus = "queued"
	StatusSent    NotificationStatus = "sent"
)

type RunStats struct {
	TicketsChecked      int
	NotificationsSent   int
	NotificationsQueued int
	Errors              int
	Duration            time.Duration
}
