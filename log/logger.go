/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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

package log

import (
	"io"
	golog "log"
)

// Logger represents an active logging object that generates lines of
// output to an io.Writer.
type Logger interface {
	// Info starts a new message with info level.
	Info(...any)
	// Infof starts a new message with info level.
	Infof(string, ...any)
	// Warn starts a new message with warn level.
	Warn(...any)
	// Warnf starts a new message with warn level.
	Warnf(string, ...any)
	// Error starts a new message with error level.
	Error(...any)
	// Errorf starts a new message with error level.
	Errorf(string, ...any)
	// Fatal starts a new message with fatal level. The os.Exit(1) function
	// is called which terminates the program immediately.
	Fatal(...any)
	// Fatalf starts a new message with fatal level. The os.Exit(1) function
	// is called which terminates the program immediately.
	Fatalf(string, ...any)
	// Panic starts a new message with panic level. The panic() function
	// is called which stops the ordinary flow of a goroutine.
	Panic(...any)
	// Panicf starts a new message with panic level. The panic() function
	// is called which stops the ordinary flow of a goroutine.
	Panicf(string, ...any)
	// Debug starts a new message with debug level.
	Debug(...any)
	// Debugf starts a new message with debug level.
	Debugf(string, ...any)
	// LogLevel returns the log level being used
	LogLevel() Level
	// LogOutput returns the log output that is set
	LogOutput() []io.Writer
	// StdLogger returns the standard logger associated to the logger
	StdLogger() *golog.Logger
}
