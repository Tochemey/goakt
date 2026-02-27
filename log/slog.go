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
	"fmt"
	"io"
	golog "log"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// SlogLog implements Logger using the standard library slog package.
// It is optimized for low GC: Enabled() checks avoid formatting when disabled,
// and With() uses typed slog.Attr for common types to avoid reflection.
var _ Logger = (*Slog)(nil)

// toSlogAttrs converts key-value pairs to slog.Attr using typed accessors.
const maxSlogAttrs = 8

// Slog wraps slog.Logger to implement the Logger interface.
type Slog struct {
	logger  *slog.Logger
	level   Level
	outputs []io.Writer
}

// NewSlog creates a Logger backed by slog with JSON output.
// Performance notes:
// - Enabled() is checked before formatting in Xf methods to avoid allocations when disabled.
// - With() uses typed slog.Attr for string/int/bool etc. to minimize reflection.
// - No buffering; for high-throughput file logging, wrap the writer in bufio.Writer.
func NewSlog(level Level, writers ...io.Writer) *Slog {
	if len(writers) == 0 {
		writers = []io.Writer{os.Stdout}
	}
	w := io.MultiWriter(writers...)
	opts := &slog.HandlerOptions{
		Level:     toSlogLevel(level),
		AddSource: true,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				// Match zap-style timestamp format for consistency
				if t, ok := a.Value.Any().(time.Time); ok {
					a.Value = slog.StringValue(t.Format("2006-01-02T15:04:05.000000Z0700"))
				}
			}
			if a.Key == slog.SourceKey {
				// Format as zap-style "file:line" (ShortCallerEncoder) and use "caller" key
				if src, ok := a.Value.Any().(*slog.Source); ok && src != nil {
					file := filepath.Base(src.File)
					a = slog.Attr{Key: "caller", Value: slog.StringValue(fmt.Sprintf("%s:%d", file, src.Line))}
				}
			}
			return a
		},
	}
	handler := slog.NewJSONHandler(w, opts)
	return &Slog{
		logger:  slog.New(handler),
		level:   level,
		outputs: writers,
	}
}

// Debug logs at debug level.
func (l *Slog) Debug(v ...any) {
	if l.Enabled(DebugLevel) {
		l.logger.Debug(fmt.Sprint(v...))
	}
}

// Debugf logs a formatted message at debug level.
func (l *Slog) Debugf(format string, v ...any) {
	if l.Enabled(DebugLevel) {
		l.logger.Debug(fmt.Sprintf(format, v...))
	}
}

// Info logs at info level.
func (l *Slog) Info(v ...any) {
	if l.Enabled(InfoLevel) {
		l.logger.Info(fmt.Sprint(v...))
	}
}

// Infof logs a formatted message at info level.
func (l *Slog) Infof(format string, v ...any) {
	if l.Enabled(InfoLevel) {
		l.logger.Info(fmt.Sprintf(format, v...))
	}
}

// Warn logs at warn level.
func (l *Slog) Warn(v ...any) {
	if l.Enabled(WarningLevel) {
		l.logger.Warn(fmt.Sprint(v...))
	}
}

// Warnf logs a formatted message at warn level.
func (l *Slog) Warnf(format string, v ...any) {
	if l.Enabled(WarningLevel) {
		l.logger.Warn(fmt.Sprintf(format, v...))
	}
}

// Error logs at error level.
func (l *Slog) Error(v ...any) {
	if l.Enabled(ErrorLevel) {
		l.logger.Error(fmt.Sprint(v...))
	}
}

// Errorf logs a formatted message at error level.
func (l *Slog) Errorf(format string, v ...any) {
	if l.Enabled(ErrorLevel) {
		l.logger.Error(fmt.Sprintf(format, v...))
	}
}

// Fatal logs at error level and exits.
func (l *Slog) Fatal(v ...any) {
	l.logger.Error(fmt.Sprint(v...))
	os.Exit(1)
}

// Fatalf logs a formatted message at error level and exits.
func (l *Slog) Fatalf(format string, v ...any) {
	l.logger.Error(fmt.Sprintf(format, v...))
	os.Exit(1)
}

