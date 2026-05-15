package logger

import (
	"log/slog"
	"os"
)

func New(verbose bool) *slog.Logger {
	level := slog.LevelInfo
	addSource := false

	if verbose {
		level = slog.LevelDebug
		addSource = true
	}

	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level:     level,
		AddSource: addSource,
	})

	return slog.New(h)
}
