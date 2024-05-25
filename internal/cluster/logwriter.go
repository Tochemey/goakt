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
	"io"
	"regexp"

	"github.com/tochemey/goakt/v2/log"
)

// logWriter is a wrapper for the olric logging
type logWriter struct {
	logger log.Logger
	info   *regexp.Regexp
	debug  *regexp.Regexp
	warn   *regexp.Regexp
	error  *regexp.Regexp
}

// make sure that the logWriter implements the io.Writer interface fully
var _ io.Writer = (*logWriter)(nil)

// newLogWriter create an instance of logWriter
func newLogWriter(logger log.Logger) *logWriter {
	return &logWriter{
		logger: logger,
		info:   regexp.MustCompile(`\[INFO\] (.+)`),
		debug:  regexp.MustCompile(`\[DEBUG\] (.+)`),
		warn:   regexp.MustCompile(`\[WARN\] (.+)`),
		error:  regexp.MustCompile(`\[ERROR\] (.+)`),
	}
}

// Write writes len(p) bytes from p to the underlying data stream.
func (l *logWriter) Write(message []byte) (n int, err error) {
	// trim all spaces
	trimmed := bytes.TrimSpace(message)
	// get the text value of the log message
	text := string(trimmed)

	// parse info message
	matches := l.info.FindStringSubmatch(text)
	if len(matches) > 1 {
		// info message found
		infoText := matches[1]
		l.logger.Info(infoText)
		return len(message), nil
	}

	// parse debug message
	matches = l.debug.FindStringSubmatch(text)
	if len(matches) > 1 {
		// debug message found
		debugText := matches[1]
		l.logger.Debug(debugText)
		return len(message), nil
	}

	// parse warn messages
	matches = l.warn.FindStringSubmatch(text)
	if len(matches) > 1 {
		// debug message found
		warnText := matches[1]
		l.logger.Warn(warnText)
		return len(message), nil
	}

	// parse error messages
	matches = l.error.FindStringSubmatch(text)
	if len(matches) > 1 {
		// error message found
		errorText := matches[1]
		l.logger.Error(errorText)
		return len(message), nil
	}

	return len(message), nil
}
