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

const maxSlogAttrs = 8
const hex = "0123456789abcdef"

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

// NewSlogFrom creates a Logger backed by the provided slog.Logger instance.
// This allows sharing the same *slog.Logger across GoAkt and other packages
// in your system without coupling them to GoAkt's log package.
// The caller owns the slog.Logger and controls its output destinations.
func NewSlogFrom(logger *slog.Logger, level Level) *Slog {
	return &Slog{
		logger: logger,
		level:  level,
	}
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

// appendJSONEscaped appends s to buf with JSON string escaping (RFC 8259).
// Zero allocation for ASCII strings that need no escaping.
//
// Algorithm: scans one byte at a time through s.
//  1. ASCII fast path (c < RuneSelf): escapes `"`, `\`, `\n`, `\r`, `\t` with backslash;
//     control characters (< 0x20) are emitted as `\u00XX`; all others are written verbatim.
//  2. Multi-byte path: decodes a full rune via utf8.DecodeRuneInString.
//     - Invalid sequences (RuneError with size 1) become `\ufffd` (Unicode replacement).
//     - Line/paragraph separators (U+2028/U+2029) are escaped as `\u202X` because they
//     are valid JSON but break JavaScript string literals.
//     - All other valid runes are copied verbatim.
func appendJSONEscaped(buf *bytes.Buffer, s string) {
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if c == '"' || c == '\\' {
				_ = buf.WriteByte('\\')
				_ = buf.WriteByte(c)
				i++
				continue
			}
			if c == '\n' || c == '\r' || c == '\t' {
				_ = buf.WriteByte('\\')
				_ = buf.WriteByte(map[byte]byte{'\n': 'n', '\r': 'r', '\t': 't'}[c])
				i++
				continue
			}
			if c < ' ' {
				_, _ = buf.WriteString(`\u00`)
				_ = buf.WriteByte(hex[c>>4])
				_ = buf.WriteByte(hex[c&0xF])
				i++
				continue
			}
			_ = buf.WriteByte(c)
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			_, _ = buf.WriteString(`\ufffd`)
			i++
			continue
		}
		if r == '\u2028' || r == '\u2029' {
			_, _ = buf.WriteString(`\u202`)
			_ = buf.WriteByte(hex[r&0xF])
			i += size
			continue
		}
		_, _ = buf.WriteString(s[i : i+size])
		i += size
	}
}

// appendSlogValue serializes a slog.Value as JSON into buf without using encoding/json
// for primitive types (zero-alloc for strings, ints, bools, floats, durations, and times).
//
// Algorithm: dispatches on v.Kind():
//   - KindString: writes a JSON-escaped quoted string via appendJSONEscaped.
//   - KindInt64/KindUint64/KindFloat64: formats into a stack-allocated [24]byte scratch
//     buffer using strconv.Append*, avoiding heap allocation.
//   - KindBool: writes the literal "true" or "false".
//   - KindDuration: serialized as nanoseconds (int64), matching zap's convention.
//   - KindTime: formatted using slogTimeFormat into a stack-allocated [32]byte scratch.
//   - KindAny: errors are JSON-escaped strings; all other types fall back to json.Encoder
//     from a sync.Pool to amortize encoder allocation across calls.
//   - Unknown kinds: written as the JSON literal `null`.
func appendSlogValue(buf *bytes.Buffer, v slog.Value) {
	var scratch [24]byte
	switch v.Kind() {
	case slog.KindString:
		_ = buf.WriteByte('"')
		appendJSONEscaped(buf, v.String())
		_ = buf.WriteByte('"')
	case slog.KindInt64:
		_, _ = buf.Write(strconv.AppendInt(scratch[:0], v.Int64(), 10))
	case slog.KindUint64:
		_, _ = buf.Write(strconv.AppendUint(scratch[:0], v.Uint64(), 10))
	case slog.KindFloat64:
		_, _ = buf.Write(strconv.AppendFloat(scratch[:0], v.Float64(), 'g', -1, 64))
	case slog.KindBool:
		if v.Bool() {
			_, _ = buf.WriteString("true")
		} else {
			_, _ = buf.WriteString("false")
		}
	case slog.KindDuration:
		_, _ = buf.Write(strconv.AppendInt(scratch[:0], int64(v.Duration()), 10))
	case slog.KindTime:
		_ = buf.WriteByte('"')
		var tsScratch [32]byte
		_, _ = buf.Write(v.Time().AppendFormat(tsScratch[:0], slogTimeFormat))
		_ = buf.WriteByte('"')
	case slog.KindAny:
		a := v.Any()
		if err, ok := a.(error); ok {
			_ = buf.WriteByte('"')
			appendJSONEscaped(buf, err.Error())
			_ = buf.WriteByte('"')
		} else {
			je := jsonEncoderPool.Get().(*jsonEncoder)
			je.buf.Reset()
			_ = je.enc.Encode(a)
			bs := je.buf.Bytes()
			if len(bs) > 0 && bs[len(bs)-1] == '\n' {
				bs = bs[:len(bs)-1]
			}
			_, _ = buf.Write(bs)
			if je.buf.Cap() <= maxBufferPoolCap {
				je.buf.Reset()
				jsonEncoderPool.Put(je)
			}
		}
	default:
		_, _ = buf.WriteString(`null`)
	}
}

