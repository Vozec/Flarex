package logger

import (
	"os"
	"time"

	"github.com/rs/zerolog"
)

// L is the global logger. Console-friendly by default (colored, human-readable).
// Switch to pure JSON with SetJSON() for production/ingest.
var L zerolog.Logger

func init() {
	zerolog.TimeFieldFormat = time.RFC3339
	zerolog.DurationFieldUnit = time.Millisecond
	zerolog.DurationFieldInteger = false
	writer := zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: "15:04:05",
		NoColor:    false,
	}
	L = zerolog.New(writer).Level(zerolog.InfoLevel).With().Timestamp().Logger()
}

func SetLevel(level string) {
	switch level {
	case "trace":
		L = L.Level(zerolog.TraceLevel)
	case "debug":
		L = L.Level(zerolog.DebugLevel)
	case "info":
		L = L.Level(zerolog.InfoLevel)
	case "warn":
		L = L.Level(zerolog.WarnLevel)
	case "error":
		L = L.Level(zerolog.ErrorLevel)
	}
}

func SetJSON() {
	L = zerolog.New(os.Stdout).Level(L.GetLevel()).With().Timestamp().Logger()
}
