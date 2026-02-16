# Logging

GoAkt provides a structured logging system through the `github.com/tochemey/goakt/v3/log` package. The default implementation uses [Zap](https://github.com/uber-go/zap) with JSON encoding.

## Logger Interface

The `log.Logger` interface defines the contract for all loggers in GoAkt:

```go
type Logger interface {
    Info(...any)
    Infof(string, ...any)
    Warn(...any)
    Warnf(string, ...any)
    Error(...any)
    Errorf(string, ...any)
    Fatal(...any)
    Fatalf(string, ...any)
    Panic(...any)
    Panicf(string, ...any)
    Debug(...any)
    Debugf(string, ...any)

    LogLevel() Level
    LogOutput() []io.Writer
    Flush() error
    StdLogger() *log.Logger
}
```

Each severity level provides two forms:

- `X(...any)` -- log values (similar to `fmt.Sprint`)
- `Xf(format, ...any)` -- log with a format string (similar to `fmt.Sprintf`)

**Behavior notes:**

- `Fatal` / `Fatalf` terminate the process via `os.Exit(1)` after emitting the entry
- `Panic` / `Panicf` call `panic` after emitting the entry
- `Flush()` forces buffered entries to be written; safe to call on unbuffered loggers (no-op)
- `StdLogger()` returns a `*log.Logger` compatible with the standard library, useful for integrating with third-party libraries

## Log Levels

```go
const (
    InfoLevel    Level = iota // General informational messages
    WarningLevel              // Potential issues
    ErrorLevel                // Errors that need attention
    FatalLevel                // Fatal errors; process terminates
    PanicLevel                // Panic-level errors; process panics
    DebugLevel                // Verbose debug output
)
```

Messages below the configured level are suppressed.

## Pre-Built Loggers

GoAkt provides three ready-to-use loggers:

| Logger              | Level        | Output       | Use Case                                    |
|---------------------|--------------|--------------|---------------------------------------------|
| `log.DefaultLogger` | `InfoLevel`  | `os.Stdout`  | General-purpose (default for actor systems) |
| `log.DebugLogger`   | `DebugLevel` | `os.Stdout`  | Development and debugging                   |
| `log.DiscardLogger` | N/A          | Discards all | Tests and benchmarks                        |

## Creating a Custom Logger

Use `log.New` to create a logger with a specific level and output destinations:

```go
import (
    "os"
    "github.com/tochemey/goakt/v3/log"
)

// Log to stdout at info level
logger := log.New(log.InfoLevel, os.Stdout)

// Log to stderr at debug level
logger := log.New(log.DebugLevel, os.Stderr)

// Log to a file
file, _ := os.OpenFile("app.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
logger := log.New(log.InfoLevel, file)

// Log to multiple outputs
logger := log.New(log.InfoLevel, os.Stdout, file)
```

### Buffering Behavior

The built-in logger applies intelligent buffering:

- **Unbuffered**: `os.Stdout`, `os.Stderr`, and error-level (and above) messages are written immediately
- **Buffered**: File-based outputs for Debug, Info, and Warn levels use a 256KB buffer with a 30-second flush interval

Call `logger.Flush()` before shutdown to ensure all buffered entries are written.

## Configuring the Actor System Logger

Set the logger when creating the actor system:

```go
import (
    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/log"
)

// Use the default logger (InfoLevel to stdout)
system, _ := actor.NewActorSystem("my-system")

// Use a custom logger
logger := log.New(log.DebugLevel, os.Stdout)
system, _ := actor.NewActorSystem("my-system",
    actor.WithLogger(logger),
)

// Silence all logs (useful in tests)
system, _ := actor.NewActorSystem("my-system",
    actor.WithLogger(log.DiscardLogger),
)
```

The configured logger is used by the actor system, all spawned actors, and internal components.

## Logging Inside Actors

Actors access the logger through their context:

```go
func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *ProcessOrder:
        ctx.Logger().Infof("Processing order %s", msg.OrderID)

        if err := a.process(msg); err != nil {
            ctx.Logger().Errorf("Failed to process order %s: %v", msg.OrderID, err)
            ctx.Err(err)
            return
        }

        ctx.Logger().Debugf("Order %s processed successfully", msg.OrderID)
    }
}
```

## Custom Logger Implementation

Implement the `log.Logger` interface to use any logging library:

```go
import (
    "io"
    golog "log"
    "log/slog"

    goaktlog "github.com/tochemey/goakt/v3/log"
)

type SlogAdapter struct {
    logger *slog.Logger
    level  goaktlog.Level
    output []io.Writer
}

func NewSlogAdapter(level goaktlog.Level) *SlogAdapter {
    return &SlogAdapter{
        logger: slog.Default(),
        level:  level,
        output: []io.Writer{os.Stdout},
    }
}

func (s *SlogAdapter) Info(args ...any)                 { s.logger.Info(fmt.Sprint(args...)) }
func (s *SlogAdapter) Infof(format string, args ...any) { s.logger.Info(fmt.Sprintf(format, args...)) }
func (s *SlogAdapter) Warn(args ...any)                 { s.logger.Warn(fmt.Sprint(args...)) }
func (s *SlogAdapter) Warnf(format string, args ...any) { s.logger.Warn(fmt.Sprintf(format, args...)) }
func (s *SlogAdapter) Error(args ...any)                { s.logger.Error(fmt.Sprint(args...)) }
func (s *SlogAdapter) Errorf(format string, args ...any){ s.logger.Error(fmt.Sprintf(format, args...)) }
func (s *SlogAdapter) Debug(args ...any)                { s.logger.Debug(fmt.Sprint(args...)) }
func (s *SlogAdapter) Debugf(format string, args ...any){ s.logger.Debug(fmt.Sprintf(format, args...)) }
func (s *SlogAdapter) Fatal(args ...any)                { s.logger.Error(fmt.Sprint(args...)); os.Exit(1) }
func (s *SlogAdapter) Fatalf(format string, args ...any){ s.logger.Error(fmt.Sprintf(format, args...)); os.Exit(1) }
func (s *SlogAdapter) Panic(args ...any)                { msg := fmt.Sprint(args...); s.logger.Error(msg); panic(msg) }
func (s *SlogAdapter) Panicf(format string, args ...any){ msg := fmt.Sprintf(format, args...); s.logger.Error(msg); panic(msg) }
func (s *SlogAdapter) LogLevel() goaktlog.Level         { return s.level }
func (s *SlogAdapter) LogOutput() []io.Writer           { return s.output }
func (s *SlogAdapter) Flush() error                     { return nil }
func (s *SlogAdapter) StdLogger() *golog.Logger         { return golog.Default() }

// Usage
system, _ := actor.NewActorSystem("my-system",
    actor.WithLogger(NewSlogAdapter(goaktlog.InfoLevel)),
)
```

## Best Practices

### Use Appropriate Log Levels

```go
// Debug: Detailed internal state, message flow
ctx.Logger().Debugf("Received message %T from %s", msg, ctx.Sender().Name())

// Info: Significant lifecycle events
ctx.Logger().Infof("Order %s created successfully", order.ID)

// Warn: Recoverable issues
ctx.Logger().Warnf("Retry %d/%d for operation %s", attempt, maxRetries, opID)

// Error: Failures that need investigation
ctx.Logger().Errorf("Database connection failed: %v", err)
```

### Silence Logs in Tests

```go
kit := testkit.New(ctx, t) // Uses DiscardLogger by default

// Or explicitly
kit := testkit.New(ctx, t, testkit.WithLogging(log.ErrorLevel))
```

### Flush Before Shutdown

```go
system.Stop(ctx)
logger.Flush() // Ensure buffered entries are written
```

### Use DebugLevel in Development

```go
system, _ := actor.NewActorSystem("dev-system",
    actor.WithLogger(log.DebugLogger),
)
```

## Next Steps

- [Metrics](metrics.md): OpenTelemetry metrics integration
- [Events Stream](../events_stream/overview.md): System event monitoring
