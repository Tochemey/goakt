package log

import (
	"fmt"
	"io"
	"log"
	"os"
)

// DefaultLogger define the standard log used by the package-level output functions.
var DefaultLogger = NewLogger(InfoLevel, "[Actors]", os.Stderr, io.Discard)
var DiscardLogger = NewLogger(InfoLevel, "", io.Discard)

// Info logs to INFO level.
func Info(v ...interface{}) {
	DefaultLogger.Info(v...)
}

// Infof logs to INFO level
func Infof(format string, v ...interface{}) {
	DefaultLogger.Infof(format, v...)
}

// Warning logs to the WARNING level.
func Warning(v ...interface{}) {
	DefaultLogger.Warning(v...)
}

// Warningf logs to the WARNING level.
func Warningf(format string, v ...interface{}) {
	DefaultLogger.Warningf(format, v...)
}

// Error logs to the ERROR level.
func Error(v ...interface{}) {
	DefaultLogger.Error(v...)
}

// Errorf logs to the ERROR level.
func Errorf(format string, v ...interface{}) {
	DefaultLogger.Errorf(format, v...)
}

// Fatal logs to the FATAL level followed by a call to os.Exit(1).
func Fatal(v ...interface{}) {
	DefaultLogger.Fatal(v...)
}

// Fatalf logs to the FATAL level followed by a call to os.Exit(1).
func Fatalf(format string, v ...interface{}) {
	DefaultLogger.Fatalf(format, v...)
}

// Panic logs to the PANIC level followed by a call to panic().
func Panic(v ...interface{}) {
	DefaultLogger.Panic(v...)
}

// Panicf logs to the PANIC level followed by a call to panic().
func Panicf(format string, v ...interface{}) {
	DefaultLogger.Panicf(format, v...)
}

// logger implements Logger interface
type logger struct {
	// specifies the log level
	logLevel Level
	// specifies the prefix
	prefix string
	// the underlying golang loggers
	loggers []*log.Logger
}

// NewLogger creates an instance of Logger
func NewLogger(level Level, prefix string, writers ...io.Writer) Logger {
	// set a default writer when no writers are specified
	if len(writers) == 0 {
		writers = []io.Writer{os.Stderr}
	}

	// set the various writers based
	if len(writers) < numLogLevels {
		last := writers[len(writers)-1]
		for i := len(writers); i < numLogLevels; i++ {
			writers = append(writers, last)
		}
	}

	loggers := make([]*log.Logger, numLogLevels)
	for i := range writers {
		mw := make([]io.Writer, i+1)
		for j := 0; j <= i; j++ {
			mw[j] = writers[j]
		}

		w := io.MultiWriter(mw...)
		loggers[i] = log.New(w, prefix, log.LstdFlags)
	}

	return &logger{
		logLevel: level,
		prefix:   prefix,
		loggers:  loggers,
	}
}

func (l logger) Debug(i ...interface{}) {
	l.output(DebugLevel, fmt.Sprint(i...))
}

func (l logger) Debugf(s string, i ...interface{}) {
	l.output(DebugLevel, fmt.Sprintf(s, i...))
}

func (l logger) Info(i ...interface{}) {
	l.output(InfoLevel, fmt.Sprint(i...))
}

func (l logger) Infof(s string, i ...interface{}) {
	l.output(InfoLevel, fmt.Sprintf(s, i...))
}

func (l logger) Warning(i ...interface{}) {
	l.output(WarningLevel, fmt.Sprint(i...))
}

func (l logger) Warningf(s string, i ...interface{}) {
	l.output(WarningLevel, fmt.Sprintf(s, i...))
}

func (l logger) Error(i ...interface{}) {
	l.output(ErrorLevel, fmt.Sprint(i...))
}

func (l logger) Errorf(s string, i ...interface{}) {
	l.output(ErrorLevel, fmt.Sprintf(s, i...))
}

func (l logger) Fatal(i ...interface{}) {
	l.output(FatalLevel, fmt.Sprint(i...))
	os.Exit(1)
}

func (l logger) Fatalf(s string, i ...interface{}) {
	l.output(FatalLevel, fmt.Sprintf(s, i...))
	os.Exit(1)
}

func (l logger) Panic(i ...interface{}) {
	s := fmt.Sprint(i...)
	l.output(PanicLevel, s)
	panic(s)
}

func (l logger) Panicf(s string, i ...interface{}) {
	sf := fmt.Sprintf(s, i...)
	l.output(PanicLevel, sf)
	panic(sf)
}

func (l logger) output(level Level, s string) {
	// write nothing when level is disabled
	if level == Disabled {
		return
	}
	sevStr := levels[level]
	err := l.loggers[level].Output(2, fmt.Sprintf("%v: %v", sevStr, s))
	if err != nil {
		Panic(err)
	}
}
