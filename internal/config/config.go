package config

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	// SQLite
	DBPath    string        `json:"db_path"`
	DBTimeout time.Duration `json:"db_timeout"`

	// FreeScout
	FreeScout FreeScoutConfig `json:"freescout"`

	// Slack
	Slack SlackConfig `json:"slack"`

	// Notification Rules
	OpenThreshold    time.Duration `json:"open_threshold"`
	PendingThreshold time.Duration `json:"pending_threshold"`
	CooldownPeriod   time.Duration `json:"cooldown_period"`
	MaxNotifications int           `json:"max_notifications"`

	// Business Hours
	BusinessHours BusinessHoursConfig `json:"business_hours"`

	// Cleanup
	RetentionDays int  `json:"retention_days"`
	AutoVacuum    bool `json:"auto_vacuum"`

	// Operational
	DryRun           bool   `json:"dry_run"`
	Verbose          bool   `json:"verbose"`
	LogFormat        string `json:"log_format"`
	Stats            bool   `json:"stats"`
	CheckConnections bool   `json:"-"`
	InitDB           bool   `json:"-"`
	StatsOnly        bool   `json:"-"`
	Cleanup          bool   `json:"-"`
}

type FreeScoutConfig struct {
	DSN     string        `json:"dsn"`     // Database connection string
	Timeout time.Duration `json:"timeout"` // Connection timeout
	URL     string        `json:"url"`     // Base URL for ticket links
}

type SlackConfig struct {
	WebhookURL    string        `json:"webhook_url"`
	Timeout       time.Duration `json:"timeout"`
	RetryAttempts int           `json:"retry_attempts"`
}

type BusinessHoursConfig struct {
	Enabled      bool           `json:"enabled"`
	StartHour    int            `json:"start_hour"`
	EndHour      int            `json:"end_hour"`
	Timezone     string         `json:"timezone"`
	WorkDays     []time.Weekday `json:"work_days"`
	NotifyOnOpen bool           `json:"notify_on_open"`
	HolidaysFile string         `json:"holidays_file"`
}

func ParseFlags() *Config {
	cfg := &Config{}

	// Config file flag
	configFile := flag.String("config-file", "", "Path to JSON configuration file")

	// SQLite flags
	flag.StringVar(&cfg.DBPath, "db-path", "./notifications.db", "Path to SQLite database")
	flag.DurationVar(&cfg.DBTimeout, "db-timeout", 5*time.Second, "SQLite timeout")

	// FreeScout flags - Use DSN instead of individual fields
	flag.StringVar(&cfg.FreeScout.DSN, "freescout-dsn", "user:password@tcp(localhost:3306)/freescout?parseTime=true&timeout=30s", "FreeScout database DSN (required)")
	flag.DurationVar(&cfg.FreeScout.Timeout, "freescout-timeout", 30*time.Second, "FreeScout connection timeout")
	flag.StringVar(&cfg.FreeScout.URL, "freescout-url", "https://support.example.com", "FreeScout base URL for ticket links (required)")

	// Slack flags
	flag.StringVar(&cfg.Slack.WebhookURL, "slack-webhook", "", "Slack webhook URL (required)")
	flag.DurationVar(&cfg.Slack.Timeout, "slack-timeout", 10*time.Second, "Slack request timeout")
	flag.IntVar(&cfg.Slack.RetryAttempts, "slack-retry-attempts", 3, "Slack retry attempts")

	// Notification rules
	flag.DurationVar(&cfg.OpenThreshold, "open-threshold", 2*time.Hour, "Time before notifying about open tickets")
	flag.DurationVar(&cfg.PendingThreshold, "pending-threshold", 24*time.Hour, "Time before notifying about pending tickets")
	flag.DurationVar(&cfg.CooldownPeriod, "cooldown-period", 4*time.Hour, "Cooldown between notifications for same ticket")
	flag.IntVar(&cfg.MaxNotifications, "max-notifications-per-run", 50, "Maximum notifications per run")

	// Business hours flags
	flag.BoolVar(&cfg.BusinessHours.Enabled, "business-hours-enabled", true, "Enable business hours restrictions")
	flag.IntVar(&cfg.BusinessHours.StartHour, "business-hours-start", 9, "Business hours start (0-23)")
	flag.IntVar(&cfg.BusinessHours.EndHour, "business-hours-end", 17, "Business hours end (0-23)")
	flag.StringVar(&cfg.BusinessHours.Timezone, "business-hours-timezone", "America/Chicago", "Business hours timezone")
	workDaysStr := flag.String("business-hours-days", "1,2,3,4,5", "Business days (1=Mon, 7=Sun)")
	flag.BoolVar(&cfg.BusinessHours.NotifyOnOpen, "notify-on-hours-start", true, "Send queued notifications when business hours start")
	flag.StringVar(&cfg.BusinessHours.HolidaysFile, "holidays-file", "", "Path to holidays JSON file")

	// Cleanup flags
	flag.IntVar(&cfg.RetentionDays, "retention-days", 90, "Days to retain notification history")
	flag.BoolVar(&cfg.AutoVacuum, "auto-vacuum", false, "Automatically vacuum database after cleanup")

	// Operational flags
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "Check tickets but don't send notifications")
	flag.BoolVar(&cfg.Verbose, "verbose", false, "Enable verbose logging")
	flag.StringVar(&cfg.LogFormat, "log-format", "text", "Log format (text or json)")
	flag.BoolVar(&cfg.Stats, "stats", false, "Print statistics at end")
	flag.BoolVar(&cfg.CheckConnections, "check-connections", false, "Test connections and exit")
	flag.BoolVar(&cfg.InitDB, "init-db", false, "Initialize database and exit")
	flag.BoolVar(&cfg.StatsOnly, "stats-only", false, "Print statistics and exit")
	flag.BoolVar(&cfg.Cleanup, "cleanup", false, "Clean up old records and exit")

	flag.Parse()

	// Load config file if specified
	if *configFile != "" {
		if err := cfg.LoadFromFile(*configFile); err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config file: %v\n", err)
			os.Exit(1)
		}
	}

	// Parse work days
	cfg.BusinessHours.WorkDays = parseWorkDays(*workDaysStr)

	return cfg
}

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

