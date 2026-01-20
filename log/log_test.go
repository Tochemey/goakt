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
	logger := New(7, buffer)

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

func TestDebug(t *testing.T) {
	t.Run("With Debug log level", func(t *testing.T) {
		// create a bytes buffer that implements an io.Writer
		buffer := new(bytes.Buffer)
		// create an instance of Log
		logger := New(DebugLevel, buffer)
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
		logger := New(InfoLevel, buffer)
		// assert Debug log
		logger.Debug("test debug")
		flushLogger(t, logger)
		require.Empty(t, buffer.String())
	})
	t.Run("When error log is enabled show nothing", func(t *testing.T) {
		// create a bytes buffer that implements an io.Writer
		buffer := new(bytes.Buffer)
		// create an instance of Log
		logger := New(ErrorLevel, buffer)
		// assert Debug log
		logger.Debug("test debug")
		flushLogger(t, logger)
		require.Empty(t, buffer.String())
	})
	t.Run("When fatal log is enabled show nothing", func(t *testing.T) {
		// create a bytes buffer that implements an io.Writer
		buffer := new(bytes.Buffer)
		// create an instance of Log
		logger := New(FatalLevel, buffer)
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
		logger := New(InfoLevel, buffer)
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
		logger := New(DebugLevel, buffer)
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
		logger := New(ErrorLevel, buffer)
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
		logger := New(WarningLevel, buffer)
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
		logger := New(DebugLevel, buffer)
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
		logger := New(ErrorLevel, buffer)
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
		logger := New(ErrorLevel, buffer)
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
		logger := New(DebugLevel, buffer)
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
		logger := New(InfoLevel, buffer)
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
		logger := New(WarningLevel, buffer)
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
	logger := New(InfoLevel, buffer)
	outputs := logger.LogOutput()
	require.NotEmpty(t, outputs)
	require.Len(t, outputs, 1)
	output := outputs[0]
	require.IsType(t, buffer, output)
}

func TestLogLevelFatal(t *testing.T) {
	buffer := new(bytes.Buffer)
	logger := New(FatalLevel, buffer)
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
	logger := &Log{logger: zap.New(core)}

	require.Equal(t, InvalidLevel, logger.LogLevel())
}

func TestPanic(t *testing.T) {
	// create a bytes buffer that implements an io.Writer
	buffer := new(bytes.Buffer)
	// create an instance of Log
	logger := New(PanicLevel, buffer)
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
		logger := New(FatalLevel, os.Stdout)
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
	logger := New(FatalLevel, os.Stdout)
	logger.Fatal("fatal message")
}

// nolint
func TestLogFatalf(t *testing.T) {
	if os.Getenv("GO_TEST_FATALF") == "1" {
		logger := New(FatalLevel, os.Stdout)
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
	logger := New(FatalLevel, os.Stdout)
	logger.Fatalf("fatal formatted %d", 42)
}

func TestStdLogger(t *testing.T) {
	buffer := new(bytes.Buffer)
	logger := New(InfoLevel, buffer)

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

func flushLogger(t *testing.T, logger *Log) {
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
