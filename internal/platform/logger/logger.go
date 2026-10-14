// Package logger builds the structured logger used across the application.
package logger

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// New returns a JSON slog.Logger writing to w (os.Stdout when w is nil) at the
// given level. Unknown levels fall back to info.
func New(level string, w io.Writer) *slog.Logger {
	if w == nil {
		w = os.Stdout
	}
	var lvl slog.Level
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: lvl})
	return slog.New(handler)
}
