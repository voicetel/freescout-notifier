package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/voicetel/freescout-notifier/internal/config"
	"github.com/voicetel/freescout-notifier/internal/database"
	"github.com/voicetel/freescout-notifier/internal/logging"
	"github.com/voicetel/freescout-notifier/internal/models"
	"github.com/voicetel/freescout-notifier/internal/notifier"
	"github.com/voicetel/freescout-notifier/internal/push"
	"github.com/voicetel/freescout-notifier/internal/slack"
)

// Version information - these will be set at build time via ldflags
var (
	Version   = "dev"     // Version number
	GitCommit = "unknown" // Git commit hash
	BuildDate = "unknown" // Build date
	GoVersion = "unknown" // Go version used to build
)

func main() {
	cfg := config.ParseFlags()

	if cfg.ShowVersion {
		printVersion()
		os.Exit(0)
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
		os.Exit(1)
	}

	logger := logging.NewLogger(cfg.LogFormat, cfg.Verbose, nil, Version, GitCommit, BuildDate)
	logger.SetAsDefault()

	// Graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Local state store; the schema is created automatically.
	store, err := database.NewStore(cfg.DBPath)
	if err != nil {
		logger.LogError("Failed to initialize state database", err)
		os.Exit(1)
	}
	defer store.Close()

	// Utility modes that only need the local state store.
	switch {
	case cfg.InitDB:
		fmt.Println("Database initialized successfully!")
		return
	case cfg.StatsOnly:
		if err := printStats(ctx, store); err != nil {
			logger.LogError("Failed to print stats", err)
			os.Exit(1)
		}
		return
	case cfg.Cleanup:
		if err := performCleanup(ctx, store, cfg, logger); err != nil {
			logger.LogError("Failed to perform cleanup", err)
			os.Exit(1)
		}
		fmt.Println("Cleanup completed successfully!")
		return
	case cfg.TestPush:
		if err := testPush(ctx, cfg, logger); err != nil {
			logger.LogError("Push test failed", err)
			os.Exit(1)
		}
		fmt.Printf("Test push sent to topic %q\n", cfg.Push.Topic)
		return
	}

	// FreeScout connection.
	fs, err := database.ConnectFreeScout(ctx, cfg.FreeScout)
	if err != nil {
		logger.LogError("Failed to connect to FreeScout", err)
		os.Exit(1)
	}
	defer fs.Close()
	logger.Verbose("FreeScout database connected",
		"last_customer_reply_at_column", fs.HasLastCustomerReply())

	if cfg.CheckConnections {
		if err := checkConnections(ctx, cfg, fs, logger); err != nil {
			logger.LogError("Connection check failed", err)
			os.Exit(1)
		}
		fmt.Println("All connections successful!")
		return
	}

	n := notifier.New(fs, store, cfg, logger)

	// Single pass mode (cron-friendly).
	if cfg.Once {
		stats, err := n.RunOnce(ctx)
		if stats != nil {
			printRunStats(stats, logger)
		}
		if err != nil {
			logger.LogError("Run completed with errors", err)
			os.Exit(1)
		}
		return
	}

	// Default: run as a daemon until interrupted.
	if err := n.RunDaemon(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.LogError("Notifier daemon stopped with error", err)
		os.Exit(1)
	}
}

func printVersion() {
	fmt.Printf("FreeScout Notifier\n")
	fmt.Printf("Version:    %s\n", Version)
	fmt.Printf("Git Commit: %s\n", GitCommit)
	fmt.Printf("Build Date: %s\n", BuildDate)
	fmt.Printf("Go Version: %s\n", GoVersion)
}

func checkConnections(ctx context.Context, cfg *config.Config, fs *database.FreeScoutDB, logger *logging.Logger) error {
	logger.Info("Checking connections...")

	logger.Info("Testing FreeScout database connection...")
	if err := fs.Ping(ctx); err != nil {
		return fmt.Errorf("FreeScout connection failed: %w", err)
	}
	logger.Info("FreeScout database connection successful")

	if cfg.Slack.WebhookURL != "" {
		logger.Info("Testing Slack webhook...")
		if err := slack.TestWebhook(ctx, cfg.Slack); err != nil {
			return fmt.Errorf("Slack webhook test failed: %w", err)
		}
		logger.Info("Slack webhook test successful")
	} else {
		logger.Info("Slack webhook not configured; skipped")
	}

	if cfg.Push.Enabled {
		logger.Info("Testing FCM push credentials...")
		client, err := push.NewClient(ctx, cfg.Push)
		if err != nil {
			return fmt.Errorf("FCM push initialization failed: %w", err)
		}
		logger.Info("FCM push credentials OK (use --test-push to send a test message)",
			"topic", client.Topic())
	} else {
		logger.Info("Mobile push not enabled; skipped")
	}

	return nil
}

