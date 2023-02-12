package log

import "context"

// Logger represents an active logging object that generates lines of
// output to an io.Writer.
type Logger interface {
	// Debug logs to DEBUG level
	Debug(...any)
	// Debugf logs to DEBUG level.
	Debugf(string, ...any)
	// Info logs to INFO level.
	Info(...any)
	// Infof logs to INFO level
	Infof(string, ...any)
	// Warning logs to the WARNING level.
	Warning(...any)
	// Warningf logs to the WARNING level
	Warningf(string, ...any)
	// Error logs to the ERROR level.
	Error(...any)
	// Errorf logs to the ERROR level.
	Errorf(string, ...any)
	// Fatal logs to the FATAL level followed by a call to os.Exit(1).
	Fatal(...any)
	// Fatalf logs to the FATAL level followed by a call to os.Exit(1).
	Fatalf(string, ...any)
	// Panic logs to the PANIC level followed by a call to panic().
	Panic(...any)
	// Panicf logs to the PANIC level followed by a call to panic().
	Panicf(string, ...any)
	// WithContext returns a context logger
	WithContext(ctx context.Context) Logger
}
