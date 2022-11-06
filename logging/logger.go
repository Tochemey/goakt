// Package logging implements a simple logging package. It defines a type, Logger,
// with methods for formatting output. It also has a predefined 'standard'
// Logger accessible through helper functions Info[f], Warning[f], Error[f], Fatal[f], and
// Panic[f], which are easier to use than creating a Logger manually.
// That logger writes to standard error and prints the date and time
// of each logged message.
// Every log message is output on a separate line: if the message being
// printed does not end in a newline, the logger will add one.
// The Fatal functions call os.Exit(1) after writing the log message.
// The Panic functions call panic after writing the log message.
package logging

import (
	"fmt"
	"io"
	"log"
	"os"
)

// DefaultLogger define the standard logger used by the package-level output functions.
var DefaultLogger = New(infoLevel, "[GO-AKT]", os.Stderr, io.Discard)

const (
	// infoLevel indicates Info log level.
	infoLevel int = iota
	// warningLevel indicates Warning log level.
	warningLevel
	// errorLevel indicates Error log level.
	errorLevel
	// fatalLevel indicates Fatal log level.
	fatalLevel
	// panicLevel indicates Panic log level
	panicLevel

	// specifies the total number of log level
	numLogLevels = 5
)

var levelName = [numLogLevels]string{
	infoLevel:    "INFO",
	warningLevel: "WARNING",
	errorLevel:   "ERROR",
	fatalLevel:   "FATAL",
	panicLevel:   "PANIC",
}

// Verbose is a boolean type that implements some logger func (like Infof) etc.
// See the documentation of V for more information.
type Verbose interface {
	// Enabled will return true if this log level is enabled, guarded by the value
	// of v.
	// See the documentation of V for usage.
	Enabled() bool
	// Info logs to INFO log. Arguments are handled in the manner of fmt.Println.
	Info(...interface{})
	// Infof logs to INFO log. Arguments are handled in the manner of fmt.Printf.
	// A newline is appended if the last character of format is not
	// already a newline.
	Infof(string, ...interface{})
	// Warning logs to the WARNING and INFO logs. Arguments are handled in the manner of fmt.Println.
	Warning(...interface{})
	// Warningf logs to the WARNING and INFO logs.. Arguments are handled in the manner of fmt.Printf.
	// A newline is appended if the last character of format is not
	// already a newline.
	Warningf(string, ...interface{})
	// Error logs to the ERROR, WARNING, and INFO logs. Arguments are handled in the manner of fmt.Print.
	Error(...interface{})
	// Errorf logs to the ERROR, WARNING, and INFO logs. Arguments are handled in the manner of fmt.Printf.
	// A newline is appended if the last character of format is not
	// already a newline.
	Errorf(string, ...interface{})
}

// Logger represents an active logging object that generates lines of
// output to an io.Writer.
type Logger interface {
	// Info logs to INFO log. Arguments are handled in the manner of fmt.Println.
	Info(...interface{})
	// Infof logs to INFO log. Arguments are handled in the manner of fmt.Printf.
	// A newline is appended if the last character of format is not
	// already a newline.
	Infof(string, ...interface{})
	// Warning logs to the WARNING and INFO logs. Arguments are handled in the manner of fmt.Println.
	Warning(...interface{})
	// Warningf logs to the WARNING and INFO logs.. Arguments are handled in the manner of fmt.Printf.
	// A newline is appended if the last character of format is not
	// already a newline.
	Warningf(string, ...interface{})
	// Error logs to the ERROR, WARNING, and INFO logs. Arguments are handled in the manner of fmt.Print.
	Error(...interface{})
	// Errorf logs to the ERROR, WARNING, and INFO logs. Arguments are handled in the manner of fmt.Printf.
	// A newline is appended if the last character of format is not
	// already a newline.
	Errorf(string, ...interface{})
	// Fatal logs to the FATAL, ERROR, WARNING, and INFO logs followed by a call to os.Exit(1).
	// Arguments are handled in the manner of fmt.Println.
	Fatal(...interface{})
	// Fatalf logs to the FATAL, ERROR, WARNING, and INFO logs followed by a call to os.Exit(1).
	// Arguments are handled in the manner of fmt.Printf.
	// A newline is appended if the last character of format is not
	// already a newline.
	Fatalf(string, ...interface{})
	// Panic logs to the PANIC, FATAL, ERROR, WARNING, and INFO logs followed by a call to panic().
	// Arguments are handled in the manner of fmt.Println.
	Panic(...interface{})
	// Panicf logs to the PANIC, ERROR, WARNING, and INFO logs followed by a call to panic().
	// Arguments are handled in the manner of fmt.Printf.
	// A newline is appended if the last character of format is not
	// already a newline.
	Panicf(string, ...interface{})

	// V reports whether verbosity at the call site is at least the requested level.
	// The returned value is an interface of type Verbose, which implements Info, Warning
	// and Error. These methods will write to the log if called.
	// Thus, one may write either
	//	if logger.V(2).Enabled() { logger.Info("log this") }
	// or
	//	logger.V(2).Info("log this").
	V(l int) Verbose
}

