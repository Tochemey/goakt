package log

import (
	"fmt"
	"io"
	golog "log"
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// DefaultLogger represents the default Log to use
// This Log wraps zerolog under the hood
var DefaultLogger = New(DebugLevel, os.Stdout)
var DiscardLogger = New(DebugLevel, io.Discard)

// Log implements Logger interface with the underlying zap as
// the underlying logging library
type Log struct {
	*zap.Logger
	outputs []io.Writer
}

// enforce compilation and linter error
var _ Logger = &Log{}

// New creates an instance of Log
// New creates an instance of Log
func New(level Level, writers ...io.Writer) *Log {
	// create the zap Log configuration
	cfg := zap.NewProductionConfig()
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
	zapLogger := zap.New(core)
	// create the instance of Log and returns it
	return &Log{
		Logger:  zapLogger,
		outputs: writers,
	}
}

// Debug starts a message with debug level
func (l *Log) Debug(v ...any) {
	defer l.Logger.Sync()
	l.Logger.Debug(fmt.Sprint(v...))
}

// Debugf starts a message with debug level
func (l *Log) Debugf(format string, v ...any) {
	defer l.Logger.Sync()
	l.Logger.Debug(fmt.Sprintf(format, v...))
}

// Panic starts a new message with panic level. The panic() function
// is called which stops the ordinary flow of a goroutine.
func (l *Log) Panic(v ...any) {
	defer l.Logger.Sync()
	l.Logger.Panic(fmt.Sprint(v...))
}

// Panicf starts a new message with panic level. The panic() function
// is called which stops the ordinary flow of a goroutine.
func (l *Log) Panicf(format string, v ...any) {
	defer l.Logger.Sync()
	l.Logger.Panic(fmt.Sprintf(format, v...))
}

// Fatal starts a new message with fatal level. The os.Exit(1) function
// is called which terminates the program immediately.
func (l *Log) Fatal(v ...any) {
	defer l.Logger.Sync()
	l.Logger.Fatal(fmt.Sprint(v...))
}

// Fatalf starts a new message with fatal level. The os.Exit(1) function
// is called which terminates the program immediately.
func (l *Log) Fatalf(format string, v ...any) {
	defer l.Logger.Sync()
	l.Logger.Fatal(fmt.Sprintf(format, v...))
}

// Error starts a new message with error level.
func (l *Log) Error(v ...any) {
	defer l.Logger.Sync()
	l.Logger.Error(fmt.Sprint(v...))
}

// Errorf starts a new message with error level.
func (l *Log) Errorf(format string, v ...any) {
	defer l.Logger.Sync()
	l.Logger.Error(fmt.Sprintf(format, v...))
}

// Warn starts a new message with warn level
func (l *Log) Warn(v ...any) {
	defer l.Logger.Sync()
	l.Logger.Warn(fmt.Sprint(v...))
}

// Warnf starts a new message with warn level
func (l *Log) Warnf(format string, v ...any) {
	defer l.Logger.Sync()
	l.Logger.Warn(fmt.Sprintf(format, v...))
}

// Info starts a message with info level
func (l *Log) Info(v ...any) {
	defer l.Logger.Sync()
	l.Logger.Info(fmt.Sprint(v...))
}

// Infof starts a message with info level
func (l *Log) Infof(format string, v ...any) {
	defer l.Logger.Sync()
	l.Logger.Info(fmt.Sprintf(format, v...))
}

// LogLevel returns the log level that is used
func (l *Log) LogLevel() Level {
	switch l.Level() {
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
	return zap.NewStdLog(l.Logger)
}
