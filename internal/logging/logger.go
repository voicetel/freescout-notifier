package logging

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

type Logger struct {
	*slog.Logger
	verbose bool
}

// NewLogger creates a new logger based on the configuration
func NewLogger(format string, verbose bool, output io.Writer, version, commit, buildDate string) *Logger {
	if output == nil {
		output = os.Stdout
	}

	var handler slog.Handler

	var level slog.Level
	if verbose {
		level = slog.LevelDebug
	} else {
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	}

	switch format {
	case "json":
		handler = slog.NewJSONHandler(output, opts)
	default:
		handler = slog.NewTextHandler(output, opts)
	}

	// Get application name from args
	var application string
	if len(os.Args) > 0 {
		application = filepath.Base(os.Args[0])
	}

	logger := slog.New(handler).With(
		slog.String("service", application),
		slog.String("version", version),
		slog.String("commit", commit),
		slog.String("build_date", buildDate),
	)

	return &Logger{
		Logger:  logger,
		verbose: verbose,
	}
}

// SetAsDefault sets this logger as the default slog logger
func (l *Logger) SetAsDefault() {
	slog.SetDefault(l.Logger)
	// Also set the standard log package to use slog
	if l.verbose {
		slog.SetLogLoggerLevel(slog.LevelDebug)
	} else {
		slog.SetLogLoggerLevel(slog.LevelInfo)
	}
}

// Verbose logs a message only if verbose logging is enabled
func (l *Logger) Verbose(msg string, args ...any) {
	if l.verbose {
		l.Debug(msg, args...)
	}
}

// LogRunStats logs the run statistics in a structured way
func (l *Logger) LogRunStats(stats map[string]interface{}) {
	attrs := make([]any, 0, len(stats)*2)
	for k, v := range stats {
		attrs = append(attrs, k, v)
	}
	l.Info("run_completed", attrs...)
}

// LogError logs an error with context
func (l *Logger) LogError(msg string, err error, args ...any) {
	allArgs := append([]any{slog.String("error", err.Error())}, args...)
	l.Error(msg, allArgs...)
}
