/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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
	"fmt"
	"io"
	golog "log"
	"os"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	// DebugLogger is a global logger configured to output messages at DebugLevel
	// and above to os.Stdout. It is typically used for detailed development and
	// debugging output.
	DebugLogger = New(DebugLevel, os.Stdout)

	// DiscardLogger is a global logger configured at DebugLevel, but its output
	// is directed to io.Discard. This effectively mutes all log messages,
	// making it useful for disabling logging in tests or production environments
	// where specific loggers are not needed.
	DiscardLogger = New(DebugLevel, io.Discard)

	// DefaultLogger is a global logger configured to output messages at InfoLevel
	// and above to os.Stdout. It serves as the standard logger for general
	// informational messages in the application.
	DefaultLogger = New(InfoLevel, os.Stdout)
)

// Log implements Logger interface with the underlying zap as
// the underlying logging library
type Log struct {
	logger  *zap.Logger
	outputs []io.Writer
}

// enforce compilation and linter error
var _ Logger = &Log{}

// New creates an instance of Log
// New creates an instance of Log
func New(level Level, writers ...io.Writer) *Log {
	// create the zap Log configuration
	cfg := zap.Config{
		Development: false,
		Sampling: &zap.SamplingConfig{
			Initial:    100,
			Thereafter: 100,
		},
		Encoding: "json",
		// copied from "zap.NewProductionEncoderConfig" with some updates
		EncoderConfig: zapcore.EncoderConfig{
			TimeKey:       "ts",
			LevelKey:      "level",
			NameKey:       "logger",
			CallerKey:     "caller",
			MessageKey:    "msg",
			StacktraceKey: "stacktrace",
			LineEnding:    zapcore.DefaultLineEnding,
			EncodeLevel:   zapcore.LowercaseLevelEncoder,

			// Custom EncodeTime function to ensure we match format and precision of historic capnslog timestamps
			EncodeTime: func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
				enc.AppendString(t.Format("2006-01-02T15:04:05.000000Z0700"))
			},

			EncodeDuration: zapcore.StringDurationEncoder,
			EncodeCaller:   zapcore.ShortCallerEncoder,
		},
		OutputPaths:      []string{"stderr"},
		ErrorOutputPaths: []string{"stderr"},
	}

	// create the zap log core
	var core zapcore.Core

	// create the list of writers
	syncWriters := make([]zapcore.WriteSyncer, len(writers))
	for i, writer := range writers {
		syncWriters[i] = zapcore.AddSync(writer)
	}

	// set the log level
	switch level {
	case InfoLevel:
		core = zapcore.NewCore(
			zapcore.NewJSONEncoder(cfg.EncoderConfig),
			zap.CombineWriteSyncers(syncWriters...),
			zapcore.InfoLevel,
		)
	case DebugLevel:
		core = zapcore.NewCore(
			zapcore.NewJSONEncoder(cfg.EncoderConfig),
			zap.CombineWriteSyncers(syncWriters...),
			zapcore.DebugLevel,
		)
	case WarningLevel:
		core = zapcore.NewCore(
			zapcore.NewJSONEncoder(cfg.EncoderConfig),
			zap.CombineWriteSyncers(syncWriters...),
			zapcore.WarnLevel,
		)
	case ErrorLevel:
		core = zapcore.NewCore(
			zapcore.NewJSONEncoder(cfg.EncoderConfig),
			zap.CombineWriteSyncers(syncWriters...),
			zapcore.ErrorLevel,
		)
	case PanicLevel:
		core = zapcore.NewCore(
			zapcore.NewJSONEncoder(cfg.EncoderConfig),
			zap.CombineWriteSyncers(syncWriters...),
			zapcore.PanicLevel,
		)
	case FatalLevel:
		core = zapcore.NewCore(
			zapcore.NewJSONEncoder(cfg.EncoderConfig),
			zap.CombineWriteSyncers(syncWriters...),
			zapcore.FatalLevel,
		)
	default:
		core = zapcore.NewCore(
			zapcore.NewJSONEncoder(cfg.EncoderConfig),
			zap.CombineWriteSyncers(syncWriters...),
			zapcore.DebugLevel,
		)
	}
	// get the zap Log
	zapLogger := zap.New(core,
		zap.AddCaller(),
		zap.AddCallerSkip(1),
		zap.AddStacktrace(zapcore.PanicLevel),
		zap.AddStacktrace(zapcore.ErrorLevel),
		zap.AddStacktrace(zapcore.FatalLevel))

	// set the global logger
	zap.ReplaceGlobals(zapLogger)
	// create the instance of Log and returns it
	return &Log{
		logger:  zapLogger,
		outputs: writers,
	}
}

