package command

import (
	"io"
	"log/slog"
)

func (c *CLI) logLevel() slog.Level {
	return logLevelFor(c.Quiet, c.Verbose)
}

func (a *ArchiveCmd) logLevel() slog.Level {
	return logLevelFor(a.Quiet, a.Verbose)
}

func logLevelFor(quiet, verbose bool) slog.Level {
	switch {
	case quiet:
		return slog.LevelWarn
	case verbose:
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
