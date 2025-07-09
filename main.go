package main

import (
	"fmt"
	"log"
	"os"

	"github.com/voicetel/freescout-notifier/internal/config"
	"github.com/voicetel/freescout-notifier/internal/database"
	"github.com/voicetel/freescout-notifier/internal/logging"
	"github.com/voicetel/freescout-notifier/internal/models"
	"github.com/voicetel/freescout-notifier/internal/notifier"
)

// Version information - these will be set at build time via ldflags
var (
	Version   = "dev"     // Version number
	GitCommit = "unknown" // Git commit hash
	BuildDate = "unknown" // Build date
	GoVersion = "unknown" // Go version used to build
)

func main() {
	// Parse command line flags
	cfg := config.ParseFlags()

	// Check for version flag before other validation
	if cfg.ShowVersion {
		printVersion()
		os.Exit(0)
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		log.Fatalf("Configuration error: %v", err)
	}

	// Set up logging
	logger := logging.NewLogger(cfg.LogFormat, cfg.Verbose, nil)
	logger.SetAsDefault()

	if cfg.Verbose {
		logger.Info("Starting FreeScout Notifier",
			"version", Version,
			"git_commit", GitCommit,
			"business_hours_enabled", cfg.BusinessHours.Enabled,
			"dry_run", cfg.DryRun,
		)
	}

	// Check connections mode
	if cfg.CheckConnections {
		if err := checkConnections(cfg, logger); err != nil {
			logger.LogError("Connection check failed", err)
			os.Exit(1)
		}
		fmt.Println("All connections successful!")
		os.Exit(0)
	}

	// Initialize SQLite database
	db, err := database.InitSQLite(cfg.DBPath)
	if err != nil {
		logger.LogError("Failed to initialize SQLite", err)
		os.Exit(1)
	}
	defer db.Close()

	// Initialize database schema if requested
	if cfg.InitDB {
		if err := database.InitSchema(db); err != nil {
			logger.LogError("Failed to initialize database schema", err)
			os.Exit(1)
		}
		fmt.Println("Database initialized successfully!")
		os.Exit(0)
	}

	// Cleanup mode
	if cfg.Cleanup {
		if err := performCleanup(db, cfg, logger); err != nil {
			logger.LogError("Failed to perform cleanup", err)
			os.Exit(1)
		}
		fmt.Println("Cleanup completed successfully!")
		os.Exit(0)
	}

	// Stats only mode
	if cfg.StatsOnly {
		if err := printStats(db, logger); err != nil {
			logger.LogError("Failed to print stats", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Initialize FreeScout connection
	fsDB, err := database.ConnectFreeScout(cfg.FreeScout)
	if err != nil {
		logger.LogError("Failed to connect to FreeScout", err)
		os.Exit(1)
	}
	defer fsDB.Close()

	// Create notifier
	n := notifier.New(fsDB, db, cfg)

	// Run notification check
	stats, err := n.Run()
	if err != nil {
		logger.LogError("Notification run failed", err)
		os.Exit(1)
	}

	// Print statistics if requested
	if cfg.Stats || cfg.Verbose {
		printRunStats(stats, logger)
	}
}

func printVersion() {
	fmt.Printf("FreeScout Notifier\n")
	fmt.Printf("Version:    %s\n", Version)
	fmt.Printf("Git Commit: %s\n", GitCommit)
	fmt.Printf("Build Date: %s\n", BuildDate)
	fmt.Printf("Go Version: %s\n", GoVersion)
}

func checkConnections(cfg *config.Config, logger *logging.Logger) error {
	logger.Info("Checking connections...")

	// Check FreeScout database
	logger.Info("Testing FreeScout database connection...")
	fsDB, err := database.ConnectFreeScout(cfg.FreeScout)
	if err != nil {
		return fmt.Errorf("FreeScout connection failed: %w", err)
	}
	fsDB.Close()
	logger.Info("FreeScout database connection successful")

	// Check Slack webhook
	if cfg.Slack.WebhookURL != "" {
		logger.Info("Testing Slack webhook...")
		if err := notifier.TestSlackWebhook(cfg.Slack.WebhookURL); err != nil {
			return fmt.Errorf("Slack webhook test failed: %w", err)
		}
		logger.Info("Slack webhook test successful")
	}

	return nil
}

func printStats(db *database.DB, logger *logging.Logger) error {
	stats, err := db.GetNotificationStats()
	if err != nil {
		return fmt.Errorf("failed to get statistics: %w", err)
	}

	// Always use human-readable format for --stats-only
	printHumanReadableStats(stats)
	return nil
}

func printHumanReadableStats(stats map[string]interface{}) {
	fmt.Printf("\n=== FreeScout Notifier Statistics ===\n\n")

	// Total notifications
	if total, ok := stats["total_notifications"].(int); ok {
		fmt.Printf("Total Notifications: %d\n\n", total)
	}

	// By status
	if statusMap, ok := stats["by_status"].(map[string]int); ok {
		fmt.Printf("By Status:\n")
		for status, count := range statusMap {
			fmt.Printf("  %s: %d\n", status, count)
		}
		fmt.Println()
	}

	// By type
	if typeMap, ok := stats["by_type"].(map[string]int); ok {
		fmt.Printf("By Type:\n")
		for notifType, count := range typeMap {
			fmt.Printf("  %s: %d\n", notifType, count)
		}
		fmt.Println()
	}

	// Recent activity
	if sent24h, ok := stats["sent_last_24h"].(int); ok {
		fmt.Printf("Sent in Last 24 Hours: %d\n", sent24h)
	}

	if queueSize, ok := stats["current_queue_size"].(int); ok {
		fmt.Printf("Current Queue Size: %d\n\n", queueSize)
	}

	// Business hours stats
	if burstEvents, ok := stats["burst_events_7d"].(int); ok {
		if burstSent, ok := stats["burst_notifications_7d"].(int); ok {
			fmt.Printf("Business Hours Bursts (Last 7 Days):\n")
			fmt.Printf("  Events: %d\n", burstEvents)
			fmt.Printf("  Notifications Sent: %d\n\n", burstSent)
		}
	}

	// Response time stats
	if waitStats, ok := stats["response_times_7d"].(map[string]interface{}); ok {
		fmt.Printf("Response Times (Last 7 Days):\n")
		if avg, ok := waitStats["average_minutes"].(float64); ok {
			fmt.Printf("  Average: %.1f minutes\n", avg)
		}
		if min, ok := waitStats["minimum_minutes"].(float64); ok {
			fmt.Printf("  Minimum: %.1f minutes\n", min)
		}
		if max, ok := waitStats["maximum_minutes"].(float64); ok {
			fmt.Printf("  Maximum: %.1f minutes\n", max)
		}
	}
}

func printRunStats(stats *models.RunStats, logger *logging.Logger) {
	statsMap := map[string]interface{}{
		"tickets_checked":      stats.TicketsChecked,
		"notifications_sent":   stats.NotificationsSent,
		"notifications_queued": stats.NotificationsQueued,
		"errors":               stats.Errors,
		"duration":             stats.Duration.String(),
	}

	// Use the logger's structured logging capability
	logger.LogRunStats(statsMap)

	// Also print human-readable format for console output
	fmt.Printf("\n=== Run Statistics ===\n")
	fmt.Printf("Tickets checked: %d\n", stats.TicketsChecked)
	fmt.Printf("Notifications sent: %d\n", stats.NotificationsSent)
	fmt.Printf("Notifications queued: %d\n", stats.NotificationsQueued)
	fmt.Printf("Errors: %d\n", stats.Errors)
	fmt.Printf("Duration: %s\n", stats.Duration)
}

func performCleanup(db *database.DB, cfg *config.Config, logger *logging.Logger) error {
	logger.Info("Starting database cleanup",
		"retention_days", cfg.RetentionDays,
		"auto_vacuum", cfg.AutoVacuum,
	)

	if err := notifier.CleanupOldNotifications(db, cfg.RetentionDays); err != nil {
		return fmt.Errorf("failed to cleanup old notifications: %w", err)
	}

	if cfg.AutoVacuum {
		if err := notifier.VacuumDatabase(db); err != nil {
			return fmt.Errorf("failed to vacuum database: %w", err)
		}
	}

	return nil
}
