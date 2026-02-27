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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	golog "log"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// SlogLog implements Logger using the standard library slog package.
// It is optimized for low GC: Enabled() checks avoid formatting when disabled,
// and With() uses typed slog.Attr for common types to avoid reflection.
var _ Logger = (*Slog)(nil)

// toSlogAttrs converts key-value pairs to slog.Attr using typed accessors.
const maxSlogAttrs = 8

const (
	bufferPoolSize   = 256
	maxBufferPoolCap = 16 << 10 // don't return buffers larger than 16KB to pool
	slogTimeFormat   = "2006-01-02T15:04:05.000000Z0700"
)

var bufferPool = sync.Pool{
	New: func() any {
		b := bytes.NewBuffer(make([]byte, 0, bufferPoolSize))
		return b
	},
}

type jsonEncoder struct {
	buf *bytes.Buffer
	enc *json.Encoder
}

var jsonEncoderPool = sync.Pool{
	New: func() any {
		buf := bytes.NewBuffer(make([]byte, 0, 256))
		enc := json.NewEncoder(buf)
		enc.SetEscapeHTML(false)
		return &jsonEncoder{buf: buf, enc: enc}
	},
}

// slogLevelStrings maps slog.Level to lowercase string. Avoids Level.String() + ToLower allocations.
var slogLevelStrings = map[slog.Level]string{
	slog.LevelDebug: "debug",
	slog.LevelInfo:  "info",
	slog.LevelWarn:  "warn",
	slog.LevelError: "error",
}

// callerCache caches caller slog.Attr by PC to avoid repeated runtime.Caller and string building.
var callerCache sync.Map // map[uintptr]slog.Attr

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
	handler := newSlogOrderedHandler(w, toSlogLevel(level), nil)
	return &Slog{
		logger:  slog.New(handler),
		level:   level,
		outputs: writers,
	}
}

// appendJSONEscaped appends s to buf with JSON string escaping. Zero alloc for strings
// that need no escaping; otherwise escapes in-place into buf.
func appendJSONEscaped(buf *bytes.Buffer, s string) {
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if c == '"' || c == '\\' {
				buf.WriteByte('\\')
				buf.WriteByte(c)
				i++
				continue
			}
			if c == '\n' || c == '\r' || c == '\t' {
				buf.WriteByte('\\')
				buf.WriteByte(map[byte]byte{'\n': 'n', '\r': 'r', '\t': 't'}[c])
				i++
				continue
			}
			if c < ' ' {
				buf.WriteString(`\u00`)
				buf.WriteByte(hex[c>>4])
				buf.WriteByte(hex[c&0xF])
				i++
				continue
			}
			buf.WriteByte(c)
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			buf.WriteString(`\ufffd`)
			i++
			continue
		}
		if r == '\u2028' || r == '\u2029' {
			buf.WriteString(`\u202`)
			buf.WriteByte(hex[r&0xF])
			i += size
			continue
		}
		buf.WriteString(s[i : i+size])
		i += size
	}
}

const hex = "0123456789abcdef"

