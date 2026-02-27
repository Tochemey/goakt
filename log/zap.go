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
	"context"
	"io"
	golog "log"
	"os"
	"time"

	"go.uber.org/multierr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	// DebugLogger is a global logger configured to output messages at DebugLevel
	// and above to os.Stdout. It is typically used for detailed development and
	// debugging output.
	DebugLogger = NewZap(DebugLevel, os.Stdout)

	// DiscardLogger is a no-op logger that discards all log messages.
	DiscardLogger Logger = discardLogger{}

	// DefaultLogger is a global logger configured to output messages at InfoLevel
	// and above to os.Stdout. It serves as the standard logger for general
	// informational messages in the application.
	DefaultLogger = NewZap(InfoLevel, os.Stdout)
)

const (
	bufferedWriteSize     = 256 * 1024
	bufferedFlushInterval = 30 * time.Second
	// maxInlineFields is the maximum number of key-value pairs for stack-allocated fields.
	// With() uses a stack array for small N to avoid heap allocation.
	maxInlineFields = 6
)

// Zap implements Logger interface with zap as the underlying logging library.
// It is optimized for low overhead: message formatting is skipped when levels
// are disabled, and file outputs are buffered for Info/Warn/Debug to reduce
// syscalls. Stdout/stderr remain unbuffered for immediate visibility. Call
// Flush during graceful shutdown to drain buffered file output.
type Zap struct {
	logger              *zap.Logger
	sugar               *zap.SugaredLogger
	outputs             []io.Writer
	bufferedWriteSyncer *zapcore.BufferedWriteSyncer
}

// enforce compilation and linter error
var _ Logger = &Zap{}

// NewZap creates an instance of Log.
// Performance notes:
// - Debug/Info/Warn logs are buffered only for file outputs to reduce syscalls.
// - Stdout/stderr and non-file writers are unbuffered for immediate visibility.
// - Error and above are unbuffered for all outputs.
// Call Flush during graceful shutdown to ensure buffered file logs are written.
func NewZap(level Level, writers ...io.Writer) *Zap {
	config := newZapConfig()
	logLevel := toZapLevel(level)
	immediateSyncers, bufferedSyncers := splitWriteSyncers(writers...)
	core, bufferedWriteSyncer := newZapCore(config, logLevel, immediateSyncers, bufferedSyncers)

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
	return &Zap{
		logger:              zapLogger,
		sugar:               zapLogger.Sugar(),
		outputs:             writers,
		bufferedWriteSyncer: bufferedWriteSyncer,
	}
}

// Debug starts a message with debug level
func (z *Zap) Debug(v ...any) {
	z.sugar.Debug(v...)
}

// Debugf starts a message with debug level
func (z *Zap) Debugf(format string, v ...any) {
	z.sugar.Debugf(format, v...)
}

// DebugContext logs at debug level with the given context.
func (z *Zap) DebugContext(ctx context.Context, v ...any) {
	_ = ctx
	z.sugar.Debug(v...)
}

// DebugfContext logs a formatted message at debug level with the given context.
func (z *Zap) DebugfContext(ctx context.Context, format string, v ...any) {
	_ = ctx
	z.sugar.Debugf(format, v...)
}

// Panic starts a new message with panic level. The panic() function
// is called which stops the ordinary flow of a goroutine.
func (z *Zap) Panic(v ...any) {
	z.sugar.Panic(v...)
}

// Panicf starts a new message with panic level. The panic() function
// is called which stops the ordinary flow of a goroutine.
func (z *Zap) Panicf(format string, v ...any) {
	z.sugar.Panicf(format, v...)
}

// Fatal starts a new message with fatal level. The os.Exit(1) function
// is called which terminates the program immediately.
func (z *Zap) Fatal(v ...any) {
	z.sugar.Fatal(v...)
}

// Fatalf starts a new message with fatal level. The os.Exit(1) function
// is called which terminates the program immediately.
func (z *Zap) Fatalf(format string, v ...any) {
	z.sugar.Fatalf(format, v...)
}

// Error starts a new message with error level.
func (z *Zap) Error(v ...any) {
	z.sugar.Error(v...)
}

// Errorf starts a new message with error level.
func (z *Zap) Errorf(format string, v ...any) {
	z.sugar.Errorf(format, v...)
}

// ErrorContext logs at error level with the given context.
func (z *Zap) ErrorContext(ctx context.Context, v ...any) {
	_ = ctx
	z.sugar.Error(v...)
}

