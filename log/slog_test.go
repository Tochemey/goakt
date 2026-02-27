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
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// expectedSlogLevel returns the slog JSON output level string for a given Level.
// Matches zap format: lowercase ("info", "debug", "warn", "error").
func expectedSlogLevel(l Level) string {
	switch l {
	case DebugLevel:
		return "debug"
	case InfoLevel:
		return "info"
	case WarningLevel:
		return "warn"
	case ErrorLevel, FatalLevel, PanicLevel:
		return "error"
	default:
		return "info"
	}
}

func flushSlogLogger(t *testing.T, logger *Slog) {
	t.Helper()
	require.NoError(t, logger.Flush())
}

func extractSlogMessage(b []byte) (string, error) {
	var c map[string]json.RawMessage
	if err := json.Unmarshal(b, &c); err != nil {
		return "", err
	}
	if v, ok := c["msg"]; ok {
		return strconv.Unquote(string(v))
	}
	return "", nil
}

func extractSlogLevel(b []byte) (string, error) {
	var c map[string]json.RawMessage
	if err := json.Unmarshal(b, &c); err != nil {
		return "", err
	}
	if v, ok := c["level"]; ok {
		return strconv.Unquote(string(v))
	}
	return "", nil
}

func extractSlogLogLine(out []byte) []byte {
	for _, line := range bytes.Split(out, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if line[0] != '{' {
			continue
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(line, &payload); err != nil {
			continue
		}
		if _, ok := payload["msg"]; ok {
			return line
		}
	}
	return nil
}

func TestNewSlog(t *testing.T) {
	t.Run("defaults to stdout when no writers", func(t *testing.T) {
		logger := NewSlog(InfoLevel)
		require.NotNil(t, logger)
		require.NotEmpty(t, logger.LogOutput())
		require.Equal(t, os.Stdout, logger.LogOutput()[0])
	})

	t.Run("uses provided writers", func(t *testing.T) {
		buf := new(bytes.Buffer)
		logger := NewSlog(InfoLevel, buf)
		require.NotNil(t, logger)
		logger.Infof("hello")
		require.Contains(t, buf.String(), "hello")
	})

	t.Run("stores level as given", func(t *testing.T) {
		logger := NewSlog(Level(99))
		require.Equal(t, Level(99), logger.LogLevel())
	})
}

func TestSlogAddSourceOutputsCaller(t *testing.T) {
	var buf bytes.Buffer
	l := NewSlog(InfoLevel, &buf)
	l.Infof("test message")

	var m map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
	caller, ok := m["caller"]
	require.True(t, ok, "expected caller field in log output")
	callerStr, ok := caller.(string)
	require.True(t, ok)
	require.Contains(t, callerStr, "slog_test.go", "caller should reference actual call site (e.g. log/slog_test.go), not slog.go wrapper")
	require.Regexp(t, `:\d+$`, callerStr, "caller should end with :line")
}

func TestSlogDebug(t *testing.T) {
	t.Run("with debug level", func(t *testing.T) {
		buf := new(bytes.Buffer)
		logger := NewSlog(DebugLevel, buf)
		logger.Debug("test debug")
		flushSlogLogger(t, logger)

		msg, err := extractSlogMessage(buf.Bytes())
		require.NoError(t, err)
		require.Equal(t, "test debug", msg)

		lvl, err := extractSlogLevel(buf.Bytes())
		require.NoError(t, err)
		require.Equal(t, expectedSlogLevel(DebugLevel), lvl)
		require.Equal(t, DebugLevel, logger.LogLevel())

		buf.Reset()
		logger.Debugf("hello %s", "world")
		flushSlogLogger(t, logger)
		msg, err = extractSlogMessage(buf.Bytes())
		require.NoError(t, err)
		require.Equal(t, "hello world", msg)
	})

	t.Run("suppressed at info level", func(t *testing.T) {
		buf := new(bytes.Buffer)
		logger := NewSlog(InfoLevel, buf)
		logger.Debug("test debug")
		require.Empty(t, buf.String())
	})

	t.Run("suppressed at error level", func(t *testing.T) {
		buf := new(bytes.Buffer)
		logger := NewSlog(ErrorLevel, buf)
		logger.Debug("test debug")
		require.Empty(t, buf.String())
	})
}

func TestSlogInfo(t *testing.T) {
	t.Run("with info level", func(t *testing.T) {
		buf := new(bytes.Buffer)
		logger := NewSlog(InfoLevel, buf)
		logger.Info("test info")
		flushSlogLogger(t, logger)

		msg, err := extractSlogMessage(buf.Bytes())
		require.NoError(t, err)
		require.Equal(t, "test info", msg)

		lvl, err := extractSlogLevel(buf.Bytes())
		require.NoError(t, err)
		require.Equal(t, expectedSlogLevel(InfoLevel), lvl)

		buf.Reset()
		logger.Infof("hello %s", "world")
		flushSlogLogger(t, logger)
		msg, err = extractSlogMessage(buf.Bytes())
		require.NoError(t, err)
		require.Equal(t, "hello world", msg)
	})

	t.Run("with debug level", func(t *testing.T) {
		buf := new(bytes.Buffer)
		logger := NewSlog(DebugLevel, buf)
		logger.Info("test info")
		flushSlogLogger(t, logger)
		msg, err := extractSlogMessage(buf.Bytes())
		require.NoError(t, err)
		require.Equal(t, "test info", msg)
	})

	t.Run("suppressed at error level", func(t *testing.T) {
		buf := new(bytes.Buffer)
		logger := NewSlog(ErrorLevel, buf)
		logger.Info("test info")
		require.Empty(t, buf.String())
	})
}

func TestSlogWarn(t *testing.T) {
	t.Run("with warn level", func(t *testing.T) {
		buf := new(bytes.Buffer)
		logger := NewSlog(WarningLevel, buf)
		logger.Warn("test warn")
		flushSlogLogger(t, logger)

		msg, err := extractSlogMessage(buf.Bytes())
		require.NoError(t, err)
		require.Equal(t, "test warn", msg)

		lvl, err := extractSlogLevel(buf.Bytes())
		require.NoError(t, err)
		require.Equal(t, expectedSlogLevel(WarningLevel), lvl)

		buf.Reset()
		logger.Warnf("hello %s", "world")
		flushSlogLogger(t, logger)
		msg, err = extractSlogMessage(buf.Bytes())
		require.NoError(t, err)
		require.Equal(t, "hello world", msg)
	})

	t.Run("suppressed at error level", func(t *testing.T) {
		buf := new(bytes.Buffer)
		logger := NewSlog(ErrorLevel, buf)
		logger.Warn("test warn")
		require.Empty(t, buf.String())
	})
}

func TestSlogError(t *testing.T) {
	t.Run("with error level", func(t *testing.T) {
		buf := new(bytes.Buffer)
		logger := NewSlog(ErrorLevel, buf)
		logger.Error("test error")
		flushSlogLogger(t, logger)

		msg, err := extractSlogMessage(buf.Bytes())
		require.NoError(t, err)
		require.Equal(t, "test error", msg)

		lvl, err := extractSlogLevel(buf.Bytes())
		require.NoError(t, err)
		require.Equal(t, expectedSlogLevel(ErrorLevel), lvl)

		buf.Reset()
		logger.Errorf("hello %s", "world")
		flushSlogLogger(t, logger)
		msg, err = extractSlogMessage(buf.Bytes())
		require.NoError(t, err)
		require.Equal(t, "hello world", msg)
	})

	t.Run("with info level", func(t *testing.T) {
		buf := new(bytes.Buffer)
		logger := NewSlog(InfoLevel, buf)
		logger.Error("test error")
		flushSlogLogger(t, logger)
		msg, err := extractSlogMessage(buf.Bytes())
		require.NoError(t, err)
		require.Equal(t, "test error", msg)
	})
}

func TestSlogEnabled(t *testing.T) {
	buf := new(bytes.Buffer)
	logger := NewSlog(DebugLevel, buf)

	require.True(t, logger.Enabled(DebugLevel))
	require.True(t, logger.Enabled(InfoLevel))
	require.True(t, logger.Enabled(WarningLevel))
	require.True(t, logger.Enabled(ErrorLevel))
	require.True(t, logger.Enabled(FatalLevel))
	require.True(t, logger.Enabled(PanicLevel))

	loggerErr := NewSlog(ErrorLevel, buf)
	require.False(t, loggerErr.Enabled(DebugLevel))
	require.False(t, loggerErr.Enabled(InfoLevel))
	require.False(t, loggerErr.Enabled(WarningLevel))
	require.True(t, loggerErr.Enabled(ErrorLevel))
	require.True(t, loggerErr.Enabled(FatalLevel))
	require.True(t, loggerErr.Enabled(PanicLevel))
}

func TestSlogWith(t *testing.T) {
	t.Run("adds structured fields to output", func(t *testing.T) {
		buf := new(bytes.Buffer)
		logger := NewSlog(InfoLevel, buf)
		logger.With("actor", "user-service", "host", "127.0.0.1").Info("started successfully")
		flushSlogLogger(t, logger)

		var m map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
		msg, _ := extractSlogMessage(buf.Bytes())
		require.Equal(t, "started successfully", msg)
		require.Contains(t, m, "actor")
		require.Contains(t, m, "host")
	})

	t.Run("returns same logger when keyValues empty", func(t *testing.T) {
		buf := new(bytes.Buffer)
		logger := NewSlog(InfoLevel, buf)
		withLogger := logger.With()
		assert.Equal(t, logger, withLogger)
	})

	t.Run("odd keyValues uses _ for orphan", func(t *testing.T) {
		buf := new(bytes.Buffer)
		logger := NewSlog(InfoLevel, buf)
		logger.With("a", 1, "orphan").Info("msg")
		flushSlogLogger(t, logger)
		var m map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
		require.Contains(t, m, "a")
		require.Contains(t, m, "_")
	})

	t.Run("skips non-string keys", func(t *testing.T) {
		buf := new(bytes.Buffer)
		logger := NewSlog(InfoLevel, buf)
		sub := logger.With(42, "ignored", "k", "v")
		sub.Info("msg")
		flushSlogLogger(t, sub.(*Slog))
		var m map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
		require.Contains(t, m, "k")
	})

	t.Run("more than 8 pairs uses heap allocation", func(t *testing.T) {
		buf := new(bytes.Buffer)
		logger := NewSlog(InfoLevel, buf)
		sub := logger.With("a", 1, "b", 2, "c", 3, "d", 4, "e", 5, "f", 6, "g", 7, "h", 8, "i", 9)
		sub.Info("msg")
		flushSlogLogger(t, sub.(*Slog))
		var m map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
		require.Contains(t, m, "a")
		require.Contains(t, m, "i")
	})

	t.Run("all non-string keys returns same logger", func(t *testing.T) {
		buf := new(bytes.Buffer)
		logger := NewSlog(InfoLevel, buf)
		sub := logger.With(1, 2, 3, 4)
		assert.Equal(t, logger, sub)
	})

	t.Run("toSlogAttr type coverage", func(t *testing.T) {
		buf := new(bytes.Buffer)
		logger := NewSlog(InfoLevel, buf)
		sub := logger.With(
			"s", "str",
			"i", int(42),
			"i32", int32(32),
			"i64", int64(64),
			"u", uint(10),
			"u32", uint32(32),
			"u64", uint64(64),
			"b", true,
			"f", float64(3.14),
			"tm", time.Now(),
			"any", []int{1, 2},
		)
		sub.Info("typed fields")
		flushSlogLogger(t, sub.(*Slog))
		var m map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
		require.Contains(t, m, "s")
		require.Contains(t, m, "i")
		require.Contains(t, m, "i32")
		require.Contains(t, m, "i64")
		require.Contains(t, m, "u")
		require.Contains(t, m, "u32")
		require.Contains(t, m, "u64")
		require.Contains(t, m, "b")
		require.Contains(t, m, "f")
		require.Contains(t, m, "tm")
		require.Contains(t, m, "any")
	})
}

func TestSlogLogOutput(t *testing.T) {
	buf := new(bytes.Buffer)
	logger := NewSlog(InfoLevel, buf)
	outputs := logger.LogOutput()
	require.NotEmpty(t, outputs)
	require.Len(t, outputs, 1)
	require.Equal(t, buf, outputs[0])
}

func TestSlogLogLevel(t *testing.T) {
	buf := new(bytes.Buffer)
	logger := NewSlog(FatalLevel, buf)
	require.Equal(t, FatalLevel, logger.LogLevel())
}

func TestSlogPanic(t *testing.T) {
	buf := new(bytes.Buffer)
	logger := NewSlog(PanicLevel, buf)
	require.Equal(t, PanicLevel, logger.LogLevel())
	assert.Panics(t, func() {
		logger.Panic("test panic")
	})
	assert.Panics(t, func() {
		logger.Panicf("%s", "test panicf")
	})
}

func TestSlogFatal(t *testing.T) {
	if os.Getenv("GO_TEST_SLOG_FATAL") == "1" {
		logger := NewSlog(FatalLevel, os.Stdout)
		logger.Fatal("fatal message")
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestSlogFatalHelperProcess$", "-test.v") // #nosec G204 -- test helper subprocess
	cmd.Env = append(os.Environ(), "GO_TEST_SLOG_FATAL=1")

	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 1, exitErr.ExitCode())

	line := extractSlogLogLine(out)
	require.NotNil(t, line, "expected fatal log output")

	msg, err := extractSlogMessage(line)
	require.NoError(t, err)
	assert.Equal(t, "fatal message", msg)

	lvl, err := extractSlogLevel(line)
	require.NoError(t, err)
	assert.Equal(t, expectedSlogLevel(FatalLevel), lvl)
}

func TestSlogFatalHelperProcess(t *testing.T) {
	if os.Getenv("GO_TEST_SLOG_FATAL") != "1" {
		t.Skip("helper process")
	}
	logger := NewSlog(FatalLevel, os.Stdout)
	logger.Fatal("fatal message")
}

func TestSlogFatalf(t *testing.T) {
	if os.Getenv("GO_TEST_SLOG_FATALF") == "1" {
		logger := NewSlog(FatalLevel, os.Stdout)
		logger.Fatalf("fatal formatted %d", 42)
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestSlogFatalfHelperProcess$", "-test.v") // #nosec G204 -- test helper subprocess
	cmd.Env = append(os.Environ(), "GO_TEST_SLOG_FATALF=1")

	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 1, exitErr.ExitCode())

	line := extractSlogLogLine(out)
	require.NotNil(t, line, "expected fatalf log output")

	msg, err := extractSlogMessage(line)
	require.NoError(t, err)
	assert.Equal(t, "fatal formatted 42", msg)

	lvl, err := extractSlogLevel(line)
	require.NoError(t, err)
	assert.Equal(t, expectedSlogLevel(FatalLevel), lvl)
}

func TestSlogFatalfHelperProcess(t *testing.T) {
	if os.Getenv("GO_TEST_SLOG_FATALF") != "1" {
		t.Skip("helper process")
	}
	logger := NewSlog(FatalLevel, os.Stdout)
	logger.Fatalf("fatal formatted %d", 42)
}

func TestSlogStdLogger(t *testing.T) {
	buf := new(bytes.Buffer)
	logger := NewSlog(InfoLevel, buf)

	std := logger.StdLogger()
	require.NotNil(t, std)
	std.Print("std logger message")
	flushSlogLogger(t, logger)

	line := buf.Bytes()
	require.NotEmpty(t, line)

	msg, err := extractSlogMessage(line)
	require.NoError(t, err)
	assert.Equal(t, "std logger message", msg)

	lvl, err := extractSlogLevel(line)
	require.NoError(t, err)
	assert.Equal(t, expectedSlogLevel(InfoLevel), lvl)
}

func TestSlogFlush(t *testing.T) {
	buf := new(bytes.Buffer)
	logger := NewSlog(InfoLevel, buf)
	logger.Info("msg")
	require.NoError(t, logger.Flush())
}

func TestSlogTimestampFormat(t *testing.T) {
	buf := new(bytes.Buffer)
	logger := NewSlog(InfoLevel, buf)
	logger.Infof("test")

	var m map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
	ts, ok := m["ts"].(string)
	require.True(t, ok)
	require.Regexp(t, `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d+Z`, ts, "ts should match zap-style format")
}

func TestSlogFieldOrder(t *testing.T) {
	buf := new(bytes.Buffer)
	logger := NewSlog(InfoLevel, buf)
	logger.Infof("actor system=Remoting started")

	raw := buf.String()
	// Output order must be: level, ts, caller, msg (matching zap)
	require.Regexp(t, `^\{"level":`, raw, "level must be first")
	require.Regexp(t, `"ts":"[^"]*"`, raw, "ts must be present")
	require.Regexp(t, `"caller":"[^"]*"`, raw, "caller must be present")
	require.Regexp(t, `"msg":"[^"]*"\}`, raw, "msg must be last of fixed fields")
	// Verify order: level before ts before caller before msg
	levelPos := bytes.Index(buf.Bytes(), []byte(`"level":`))
	tsPos := bytes.Index(buf.Bytes(), []byte(`"ts":`))
	callerPos := bytes.Index(buf.Bytes(), []byte(`"caller":`))
	msgPos := bytes.Index(buf.Bytes(), []byte(`"msg":`))
	require.True(t, levelPos < tsPos && tsPos < callerPos && callerPos < msgPos,
		"field order must be level < ts < caller < msg, got level=%d ts=%d caller=%d msg=%d",
		levelPos, tsPos, callerPos, msgPos)
}

// TestSlogJSONEscaping exercises appendJSONEscaped paths: quotes, backslash, newline, tab, control chars, unicode.
func TestSlogJSONEscaping(t *testing.T) {
	buf := new(bytes.Buffer)
	logger := NewSlog(InfoLevel, buf)

	// Message with special chars that need JSON escaping
	logger.Infof("msg with \"quotes\" and \\backslash and \nnewline and \ttab")
	flushSlogLogger(t, logger)

	var m map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
	msg, ok := m["msg"].(string)
	require.True(t, ok)
	require.Contains(t, msg, "quotes")
	require.Contains(t, msg, "backslash")
	require.Contains(t, msg, "newline")
	require.Contains(t, msg, "tab")

	// Field value with special chars
	buf.Reset()
	logger.With("key", "val\"ue\nwith\tescapes").Info("plain")
	flushSlogLogger(t, logger)
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
	require.Contains(t, buf.String(), `"key":`)

	// Control chars (c < ' ') - triggers \u00XX escape
	buf.Reset()
	logger.With("ctrl", "a\x00b\x01").Info("control")
	flushSlogLogger(t, logger)
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m))

	// Unicode line/paragraph separators (U+2028, U+2029)
	buf.Reset()
	logger.With("unicode", "\u2028x\u2029").Info("separators")
	flushSlogLogger(t, logger)
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
}

