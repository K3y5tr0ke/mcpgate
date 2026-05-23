package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

type contextKey string

const loggerKey contextKey = "logger"

// New creates a structured slog.Logger with the given level and format ("json" or "text").
func New(level, format string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl}

	var handler slog.Handler
	if strings.ToLower(format) == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}

// WithLogger stores a logger in the context for retrieval by downstream handlers.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, logger)
}

// FromContext retrieves the logger stored in ctx, falling back to slog.Default().
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}
