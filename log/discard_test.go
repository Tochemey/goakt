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
	"io"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscardLoggerBasics(t *testing.T) {
	logger := DiscardLogger

	logger.Debug("debug")
	logger.Debugf("debug %s", "msg")
	logger.Info("info")
	logger.Infof("info %s", "msg")
	logger.Warn("warn")
	logger.Warnf("warn %s", "msg")
	logger.Error("error")
	logger.Errorf("error %s", "msg")

	require.Equal(t, InfoLevel, logger.LogLevel())

	outputs := logger.LogOutput()
	require.Len(t, outputs, 1)
	require.Equal(t, io.Discard, outputs[0])

	std := logger.StdLogger()
	require.Equal(t, discardStdLogger, std)
	std.Print("discard")

	require.NoError(t, logger.Flush())
}

func TestDiscardLoggerPanic(t *testing.T) {
	assert.PanicsWithValue(t, "panic", func() {
		DiscardLogger.Panic("panic")
	})
}

func TestDiscardLoggerPanicf(t *testing.T) {
	assert.PanicsWithValue(t, "panic 42", func() {
		DiscardLogger.Panicf("panic %d", 42)
	})
}

// nolint
func TestDiscardLoggerFatal(t *testing.T) {
	if os.Getenv("GO_TEST_DISCARD_FATAL") == "1" {
		DiscardLogger.Fatal("fatal message")
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestDiscardLoggerFatalHelper$", "-test.v")
	cmd.Env = append(os.Environ(), "GO_TEST_DISCARD_FATAL=1")

	_, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 1, exitErr.ExitCode())
}

func TestDiscardLoggerFatalHelper(t *testing.T) {
	if os.Getenv("GO_TEST_DISCARD_FATAL") != "1" {
		t.Skip("helper process")
	}
	DiscardLogger.Fatal("fatal message")
}

// nolint
func TestDiscardLoggerFatalf(t *testing.T) {
	if os.Getenv("GO_TEST_DISCARD_FATALF") == "1" {
		DiscardLogger.Fatalf("fatal formatted %d", 42)
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestDiscardLoggerFatalfHelper$", "-test.v")
	cmd.Env = append(os.Environ(), "GO_TEST_DISCARD_FATALF=1")

	_, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 1, exitErr.ExitCode())
}

func TestDiscardLoggerFatalfHelper(t *testing.T) {
	if os.Getenv("GO_TEST_DISCARD_FATALF") != "1" {
		t.Skip("helper process")
	}
	DiscardLogger.Fatalf("fatal formatted %d", 42)
}
