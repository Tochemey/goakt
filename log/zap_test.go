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
	"io"
	"os"
	"os/exec"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestDefaultLogger(t *testing.T) {
	// create a bytes buffer that implements an io.Writer
	buffer := new(bytes.Buffer)
	// create an instance of Log with a fake level value
	logger := NewZap(7, buffer)

	require.Equal(t, DebugLevel, logger.LogLevel())

	// assert Debug log
	logger.Debug("test debug")
	flushLogger(t, logger)
	expected := "test debug"
	actual, err := extractMessage(buffer.Bytes())
	require.NoError(t, err)
	require.Equal(t, expected, actual)

	lvl, err := extractLevel(buffer.Bytes())
	require.NoError(t, err)
	require.Equal(t, DebugLevel.String(), lvl)
	require.Equal(t, DebugLevel, logger.LogLevel())
}

func TestLogWith(t *testing.T) {
	t.Run("Log.With adds structured fields to output", func(t *testing.T) {
		buffer := new(bytes.Buffer)
		logger := NewZap(InfoLevel, buffer)
		logger.With("actor", "user-service", "host", "127.0.0.1").Info("started successfully")
		flushLogger(t, logger)

		var m map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(buffer.Bytes(), &m))
		msg, _ := extractMessage(buffer.Bytes())
		require.Equal(t, "started successfully", msg)
		require.Contains(t, m, "actor")
		require.Contains(t, m, "host")
	})

	t.Run("Log.With returns same logger when keyValues empty", func(t *testing.T) {
		buffer := new(bytes.Buffer)
		logger := NewZap(InfoLevel, buffer)
		withLogger := logger.With()
		assert.Equal(t, logger, withLogger)
	})

	t.Run("DiscardLogger.With returns DiscardLogger", func(t *testing.T) {
		result := DiscardLogger.With("actor", "test")
		assert.Equal(t, DiscardLogger, result)
	})

	t.Run("Log.With odd keyValues uses _ for orphan", func(t *testing.T) {
		buffer := new(bytes.Buffer)
		logger := NewZap(InfoLevel, buffer)
		logger.With("a", 1, "orphan").Info("msg")
		flushLogger(t, logger)
		var m map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(buffer.Bytes(), &m))
		require.Contains(t, m, "a")
		require.Contains(t, m, "_")
	})

	t.Run("Log.With skips non-string keys", func(t *testing.T) {
		buffer := new(bytes.Buffer)
		logger := NewZap(InfoLevel, buffer)
		sub := logger.With(42, "ignored", "k", "v")
		sub.Info("msg")
		flushLogger(t, sub.(*Zap))
		var m map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(buffer.Bytes(), &m))
		require.Contains(t, m, "k")
	})

	t.Run("Log.With more than 6 pairs uses heap allocation", func(t *testing.T) {
		buffer := new(bytes.Buffer)
		logger := NewZap(InfoLevel, buffer)
		sub := logger.With("a", 1, "b", 2, "c", 3, "d", 4, "e", 5, "f", 6, "g", 7)
		sub.Info("msg")
		flushLogger(t, sub.(*Zap))
		var m map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(buffer.Bytes(), &m))
		require.Contains(t, m, "a")
		require.Contains(t, m, "g")
	})

	t.Run("Log.With all non-string keys returns same logger", func(t *testing.T) {
		buffer := new(bytes.Buffer)
		logger := NewZap(InfoLevel, buffer)
		sub := logger.With(1, 2, 3, 4)
		assert.Equal(t, logger, sub)
	})

	t.Run("Log.With toZapField type coverage", func(t *testing.T) {
		buffer := new(bytes.Buffer)
		logger := NewZap(InfoLevel, buffer)
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
			"any", []int{1, 2},
		)
		sub.Info("typed fields")
		flushLogger(t, sub.(*Zap))
		var m map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(buffer.Bytes(), &m))
		require.Contains(t, m, "s")
		require.Contains(t, m, "i")
		require.Contains(t, m, "i32")
		require.Contains(t, m, "i64")
		require.Contains(t, m, "u")
		require.Contains(t, m, "u32")
		require.Contains(t, m, "u64")
		require.Contains(t, m, "b")
		require.Contains(t, m, "f")
		require.Contains(t, m, "any")
	})
}