// callerAttr returns a slog.Attr with the caller's file:line in zap-style short format
// (e.g. "actor/scheduler.go:98"). skip is the number of stack frames to skip
// (3 = caller of our public API: Debug/Info/etc or DebugContext/InfoContext/etc).
//
// Algorithm: uses runtime.Caller to obtain the program counter (PC), then checks
// callerCache (a sync.Map keyed by PC). On cache hit, returns the cached Attr directly
// (zero alloc). On cache miss, builds the caller string via buildCallerString and stores
// it in the cache for future calls. The cache is never evicted, which is safe because
// the set of distinct call sites in a program is bounded and small.
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

// buildCallerString produces a short "parent/file.go:line" string from a full file path.
//
// Algorithm: scans backwards from the end of file to find the last two path separators
// ('/' or '\\'). This yields "parentDir/base.go". The line number is appended using
// strconv.AppendInt into a stack-allocated [12]byte scratch buffer to avoid heap allocation.
// The string is assembled in a pooled bytes.Buffer (bufferPool) to amortize allocation
// across calls. If there is no parent directory, returns "file.go:line" directly.
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
		_, _ = buf.WriteString(file[prevSlash+1 : lastSlash])
		_ = buf.WriteByte('/')
	}
	_, _ = buf.WriteString(base)
	_ = buf.WriteByte(':')
	_, _ = buf.Write(strconv.AppendInt(scratch[:0], int64(line), 10))
	return buf.String()
}

// Debug logs at debug level.
func (sl *Slog) Debug(v ...any) {
	sl.DebugContext(context.Background(), v...)
}

// Debugf logs a formatted message at debug level.
func (sl *Slog) Debugf(format string, v ...any) {
	sl.DebugfContext(context.Background(), format, v...)
}

// DebugContext logs at debug level with the given context.
func (sl *Slog) DebugContext(ctx context.Context, v ...any) {
	if sl.Enabled(DebugLevel) {
		sl.logger.LogAttrs(ctx, slog.LevelDebug, fmt.Sprint(v...), callerAttr(3))
	}
}

// DebugfContext logs a formatted message at debug level with the given context.
func (sl *Slog) DebugfContext(ctx context.Context, format string, v ...any) {
	if sl.Enabled(DebugLevel) {
		sl.logger.LogAttrs(ctx, slog.LevelDebug, formatMsg(format, v), callerAttr(3))
	}
}

// Info logs at info level.
func (sl *Slog) Info(v ...any) {
	sl.InfoContext(context.Background(), v...)
}

// Infof logs a formatted message at info level.
func (sl *Slog) Infof(format string, v ...any) {
	sl.InfofContext(context.Background(), format, v...)
}

// InfoContext logs at info level with the given context.
func (sl *Slog) InfoContext(ctx context.Context, v ...any) {
	if sl.Enabled(InfoLevel) {
		sl.logger.LogAttrs(ctx, slog.LevelInfo, fmt.Sprint(v...), callerAttr(3))
	}
}

// InfofContext logs a formatted message at info level with the given context.
func (sl *Slog) InfofContext(ctx context.Context, format string, v ...any) {
	if sl.Enabled(InfoLevel) {
		sl.logger.LogAttrs(ctx, slog.LevelInfo, formatMsg(format, v), callerAttr(3))
	}
}

// Warn logs at warn level.
func (sl *Slog) Warn(v ...any) {
	sl.WarnContext(context.Background(), v...)
}

// Warnf logs a formatted message at warn level.
func (sl *Slog) Warnf(format string, v ...any) {
	sl.WarnfContext(context.Background(), format, v...)
}