// Panic logs at error level and panics.
func (l *Slog) Panic(v ...any) {
	msg := fmt.Sprint(v...)
	l.logger.Error(msg)
	panic(msg)
}

// Panicf logs a formatted message at error level and panics.
func (l *Slog) Panicf(format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	l.logger.Error(msg)
	panic(msg)
}

// Enabled reports whether the given level is enabled.
func (l *Slog) Enabled(level Level) bool {
	ctx := context.Background()
	return l.logger.Enabled(ctx, l.slogLevel(level))
}

// LogLevel returns the configured minimum level.
func (l *Slog) LogLevel() Level {
	return l.level
}

// With returns a Logger that includes the given key-value pairs.
// Uses typed slog.Attr for common types to avoid reflection.
func (l *Slog) With(keyValues ...any) Logger {
	attrs := toSlogAttrs(keyValues)
	if len(attrs) == 0 {
		return l
	}
	// slog.Logger.With takes ...any (key, val, key, val...); convert attrs
	const maxWithArgs = 16
	n := len(attrs) * 2
	if n <= maxWithArgs {
		var buf [maxWithArgs]any
		for i, a := range attrs {
			buf[i*2] = a.Key
			buf[i*2+1] = a.Value.Any()
		}
		return &Slog{
			logger:  l.logger.With(buf[:n]...),
			level:   l.level,
			outputs: l.outputs,
		}
	}
	args := make([]any, 0, n)
	for _, a := range attrs {
		args = append(args, a.Key, a.Value.Any())
	}
	return &Slog{
		logger:  l.logger.With(args...),
		level:   l.level,
		outputs: l.outputs,
	}
}

func toSlogAttrs(keyValues []any) []slog.Attr {
	n := (len(keyValues) + 1) / 2
	if n == 0 {
		return nil
	}
	var buf [maxSlogAttrs]slog.Attr
	var attrs []slog.Attr
	if n <= maxSlogAttrs {
		attrs = buf[:0:n]
	} else {
		attrs = make([]slog.Attr, 0, n)
	}
	for i := 0; i < len(keyValues); i += 2 {
		if i+1 >= len(keyValues) {
			attrs = append(attrs, toSlogAttr("_", keyValues[i]))
			break
		}
		k, ok := keyValues[i].(string)
		if !ok {
			continue
		}
		attrs = append(attrs, toSlogAttr(k, keyValues[i+1]))
	}
	return attrs
}

func toSlogAttr(key string, val any) slog.Attr {
	switch v := val.(type) {
	case string:
		return slog.String(key, v)
	case int:
		return slog.Int(key, v)
	case int32:
		return slog.Int64(key, int64(v))
	case int64:
		return slog.Int64(key, v)
	case uint:
		return slog.Uint64(key, uint64(v))
	case uint32:
		return slog.Uint64(key, uint64(v))
	case uint64:
		return slog.Uint64(key, v)
	case bool:
		return slog.Bool(key, v)
	case float64:
		return slog.Float64(key, v)
	case time.Time:
		return slog.Time(key, v)
	default:
		return slog.Any(key, val)
	}
}

// LogOutput returns the configured writers.
func (l *Slog) LogOutput() []io.Writer {
	return l.outputs
}

// Flush is a no-op for slog (unbuffered).
func (l *Slog) Flush() error {
	return nil
}

// StdLogger returns a *log.Logger that writes to this logger at its configured level.
func (l *Slog) StdLogger() *golog.Logger {
	return slog.NewLogLogger(l.logger.Handler(), l.slogLevel(l.level))
}

func toSlogLevel(level Level) slog.Level {
	switch level {
	case DebugLevel:
		return slog.LevelDebug
	case InfoLevel:
		return slog.LevelInfo
	case WarningLevel:
		return slog.LevelWarn
	case ErrorLevel:
		return slog.LevelError
	case FatalLevel, PanicLevel:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func (l *Slog) slogLevel(level Level) slog.Level {
	return toSlogLevel(level)
}