func TestDiscardLoggerNoOps(t *testing.T) {
	// DiscardLogger no-op methods (Debug, Info, Warn, Error) - call for coverage
	DiscardLogger.Debug("discarded")
	DiscardLogger.Debugf("discarded %s", "msg")
	DiscardLogger.Info("discarded")
	DiscardLogger.Infof("discarded %s", "msg")
	DiscardLogger.Warn("discarded")
	DiscardLogger.Warnf("discarded %s", "msg")
	DiscardLogger.Error("discarded")
	DiscardLogger.Errorf("discarded %s", "msg")

	assert.Equal(t, InfoLevel, DiscardLogger.LogLevel())
	assert.False(t, DiscardLogger.Enabled(DebugLevel))
	assert.False(t, DiscardLogger.Enabled(InfoLevel))
	assert.True(t, DiscardLogger.Enabled(FatalLevel))
	assert.True(t, DiscardLogger.Enabled(PanicLevel))
	assert.NotEmpty(t, DiscardLogger.LogOutput())
	require.NoError(t, DiscardLogger.Flush())
	require.NotNil(t, DiscardLogger.StdLogger())
}

func TestLogEnabled(t *testing.T) {
	buffer := new(bytes.Buffer)
	logger := NewZap(DebugLevel, buffer)

	// At DebugLevel, all levels are enabled
	assert.True(t, logger.Enabled(DebugLevel))
	assert.True(t, logger.Enabled(InfoLevel))
	assert.True(t, logger.Enabled(WarningLevel))
	assert.True(t, logger.Enabled(ErrorLevel))
	assert.True(t, logger.Enabled(FatalLevel))
	assert.True(t, logger.Enabled(PanicLevel))

	// At ErrorLevel, only Error and above
	loggerErr := NewZap(ErrorLevel, buffer)
	assert.False(t, loggerErr.Enabled(DebugLevel))
	assert.False(t, loggerErr.Enabled(InfoLevel))
	assert.False(t, loggerErr.Enabled(WarningLevel))
	assert.True(t, loggerErr.Enabled(ErrorLevel))
	assert.True(t, loggerErr.Enabled(FatalLevel))
	assert.True(t, loggerErr.Enabled(PanicLevel))
}

func TestDebug(t *testing.T) {
	t.Run("With Debug log level", func(t *testing.T) {
		// create a bytes buffer that implements an io.Writer
		buffer := new(bytes.Buffer)
		// create an instance of Log
		logger := NewZap(DebugLevel, buffer)
		// assert Debug log
		logger.Debug("test debug")
		flushLogger(t, logger)
		expected := "test debug"
		actual, err := extractMessage(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, expected, actual)

		lvl, err := extractLevel(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, DebugLevel.String(), lvl)
		require.Equal(t, DebugLevel, logger.LogLevel())

		// reset the buffer
		buffer.Reset()
		// assert Debug log
		name := "world"
		logger.Debugf("hello %s", name)
		flushLogger(t, logger)
		actual, err = extractMessage(buffer.Bytes())
		require.NoError(t, err)
		expected = "hello world"
		require.Equal(t, expected, actual)

		lvl, err = extractLevel(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, DebugLevel.String(), lvl)
		require.Equal(t, DebugLevel, logger.LogLevel())
	})
	t.Run("When info log is enabled show nothing", func(t *testing.T) {
		// create a bytes buffer that implements an io.Writer
		buffer := new(bytes.Buffer)
		// create an instance of Log
		logger := NewZap(InfoLevel, buffer)
		// assert Debug log
		logger.Debug("test debug")
		flushLogger(t, logger)
		require.Empty(t, buffer.String())
	})
	t.Run("When error log is enabled show nothing", func(t *testing.T) {
		// create a bytes buffer that implements an io.Writer
		buffer := new(bytes.Buffer)
		// create an instance of Log
		logger := NewZap(ErrorLevel, buffer)
		// assert Debug log
		logger.Debug("test debug")
		flushLogger(t, logger)
		require.Empty(t, buffer.String())
	})
	t.Run("When fatal log is enabled show nothing", func(t *testing.T) {
		// create a bytes buffer that implements an io.Writer
		buffer := new(bytes.Buffer)
		// create an instance of Log
		logger := NewZap(FatalLevel, buffer)
		// assert Debug log
		logger.Debug("test debug")
		flushLogger(t, logger)
		require.Empty(t, buffer.String())
	})
}

