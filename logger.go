package probot

import (
	"io"
	"os"
	"strings"

	"github.com/rs/zerolog"
)

// newLogger builds a zerolog logger honoring the configured level and format.
func newLogger(level, format string) zerolog.Logger {
	lvl := parseLogLevel(level)
	zerolog.SetGlobalLevel(lvl)

	var w io.Writer = os.Stdout
	if format != "json" {
		w = zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: "15:04:05"}
	}
	return zerolog.New(w).With().Timestamp().Logger()
}

func parseLogLevel(s string) zerolog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "trace":
		return zerolog.TraceLevel
	case "debug":
		return zerolog.DebugLevel
	case "info":
		return zerolog.InfoLevel
	case "warn", "warning":
		return zerolog.WarnLevel
	case "error":
		return zerolog.ErrorLevel
	case "fatal":
		return zerolog.FatalLevel
	case "panic":
		return zerolog.PanicLevel
	case "disabled":
		return zerolog.Disabled
	default:
		return zerolog.InfoLevel
	}
}