// ErrorfContext logs a formatted message at error level with the given context.
func (z *Zap) ErrorfContext(ctx context.Context, format string, v ...any) {
	_ = ctx
	z.sugar.Errorf(format, v...)
}

// Warn starts a new message with warn level
func (z *Zap) Warn(v ...any) {
	z.sugar.Warn(v...)
}

// Warnf starts a new message with warn level
func (z *Zap) Warnf(format string, v ...any) {
	z.sugar.Warnf(format, v...)
}

// WarnContext logs at warn level with the given context.
func (z *Zap) WarnContext(ctx context.Context, v ...any) {
	_ = ctx
	z.sugar.Warn(v...)
}

// WarnfContext logs a formatted message at warn level with the given context.
func (z *Zap) WarnfContext(ctx context.Context, format string, v ...any) {
	_ = ctx
	z.sugar.Warnf(format, v...)
}

// Info starts a message with info level
func (z *Zap) Info(v ...any) {
	z.sugar.Info(v...)
}

// Infof starts a message with info level
func (z *Zap) Infof(format string, v ...any) {
	z.sugar.Infof(format, v...)
}

// InfoContext logs at info level with the given context.
func (z *Zap) InfoContext(ctx context.Context, v ...any) {
	_ = ctx
	z.sugar.Info(v...)
}

// InfofContext logs a formatted message at info level with the given context.
func (z *Zap) InfofContext(ctx context.Context, format string, v ...any) {
	_ = ctx
	z.sugar.Infof(format, v...)
}

// Enabled reports whether the given level is enabled.
func (z *Zap) Enabled(level Level) bool {
	return z.logger.Core().Enabled(toZapLevel(level))
}

// With returns a Logger that includes the given key-value pairs in all subsequent log entries.
// Optimized for low GC: uses zap.String/zap.Int64 for common types (avoids zap.Any reflection),
// and stack-allocated fields for up to maxInlineFields pairs.
func (z *Zap) With(keyValues ...any) Logger {
	n := (len(keyValues) + 1) / 2 // pairs, including odd last value as ("_", val)
	if n == 0 {
		return z
	}

	var buf [maxInlineFields]zap.Field
	var fields []zap.Field
	if n <= maxInlineFields {
		fields = buf[:0:n]
	} else {
		fields = make([]zap.Field, 0, n)
	}

	for i := 0; i < len(keyValues); i += 2 {
		if i+1 >= len(keyValues) {
			fields = append(fields, toZapField("_", keyValues[i]))
			break
		}
		k, ok := keyValues[i].(string)
		if !ok {
			continue
		}
		fields = append(fields, toZapField(k, keyValues[i+1]))
	}
	if len(fields) == 0 {
		return z
	}

	newLogger := z.logger.With(fields...)
	return &Zap{
		logger:              newLogger,
		sugar:               newLogger.Sugar(),
		outputs:             z.outputs,
		bufferedWriteSyncer: z.bufferedWriteSyncer,
	}
}

// toZapField converts a key-value pair to zap.Field using typed accessors where possible
// to avoid zap.Any reflection and reduce allocations.
func toZapField(key string, val any) zap.Field {
	switch v := val.(type) {
	case string:
		return zap.String(key, v)
	case int:
		return zap.Int(key, v)
	case int32:
		return zap.Int32(key, v)
	case int64:
		return zap.Int64(key, v)
	case uint:
		return zap.Uint(key, v)
	case uint32:
		return zap.Uint32(key, v)
	case uint64:
		return zap.Uint64(key, v)
	case bool:
		return zap.Bool(key, v)
	case float64:
		return zap.Float64(key, v)
	default:
		return zap.Any(key, val)
	}
}