// Debug starts a message with debug level
func (l *Log) Debug(v ...any) {
	l.logger.Sugar().Debug(fmt.Sprint(v...))
}

// Debugf starts a message with debug level
func (l *Log) Debugf(format string, v ...any) {
	l.logger.Sugar().Debug(fmt.Sprintf(format, v...))
}

// Panic starts a new message with panic level. The panic() function
// is called which stops the ordinary flow of a goroutine.
func (l *Log) Panic(v ...any) {
	l.logger.Sugar().Panic(fmt.Sprint(v...))
}

// Panicf starts a new message with panic level. The panic() function
// is called which stops the ordinary flow of a goroutine.
func (l *Log) Panicf(format string, v ...any) {
	l.logger.Sugar().Panic(fmt.Sprintf(format, v...))
}

// Fatal starts a new message with fatal level. The os.Exit(1) function
// is called which terminates the program immediately.
func (l *Log) Fatal(v ...any) {
	l.logger.Sugar().Fatal(fmt.Sprint(v...))
}

// Fatalf starts a new message with fatal level. The os.Exit(1) function
// is called which terminates the program immediately.
func (l *Log) Fatalf(format string, v ...any) {
	l.logger.Sugar().Fatal(fmt.Sprintf(format, v...))
}

// Error starts a new message with error level.
func (l *Log) Error(v ...any) {
	l.logger.Sugar().Error(fmt.Sprint(v...))
}

// Errorf starts a new message with error level.
func (l *Log) Errorf(format string, v ...any) {
	l.logger.Sugar().Error(fmt.Sprintf(format, v...))
}

// Warn starts a new message with warn level
func (l *Log) Warn(v ...any) {
	l.logger.Sugar().Warn(fmt.Sprint(v...))
}

// Warnf starts a new message with warn level
func (l *Log) Warnf(format string, v ...any) {
	l.logger.Sugar().Warn(fmt.Sprintf(format, v...))
}

// Info starts a message with info level
func (l *Log) Info(v ...any) {
	l.logger.Sugar().Info(fmt.Sprint(v...))
}

// Infof starts a message with info level
func (l *Log) Infof(format string, v ...any) {
	l.logger.Sugar().Info(fmt.Sprintf(format, v...))
}

// LogLevel returns the log level that is used
func (l *Log) LogLevel() Level {
	switch l.logger.Level() {
	case zapcore.FatalLevel:
		return FatalLevel
	case zapcore.PanicLevel:
		return PanicLevel
	case zapcore.ErrorLevel:
		return ErrorLevel
	case zapcore.InfoLevel:
		return InfoLevel
	case zapcore.DebugLevel:
		return DebugLevel
	case zapcore.WarnLevel:
		return WarningLevel
	default:
		return InvalidLevel
	}
}

// LogOutput returns the log output that is set
func (l *Log) LogOutput() []io.Writer {
	return l.outputs
}

// StdLogger returns the standard logger associated to the logger
func (l *Log) StdLogger() *golog.Logger {
	stdlogger, _ := zap.NewStdLogAt(l.logger, l.logger.Level())
	redirect, _ := zap.RedirectStdLogAt(l.logger, l.logger.Level())
	defer redirect()
	return stdlogger
}