func TestInfo(t *testing.T) {
	t.Run("With Info log level", func(t *testing.T) {
		// create a bytes buffer that implements an io.Writer
		buffer := new(bytes.Buffer)
		// create an instance of Log
		logger := NewZap(InfoLevel, buffer)
		// assert Debug log
		logger.Info("test debug")
		flushLogger(t, logger)
		expected := "test debug"
		actual, err := extractMessage(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, expected, actual)

		lvl, err := extractLevel(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, InfoLevel.String(), lvl)
		require.Equal(t, InfoLevel, logger.LogLevel())

		// reset the buffer
		buffer.Reset()
		// assert Debug log
		name := "world"
		logger.Infof("hello %s", name)
		flushLogger(t, logger)
		actual, err = extractMessage(buffer.Bytes())
		require.NoError(t, err)
		expected = "hello world"
		require.Equal(t, expected, actual)

		lvl, err = extractLevel(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, InfoLevel.String(), lvl)
		require.Equal(t, InfoLevel, logger.LogLevel())
	})
	t.Run("With Debug log level", func(t *testing.T) {
		// create a bytes buffer that implements an io.Writer
		buffer := new(bytes.Buffer)
		// create an instance of Log
		logger := NewZap(DebugLevel, buffer)
		// assert Debug log
		logger.Info("test debug")
		flushLogger(t, logger)
		expected := "test debug"
		actual, err := extractMessage(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, expected, actual)

		lvl, err := extractLevel(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, InfoLevel.String(), lvl)

		// reset the buffer
		buffer.Reset()
		// assert Debug log
		name := "world"
		logger.Infof("hello %s", name)
		flushLogger(t, logger)
		actual, err = extractMessage(buffer.Bytes())
		require.NoError(t, err)
		expected = "hello world"
		require.Equal(t, expected, actual)

		lvl, err = extractLevel(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, InfoLevel.String(), lvl)
	})
	t.Run("With Error log level", func(t *testing.T) {
		// create a bytes buffer that implements an io.Writer
		buffer := new(bytes.Buffer)
		// create an instance of Log
		logger := NewZap(ErrorLevel, buffer)
		// assert Debug log
		logger.Info("test debug")
		flushLogger(t, logger)
		require.Empty(t, buffer.String())
	})
}

func TestWarn(t *testing.T) {
	t.Run("With Warn log level", func(t *testing.T) {
		// create a bytes buffer that implements an io.Writer
		buffer := new(bytes.Buffer)
		// create an instance of Log
		logger := NewZap(WarningLevel, buffer)
		// assert Debug log
		logger.Warn("test debug")
		flushLogger(t, logger)
		expected := "test debug"
		actual, err := extractMessage(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, expected, actual)

		lvl, err := extractLevel(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, WarningLevel.String(), lvl)
		require.Equal(t, WarningLevel, logger.LogLevel())

		// reset the buffer
		buffer.Reset()
		// assert Debug log
		name := "world"
		logger.Warnf("hello %s", name)
		flushLogger(t, logger)
		actual, err = extractMessage(buffer.Bytes())
		require.NoError(t, err)
		expected = "hello world"
		require.Equal(t, expected, actual)

		lvl, err = extractLevel(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, WarningLevel.String(), lvl)
		require.Equal(t, WarningLevel, logger.LogLevel())
	})
	t.Run("With Debug log level", func(t *testing.T) {
		// create a bytes buffer that implements an io.Writer
		buffer := new(bytes.Buffer)
		// create an instance of Log
		logger := NewZap(DebugLevel, buffer)
		// assert Debug log
		logger.Warn("test debug")
		flushLogger(t, logger)
		expected := "test debug"
		actual, err := extractMessage(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, expected, actual)

		lvl, err := extractLevel(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, WarningLevel.String(), lvl)

		// reset the buffer
		buffer.Reset()
		// assert Debug log
		name := "world"
		logger.Warnf("hello %s", name)
		flushLogger(t, logger)
		actual, err = extractMessage(buffer.Bytes())
		require.NoError(t, err)
		expected = "hello world"
		require.Equal(t, expected, actual)

		lvl, err = extractLevel(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, WarningLevel.String(), lvl)
	})
	t.Run("With Error log level", func(t *testing.T) {
		// create a bytes buffer that implements an io.Writer
		buffer := new(bytes.Buffer)
		// create an instance of Log
		logger := NewZap(ErrorLevel, buffer)
		// assert Debug log
		logger.Warn("test debug")
		flushLogger(t, logger)
		require.Empty(t, buffer.String())
	})
}