// TestSlogAppendSlogValueError exercises KindAny with error type in appendSlogValue.
func TestSlogAppendSlogValueError(t *testing.T) {
	buf := new(bytes.Buffer)
	logger := NewSlog(InfoLevel, buf)

	err := errors.New("test error message")
	logger.With("err", err).Info("with error field")
	flushSlogLogger(t, logger)

	var m map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
	errVal, ok := m["err"].(string)
	require.True(t, ok)
	require.Equal(t, "test error message", errVal)
}

// TestSlogHandlerWithGroup exercises slogOrderedHandler.WithGroup.
func TestSlogHandlerWithGroup(t *testing.T) {
	buf := new(bytes.Buffer)
	levelVar := new(slog.LevelVar)
	levelVar.Set(slog.LevelInfo)

	base := newSlogOrderedHandler(buf, levelVar, nil)
	withGroup := base.WithGroup("group")
	require.NotNil(t, withGroup)

	logger := slog.New(withGroup)
	logger.Info("msg", "k", "v")
	require.NotEmpty(t, buf.String())
	require.Contains(t, buf.String(), "msg")
}

func BenchmarkSlogInfof(b *testing.B) {
	var buf bytes.Buffer
	logger := NewSlog(InfoLevel, &buf)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Infof("actor system=Remoting started")
	}
}

func BenchmarkSlogInfofWithFields(b *testing.B) {
	var buf bytes.Buffer
	logger := NewSlog(InfoLevel, &buf).With("actor", "Pong", "host", "127.0.0.1")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Infof("actor=Pong readiness probe completed")
	}
}