// WarnContext logs at warn level with the given context.
func (sl *Slog) WarnContext(ctx context.Context, v ...any) {
	if sl.Enabled(WarningLevel) {
		sl.logger.LogAttrs(ctx, slog.LevelWarn, fmt.Sprint(v...), callerAttr(3))
	}
}

// WarnfContext logs a formatted message at warn level with the given context.
func (sl *Slog) WarnfContext(ctx context.Context, format string, v ...any) {
	if sl.Enabled(WarningLevel) {
		sl.logger.LogAttrs(ctx, slog.LevelWarn, formatMsg(format, v), callerAttr(3))
	}
}

// Error logs at error level.
func (sl *Slog) Error(v ...any) {
	sl.ErrorContext(context.Background(), v...)
}

// Errorf logs a formatted message at error level.
func (sl *Slog) Errorf(format string, v ...any) {
	sl.ErrorfContext(context.Background(), format, v...)
}

// ErrorContext logs at error level with the given context.
func (sl *Slog) ErrorContext(ctx context.Context, v ...any) {
	if sl.Enabled(ErrorLevel) {
		sl.logger.LogAttrs(ctx, slog.LevelError, fmt.Sprint(v...), callerAttr(3))
	}
}

// ErrorfContext logs a formatted message at error level with the given context.
func (sl *Slog) ErrorfContext(ctx context.Context, format string, v ...any) {
	if sl.Enabled(ErrorLevel) {
		sl.logger.LogAttrs(ctx, slog.LevelError, formatMsg(format, v), callerAttr(3))
	}
}

// Fatal logs at error level and exits.
func (sl *Slog) Fatal(v ...any) {
	sl.logger.LogAttrs(context.Background(), slog.LevelError, fmt.Sprint(v...), callerAttr(3))
	os.Exit(1)
}

// Fatalf logs a formatted message at error level and exits.
func (sl *Slog) Fatalf(format string, v ...any) {
	sl.logger.LogAttrs(context.Background(), slog.LevelError, formatMsg(format, v), callerAttr(3))
	os.Exit(1)
}

// Panic logs at error level and panics.
func (sl *Slog) Panic(v ...any) {
	msg := fmt.Sprint(v...)
	sl.logger.LogAttrs(context.Background(), slog.LevelError, msg, callerAttr(3))
	panic(msg)
}

// Panicf logs a formatted message at error level and panics.
func (sl *Slog) Panicf(format string, v ...any) {
	msg := formatMsg(format, v)
	sl.logger.LogAttrs(context.Background(), slog.LevelError, msg, callerAttr(3))
	panic(msg)
}

// Enabled reports whether the given level is enabled.
func (sl *Slog) Enabled(level Level) bool {
	return sl.logger.Enabled(context.Background(), sl.slogLevel(level))
}

// LogLevel returns the configured minimum level.
func (sl *Slog) LogLevel() Level {
	return sl.level
}

// With returns a new Logger that includes the given key-value pairs in every
// subsequent log entry. Keys and values alternate: ("key1", val1, "key2", val2, ...).
// Keys must be strings; non-string keys are silently skipped. An odd trailing
// value is stored under the key "_".
//
// Algorithm: converts the key-value pairs to typed slog.Attr via toSlogAttrs
// (avoiding reflection for common Go types). Then flattens the attrs back into
// alternating (key, value) args for slog.Logger.With. For small attr counts
// (<=8 pairs / 16 args), a stack-allocated [16]any array is used to avoid a
// heap allocation for the args slice.
func (sl *Slog) With(keyValues ...any) Logger {
	attrs := toSlogAttrs(keyValues)
	if len(attrs) == 0 {
		return sl
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
			logger:  sl.logger.With(buf[:n]...),
			level:   sl.level,
			outputs: sl.outputs,
		}
	}
	args := make([]any, 0, n)
	for _, a := range attrs {
		args = append(args, a.Key, a.Value.Any())
	}
	return &Slog{
		logger:  sl.logger.With(args...),
		level:   sl.level,
		outputs: sl.outputs,
	}
}

// toSlogAttrs converts alternating key-value pairs into typed slog.Attr slices.
//
// Algorithm: iterates in steps of 2. For <=8 pairs, uses a stack-allocated
// [maxSlogAttrs]slog.Attr array to avoid heap allocation. Each value is dispatched
// through toSlogAttr which type-switches on common Go types (string, int, int64,
// bool, float64, time.Time, etc.) to produce typed slog.Attr constructors, falling
// back to slog.Any for unknown types. Non-string keys are skipped; an odd trailing
// value is stored under the placeholder key "_".
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

