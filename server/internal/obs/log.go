// Package obs is the observability skeleton (PLAN.md §3 P0-a "관측 스켈레톤").
// Structured JSON logs now; metrics for PRD §9/§11 are added per phase.
package obs

import (
	"log/slog"
	"os"
)

// NewLogger returns a JSON logger tagged with the component name.
// Level is taken from COLAB_LOG_LEVEL (debug|info|warn|error), default info.
func NewLogger(component string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(os.Getenv("COLAB_LOG_LEVEL"))); err != nil {
		lvl = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	return slog.New(h).With("component", component)
}
