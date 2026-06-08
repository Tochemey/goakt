// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package log

import (
	"context"
	golog "log"
)

// Logger represents a leveled logger used by GoAkt.
//
// Implementations typically encode a severity level (debug/info/warn/error),
// attach optional metadata (e.g., timestamps), and write entries to one or more io.Writer
// destinations.
//
// Methods come in two forms:
//   - X(...any): log values (commonly formatted similarly to fmt.Sprint)
//   - Xf(format string, ...any): log using a format string (commonly formatted similarly to fmt.Sprintf)
//
// The interface intentionally omits Fatal/Panic methods: a library must not terminate
// the host process or panic on the application's behalf. Callers that want such behavior
// should log at error level and then decide how to halt. Concrete implementations in this
// package still expose Fatal/Panic methods for application code that holds the concrete type.
//
// Flush may be used to force buffered implementations to write pending entries; for
// unbuffered implementations it may be a no-op.
type Logger interface {
	// Info logs a message at info level.
	//
	// The arguments are implementation-defined but typically formatted similarly to fmt.Sprint.
	Info(...any)

	// Infof logs a formatted message at info level.
	//
	// The format string and arguments are implementation-defined but typically follow fmt.Sprintf.
	Infof(string, ...any)

	// InfoContext logs a message at info level with the given context.
	InfoContext(context.Context, ...any)

	// InfofContext logs a formatted message at info level with the given context.
	InfofContext(context.Context, string, ...any)

	// Warn logs a message at warn level.
	//
	// The arguments are implementation-defined but typically formatted similarly to fmt.Sprint.
	Warn(...any)

	// Warnf logs a formatted message at warn level.
	//
	// The format string and arguments are implementation-defined but typically follow fmt.Sprintf.
	Warnf(string, ...any)

	// WarnContext logs a message at warn level with the given context.
	WarnContext(context.Context, ...any)

	// WarnfContext logs a formatted message at warn level with the given context.
	WarnfContext(context.Context, string, ...any)

	// Error logs a message at error level.
	//
	// The arguments are implementation-defined but typically formatted similarly to fmt.Sprint.
	Error(...any)

	// Errorf logs a formatted message at error level.
	//
	// The format string and arguments are implementation-defined but typically follow fmt.Sprintf.
	Errorf(string, ...any)

	// ErrorContext logs a message at error level with the given context.
	ErrorContext(context.Context, ...any)

	// ErrorfContext logs a formatted message at error level with the given context.
	ErrorfContext(context.Context, string, ...any)

	// Debug logs a message at debug level.
	//
	// The arguments are implementation-defined but typically formatted similarly to fmt.Sprint.
	Debug(...any)

	// Debugf logs a formatted message at debug level.
	//
	// The format string and arguments are implementation-defined but typically follow fmt.Sprintf.
	Debugf(string, ...any)

	// DebugContext logs a message at debug level with the given context.
	DebugContext(context.Context, ...any)

	// DebugfContext logs a formatted message at debug level with the given context.
	DebugfContext(context.Context, string, ...any)

	// LogLevel returns the configured minimum severity level used by the logger.
	//
	// Messages below this level are typically suppressed.
	LogLevel() Level

	// Enabled reports whether the given level is enabled.
	// Use this to avoid evaluating expensive arguments when logging is disabled:
	//
	//	if logger.Enabled(log.DebugLevel) {
	//	    logger.Debugf("expensive: %v", expensiveFunc())
	//	}
	Enabled(level Level) bool

	// With returns a Logger that includes the given key-value pairs in all
	// subsequent log entries. Keys and values alternate; keys must be strings.
	// Implementations that do not support structured fields may ignore the
	// pairs and return the receiver unchanged.
	With(keyValues ...any) Logger

	// Flush forces any buffered log entries to be written to their outputs.
	//
	// It should return a non-nil error if flushing fails. For non-buffered implementations,
	// Flush may return nil without doing anything.
	Flush() error

	// StdLogger returns a *log.Logger compatible with the standard library's log package.
	//
	// This is useful for integrating dependencies that accept a *log.Logger.
	StdLogger() *golog.Logger
}
