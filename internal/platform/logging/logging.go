package logging

import (
	"io"
	"log/slog"
)

// New creates a JSON structured logger at the requested minimum level.
func New(output io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{Level: level}))
}
