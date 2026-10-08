package command

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLogLevel(t *testing.T) {
	assert.Equal(t, slog.LevelInfo, (&CLI{}).logLevel())
	assert.Equal(t, slog.LevelWarn, (&CLI{Quiet: true}).logLevel())
	assert.Equal(t, slog.LevelDebug, (&CLI{Verbose: true}).logLevel())
}

func TestNewLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := newLogger(&buf, slog.LevelInfo)
	logger.Debug("hidden")
	logger.Info("shown", "count", 3)
	assert.Equal(t, "level=INFO msg=shown count=3\n", buf.String())
}
