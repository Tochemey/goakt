package log

import (
	"fmt"
	"io"
	"os"

	"github.com/rs/zerolog"
)

// DefaultLogger represents the default logger to use
// This logger wraps zerolog under the hood
var DefaultLogger = NewLogger(os.Stderr)
var DiscardLogger = NewLogger(io.Discard)

// Info logs to INFO level.
func Info(v ...interface{}) {
	DefaultLogger.Info(v...)
}

// Infof logs to INFO level
func Infof(format string, v ...interface{}) {
	DefaultLogger.Infof(format, v...)
}

// Warning logs to the WARNING level.
func Warning(v ...interface{}) {
	DefaultLogger.Warn(v...)
}

// Warningf logs to the WARNING level.
func Warningf(format string, v ...interface{}) {
	DefaultLogger.Warnf(format, v...)
}

// Error logs to the ERROR level.
func Error(v ...interface{}) {
	DefaultLogger.Error(v...)
}

// Errorf logs to the ERROR level.
func Errorf(format string, v ...interface{}) {
	DefaultLogger.Errorf(format, v...)
}

// Fatal logs to the FATAL level followed by a call to os.Exit(1).
func Fatal(v ...interface{}) {
	DefaultLogger.Fatal(v...)
}

// Fatalf logs to the FATAL level followed by a call to os.Exit(1).
func Fatalf(format string, v ...interface{}) {
	DefaultLogger.Fatalf(format, v...)
}

// Panic logs to the PANIC level followed by a call to panic().
func Panic(v ...interface{}) {
	DefaultLogger.Panic(v...)
}

// Panicf logs to the PANIC level followed by a call to panic().
func Panicf(format string, v ...interface{}) {
	DefaultLogger.Panicf(format, v...)
}

// logger implements Logger interface with the underlying zap as
// the underlying logging library
type logger struct {
	// specifies the prefix
	prefix string
	// specifies the underlying logger
	underlying zerolog.Logger
}

// NewLogger creates an instance of logger
func NewLogger(w io.Writer) Logger {
	// create an instance of zerolog logger
	zlogger := zerolog.New(w).With().Timestamp().Logger()
	// create the instance of logger and returns it
	return &logger{underlying: zlogger}
}

// Debug starts a message with debug level
func (l *logger) Debug(v ...any) {
	l.underlying.Debug().Msg(fmt.Sprint(v...))
}

// Debugf starts a message with debug level
func (l *logger) Debugf(format string, v ...any) {
	l.underlying.Debug().Msgf(format, v...)
}

// Panic starts a new message with panic level. The panic() function
// is called which stops the ordinary flow of a goroutine.
func (l *logger) Panic(v ...any) {
	l.underlying.Panic().Msg(fmt.Sprint(v...))
}

// Panicf starts a new message with panic level. The panic() function
// is called which stops the ordinary flow of a goroutine.
func (l *logger) Panicf(format string, v ...any) {
	l.underlying.Panic().Msgf(format, v...)
}

// Fatal starts a new message with fatal level. The os.Exit(1) function
// is called which terminates the program immediately.
func (l *logger) Fatal(v ...any) {
	l.underlying.Fatal().Msg(fmt.Sprint(v...))
}

// Fatalf starts a new message with fatal level. The os.Exit(1) function
// is called which terminates the program immediately.
func (l *logger) Fatalf(format string, v ...any) {
	l.underlying.Fatal().Msgf(format, v...)
}

// Error starts a new message with error level.
func (l *logger) Error(v ...any) {
	l.underlying.Error().Msg(fmt.Sprint(v...))
}

// Errorf starts a new message with error level.
func (l *logger) Errorf(format string, v ...any) {
	l.underlying.Error().Msgf(format, v...)
}

// Warn starts a new message with warn level
func (l *logger) Warn(v ...any) {
	l.underlying.Warn().Msg(fmt.Sprint(v...))
}

// Warnf starts a new message with warn level
func (l *logger) Warnf(format string, v ...any) {
	l.underlying.Warn().Msgf(format, v...)
}

// Info starts a message with info level
func (l *logger) Info(v ...any) {
	l.underlying.Info().Msg(fmt.Sprint(v...))
}

// Infof starts a message with info level
func (l *logger) Infof(format string, v ...any) {
	l.underlying.Info().Msgf(format, v...)
}

// Trace starts a new message with trace level
func (l *logger) Trace(v ...any) {
	l.underlying.Trace().Msg(fmt.Sprint(v...))
}

// Tracef starts a new message with trace level
func (l *logger) Tracef(format string, v ...any) {
	l.underlying.Trace().Msgf(format, v...)
}