// toSlogAttr converts a single key-value pair into a typed slog.Attr.
// Uses a type switch to call the most specific slog constructor (e.g. slog.String,
// slog.Int64) which avoids boxing the value into an interface when the handler
// later reads it. Falls back to slog.Any for unrecognized types.
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
func (sl *Slog) LogOutput() []io.Writer {
	return sl.outputs
}

// Flush is a no-op for slog because writes go directly to the underlying io.Writer
// without intermediate buffering. Always returns nil.
func (sl *Slog) Flush() error {
	return nil
}

// StdLogger returns a standard library *log.Logger that writes through this Slog
// instance at its configured minimum level. Useful for passing to third-party
// libraries that accept a *log.Logger.
func (sl *Slog) StdLogger() *golog.Logger {
	return slog.NewLogLogger(sl.logger.Handler(), sl.slogLevel(sl.level))
}

// toSlogLevel maps a GoAkt log.Level to the corresponding slog.Level.
// FatalLevel and PanicLevel both map to slog.LevelError since slog has no
// fatal/panic severity; the actual exit/panic behavior is handled by the
// caller (Fatal/Panic methods). Unknown levels default to slog.LevelInfo.
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

// slogLevel is a receiver method that delegates to toSlogLevel for use in
// methods that need the conversion as a method expression.
func (sl *Slog) slogLevel(level Level) slog.Level {
	return toSlogLevel(level)
}

// formatMsg returns the formatted message using fmt.Sprintf.
// Optimization: when v is empty and format contains no '%' verb, returns format
// directly without calling Sprintf, avoiding an unnecessary allocation.
func formatMsg(format string, v []any) string {
	if len(v) == 0 && !strings.Contains(format, "%") {
		return format
	}
	return fmt.Sprintf(format, v...)
}

// slogOrderedHandler is a custom slog.Handler that emits JSON log lines with a
// deterministic field order matching zap's convention: level, ts, caller, msg,
// then any additional attributes. This is important for log aggregation tools
// that expect consistent field ordering.
//
// Thread safety: all writes to w are serialized via mu. The mutex is shared
// across handlers derived via WithAttrs/WithGroup so that interleaved writes
// from concurrent goroutines produce complete (non-garbled) JSON lines.
type slogOrderedHandler struct {
	mu     *sync.Mutex
	w      io.Writer
	level  slog.Leveler
	attrs  []slog.Attr
	groups []string
}

// newSlogOrderedHandler creates a new slogOrderedHandler that writes JSON to w,
// filtering out records below the given level. Initial attrs are included in
// every log line (useful for pre-set fields like service name).
func newSlogOrderedHandler(w io.Writer, level slog.Leveler, attrs []slog.Attr) *slogOrderedHandler {
	return &slogOrderedHandler{
		mu:     &sync.Mutex{},
		w:      w,
		level:  level,
		attrs:  attrs,
		groups: nil,
	}
}

// Enabled reports whether the handler is configured to log at the given level.
// Returns true when level >= the handler's minimum level (defaults to Info).
func (h *slogOrderedHandler) Enabled(_ context.Context, level slog.Level) bool {
	minLevel := slog.LevelInfo
	if h.level != nil {
		minLevel = h.level.Level()
	}
	return level >= minLevel
}

// WithAttrs returns a new handler whose output includes the given attrs in
// addition to any attrs already set. The new handler shares the same mutex and
// writer as the parent, ensuring serialized output.
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

// WithGroup returns a new handler that nests subsequent attrs under the given
// group name. The new handler shares the same mutex and writer as the parent.
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

// Handle serializes a slog.Record as a single JSON line and writes it to h.w.
//
// Algorithm:
//  1. Acquires a bytes.Buffer from bufferPool to build the JSON line (amortizes alloc).
//  2. Extracts the "caller" attr from the record's attrs (set by callerAttr) and
//     removes it from the general attr list so it can be placed in a fixed position.
//  3. Writes the four fixed fields in zap-compatible order:
//     {"level":"...","ts":"...","caller":"...","msg":"..."}
//  4. Appends any remaining attrs as additional JSON fields, skipping duplicates
//     of the fixed keys (caller, time, level, msg) and empty attrs.
//  5. Each field value is serialized via appendSlogValue (zero-alloc for primitives).
//  6. Terminates the line with "}\n" and writes the complete buffer to h.w under
//     the shared mutex to prevent interleaved output from concurrent goroutines.
//  7. Returns the buffer to the pool if it hasn't grown beyond maxBufferPoolCap (16KB).
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