func TestError(t *testing.T) {
	t.Run("With the Error log level", func(t *testing.T) {
		// create a bytes buffer that implements an io.Writer
		buffer := new(bytes.Buffer)
		// create an instance of Log
		logger := NewZap(ErrorLevel, buffer)
		// assert Debug log
		logger.Error("test debug")
		flushLogger(t, logger)
		expected := "test debug"
		actual, err := extractMessage(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, expected, actual)

		lvl, err := extractLevel(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, logger.LogLevel().String(), lvl)
	})
	t.Run("When debug log is enabled", func(t *testing.T) {
		// create a bytes buffer that implements an io.Writer
		buffer := new(bytes.Buffer)
		// create an instance of Log
		logger := NewZap(DebugLevel, buffer)
		// assert Debug log
		logger.Error("test debug")
		flushLogger(t, logger)
		expected := "test debug"
		actual, err := extractMessage(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, expected, actual)

		lvl, err := extractLevel(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, ErrorLevel.String(), lvl)
		require.Equal(t, DebugLevel, logger.LogLevel())

		// reset the buffer
		buffer.Reset()
		// assert Debug log
		name := "world"
		logger.Errorf("hello %s", name)
		flushLogger(t, logger)
		actual, err = extractMessage(buffer.Bytes())
		require.NoError(t, err)
		expected = "hello world"
		require.Equal(t, expected, actual)

		lvl, err = extractLevel(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, ErrorLevel.String(), lvl)
		require.Equal(t, DebugLevel, logger.LogLevel())
	})
	t.Run("When info log is enabled", func(t *testing.T) {
		// create a bytes buffer that implements an io.Writer
		buffer := new(bytes.Buffer)
		// create an instance of Log
		logger := NewZap(InfoLevel, buffer)
		// assert Debug log
		logger.Error("test debug")
		flushLogger(t, logger)
		expected := "test debug"
		actual, err := extractMessage(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, expected, actual)

		lvl, err := extractLevel(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, ErrorLevel.String(), lvl)
		require.Equal(t, InfoLevel, logger.LogLevel())

		// reset the buffer
		buffer.Reset()
		// assert Debug log
		name := "world"
		logger.Errorf("hello %s", name)
		flushLogger(t, logger)
		actual, err = extractMessage(buffer.Bytes())
		require.NoError(t, err)
		expected = "hello world"
		require.Equal(t, expected, actual)

		lvl, err = extractLevel(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, ErrorLevel.String(), lvl)
		require.Equal(t, InfoLevel, logger.LogLevel())
	})
	t.Run("When warning log is enabled", func(t *testing.T) {
		// create a bytes buffer that implements an io.Writer
		buffer := new(bytes.Buffer)
		// create an instance of Log
		logger := NewZap(WarningLevel, buffer)
		// assert Debug log
		logger.Error("test debug")
		flushLogger(t, logger)
		expected := "test debug"
		actual, err := extractMessage(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, expected, actual)

		lvl, err := extractLevel(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, ErrorLevel.String(), lvl)
		require.Equal(t, WarningLevel, logger.LogLevel())

		// reset the buffer
		buffer.Reset()
		// assert Debug log
		name := "world"
		logger.Errorf("hello %s", name)
		flushLogger(t, logger)
		actual, err = extractMessage(buffer.Bytes())
		require.NoError(t, err)
		expected = "hello world"
		require.Equal(t, expected, actual)

		lvl, err = extractLevel(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, ErrorLevel.String(), lvl)
		require.Equal(t, WarningLevel, logger.LogLevel())
	})
}

func TestLogOutput(t *testing.T) {
	// create a bytes buffer that implements an io.Writer
	buffer := new(bytes.Buffer)
	// create an instance of Log
	logger := NewZap(InfoLevel, buffer)
	outputs := logger.LogOutput()
	require.NotEmpty(t, outputs)
	require.Len(t, outputs, 1)
	output := outputs[0]
	require.IsType(t, buffer, output)
}

func TestLogLevelFatal(t *testing.T) {
	buffer := new(bytes.Buffer)
	logger := NewZap(FatalLevel, buffer)
	require.Equal(t, FatalLevel, logger.LogLevel())
}