// LogLevel returns the log level that is used
func (z *Zap) LogLevel() Level {
	switch z.logger.Level() {
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
func (z *Zap) LogOutput() []io.Writer {
	return z.outputs
}

// Flush flushes buffered log entries. Call this during a graceful shutdown
// when no more log writes are expected. If no buffered outputs are configured,
// Flush is a no-op.
func (z *Zap) Flush() error {
	if z.bufferedWriteSyncer != nil {
		return z.bufferedWriteSyncer.Stop()
	}

	return syncFileOutputs(z.outputs)
}

func syncFileOutputs(outputs []io.Writer) error {
	var err error
	for _, output := range outputs {
		file, ok := output.(*os.File)
		if !ok || isStdStream(file) {
			continue
		}
		if syncErr := file.Sync(); syncErr != nil {
			err = multierr.Combine(err, syncErr)
		}
	}
	return err
}

// StdLogger returns the standard logger associated to the logger
func (z *Zap) StdLogger() *golog.Logger {
	stdlogger, _ := zap.NewStdLogAt(z.logger, z.logger.Level())
	redirect, _ := zap.RedirectStdLogAt(z.logger, z.logger.Level())
	defer redirect()
	return stdlogger
}

func newZapConfig() zap.Config {
	return zap.Config{
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
}

func splitWriteSyncers(writers ...io.Writer) ([]zapcore.WriteSyncer, []zapcore.WriteSyncer) {
	immediate := make([]zapcore.WriteSyncer, 0, len(writers))
	buffered := make([]zapcore.WriteSyncer, 0, len(writers))
	for _, writer := range writers {
		if file, ok := writer.(*os.File); ok && !isStdStream(file) {
			buffered = append(buffered, zapcore.AddSync(writer))
			continue
		}
		immediate = append(immediate, zapcore.AddSync(writer))
	}
	return immediate, buffered
}

func combineWriteSyncers(syncers []zapcore.WriteSyncer) zapcore.WriteSyncer {
	if len(syncers) == 0 {
		return nil
	}
	return zap.CombineWriteSyncers(syncers...)
}

func isStdStream(file *os.File) bool {
	if file == nil {
		return false
	}
	fd := file.Fd()
	return fd == os.Stdout.Fd() || fd == os.Stderr.Fd()
}

func toZapLevel(level Level) zapcore.Level {
	switch level {
	case InfoLevel:
		return zapcore.InfoLevel
	case DebugLevel:
		return zapcore.DebugLevel
	case WarningLevel:
		return zapcore.WarnLevel
	case ErrorLevel:
		return zapcore.ErrorLevel
	case PanicLevel:
		return zapcore.PanicLevel
	case FatalLevel:
		return zapcore.FatalLevel
	default:
		return zapcore.DebugLevel
	}
}

func newJSONEncoder(cfg zap.Config) zapcore.Encoder {
	return zapcore.NewJSONEncoder(cfg.EncoderConfig)
}

func newZapCore(cfg zap.Config, logLevel zapcore.Level, immediateSyncers, bufferedSyncers []zapcore.WriteSyncer) (zapcore.Core, *zapcore.BufferedWriteSyncer) {
	immediateWriteSyncer := combineWriteSyncers(immediateSyncers)
	fileWriteSyncer := combineWriteSyncers(bufferedSyncers)
	allSyncers := make([]zapcore.WriteSyncer, 0, 2)

	if immediateWriteSyncer != nil {
		allSyncers = append(allSyncers, immediateWriteSyncer)
	}

	if fileWriteSyncer != nil {
		allSyncers = append(allSyncers, fileWriteSyncer)
	}

	allWriteSyncer := zap.CombineWriteSyncers(allSyncers...)
	if logLevel >= zapcore.ErrorLevel {
		return zapcore.NewCore(newJSONEncoder(cfg), allWriteSyncer, logLevel), nil
	}

	// Buffer lower-severity logs to reduce syscalls; keep errors unbuffered.
	var bufferedWriteSyncer *zapcore.BufferedWriteSyncer

	lowPriority := zap.LevelEnablerFunc(func(level zapcore.Level) bool {
		return level >= logLevel && level < zapcore.ErrorLevel
	})

	highPriority := zap.LevelEnablerFunc(func(level zapcore.Level) bool {
		return level >= logLevel && level >= zapcore.ErrorLevel
	})

	cores := make([]zapcore.Core, 0, 3)
	if immediateWriteSyncer != nil {
		cores = append(cores, zapcore.NewCore(newJSONEncoder(cfg), immediateWriteSyncer, lowPriority))
	}

	if fileWriteSyncer != nil {
		bufferedWriteSyncer = &zapcore.BufferedWriteSyncer{
			WS:            fileWriteSyncer,
			Size:          bufferedWriteSize,
			FlushInterval: bufferedFlushInterval,
		}
		cores = append(cores, zapcore.NewCore(newJSONEncoder(cfg), bufferedWriteSyncer, lowPriority))
	}

	cores = append(cores, zapcore.NewCore(newJSONEncoder(cfg), allWriteSyncer, highPriority))
	if len(cores) == 1 {
		return cores[0], bufferedWriteSyncer
	}

	return zapcore.NewTee(cores...), bufferedWriteSyncer
}
