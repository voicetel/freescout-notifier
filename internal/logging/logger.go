package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
)

type Logger struct {
	*slog.Logger
	verbose bool
}

// NewLogger creates a new logger based on the configuration
func NewLogger(format string, verbose bool, output io.Writer) *Logger {
	if output == nil {
		output = os.Stdout
	}

	var handler slog.Handler

	opts := &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}

	if verbose {
		opts.Level = slog.LevelDebug
	}

	switch format {
	case "json":
		handler = slog.NewJSONHandler(output, opts)
	default:
		handler = slog.NewTextHandler(output, opts)
	}

	logger := slog.New(handler)

	return &Logger{
		Logger:  logger,
		verbose: verbose,
	}
}

// SetAsDefault sets this logger as the default slog logger
func (l *Logger) SetAsDefault() {
	slog.SetDefault(l.Logger)
}

// Verbose logs a message only if verbose logging is enabled
func (l *Logger) Verbose(msg string, args ...any) {
	if l.verbose {
		l.Debug(msg, args...)
	}
}

// LogRunStats logs the run statistics in a structured way
func (l *Logger) LogRunStats(stats interface{}) {
	if l.Logger.Handler().Enabled(context.Background(), slog.LevelInfo) {
		l.Info("run_completed", "stats", stats)
	}
}

// LogError logs an error with context
func (l *Logger) LogError(msg string, err error, args ...any) {
	allArgs := append(args, "error", err)
	l.Error(msg, allArgs...)
}