func (c *Config) SaveToFile(filename string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

func (c *Config) Validate() error {
	// Required fields
	if c.FreeScout.DSN == "" {
		return fmt.Errorf("--freescout-dsn is required")
	}

	// Validate DSN format
	if err := c.validateDSN(); err != nil {
		return fmt.Errorf("invalid DSN: %w", err)
	}

	if c.FreeScout.URL == "" {
		return fmt.Errorf("--freescout-url is required")
	}
	if c.Slack.WebhookURL == "" && !c.DryRun && !c.CheckConnections && !c.InitDB && !c.StatsOnly {
		return fmt.Errorf("--slack-webhook is required")
	}

	// Validate business hours
	if c.BusinessHours.StartHour < 0 || c.BusinessHours.StartHour > 23 {
		return fmt.Errorf("--business-hours-start must be 0-23")
	}
	if c.BusinessHours.EndHour < 0 || c.BusinessHours.EndHour > 23 {
		return fmt.Errorf("--business-hours-end must be 0-23")
	}
	if c.BusinessHours.StartHour >= c.BusinessHours.EndHour {
		return fmt.Errorf("--business-hours-start must be before --business-hours-end")
	}

	return nil
}

// validateDSN performs basic validation on the MySQL DSN format
func (c *Config) validateDSN() error {
	dsn := c.FreeScout.DSN

	// Basic format check: should contain @ and /
	if !strings.Contains(dsn, "@") || !strings.Contains(dsn, "/") {
		return fmt.Errorf("DSN must be in format 'user:password@tcp(host:port)/database?options'")
	}

	// Try to parse as URL to catch common formatting errors
	if strings.HasPrefix(dsn, "tcp://") {
		return fmt.Errorf("DSN should not include 'tcp://' scheme, use format: 'user:password@tcp(host:port)/database'")
	}

	return nil
}

// GetDSNInfo returns parsed information from the DSN for display purposes
func (c *Config) GetDSNInfo() map[string]string {
	info := make(map[string]string)
	dsn := c.FreeScout.DSN

	// Parse DSN components
	parts := strings.Split(dsn, "@")
	if len(parts) >= 2 {
		// Extract user (hide password)
		userPass := strings.Split(parts[0], ":")
		if len(userPass) >= 1 {
			info["user"] = userPass[0]
		}

		// Extract host/port/database
		remaining := parts[1]
		if strings.HasPrefix(remaining, "tcp(") {
			end := strings.Index(remaining, ")")
			if end > 4 {
				hostPort := remaining[4:end]
				info["host_port"] = hostPort

				// Extract host and port separately
				hostPortParts := strings.Split(hostPort, ":")
				if len(hostPortParts) >= 2 {
					info["host"] = hostPortParts[0]
					info["port"] = hostPortParts[1]
				}
			}

			// Extract database
			remaining = remaining[end+1:]
			if strings.HasPrefix(remaining, "/") {
				dbParts := strings.Split(remaining[1:], "?")
				if len(dbParts) >= 1 {
					info["database"] = dbParts[0]
				}
			}
		}
	}

	return info
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

	return days
}