func TestLogLevelInvalid(t *testing.T) {
	encoderCfg := zapcore.EncoderConfig{
		MessageKey:  "msg",
		LevelKey:    "level",
		EncodeLevel: zapcore.LowercaseLevelEncoder,
	}
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderCfg),
		zapcore.AddSync(io.Discard),
		zapcore.DPanicLevel,
	)
	logger := &Zap{logger: zap.New(core)}

	require.Equal(t, InvalidLevel, logger.LogLevel())
}

func TestPanic(t *testing.T) {
	// create a bytes buffer that implements an io.Writer
	buffer := new(bytes.Buffer)
	// create an instance of Log
	logger := NewZap(PanicLevel, buffer)
	require.Equal(t, PanicLevel, logger.LogLevel())
	// assert Debug log
	assert.Panics(t, func() {
		logger.Panic("test debug")
	})
	// assert Debug log
	assert.Panics(t, func() {
		msg := "test debug"
		logger.Panicf("%s", msg)
	})
}

// nolint
func TestLogFatal(t *testing.T) {
	if os.Getenv("GO_TEST_FATAL") == "1" {
		logger := NewZap(FatalLevel, os.Stdout)
		logger.Fatal("fatal message")
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestFatalHelperProcess$", "-test.v")
	cmd.Env = append(os.Environ(), "GO_TEST_FATAL=1")

	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 1, exitErr.ExitCode())

	line := extractLogLine(out)
	require.NotNil(t, line, "expected fatal log output")

	msg, err := extractMessage(line)
	require.NoError(t, err)
	assert.Equal(t, "fatal message", msg)

	lvl, err := extractLevel(line)
	require.NoError(t, err)
	assert.Equal(t, FatalLevel.String(), lvl)
}

func TestFatalHelperProcess(t *testing.T) {
	if os.Getenv("GO_TEST_FATAL") != "1" {
		t.Skip("helper process")
	}
	logger := NewZap(FatalLevel, os.Stdout)
	logger.Fatal("fatal message")
}

// nolint
func TestLogFatalf(t *testing.T) {
	if os.Getenv("GO_TEST_FATALF") == "1" {
		logger := NewZap(FatalLevel, os.Stdout)
		logger.Fatalf("fatal formatted %d", 42)
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestFatalfHelperProcess$", "-test.v")
	cmd.Env = append(os.Environ(), "GO_TEST_FATALF=1")

	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 1, exitErr.ExitCode())

	line := extractLogLine(out)
	require.NotNil(t, line, "expected fatalf log output")

	msg, err := extractMessage(line)
	require.NoError(t, err)
	assert.Equal(t, "fatal formatted 42", msg)

	lvl, err := extractLevel(line)
	require.NoError(t, err)
	assert.Equal(t, FatalLevel.String(), lvl)
}

func TestFatalfHelperProcess(t *testing.T) {
	if os.Getenv("GO_TEST_FATALF") != "1" {
		t.Skip("helper process")
	}
	logger := NewZap(FatalLevel, os.Stdout)
	logger.Fatalf("fatal formatted %d", 42)
}

func TestStdLogger(t *testing.T) {
	buffer := new(bytes.Buffer)
	logger := NewZap(InfoLevel, buffer)

	std := logger.StdLogger()
	std.Print("std logger message")
	flushLogger(t, logger)

	line := buffer.Bytes()
	require.NotEmpty(t, line)

	msg, err := extractMessage(line)
	require.NoError(t, err)
	assert.Equal(t, "std logger message", msg)

	lvl, err := extractLevel(line)
	require.NoError(t, err)
	assert.Equal(t, InfoLevel.String(), lvl)
}

func flushLogger(t *testing.T, logger *Zap) {
	t.Helper()
	require.NoError(t, logger.logger.Sync())
}

func extractLogLine(out []byte) []byte {
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

func extractMessage(bytes []byte) (string, error) {
	// a map container to decode the JSON structure into
	c := make(map[string]json.RawMessage)

	// unmarshal JSON
	if err := json.Unmarshal(bytes, &c); err != nil {
		return "", err
	}
	for k, v := range c {
		if k == "msg" {
			return strconv.Unquote(string(v))
		}
	}

	return "", nil
}

func extractLevel(bytes []byte) (string, error) {
	// a map container to decode the JSON structure into
	c := make(map[string]json.RawMessage)

	// unmarshal JSON
	if err := json.Unmarshal(bytes, &c); err != nil {
		return "", err
	}
	for k, v := range c {
		if k == "level" {
			return strconv.Unquote(string(v))
		}
	}

	return "", nil
}
