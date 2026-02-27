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
	"fmt"
	"io"
	golog "log"
	"os"
)

var discardOutputs = []io.Writer{io.Discard}
var discardStdLogger = golog.New(io.Discard, "", 0)

type discardLogger struct{}

func (discardLogger) Debug(v ...any)                 { _ = v }
func (discardLogger) Debugf(format string, v ...any) { _, _ = format, v }
func (discardLogger) Info(v ...any)                  { _ = v }
func (discardLogger) Infof(format string, v ...any)  { _, _ = format, v }
func (discardLogger) Warn(v ...any)                  { _ = v }
func (discardLogger) Warnf(format string, v ...any)  { _, _ = format, v }
func (discardLogger) Error(v ...any)                 { _ = v }
func (discardLogger) Errorf(format string, v ...any) { _, _ = format, v }

func (discardLogger) Fatal(v ...any) {
	_ = fmt.Sprint(v...)
	os.Exit(1)
}

func (discardLogger) Fatalf(format string, v ...any) {
	_ = fmt.Sprintf(format, v...)
	os.Exit(1)
}

func (discardLogger) Panic(v ...any) {
	panic(fmt.Sprint(v...))
}

func (discardLogger) Panicf(format string, v ...any) {
	panic(fmt.Sprintf(format, v...))
}

func (discardLogger) LogLevel() Level {
	return InfoLevel
}

// Enabled returns false for all levels except Fatal and Panic, which always execute.
func (discardLogger) Enabled(level Level) bool {
	return level == FatalLevel || level == PanicLevel
}

// With returns the receiver unchanged; DiscardLogger ignores structured fields.
func (discardLogger) With(keyValues ...any) Logger {
	return DiscardLogger
}

func (discardLogger) LogOutput() []io.Writer {
	return discardOutputs
}

func (discardLogger) StdLogger() *golog.Logger {
	return discardStdLogger
}

func (discardLogger) Flush() error {
	return nil
}