// New create new logger from the given writers and verbosity.
// Each log level must have its own writer, if len of writers less than
// num of log level New will use last writer to fulfill missing.
// Otherwise, New will use os.Stderr as default.
//
// Use io.Discard to suppress message repetition to all lower writers.
//
// info := os.Stderr
// warn := io.Discard
// New(1, "", info, warn)
//
// Use io.Discard in all lower writer's to set desired log level.
//
// info := io.Discard
// warn := io.Discard
// err :=  os.Stderr
// ....
// New(1, "", info, warn, err)
//
// Note: a messages of a given log level are logged not only in the writer for that log level,
// but also in all writer's of lower log level. E.g.,
// a message of log level FATAL will be logged to the writers of log level FATAL, ERROR, WARNING, and INFO.
func New(verbosity int, prefix string, writers ...io.Writer) Logger {
	if len(writers) == 0 {
		writers = []io.Writer{os.Stderr}
	}

	if len(writers) < numLogLevels {
		last := writers[len(writers)-1]
		for i := len(writers); i < numLogLevels; i++ {
			writers = append(writers, last)
		}
	}

	ll := make([]*log.Logger, numLogLevels)
	for i := range writers {
		mw := make([]io.Writer, i+1)
		for j := 0; j <= i; j++ {
			mw[j] = writers[j]
		}

		w := io.MultiWriter(mw...)
		ll[i] = log.New(w, prefix, log.LstdFlags)
	}

	return &logger{
		v:  verbosity,
		ll: ll,
	}
}

// V reports whether verbosity at the call site is at least the requested level.
// The returned value is a interface of type Verbose, which implements Info, Warning
// and Error. These methods will write to the log if called.
// Thus, one may write either
//
//	if logger.V(2).Enabled() { logger.Info("log this") }
//
// or
//
//	logger.V(2).Info("log this").
func V(l int) Verbose {
	return DefaultLogger.V(l)
}

// Info logs to INFO log. Arguments are handled in the manner of fmt.Println.
func Info(v ...interface{}) {
	DefaultLogger.Info(v...)
}

// Infof logs to INFO log. Arguments are handled in the manner of fmt.Printf.
// A newline is appended if the last character of format is not
// already a newline.
func Infof(format string, v ...interface{}) {
	DefaultLogger.Infof(format, v...)
}

// Warning logs to the WARNING and INFO logs. Arguments are handled in the manner of fmt.Println.
func Warning(v ...interface{}) {
	DefaultLogger.Warning(v...)
}

// Warningf logs to the WARNING and INFO logs.. Arguments are handled in the manner of fmt.Printf.
// A newline is appended if the last character of format is not
// already a newline.
func Warningf(format string, v ...interface{}) {
	DefaultLogger.Warningf(format, v...)
}

// Error logs to the ERROR, WARNING, and INFO logs. Arguments are handled in the manner of fmt.Print.
func Error(v ...interface{}) {
	DefaultLogger.Error(v...)
}

// Errorf logs to the ERROR, WARNING, and INFO logs. Arguments are handled in the manner of fmt.Printf.
// A newline is appended if the last character of format is not
// already a newline.
func Errorf(format string, v ...interface{}) {
	DefaultLogger.Errorf(format, v...)
}

