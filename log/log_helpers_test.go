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
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestSplitWriteSyncers(t *testing.T) {
	buffer := new(bytes.Buffer)
	file := createTempLogFile(t)
	defer file.Close()

	immediate, buffered := splitWriteSyncers(buffer, os.Stdout, file)
	require.Len(t, immediate, 2)
	require.Len(t, buffered, 1)
}

func TestIsStdStream(t *testing.T) {
	file := createTempLogFile(t)
	defer file.Close()

	assert.True(t, isStdStream(os.Stdout))
	assert.True(t, isStdStream(os.Stderr))
	assert.False(t, isStdStream(file))
	assert.False(t, isStdStream(nil))
}

func TestCombineWriteSyncers(t *testing.T) {
	require.Nil(t, combineWriteSyncers(nil))
	require.Nil(t, combineWriteSyncers([]zapcore.WriteSyncer{}))

	ws := combineWriteSyncers([]zapcore.WriteSyncer{zapcore.AddSync(io.Discard)})
	require.NotNil(t, ws)
}

func TestNewZapCoreNoWritersLowLevel(t *testing.T) {
	cfg := newZapConfig()
	core, buffered := newZapCore(cfg, zapcore.InfoLevel, nil, nil)
	require.NotNil(t, core)
	require.Nil(t, buffered)

	logger := zap.New(core)
	logger.Info("noop")
	require.NoError(t, logger.Sync())
}

func TestNewZapCoreBufferedFile(t *testing.T) {
	file := createTempLogFile(t)
	defer file.Close()

	immediate, buffered := splitWriteSyncers(file)
	require.Empty(t, immediate)
	require.NotEmpty(t, buffered)

	cfg := newZapConfig()
	core, bufferedSyncer := newZapCore(cfg, zapcore.InfoLevel, immediate, buffered)
	require.NotNil(t, core)
	require.NotNil(t, bufferedSyncer)

	logger := zap.New(core)
	logger.Info("buffered")
	require.NoError(t, bufferedSyncer.Stop())
}

func TestLogFlush(t *testing.T) {
	t.Run("unbuffered", func(t *testing.T) {
		buffer := new(bytes.Buffer)
		logger := New(InfoLevel, buffer)
		logger.Info("msg")
		require.NoError(t, logger.Flush())
	})

	t.Run("buffered", func(t *testing.T) {
		file := createTempLogFile(t)
		defer file.Close()

		logger := New(InfoLevel, file)
		require.NotNil(t, logger.bufferedWriteSyncer)
		logger.Info("msg")
		require.NoError(t, logger.Flush())
	})
}

func createTempLogFile(t *testing.T) *os.File {
	t.Helper()

	file, err := os.CreateTemp(t.TempDir(), "log")
	require.NoError(t, err)

	return file
}
