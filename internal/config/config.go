// Package config handles command line flags, JSON configuration files and
// validation for the notifier.
package config

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// Config holds all runtime configuration.
type Config struct {
	// Local state database (SQLite)
	DBPath string `json:"db_path"`

	// Integrations
	FreeScout FreeScoutConfig `json:"freescout"`
	Slack     SlackConfig     `json:"slack"`

	// Scheduling
	PollInterval   Duration `json:"poll_interval"`   // how often to look for new tickets/replies
	CheckInterval  Duration `json:"check_interval"`  // how often to run the aging sweeps
	LookbackWindow Duration `json:"lookback_window"` // how far back to scan for new events

	// Notification rules
	UnansweredThreshold Duration `json:"unanswered_threshold"` // alert when an active ticket waits this long for an agent
	PendingThreshold    Duration `json:"pending_threshold"`    // alert when a pending ticket ages this long
	ReminderInterval    Duration `json:"reminder_interval"`    // re-alert frequency while a condition persists
	MaxNotifications    int      `json:"max_notifications"`    // delivery cap per cycle

	// Business hours
	BusinessHours BusinessHoursConfig `json:"business_hours"`

	// Mobile push (FCM)
	Push PushConfig `json:"push"`

	// Maintenance
	RetentionDays int  `json:"retention_days"`
	AutoVacuum    bool `json:"auto_vacuum"`

	// Operational
	DryRun    bool   `json:"dry_run"`
	Verbose   bool   `json:"verbose"`
	LogFormat string `json:"log_format"`

	// CLI-only modes
	Once             bool `json:"-"`
	CheckConnections bool `json:"-"`
	InitDB           bool `json:"-"`
	StatsOnly        bool `json:"-"`
	Cleanup          bool `json:"-"`
	TestPush         bool `json:"-"`
	ShowVersion      bool `json:"-"`
}

// FreeScoutConfig describes how to reach the FreeScout installation.
type FreeScoutConfig struct {
	DSN string `json:"dsn"` // MySQL DSN; parseTime=true is appended automatically when missing
	URL string `json:"url"` // base URL used for ticket links
}

// SlackConfig describes the incoming webhook used for delivery.
type SlackConfig struct {
	WebhookURL    string   `json:"webhook_url"`
	Timeout       Duration `json:"timeout"`
	RetryAttempts int      `json:"retry_attempts"`
}

// BusinessHoursConfig controls when notifications are delivered directly
// versus queued for the start of the business day.
type BusinessHoursConfig struct {
	Enabled      bool           `json:"enabled"`
	StartHour    int            `json:"start_hour"`
	EndHour      int            `json:"end_hour"`
	Timezone     string         `json:"timezone"`
	WorkDays     []time.Weekday `json:"work_days"`
	NotifyOnOpen bool           `json:"notify_on_open"` // flush queued notifications while business hours are open
	HolidaysFile string         `json:"holidays_file"`
}

// PushConfig controls Firebase Cloud Messaging delivery to the VoiceTel Desk
// mobile app (our own Firebase project; no third-party relay).
type PushConfig struct {
	Enabled         bool     `json:"enabled"`
	CredentialsFile string   `json:"credentials_file"` // Firebase service-account key JSON
	Topic           string   `json:"topic"`            // FCM topic the app subscribes to (default "desk-alerts")
	AlertTypes      []string `json:"alert_types"`      // which alert types to push (default: all)
}

