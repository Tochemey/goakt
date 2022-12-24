package log

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	levels = map[Level]zerolog.Level{
		InfoLevel:    zerolog.InfoLevel,
		DebugLevel:   zerolog.DebugLevel,
		WarningLevel: zerolog.WarnLevel,
		ErrorLevel:   zerolog.ErrorLevel,
		PanicLevel:   zerolog.PanicLevel,
		FatalLevel:   zerolog.FatalLevel,
		Disabled:     zerolog.Disabled,
	}

	DefaultLogger = New(DebugLevel, os.Stderr)
	DiscardLogger = New(Disabled, io.Discard)
)

type logger struct {
	log zerolog.Logger
}

var _ Logger = &logger{}

// New create new logger from the given writers and level.
func New(level Level, writer io.Writer) Logger {
	// set the log level
	logLevel := levels[level]
	zerolog.SetGlobalLevel(logLevel)
	l := log.Output(zerolog.ConsoleWriter{Out: writer, TimeFormat: time.RFC3339})
	return &logger{log: l}
}

// Debug logs to DEBUG level.
func (l *logger) Debug(args ...any) {
	l.log.Debug().Msg(fmt.Sprint(args...))
}

// Debugf logs to DEBUG level.
func (l *logger) Debugf(format string, args ...any) {
	l.log.Debug().Msgf(format, args...)
}

// Info logs to INFO level
func (l *logger) Info(args ...any) {
	l.log.Info().Msg(fmt.Sprint(args...))
}

// Infof logs to INFO level
func (l *logger) Infof(format string, args ...any) {
	l.log.Info().Msgf(format, args...)
}

// Warning logs to the WARNING level
func (l *logger) Warning(args ...any) {
	l.log.Warn().Msg(fmt.Sprint(args...))
}

// Warningf logs to the WARNING level
func (l *logger) Warningf(format string, args ...any) {
	l.log.Warn().Msgf(format, args...)
}

// Error logs to the ERROR level.
func (l *logger) Error(args ...any) {
	l.log.Error().Msg(fmt.Sprint(args...))
}

// Errorf logs to the ERROR level.
func (l *logger) Errorf(format string, args ...any) {
	l.log.Error().Msgf(format, args...)
}

// Fatal logs to the FATAL level followed by a call to os.Exit(1).
func (l *logger) Fatal(args ...any) {
	l.log.Fatal().Msg(fmt.Sprint(args...))
}

// Fatalf logs to the FATAL level followed by a call to os.Exit(1).
func (l *logger) Fatalf(format string, args ...any) {
	l.log.Fatal().Msgf(format, args...)
}

// Panic logs to the PANIC level followed by a call to panic().
func (l *logger) Panic(args ...any) {
	l.log.Panic().Msg(fmt.Sprint(args...))
}

// Panicf logs to the PANIC level followed by a call to panic().
func (l *logger) Panicf(format string, args ...any) {
	l.log.Panic().Msgf(format, args...)
}