// testPush sends a verification message to the configured FCM topic.
func testPush(ctx context.Context, cfg *config.Config, logger *logging.Logger) error {
	logger.Info("Sending test push notification...", "topic", cfg.Push.Topic)
	client, err := push.NewClient(ctx, cfg.Push)
	if err != nil {
		return fmt.Errorf("failed to initialize FCM client: %w", err)
	}
	return client.Test(ctx, cfg.FreeScout.URL)
}

func printStats(ctx context.Context, store *database.Store) error {
	stats, err := store.Stats(ctx)
	if err != nil {
		return fmt.Errorf("failed to get statistics: %w", err)
	}
	printHumanReadableStats(stats)
	return nil
}

func printHumanReadableStats(stats *models.Stats) {
	fmt.Printf("\n=== FreeScout Notifier Statistics ===\n\n")
	fmt.Printf("Total Notifications Sent: %d\n", stats.TotalSent)
	fmt.Printf("Sent in Last 24 Hours:    %d\n", stats.SentLast24h)
	fmt.Printf("Failed in Last 24 Hours:  %d\n", stats.FailedLast24h)
	fmt.Printf("Currently Queued:         %d\n\n", stats.QueuedNow)

	if len(stats.ByType) > 0 {
		fmt.Printf("Sent by Type:\n")
		for _, notifType := range []string{
			string(models.NewTicket),
			string(models.CustomerReply),
			string(models.UnansweredActive),
			string(models.AgingPending),
		} {
			if count, ok := stats.ByType[notifType]; ok {
				fmt.Printf("  %s: %d\n", notifType, count)
			}
		}
		for notifType, count := range stats.ByType {
			switch models.NotificationType(notifType) {
			case models.NewTicket, models.CustomerReply, models.UnansweredActive, models.AgingPending:
			default:
				fmt.Printf("  %s: %d\n", notifType, count)
			}
		}
		fmt.Println()
	}

	if len(stats.ActiveReminders) > 0 {
		fmt.Printf("Tickets Currently Tracked for Reminders:\n")
		for alertType, count := range stats.ActiveReminders {
			fmt.Printf("  %s: %d\n", alertType, count)
		}
		fmt.Println()
	}
}

func printRunStats(stats *models.CycleStats, logger *logging.Logger) {
	logger.LogRunStats(map[string]interface{}{
		"new_tickets":      stats.NewTickets,
		"customer_replies": stats.CustomerReplies,
		"sent":             stats.Sent,
		"queued":           stats.Queued,
		"reminders":        stats.Reminders,
		"skipped":          stats.Skipped,
		"errors":           stats.Errors,
		"duration":         stats.Duration.String(),
	})

	fmt.Printf("\n=== Run Statistics ===\n")
	fmt.Printf("New tickets detected:      %d\n", stats.NewTickets)
	fmt.Printf("Customer replies detected: %d\n", stats.CustomerReplies)
	fmt.Printf("Notifications sent:        %d\n", stats.Sent)
	fmt.Printf("Notifications queued:      %d\n", stats.Queued)
	fmt.Printf("Reminders sent:            %d\n", stats.Reminders)
	fmt.Printf("Skipped (rate limit):      %d\n", stats.Skipped)
	fmt.Printf("Errors:                    %d\n", stats.Errors)
	fmt.Printf("Duration:                  %s\n", stats.Duration)
}

func performCleanup(ctx context.Context, store *database.Store, cfg *config.Config, logger *logging.Logger) error {
	logger.Info("Starting state database cleanup",
		"retention_days", cfg.RetentionDays,
		"auto_vacuum", cfg.AutoVacuum,
	)

	deleted, err := store.Cleanup(ctx, cfg.RetentionDays)
	if err != nil {
		return fmt.Errorf("failed to cleanup old state: %w", err)
	}
	for table, rows := range deleted {
		if rows > 0 {
			logger.Info("Cleaned up old records", "table", table, "rows", rows)
		}
	}

	if cfg.AutoVacuum {
		logger.Info("Performing database vacuum...")
		if err := store.Vacuum(ctx); err != nil {
			return fmt.Errorf("failed to vacuum database: %w", err)
		}
	}

	return nil
}