// ParseFlags builds the configuration from CLI flags, optionally overridden
// by a JSON configuration file.
func ParseFlags() *Config {
	cfg := &Config{}

	configFile := flag.String("config-file", "", "Path to JSON configuration file")

	flag.BoolVar(&cfg.ShowVersion, "version", false, "Show version information and exit")

	// State database
	flag.StringVar(&cfg.DBPath, "db-path", "./notifications.db", "Path to the SQLite state database")

	// FreeScout
	flag.StringVar(&cfg.FreeScout.DSN, "freescout-dsn", "", "FreeScout MySQL DSN, e.g. 'user:pass@tcp(localhost:3306)/freescout?parseTime=true' (required)")
	flag.StringVar(&cfg.FreeScout.URL, "freescout-url", "", "FreeScout base URL for ticket links (required)")

	// Slack
	flag.StringVar(&cfg.Slack.WebhookURL, "slack-webhook", "", "Slack incoming webhook URL (required unless --dry-run)")
	var slackTimeout time.Duration
	flag.DurationVar(&slackTimeout, "slack-timeout", 10*time.Second, "Slack request timeout")
	flag.IntVar(&cfg.Slack.RetryAttempts, "slack-retry-attempts", 3, "Slack delivery attempts per message")

	// Scheduling
	var pollInterval, checkInterval, lookbackWindow time.Duration
	flag.DurationVar(&pollInterval, "poll-interval", 30*time.Second, "How often to poll for new tickets and customer replies")
	flag.DurationVar(&checkInterval, "check-interval", 5*time.Minute, "How often to run the unanswered/aging sweeps")
	flag.DurationVar(&lookbackWindow, "lookback-window", 2*time.Hour, "How far back to scan for new events (covers restarts and downtime)")

	// Notification rules
	var unansweredThreshold, pendingThreshold, reminderInterval time.Duration
	flag.DurationVar(&unansweredThreshold, "unanswered-threshold", 2*time.Hour, "Wait time before alerting about an active ticket with no agent reply")
	flag.DurationVar(&pendingThreshold, "pending-threshold", 24*time.Hour, "Age before alerting about a pending ticket")
	flag.DurationVar(&reminderInterval, "reminder-interval", 4*time.Hour, "How often to repeat an alert while its condition persists")
	flag.IntVar(&cfg.MaxNotifications, "max-notifications-per-run", 50, "Maximum notifications delivered per cycle")

	// Business hours
	flag.BoolVar(&cfg.BusinessHours.Enabled, "business-hours-enabled", true, "Queue notifications outside business hours")
	flag.IntVar(&cfg.BusinessHours.StartHour, "business-hours-start", 9, "Business hours start (0-23)")
	flag.IntVar(&cfg.BusinessHours.EndHour, "business-hours-end", 17, "Business hours end (0-23)")
	flag.StringVar(&cfg.BusinessHours.Timezone, "business-hours-timezone", "America/Chicago", "Business hours timezone")
	workDaysStr := flag.String("business-hours-days", "1,2,3,4,5", "Business days (1=Mon ... 7=Sun)")
	flag.BoolVar(&cfg.BusinessHours.NotifyOnOpen, "notify-on-hours-start", true, "Flush queued notifications while business hours are open")
	flag.StringVar(&cfg.BusinessHours.HolidaysFile, "holidays-file", "", "Path to holidays JSON file")

	// Maintenance
	flag.IntVar(&cfg.RetentionDays, "retention-days", 90, "Days to retain notification history and dedup state")
	flag.BoolVar(&cfg.AutoVacuum, "auto-vacuum", true, "Vacuum the SQLite database during --cleanup")

	// Operational
	flag.BoolVar(&cfg.Once, "once", false, "Run a single monitoring pass and exit (for cron)")
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "Do everything except actually send Slack messages")
	flag.BoolVar(&cfg.Verbose, "verbose", false, "Enable verbose logging")
	flag.StringVar(&cfg.LogFormat, "log-format", "text", "Log format: text or json")
	flag.BoolVar(&cfg.CheckConnections, "check-connections", false, "Test FreeScout and Slack connections and exit")
	flag.BoolVar(&cfg.InitDB, "init-db", false, "Initialize the state database and exit")
	flag.BoolVar(&cfg.StatsOnly, "stats-only", false, "Print statistics and exit")
	flag.BoolVar(&cfg.Cleanup, "cleanup", false, "Delete state older than --retention-days and exit")
	flag.BoolVar(&cfg.TestPush, "test-push", false, "Send a test push notification to the configured FCM topic and exit")

	flag.Parse()

	// Snapshot the flag-derived values so that flags explicitly set on the
	// command line can be re-applied after the config file is loaded.
	flagCfg := *cfg
	var explicitFlags []string
	flag.Visit(func(f *flag.Flag) { explicitFlags = append(explicitFlags, f.Name) })

	cfg.Slack.Timeout = Duration{Duration: slackTimeout}
	cfg.PollInterval = Duration{Duration: pollInterval}
	cfg.CheckInterval = Duration{Duration: checkInterval}
	cfg.LookbackWindow = Duration{Duration: lookbackWindow}
	cfg.UnansweredThreshold = Duration{Duration: unansweredThreshold}
	cfg.PendingThreshold = Duration{Duration: pendingThreshold}
	cfg.ReminderInterval = Duration{Duration: reminderInterval}

	// A config file provides base values; flags explicitly set on the command
	// line always win over the file.
	if *configFile != "" {
		if err := cfg.LoadFromFile(*configFile); err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config file: %v\n", err)
			os.Exit(1)
		}
		for _, name := range explicitFlags {
			switch name {
			case "db-path":
				cfg.DBPath = flagCfg.DBPath
			case "freescout-dsn":
				cfg.FreeScout.DSN = flagCfg.FreeScout.DSN
			case "freescout-url":
				cfg.FreeScout.URL = flagCfg.FreeScout.URL
			case "slack-webhook":
				cfg.Slack.WebhookURL = flagCfg.Slack.WebhookURL
			case "slack-timeout":
				cfg.Slack.Timeout = Duration{Duration: slackTimeout}
			case "slack-retry-attempts":
				cfg.Slack.RetryAttempts = flagCfg.Slack.RetryAttempts
			case "poll-interval":
				cfg.PollInterval = Duration{Duration: pollInterval}
			case "check-interval":
				cfg.CheckInterval = Duration{Duration: checkInterval}
			case "lookback-window":
				cfg.LookbackWindow = Duration{Duration: lookbackWindow}
			case "unanswered-threshold":
				cfg.UnansweredThreshold = Duration{Duration: unansweredThreshold}
			case "pending-threshold":
				cfg.PendingThreshold = Duration{Duration: pendingThreshold}
			case "reminder-interval":
				cfg.ReminderInterval = Duration{Duration: reminderInterval}
			case "max-notifications-per-run":
				cfg.MaxNotifications = flagCfg.MaxNotifications
			case "business-hours-enabled":
				cfg.BusinessHours.Enabled = flagCfg.BusinessHours.Enabled
			case "business-hours-start":
				cfg.BusinessHours.StartHour = flagCfg.BusinessHours.StartHour
			case "business-hours-end":
				cfg.BusinessHours.EndHour = flagCfg.BusinessHours.EndHour
			case "business-hours-timezone":
				cfg.BusinessHours.Timezone = flagCfg.BusinessHours.Timezone
			case "business-hours-days":
				cfg.BusinessHours.WorkDays = parseWorkDays(*workDaysStr)
			case "notify-on-hours-start":
				cfg.BusinessHours.NotifyOnOpen = flagCfg.BusinessHours.NotifyOnOpen
			case "holidays-file":
				cfg.BusinessHours.HolidaysFile = flagCfg.BusinessHours.HolidaysFile
			case "retention-days":
				cfg.RetentionDays = flagCfg.RetentionDays
			case "auto-vacuum":
				cfg.AutoVacuum = flagCfg.AutoVacuum
			case "dry-run":
				cfg.DryRun = flagCfg.DryRun
			case "verbose":
				cfg.Verbose = flagCfg.Verbose
			case "log-format":
				cfg.LogFormat = flagCfg.LogFormat
			}
		}
	}

	// Work days can only be expressed via flag string or JSON array.
	if len(cfg.BusinessHours.WorkDays) == 0 {
		cfg.BusinessHours.WorkDays = parseWorkDays(*workDaysStr)
	}

	return cfg
}