func appendSlogValue(buf *bytes.Buffer, v slog.Value) {
	var scratch [24]byte
	switch v.Kind() {
	case slog.KindString:
		buf.WriteByte('"')
		appendJSONEscaped(buf, v.String())
		buf.WriteByte('"')
	case slog.KindInt64:
		buf.Write(strconv.AppendInt(scratch[:0], v.Int64(), 10))
	case slog.KindUint64:
		buf.Write(strconv.AppendUint(scratch[:0], v.Uint64(), 10))
	case slog.KindFloat64:
		buf.Write(strconv.AppendFloat(scratch[:0], v.Float64(), 'g', -1, 64))
	case slog.KindBool:
		if v.Bool() {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case slog.KindDuration:
		buf.Write(strconv.AppendInt(scratch[:0], int64(v.Duration()), 10))
	case slog.KindTime:
		buf.WriteByte('"')
		var tsScratch [32]byte
		buf.Write(v.Time().AppendFormat(tsScratch[:0], slogTimeFormat))
		buf.WriteByte('"')
	case slog.KindAny:
		a := v.Any()
		if err, ok := a.(error); ok {
			buf.WriteByte('"')
			appendJSONEscaped(buf, err.Error())
			buf.WriteByte('"')
		} else {
			je := jsonEncoderPool.Get().(*jsonEncoder)
			je.buf.Reset()
			_ = je.enc.Encode(a)
			bs := je.buf.Bytes()
			if len(bs) > 0 && bs[len(bs)-1] == '\n' {
				bs = bs[:len(bs)-1]
			}
			buf.Write(bs)
			if je.buf.Cap() <= maxBufferPoolCap {
				je.buf.Reset()
				jsonEncoderPool.Put(je)
			}
		}
	default:
		buf.WriteString(`null`)
	}
}

// callerAttr returns a slog.Attr with the caller's file:line in zap-style format
// (e.g. "actor/scheduler.go:98"). skip is the number of stack frames to skip
// (3 = caller of our public API: Debug/Info/etc or DebugContext/InfoContext/etc).
func callerAttr(skip int) slog.Attr {
	pc, file, line, ok := runtime.Caller(skip)
	if !ok {
		return slog.String("caller", "???")
	}
	if cached, ok := callerCache.Load(pc); ok {
		return cached.(slog.Attr)
	}
	attr := slog.String("caller", buildCallerString(file, line))
	callerCache.Store(pc, attr)
	return attr
}

func buildCallerString(file string, line int) string {
	lastSlash := -1
	for i := len(file) - 1; i >= 0; i-- {
		if file[i] == '/' || file[i] == '\\' {
			lastSlash = i
			break
		}
	}
	if lastSlash < 0 {
		var scratch [12]byte
		return file + ":" + string(strconv.AppendInt(scratch[:0], int64(line), 10))
	}
	base := file[lastSlash+1:]
	prevSlash := -1
	for i := lastSlash - 1; i >= 0; i-- {
		if file[i] == '/' || file[i] == '\\' {
			prevSlash = i
			break
		}
	}
	buf := bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer func() {
		if buf.Cap() <= maxBufferPoolCap {
			buf.Reset()
			bufferPool.Put(buf)
		}
	}()
	var scratch [12]byte
	if prevSlash >= 0 {
		buf.WriteString(file[prevSlash+1 : lastSlash])
		buf.WriteByte('/')
	}
	buf.WriteString(base)
	buf.WriteByte(':')
	buf.Write(strconv.AppendInt(scratch[:0], int64(line), 10))
	return buf.String()
}

// Debug logs at debug level.
func (l *Slog) Debug(v ...any) {
	l.DebugContext(context.Background(), v...)
}

// Debugf logs a formatted message at debug level.
func (l *Slog) Debugf(format string, v ...any) {
	l.DebugfContext(context.Background(), format, v...)
}

// DebugContext logs at debug level with the given context.
func (l *Slog) DebugContext(ctx context.Context, v ...any) {
	if l.Enabled(DebugLevel) {
		l.logger.LogAttrs(ctx, slog.LevelDebug, fmt.Sprint(v...), callerAttr(3))
	}
}

// DebugfContext logs a formatted message at debug level with the given context.
func (l *Slog) DebugfContext(ctx context.Context, format string, v ...any) {
	if l.Enabled(DebugLevel) {
		l.logger.LogAttrs(ctx, slog.LevelDebug, formatMsg(format, v), callerAttr(3))
	}
}

// Info logs at info level.
func (l *Slog) Info(v ...any) {
	l.InfoContext(context.Background(), v...)
}

// Infof logs a formatted message at info level.
func (l *Slog) Infof(format string, v ...any) {
	l.InfofContext(context.Background(), format, v...)
}

// InfoContext logs at info level with the given context.
func (l *Slog) InfoContext(ctx context.Context, v ...any) {
	if l.Enabled(InfoLevel) {
		l.logger.LogAttrs(ctx, slog.LevelInfo, fmt.Sprint(v...), callerAttr(3))
	}
}

// InfofContext logs a formatted message at info level with the given context.
func (l *Slog) InfofContext(ctx context.Context, format string, v ...any) {
	if l.Enabled(InfoLevel) {
		l.logger.LogAttrs(ctx, slog.LevelInfo, formatMsg(format, v), callerAttr(3))
	}
}

// Warn logs at warn level.
func (l *Slog) Warn(v ...any) {
	l.WarnContext(context.Background(), v...)
}

// Warnf logs a formatted message at warn level.
func (l *Slog) Warnf(format string, v ...any) {
	l.WarnfContext(context.Background(), format, v...)
}

// WarnContext logs at warn level with the given context.
func (l *Slog) WarnContext(ctx context.Context, v ...any) {
	if l.Enabled(WarningLevel) {
		l.logger.LogAttrs(ctx, slog.LevelWarn, fmt.Sprint(v...), callerAttr(3))
	}
}

// WarnfContext logs a formatted message at warn level with the given context.
func (l *Slog) WarnfContext(ctx context.Context, format string, v ...any) {
	if l.Enabled(WarningLevel) {
		l.logger.LogAttrs(ctx, slog.LevelWarn, formatMsg(format, v), callerAttr(3))
	}
}

// Error logs at error level.
func (l *Slog) Error(v ...any) {
	l.ErrorContext(context.Background(), v...)
}

// Errorf logs a formatted message at error level.
func (l *Slog) Errorf(format string, v ...any) {
	l.ErrorfContext(context.Background(), format, v...)
}

// ErrorContext logs at error level with the given context.
func (l *Slog) ErrorContext(ctx context.Context, v ...any) {
	if l.Enabled(ErrorLevel) {
		l.logger.LogAttrs(ctx, slog.LevelError, fmt.Sprint(v...), callerAttr(3))
	}
}

// ErrorfContext logs a formatted message at error level with the given context.
func (l *Slog) ErrorfContext(ctx context.Context, format string, v ...any) {
	if l.Enabled(ErrorLevel) {
		l.logger.LogAttrs(ctx, slog.LevelError, formatMsg(format, v), callerAttr(3))
	}
}

// Fatal logs at error level and exits.
func (l *Slog) Fatal(v ...any) {
	l.logger.LogAttrs(context.Background(), slog.LevelError, fmt.Sprint(v...), callerAttr(3))
	os.Exit(1)
}

// Fatalf logs a formatted message at error level and exits.
func (l *Slog) Fatalf(format string, v ...any) {
	l.logger.LogAttrs(context.Background(), slog.LevelError, formatMsg(format, v), callerAttr(3))
	os.Exit(1)
}

// Panic logs at error level and panics.
func (l *Slog) Panic(v ...any) {
	msg := fmt.Sprint(v...)
	l.logger.LogAttrs(context.Background(), slog.LevelError, msg, callerAttr(3))
	panic(msg)
}

// Panicf logs a formatted message at error level and panics.
func (l *Slog) Panicf(format string, v ...any) {
	msg := formatMsg(format, v)
	l.logger.LogAttrs(context.Background(), slog.LevelError, msg, callerAttr(3))
	panic(msg)
}

// Enabled reports whether the given level is enabled.
func (l *Slog) Enabled(level Level) bool {
	return l.logger.Enabled(context.Background(), l.slogLevel(level))
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

// formatMsg returns the formatted message. Avoids fmt.Sprintf alloc when format has no verbs and no args.
func formatMsg(format string, v []any) string {
	if len(v) == 0 && !strings.Contains(format, "%") {
		return format
	}
	return fmt.Sprintf(format, v...)
}

// slogOrderedHandler is a slog.Handler that outputs JSON with field order matching zap:
// level, ts, caller, msg, then other attrs.
type slogOrderedHandler struct {
	mu     *sync.Mutex
	w      io.Writer
	level  slog.Leveler
	attrs  []slog.Attr
	groups []string
}

func newSlogOrderedHandler(w io.Writer, level slog.Leveler, attrs []slog.Attr) *slogOrderedHandler {
	return &slogOrderedHandler{
		mu:     &sync.Mutex{},
		w:      w,
		level:  level,
		attrs:  attrs,
		groups: nil,
	}
}

func (h *slogOrderedHandler) Enabled(_ context.Context, level slog.Level) bool {
	minLevel := slog.LevelInfo
	if h.level != nil {
		minLevel = h.level.Level()
	}
	return level >= minLevel
}

func (h *slogOrderedHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	newAttrs = append(newAttrs, h.attrs...)
	newAttrs = append(newAttrs, attrs...)
	return &slogOrderedHandler{
		mu:     h.mu,
		w:      h.w,
		level:  h.level,
		attrs:  newAttrs,
		groups: h.groups,
	}
}

func (h *slogOrderedHandler) WithGroup(name string) slog.Handler {
	newGroups := make([]string, 0, len(h.groups)+1)
	newGroups = append(newGroups, h.groups...)
	newGroups = append(newGroups, name)
	return &slogOrderedHandler{
		mu:     h.mu,
		w:      h.w,
		level:  h.level,
		attrs:  h.attrs,
		groups: newGroups,
	}
}

func (h *slogOrderedHandler) Handle(_ context.Context, r slog.Record) error {
	buf := bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer func() {
		if buf.Cap() <= maxBufferPoolCap {
			buf.Reset()
			bufferPool.Put(buf)
		}
	}()

	levelStr := slogLevelStrings[r.Level]
	if levelStr == "" {
		levelStr = "info"
	}
	callerStr := "???"
	msgStr := r.Message

	var allAttrs []slog.Attr
	if len(h.attrs) > 0 {
		allAttrs = make([]slog.Attr, len(h.attrs), len(h.attrs)+8)
		copy(allAttrs, h.attrs)
	}
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "caller" && a.Value.Kind() == slog.KindString {
			callerStr = a.Value.String()
			return true
		}
		allAttrs = append(allAttrs, a)
		return true
	})

	buf.WriteString(`{"level":"`)
	appendJSONEscaped(buf, levelStr)
	buf.WriteString(`","ts":"`)
	var tsScratch [32]byte
	buf.Write(r.Time.AppendFormat(tsScratch[:0], slogTimeFormat))
	buf.WriteString(`","caller":"`)
	appendJSONEscaped(buf, callerStr)
	buf.WriteString(`","msg":"`)
	appendJSONEscaped(buf, msgStr)
	buf.WriteByte('"')

	for _, a := range allAttrs {
		if a.Key == "caller" || a.Key == slog.TimeKey || a.Key == slog.LevelKey || a.Key == slog.MessageKey {
			continue
		}
		if a.Equal(slog.Attr{}) {
			continue
		}
		buf.WriteString(`,"`)
		appendJSONEscaped(buf, a.Key)
		buf.WriteString(`":`)
		appendSlogValue(buf, a.Value)
	}
	buf.WriteString("}\n")

	h.mu.Lock()
	_, err := h.w.Write(buf.Bytes())
	h.mu.Unlock()
	return err
}
