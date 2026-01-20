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

func (discardLogger) Debug(...any)          {}
func (discardLogger) Debugf(string, ...any) {}
func (discardLogger) Info(...any)           {}
func (discardLogger) Infof(string, ...any)  {}
func (discardLogger) Warn(...any)           {}
func (discardLogger) Warnf(string, ...any)  {}
func (discardLogger) Error(...any)          {}
func (discardLogger) Errorf(string, ...any) {}

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

func (discardLogger) LogOutput() []io.Writer {
	return discardOutputs
}

func (discardLogger) StdLogger() *golog.Logger {
	return discardStdLogger
}

func (discardLogger) Flush() error {
	return nil
}
