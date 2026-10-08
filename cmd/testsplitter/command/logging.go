package command

import (
	"io"
	"log/slog"
)

func (c *CLI) logLevel() slog.Level {
	switch {
	case c.Quiet:
		return slog.LevelWarn
	case c.Verbose:
		return slog.LevelDebug
	default:
		return slog.LevelInfo
	}
}

// newLogger returns a text logger without timestamps, which are noise for a CLI.
func newLogger(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	}))
}
