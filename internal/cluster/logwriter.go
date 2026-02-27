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

package cluster

import (
	"bytes"
	"io"

	"github.com/tochemey/goakt/v4/log"
)

// logLevel enumerates recognized severity levels parsed from Olric log lines.
// It is an internal representation used to dispatch to the appropriate Logger method.
type logLevel uint8

const (
	// infoLevel maps to informational messages.
	infoLevel logLevel = iota + 1
	// debugLevel maps to verbose debug messages.
	debugLevel
	// warnLevel maps to warning conditions.
	warnLevel
	// errorLevel maps to error conditions.
	errorLevel
)

var (
	infoPrefix  = []byte("[INFO]")
	debugPrefix = []byte("[DEBUG]")
	warnPrefix  = []byte("[WARN]")
	errorPrefix = []byte("[ERROR]")
)

type parsedMessage struct {
	level logLevel
	text  string
}

// logWriter adapts Olric's raw log output (written via io.Writer) to the goakt Logger.
// It parses severity prefixes ([INFO],[DEBUG],[WARN],[ERROR]) in each line and forwards
// the stripped message to the matching Logger method. Unknown lines are ignored.
// It is safe for concurrent use because the underlying Logger is expected to be thread‑safe.
type logWriter struct {
	logger log.Logger
}

// make sure that the logWriter implements the io.Writer interface fully
var _ io.Writer = (*logWriter)(nil)

// newLogWriter creates a logWriter bound to the provided Logger.
//
// Usage:
//
//	lw := newLogWriter(logger) // pass lw to Olric configuration expecting an io.Writer
//
// The returned writer performs lightweight prefix parsing on each Write without regex.
func newLogWriter(logger log.Logger) *logWriter {
	return &logWriter{logger: logger}
}

// Write parses a single Olric log line and dispatches it to the Logger.
// Behavior:
//   - Trims surrounding whitespace.
//   - Detects the earliest occurrence of any known severity prefix.
//   - Strips one optional space after the prefix.
//   - Forwards remaining text to the appropriate logger method.
//   - Ignores lines with no known prefix (returns len(message) with nil error).
//
// Concurrency:
//   - Safe if the provided Logger is concurrency-safe.
//
// It never returns an error to avoid breaking Olric's logging flow.
func (l *logWriter) Write(message []byte) (n int, err error) {
	switch msg, ok := parseLogLine(bytes.TrimSpace(message)); {
	case !ok:
		// message does not have a known prefix; ignore
	case msg.level == infoLevel:
		if l.logger.Enabled(log.InfoLevel) {
			l.logger.Info(msg.text)
		}
	case msg.level == debugLevel:
		if l.logger.Enabled(log.DebugLevel) {
			l.logger.Debug(msg.text)
		}
	case msg.level == warnLevel:
		if l.logger.Enabled(log.WarningLevel) {
			l.logger.Warn(msg.text)
		}
	case msg.level == errorLevel:
		if l.logger.Enabled(log.ErrorLevel) {
			l.logger.Error(msg.text)
		}
	}

	return len(message), nil
}

// parseLogLine extracts severity level and message body from a trimmed log line.
// Implementation details:
//   - Scans for each known prefix and picks the earliest match (works with timestamped lines).
//   - Strips a single leading space after the prefix when present.
//   - Avoids regex and minimizes allocations (only converts the tail segment to string).
//
// Returns:
//   - parsedMessage plus true when a prefix was found.
//   - Zero value plus false when no known prefix exists.
func parseLogLine(line []byte) (parsedMessage, bool) {
	if len(line) == 0 {
		return parsedMessage{}, false
	}

	level, idx, plen, ok := findFirstPrefix(line)
	if !ok {
		return parsedMessage{}, false
	}

	text := line[idx+plen:]
	if len(text) > 0 && text[0] == ' ' {
		text = text[1:]
	}

	return parsedMessage{
		level: level,
		text:  string(text),
	}, true
}

// findFirstPrefix searches line for all known severity prefixes and returns metadata
// for the earliest occurrence.
// Returns:
//   - level: matched logLevel
//   - idx: start index of the matched prefix
//   - plen: length of the matched prefix
//   - ok: true if any prefix was found
//
// Complexity is proportional to the number of prefixes times bytes.Index cost.
// Given the tiny fixed set, this is effectively O(n) for n = len(line).
func findFirstPrefix(line []byte) (level logLevel, idx int, plen int, ok bool) {
	firstIdx := -1
	// save keeps the earliest valid prefix candidate seen so far.
	save := func(candidateIdx int, l logLevel, p []byte) {
		if candidateIdx >= 0 && (firstIdx == -1 || candidateIdx < firstIdx) {
			firstIdx, level, plen, ok = candidateIdx, l, len(p), true
		}
	}

	save(bytes.Index(line, infoPrefix), infoLevel, infoPrefix)
	save(bytes.Index(line, debugPrefix), debugLevel, debugPrefix)
	save(bytes.Index(line, warnPrefix), warnLevel, warnPrefix)
	save(bytes.Index(line, errorPrefix), errorLevel, errorPrefix)

	return level, firstIdx, plen, ok
}
