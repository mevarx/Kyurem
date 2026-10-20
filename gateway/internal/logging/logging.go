// Package logging wires the structured logger used across the gateway.
// JSON by default (PRD §7: structured logs with trace IDs); payload or
// secret data must never be logged (PRD §15).
package logging

import (
	"log/slog"
	"os"
	"strings"

	"github.com/kyurem-project/kyurem/gateway/internal/config"
)

// New builds the process logger from configuration level/format settings.
func New(cfg config.LogConfig) *slog.Logger {
	level := parseLevel(cfg.Level)
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if strings.EqualFold(cfg.Format, "text") {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(h)
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