// LoadFromFile merges a JSON configuration file into the config.
func (c *Config) LoadFromFile(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}
	if err := json.Unmarshal(data, c); err != nil {
		return fmt.Errorf("failed to parse config file: %w", err)
	}
	return nil
}

// SaveToFile writes the configuration as indented JSON.
func (c *Config) SaveToFile(filename string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	if err := os.WriteFile(filename, data, 0o644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	return nil
}

// Validate normalizes defaults and verifies the configuration is usable.
func (c *Config) Validate() error {
	// Normalize values that may be missing from a partial config file.
	if c.DBPath == "" {
		c.DBPath = "./notifications.db"
	}
	if c.Slack.Timeout.Duration <= 0 {
		c.Slack.Timeout = Duration{Duration: 10 * time.Second}
	}
	if c.Slack.RetryAttempts <= 0 {
		c.Slack.RetryAttempts = 3
	}
	if c.MaxNotifications <= 0 {
		c.MaxNotifications = 50
	}
	if c.RetentionDays <= 0 {
		c.RetentionDays = 90
	}
	if c.LogFormat == "" {
		c.LogFormat = "text"
	}
	if c.LogFormat != "text" && c.LogFormat != "json" {
		return fmt.Errorf("log-format must be 'text' or 'json'")
	}
	c.FreeScout.DSN = normalizeDSN(c.FreeScout.DSN)
	c.FreeScout.URL = strings.TrimRight(strings.TrimSpace(c.FreeScout.URL), "/")

	// Required fields. Utility modes only touch the local state store and
	// need no FreeScout or Slack configuration.
	utilityMode := c.InitDB || c.StatsOnly || c.Cleanup || c.TestPush
	if !utilityMode {
		if c.FreeScout.DSN == "" {
			return fmt.Errorf("freescout DSN is required (--freescout-dsn or config file)")
		}
		if err := c.validateDSN(); err != nil {
			return fmt.Errorf("invalid DSN: %w", err)
		}
		if c.FreeScout.URL == "" {
			return fmt.Errorf("freescout URL is required (--freescout-url or config file)")
		}
		if !c.DryRun && !c.CheckConnections && c.Slack.WebhookURL == "" {
			return fmt.Errorf("slack webhook is required (--slack-webhook or config file)")
		}
	}

	// Scheduling sanity.
	if c.PollInterval.Duration < 5*time.Second {
		return fmt.Errorf("poll-interval must be at least 5s")
	}
	if c.CheckInterval.Duration < 30*time.Second {
		return fmt.Errorf("check-interval must be at least 30s")
	}
	if c.LookbackWindow.Duration < 5*time.Minute {
		return fmt.Errorf("lookback-window must be at least 5m")
	}
	if c.LookbackWindow.Duration < 2*c.PollInterval.Duration {
		return fmt.Errorf("lookback-window must be at least twice the poll-interval")
	}

	// Notification rules sanity.
	if c.UnansweredThreshold.Duration <= 0 {
		return fmt.Errorf("unanswered-threshold must be positive")
	}
	if c.PendingThreshold.Duration <= 0 {
		return fmt.Errorf("pending-threshold must be positive")
	}
	if c.ReminderInterval.Duration <= 0 {
		return fmt.Errorf("reminder-interval must be positive")
	}

	// Business hours sanity.
	if c.BusinessHours.StartHour < 0 || c.BusinessHours.StartHour > 23 {
		return fmt.Errorf("business-hours-start must be 0-23")
	}
	if c.BusinessHours.EndHour < 0 || c.BusinessHours.EndHour > 23 {
		return fmt.Errorf("business-hours-end must be 0-23")
	}
	if c.BusinessHours.StartHour >= c.BusinessHours.EndHour {
		return fmt.Errorf("business-hours-start must be before business-hours-end")
	}
	if len(c.BusinessHours.WorkDays) == 0 {
		return fmt.Errorf("business-hours work days must not be empty")
	}

	// Mobile push (FCM).
	if c.TestPush && !c.Push.Enabled {
		return fmt.Errorf("--test-push requires push notifications to be enabled in the config")
	}
	if c.Push.Enabled {
		if c.Push.CredentialsFile == "" {
			return fmt.Errorf("push.credentials_file is required when push is enabled")
		}
		if _, err := os.Stat(c.Push.CredentialsFile); err != nil {
			return fmt.Errorf("push credentials file not accessible: %w", err)
		}
		if c.Push.Topic == "" {
			c.Push.Topic = "desk-alerts"
		}
		if len(c.Push.AlertTypes) == 0 {
			c.Push.AlertTypes = []string{"new_ticket", "customer_reply", "unanswered_active", "aging_pending"}
		}
		for _, t := range c.Push.AlertTypes {
			switch t {
			case "new_ticket", "customer_reply", "unanswered_active", "aging_pending":
			default:
				return fmt.Errorf("unknown push alert type %q (want new_ticket, customer_reply, unanswered_active or aging_pending)", t)
			}
		}
	}

	return nil
}

// normalizeDSN makes sure the MySQL driver parses DATETIME columns into
// time.Time values.
func normalizeDSN(dsn string) string {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" || strings.Contains(dsn, "parseTime") {
		return dsn
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "parseTime=true"
}

// validateDSN performs basic validation on the MySQL DSN format.
func (c *Config) validateDSN() error {
	dsn := c.FreeScout.DSN

	if !strings.Contains(dsn, "@") || !strings.Contains(dsn, "/") {
		return fmt.Errorf("DSN must be in format 'user:password@tcp(host:port)/database?options'")
	}
	if strings.HasPrefix(dsn, "tcp://") {
		return fmt.Errorf("DSN should not include the 'tcp://' scheme, use format: 'user:password@tcp(host:port)/database'")
	}
	return nil
}

func parseWorkDays(s string) []time.Weekday {
	parts := strings.Split(s, ",")
	days := make([]time.Weekday, 0, len(parts))

	for _, p := range parts {
		p = strings.TrimSpace(p)
		switch p {
		case "1":
			days = append(days, time.Monday)
		case "2":
			days = append(days, time.Tuesday)
		case "3":
			days = append(days, time.Wednesday)
		case "4":
			days = append(days, time.Thursday)
		case "5":
			days = append(days, time.Friday)
		case "6":
			days = append(days, time.Saturday)
		case "7":
			days = append(days, time.Sunday)
		}
	}

	if len(days) == 0 {
		// Default to Monday-Friday when the flag is unusable.
		days = []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday}
	}
	return days
}