// Fatal logs to the FATAL, ERROR, WARNING, and INFO logs followed by a call to os.Exit(1).
// Arguments are handled in the manner of fmt.Println.
func Fatal(v ...interface{}) {
	DefaultLogger.Fatal(v...)
}

// Fatalf logs to the FATAL, ERROR, WARNING, and INFO logs followed by a call to os.Exit(1).
// Arguments are handled in the manner of fmt.Printf.
// A newline is appended if the last character of format is not
// already a newline.
func Fatalf(format string, v ...interface{}) {
	DefaultLogger.Fatalf(format, v...)
}

// Panic logs to the PANIC, FATAL, ERROR, WARNING, and INFO logs followed by a call to panic().
// Arguments are handled in the manner of fmt.Println.
func Panic(v ...interface{}) {
	DefaultLogger.Panic(v...)
}

// Panicf logs to the PANIC, ERROR, WARNING, and INFO logs followed by a call to panic().
// Arguments are handled in the manner of fmt.Printf.
// A newline is appended if the last character of format is not
// already a newline.
func Panicf(format string, v ...interface{}) {
	DefaultLogger.Panicf(format, v...)
}

// logger is the default logger used by raft.
type logger struct {
	ll []*log.Logger
	v  int
}

func (l *logger) output(level int, s string) {
	sevStr := levelName[level]
	err := l.ll[level].Output(2, fmt.Sprintf("%v: %v", sevStr, s))
	if err != nil {
		Panic(err)
	}
}

func (l *logger) Panic(v ...interface{}) {
	s := fmt.Sprint(v...)
	l.output(panicLevel, s)
	panic(s)
}

func (l *logger) Panicf(format string, v ...interface{}) {
	s := fmt.Sprintf(format, v...)
	l.output(panicLevel, s)
	panic(s)
}

func (l *logger) Fatal(v ...interface{}) {
	l.output(fatalLevel, fmt.Sprint(v...))
	os.Exit(1)
}

func (l *logger) Fatalf(format string, v ...interface{}) {
	l.output(fatalLevel, fmt.Sprintf(format, v...))
	os.Exit(1)
}

func (l *logger) Error(v ...interface{}) {
	l.output(errorLevel, fmt.Sprint(v...))
}

func (l *logger) Errorf(format string, v ...interface{}) {
	l.output(errorLevel, fmt.Sprintf(format, v...))
}

func (l *logger) Warning(v ...interface{}) {
	l.output(warningLevel, fmt.Sprint(v...))
}

func (l *logger) Warningf(format string, v ...interface{}) {
	l.output(warningLevel, fmt.Sprintf(format, v...))
}

func (l *logger) Info(v ...interface{}) {
	l.output(infoLevel, fmt.Sprint(v...))
}

func (l *logger) Infof(format string, v ...interface{}) {
	l.output(infoLevel, fmt.Sprintf(format, v...))
}

func (l *logger) V(lv int) Verbose {
	return verbose{
		logger: l,
		l:      lv,
	}
}

type verbose struct {
	logger *logger
	l      int
}

func (ver verbose) Enabled() bool {
	return ver.l <= ver.logger.v
}

func (ver verbose) Error(v ...interface{}) {
	if ver.Enabled() {
		ver.logger.Error(v...)
	}
}

func (ver verbose) Errorf(format string, v ...interface{}) {
	if ver.Enabled() {
		ver.logger.Errorf(format, v...)
	}
}

func (ver verbose) Warning(v ...interface{}) {
	if ver.Enabled() {
		ver.logger.Warning(v...)
	}
}

func (ver verbose) Warningf(format string, v ...interface{}) {
	if ver.Enabled() {
		ver.logger.Warningf(format, v...)
	}
}

func (ver verbose) Info(v ...interface{}) {
	if ver.Enabled() {
		ver.logger.Info(v...)
	}
}

func (ver verbose) Infof(format string, v ...interface{}) {
	if ver.Enabled() {
		ver.logger.Infof(format, v...)
	}
}
