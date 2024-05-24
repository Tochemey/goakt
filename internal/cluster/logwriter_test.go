/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package cluster

import (
	"bytes"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v2/log"
)

func TestLogWriter(t *testing.T) {
	t.Run("With info log", func(t *testing.T) {
		// create a bytes buffer that implements an io.Writer
		buffer := new(bytes.Buffer)
		// create an instance of Log
		logger := log.New(log.InfoLevel, buffer)
		// create the olric message
		message := "2023/11/06 20:40:24 [INFO] Olric 0.5.4 on linux/arm64 go1.21.0 => olric.go:319"

		// create an instance of logWriter
		logWriter := newLogWriter(logger)
		res, err := logWriter.Write([]byte(message))
		require.NoError(t, err)
		require.EqualValues(t, len(message), res)

		expected := "Olric 0.5.4 on linux/arm64 go1.21.0 => olric.go:319"
		actual, err := extractMessage(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, expected, actual)

		lvl, err := extractLevel(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, log.InfoLevel.String(), lvl)

		// reset the buffer
		buffer.Reset()
		message = "[INFO] Olric 0.5.4 on linux/arm64 go1.21.0 => olric.go:319"

		res, err = logWriter.Write([]byte(message))
		require.NoError(t, err)
		require.EqualValues(t, len(message), res)

		expected = "Olric 0.5.4 on linux/arm64 go1.21.0 => olric.go:319"
		actual, err = extractMessage(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, expected, actual)

		lvl, err = extractLevel(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, log.InfoLevel.String(), lvl)
	})

	t.Run("With debug log", func(t *testing.T) {
		// create a bytes buffer that implements an io.Writer
		buffer := new(bytes.Buffer)
		// create an instance of Log
		logger := log.New(log.DebugLevel, buffer)
		// create the olric message
		message := "2023/11/06 20:40:24 [DEBUG] memberlist: Failed to join 172.30.0.4:3322: dial tcp 172.30.0.4:3322: connect: connection refused"

		// create an instance of logWriter
		logWriter := newLogWriter(logger)
		res, err := logWriter.Write([]byte(message))
		require.NoError(t, err)
		require.EqualValues(t, len(message), res)

		expected := "memberlist: Failed to join 172.30.0.4:3322: dial tcp 172.30.0.4:3322: connect: connection refused"
		actual, err := extractMessage(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, expected, actual)
		lvl, err := extractLevel(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, log.DebugLevel.String(), lvl)

		// reset the buffer
		buffer.Reset()
		message = "[DEBUG] memberlist: Failed to join 172.30.0.4:3322: dial tcp 172.30.0.4:3322: connect: connection refused"
		res, err = logWriter.Write([]byte(message))
		require.NoError(t, err)
		require.EqualValues(t, len(message), res)

		expected = "memberlist: Failed to join 172.30.0.4:3322: dial tcp 172.30.0.4:3322: connect: connection refused"
		actual, err = extractMessage(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, expected, actual)
		lvl, err = extractLevel(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, log.DebugLevel.String(), lvl)
	})

	t.Run("With error log", func(t *testing.T) {
		// create a bytes buffer that implements an io.Writer
		buffer := new(bytes.Buffer)
		// create an instance of Log
		logger := log.New(log.ErrorLevel, buffer)
		// create the olric message
		message := "2023/11/06 20:40:24 [ERROR] Failed to publish NodeJoinEvent to cluster.events: ERR ERR cannot be reached cluster quorum to operate => events.go:40"

		// create an instance of logWriter
		logWriter := newLogWriter(logger)
		res, err := logWriter.Write([]byte(message))
		require.NoError(t, err)
		require.EqualValues(t, len(message), res)

		expected := "Failed to publish NodeJoinEvent to cluster.events: ERR ERR cannot be reached cluster quorum to operate => events.go:40"
		actual, err := extractMessage(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, expected, actual)
		lvl, err := extractLevel(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, log.ErrorLevel.String(), lvl)

		// reset the buffer
		buffer.Reset()
		message = "[ERROR] Failed to publish NodeJoinEvent to cluster.events: ERR ERR cannot be reached cluster quorum to operate => events.go:40"
		res, err = logWriter.Write([]byte(message))
		require.NoError(t, err)
		require.EqualValues(t, len(message), res)

		expected = "Failed to publish NodeJoinEvent to cluster.events: ERR ERR cannot be reached cluster quorum to operate => events.go:40"
		actual, err = extractMessage(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, expected, actual)
		lvl, err = extractLevel(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, log.ErrorLevel.String(), lvl)
	})

	t.Run("With warn log", func(t *testing.T) {
		// create a bytes buffer that implements an io.Writer
		buffer := new(bytes.Buffer)
		// create an instance of Log
		logger := log.New(log.WarningLevel, buffer)
		// create the olric message
		message := "2023/11/06 20:40:24 [WARN] Balancer awaits for bootstrapping"

		// create an instance of logWriter
		logWriter := newLogWriter(logger)
		res, err := logWriter.Write([]byte(message))
		require.NoError(t, err)
		require.EqualValues(t, len(message), res)

		expected := "Balancer awaits for bootstrapping"
		actual, err := extractMessage(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, expected, actual)
		lvl, err := extractLevel(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, log.WarningLevel.String(), lvl)

		// reset the buffer
		buffer.Reset()
		message = "[WARN] Balancer awaits for bootstrapping"
		res, err = logWriter.Write([]byte(message))
		require.NoError(t, err)
		require.EqualValues(t, len(message), res)

		expected = "Balancer awaits for bootstrapping"
		actual, err = extractMessage(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, expected, actual)
		lvl, err = extractLevel(buffer.Bytes())
		require.NoError(t, err)
		require.Equal(t, log.WarningLevel.String(), lvl)
	})
}

// TODO: move this utility into some package
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

// TODO: move this utility into some package
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
