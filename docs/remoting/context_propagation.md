# Context Propagation

Context propagation allows cross-cutting metadata (trace IDs, correlation IDs, authentication tokens, custom headers) to travel across remoting boundaries. This is essential for distributed tracing, request tracking, and maintaining context in distributed actor systems.

## Overview

Context propagation in GoAkt:

- **Propagates context values** across remote actor boundaries via TCP
- **Integrates with tracing systems**: OpenTelemetry, Jaeger, Zipkin
- **Supports custom metadata**: Authentication, correlation IDs, feature flags
- **Transparent to actors**: Context automatically injected/extracted
- **Works with remoting and clustering**: Available in both modes

**Important**: Context propagation in GoAkt uses **TCP as the underlying transport**, not HTTP. The `net/http.Header` type is used as a **convenient string-keyed, multi-valued map** for storing metadata, but all communication happens over GoAkt's custom TCP-based protocol.

## How It Works

```
Actor A (Node 1)                    Actor B (Node 2)
┌─────────────┐                    ┌─────────────┐
│ Send(ctx)   │                    │ Receive()   │
│     ↓       │                    │     ↓       │
│ Inject      │  Header Format     │ Extract     │
│     ↓       ├───────────────────>│     ↓       │
│ Serialize   │  [trace-id: ...]   │ Deserialize │
│     ↓       │  [user-id: ...]    │     ↓       │
│ TCP Send    │ (via TCP Protocol) │ ctx with    │
│             │                    │ metadata    │
└─────────────┘                    └─────────────┘
```

### Process

1. **Sender**: Context values are injected into a header-like data structure (`net/http.Header` type)
2. **Transport**: The header data structure is serialized and sent **over TCP** with the message
3. **Receiver**: Headers are deserialized from the TCP payload and extracted into a new context
4. **Actor**: Receives message with propagated context via `ReceiveContext.Context()`

**Note**: The transport layer is TCP, not HTTP. GoAkt uses `net/http.Header` purely as a convenient key-value data structure format for metadata storage. The actual communication happens over GoAkt's custom TCP-based protocol.

## ContextPropagator Interface

Implement the `ContextPropagator` interface to define propagation logic:

```go
package remote

import (
    "context"
    "net/http"
)

type ContextPropagator interface {
    // Inject writes context values into the header data structure for outbound requests
    Inject(ctx context.Context, headers http.Header) error

    // Extract reads the header data structure from inbound requests and returns a new context
    Extract(ctx context.Context, headers http.Header) (context.Context, error)
}
```

**Important**: The `net/http.Header` type is used as a carrier for metadata **only for convenience** as a string-keyed, multi-valued map. The underlying transport is **TCP**, not HTTP. This design allows GoAkt to leverage existing propagation libraries (like OpenTelemetry) that expect HTTP headers, while maintaining its custom TCP-based protocol for actual communication.

## Basic Implementation

### Simple Key-Value Propagator

```go
package main

import (
    "context"
    "net/http"
)

// Simple propagator for custom metadata
type SimplePropagator struct {
    keys []string // Context keys to propagate
}

func NewSimplePropagator(keys ...string) *SimplePropagator {
    return &SimplePropagator{keys: keys}
}

func (p *SimplePropagator) Inject(ctx context.Context, headers http.Header) error {
    for _, key := range p.keys {
        if val := ctx.Value(key); val != nil {
            if strVal, ok := val.(string); ok {
                headers.Set(key, strVal)
            }
        }
    }
    return nil
}

func (p *SimplePropagator) Extract(ctx context.Context, headers http.Header) (context.Context, error) {
    for _, key := range p.keys {
        if val := headers.Get(key); val != "" {
            ctx = context.WithValue(ctx, key, val)
        }
    }
    return ctx, nil
}
```

### Usage

```go
propagator := NewSimplePropagator("request-id", "user-id")

// Configure server
remotingConfig := remote.NewConfig(
    "0.0.0.0",
    3321,
    remote.WithContextPropagator(propagator),
)

// Configure client
remotingClient := remote.NewRemoting(
    remote.WithRemotingContextPropagator(propagator),
)

system, _ := actor.NewActorSystem(
    "system-1",
    actor.WithRemote(remotingConfig),
)
```

### Sending Messages with Context

```go
// Create context with metadata
ctx := context.Background()
ctx = context.WithValue(ctx, "request-id", "req-12345")
ctx = context.WithValue(ctx, "user-id", "user-789")

// Send message (context values automatically propagated)
err := system.NoSender().RemoteTell(ctx, remoteAddr, &MyMessage{})

// Or from within an actor (context from ReceiveContext is propagated)
func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    switch ctx.Message().(type) {
    case *SendRemote:
        ctx.RemoteTell(remoteAddr, &MyMessage{})
    }
}
```

### Receiving Messages with Context

```go
type RemoteActor struct{}

func (a *RemoteActor) Receive(ctx *actor.ReceiveContext) {
    // Access propagated context values
    if requestID := ctx.Context().Value("request-id"); requestID != nil {
        log.Printf("Request ID: %v", requestID)
    }

    if userID := ctx.Context().Value("user-id"); userID != nil {
        log.Printf("User ID: %v", userID)
    }

    // Process message
    switch msg := ctx.Message().(type) {
    case *MyMessage:
        // Handle with context
    }
}
```

## OpenTelemetry Integration

### Install OpenTelemetry

```bash
go get go.opentelemetry.io/otel
go get go.opentelemetry.io/otel/exporters/jaeger
go get go.opentelemetry.io/otel/sdk/trace
go get go.opentelemetry.io/otel/propagation
```

### OpenTelemetry Propagator

```go
package main

import (
    "context"
    "net/http"

    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/propagation"
)

// OTelPropagator wraps OpenTelemetry's text map propagator
type OTelPropagator struct {
    propagator propagation.TextMapPropagator
}

func NewOTelPropagator() *OTelPropagator {
    return &OTelPropagator{
        propagator: otel.GetTextMapPropagator(),
    }
}

func (p *OTelPropagator) Inject(ctx context.Context, headers http.Header) error {
    p.propagator.Inject(ctx, propagation.HeaderCarrier(headers))
    return nil
}

func (p *OTelPropagator) Extract(ctx context.Context, headers http.Header) (context.Context, error) {
    return p.propagator.Extract(ctx, propagation.HeaderCarrier(headers)), nil
}
```

### Configure OpenTelemetry

```go
package main

import (
    "context"
    "log"

    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/exporters/jaeger"
    "go.opentelemetry.io/otel/propagation"
    "go.opentelemetry.io/otel/sdk/resource"
    sdktrace "go.opentelemetry.io/otel/sdk/trace"
    semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
)

func initTracer(serviceName string) (*sdktrace.TracerProvider, error) {
    // Create Jaeger exporter
    exporter, err := jaeger.New(
        jaeger.WithCollectorEndpoint(
            jaeger.WithEndpoint("http://localhost:14268/api/traces"),
        ),
    )
    if err != nil {
        return nil, err
    }

    // Create tracer provider
    tp := sdktrace.NewTracerProvider(
        sdktrace.WithBatcher(exporter),
        sdktrace.WithResource(resource.NewWithAttributes(
            semconv.SchemaURL,
            semconv.ServiceName(serviceName),
        )),
    )

    // Set global tracer provider
    otel.SetTracerProvider(tp)

    // Set global propagator (W3C Trace Context)
    otel.SetTextMapPropagator(
        propagation.NewCompositeTextMapPropagator(
            propagation.TraceContext{},
            propagation.Baggage{},
        ),
    )

    return tp, nil
}
```

### Complete OpenTelemetry Example

```go
package main

import (
    "context"
    "log"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/remote"
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/attribute"
    "go.opentelemetry.io/otel/trace"
)

type TracedActor struct {
    tracer trace.Tracer
}

func NewTracedActor() *TracedActor {
    return &TracedActor{
        tracer: otel.Tracer("goakt-actors"),
    }
}

func (a *TracedActor) PreStart(ctx *actor.Context) error {
    ctx.ActorSystem().Logger().Info("Traced actor started")
    return nil
}

func (a *TracedActor) Receive(ctx *actor.ReceiveContext) {
    // Extract trace context from propagated context
    actorCtx := ctx.Context()

    // Start span from propagated context
    spanCtx, span := a.tracer.Start(
        actorCtx,
        "actor.receive",
        trace.WithAttributes(
            attribute.String("actor.name", ctx.Self().Name()),
            attribute.String("message.type", fmt.Sprintf("%T", ctx.Message())),
        ),
    )
    defer span.End()

    switch msg := ctx.Message().(type) {
    case *WorkRequest:
        // Process with traced context
        a.processWork(spanCtx, msg)
        ctx.Response(&WorkResponse{Result: "completed"})
    }
}

func (a *TracedActor) processWork(ctx context.Context, work *WorkRequest) {
    // Create child span
    _, span := a.tracer.Start(ctx, "actor.processWork")
    defer span.End()

    // Your business logic here
    span.SetAttributes(attribute.String("work.id", work.ID))
}

func (a *TracedActor) PostStop(ctx *actor.Context) error {
    return nil
}

func main() {
    ctx := context.Background()

    // Initialize tracing
    tp, err := initTracer("goakt-service")
    if err != nil {
        log.Fatal(err)
    }
    defer func() {
        if err := tp.Shutdown(ctx); err != nil {
            log.Printf("Error shutting down tracer: %v", err)
        }
    }()

    // Create OpenTelemetry propagator
    otelPropagator := NewOTelPropagator()

    // Configure remoting with OTel propagator
    remotingConfig := remote.NewConfig(
        "0.0.0.0",
        3321,
        remote.WithContextPropagator(otelPropagator),
    )

    // Create actor system
    system, err := actor.NewActorSystem(
        "traced-system",
        actor.WithRemote(remotingConfig),
    )
    if err != nil {
        log.Fatal(err)
    }

    if err := system.Start(ctx); err != nil {
        log.Fatal(err)
    }
    defer system.Stop(ctx)

    // Spawn traced actor
    _, err = system.Spawn(ctx, "traced-worker", NewTracedActor())
    if err != nil {
        log.Fatal(err)
    }

    log.Println("Traced actor system running")
    select {}
}
```

### Sending Traced Messages

```go
// Create root span
tracer := otel.Tracer("goakt-client")
ctx, span := tracer.Start(context.Background(), "send-work-request")
defer span.End()

// Send message (trace context is propagated)
err := system.NoSender().RemoteTell(ctx, remoteAddr, &WorkRequest{ID: "work-1"})
if err != nil {
    span.RecordError(err)
    log.Fatal(err)
}

// The remote actor will receive the trace context and can create child spans
```

## Advanced Propagation Patterns

### Multi-Value Propagator

Propagate multiple types of metadata:

```go
type MultiPropagator struct {
    propagators []remote.ContextPropagator
}

func NewMultiPropagator(propagators ...remote.ContextPropagator) *MultiPropagator {
    return &MultiPropagator{propagators: propagators}
}

func (p *MultiPropagator) Inject(ctx context.Context, headers http.Header) error {
    for _, propagator := range p.propagators {
        if err := propagator.Inject(ctx, headers); err != nil {
            return err
        }
    }
    return nil
}

func (p *MultiPropagator) Extract(ctx context.Context, headers http.Header) (context.Context, error) {
    var err error
    for _, propagator := range p.propagators {
        ctx, err = propagator.Extract(ctx, headers)
        if err != nil {
            return ctx, err
        }
    }
    return ctx, nil
}
```

### Usage with Multiple Propagators

```go
// Combine OpenTelemetry with custom metadata
multiProp := NewMultiPropagator(
    NewOTelPropagator(),
    NewSimplePropagator("user-id", "tenant-id", "request-id"),
)

remotingConfig := remote.NewConfig(
    "0.0.0.0",
    3321,
    remote.WithContextPropagator(multiProp),
)
```

### Authentication Propagator

Propagate authentication tokens:

```go
type AuthPropagator struct {
    headerName string
}

func NewAuthPropagator(headerName string) *AuthPropagator {
    return &AuthPropagator{headerName: headerName}
}

func (p *AuthPropagator) Inject(ctx context.Context, headers http.Header) error {
    if token := ctx.Value("auth-token"); token != nil {
        if strToken, ok := token.(string); ok {
            headers.Set(p.headerName, strToken)
        }
    }
    return nil
}

func (p *AuthPropagator) Extract(ctx context.Context, headers http.Header) (context.Context, error) {
    if token := headers.Get(p.headerName); token != "" {
        // Validate token here if needed
        ctx = context.WithValue(ctx, "auth-token", token)
    }
    return ctx, nil
}
```

### Using Auth Context in Actors

```go
type SecureActor struct{}

func (a *SecureActor) Receive(ctx *actor.ReceiveContext) {
    // Extract auth token from context
    token := ctx.Context().Value("auth-token")
    if token == nil {
        ctx.Response(&ErrorResponse{Error: "Unauthorized"})
        return
    }

    // Validate and process
    if !a.validateToken(token.(string)) {
        ctx.Response(&ErrorResponse{Error: "Invalid token"})
        return
    }

    // Process authenticated request
    switch msg := ctx.Message().(type) {
    case *SecureRequest:
        a.handleSecure(ctx, msg)
    }
}
```

### Correlation ID Propagator

Track requests across services:

```go
type CorrelationPropagator struct{}

func NewCorrelationPropagator() *CorrelationPropagator {
    return &CorrelationPropagator{}
}

func (p *CorrelationPropagator) Inject(ctx context.Context, headers http.Header) error {
    if corrID := ctx.Value("correlation-id"); corrID != nil {
        headers.Set("X-Correlation-ID", corrID.(string))
    }
    return nil
}

func (p *CorrelationPropagator) Extract(ctx context.Context, headers http.Header) (context.Context, error) {
    corrID := headers.Get("X-Correlation-ID")
    if corrID == "" {
        // Generate new correlation ID if not present
        corrID = generateCorrelationID()
    }
    return context.WithValue(ctx, "correlation-id", corrID), nil
}

func generateCorrelationID() string {
    return fmt.Sprintf("corr-%d", time.Now().UnixNano())
}
```

## Integration Patterns

### HTTP API to Actors

Propagate context from HTTP requests to actors:

```go
package main

import (
    "context"
    "net/http"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/address"
)

type HTTPHandler struct {
    system actor.ActorSystem
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // Extract trace context from HTTP request
    ctx := r.Context()

    // Add request metadata
    ctx = context.WithValue(ctx, "request-id", r.Header.Get("X-Request-ID"))
    ctx = context.WithValue(ctx, "user-id", r.Header.Get("X-User-ID"))

    // Send to actor with propagated context
    actorAddr := address.New("processor", "system", "localhost", 3321)

    response, err := h.system.NoSender().RemoteAsk(ctx, actorAddr, &ProcessRequest{Data: readBody(r)}, 30*time.Second)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    // response is *anypb.Any; unpack as needed before writeResponse
    writeResponse(w, response)
}
```

## Best Practices

### Propagator Design

1. **Keep it lightweight**: Don't propagate large values
2. **Be selective**: Only propagate necessary metadata
3. **Validate on extract**: Sanitize and validate extracted values
4. **Handle missing values**: Gracefully handle absent context values
5. **Use standard keys**: Follow industry conventions (e.g., W3C Trace Context)

### Context Values

1. **Use typed keys**: Define custom types for context keys to avoid collisions
2. **Document keys**: Maintain clear documentation of propagated keys
3. **Limit scope**: Don't use context for passing data that belongs in messages
4. **Thread-safe**: Context is immutable; don't try to modify values

```go
// Good: Typed key
type contextKey string

const (
    UserIDKey     contextKey = "user-id"
    RequestIDKey  contextKey = "request-id"
)

// Usage
ctx = context.WithValue(ctx, UserIDKey, "user-123")
userID := ctx.Value(UserIDKey).(string)
```

### Security

1. **Sanitize metadata**: Validate and sanitize extracted metadata from the header structure
2. **Limit metadata size**: Prevent oversized metadata attacks (even though transported via TCP)
3. **Don't log sensitive data**: Be careful with trace/debug logging
4. **Validate tokens**: Always validate authentication tokens on extraction
5. **Use allowlists**: Only propagate known, safe metadata fields

```go
func (p *SecurePropagator) Extract(ctx context.Context, headers http.Header) (context.Context, error) {
    // Validate metadata sizes (received via TCP)
    for key, values := range headers {
        if len(key) > 256 || len(strings.Join(values, "")) > 4096 {
            return ctx, errors.New("metadata size exceeded")
        }
    }

    // Only extract allowlisted metadata fields
    allowlist := []string{"X-Request-ID", "X-User-ID", "traceparent"}
    for _, key := range allowlist {
        if val := headers.Get(key); val != "" {
            ctx = context.WithValue(ctx, key, val)
        }
    }

    return ctx, nil
}
```

### Performance

1. **Avoid heavy computation**: Inject/Extract are called on every message
2. **Cache parsed values**: Don't re-parse metadata unnecessarily
3. **Use efficient serialization**: Keep metadata payloads small (transmitted over TCP)
4. **Monitor overhead**: Track latency added by propagation

### Testing

Test propagation thoroughly:

```go
func TestContextPropagation(t *testing.T) {
    propagator := NewSimplePropagator("request-id")

    // Test inject
    ctx := context.WithValue(context.Background(), "request-id", "test-123")
    headers := http.Header{}

    err := propagator.Inject(ctx, headers)
    assert.NoError(t, err)
    assert.Equal(t, "test-123", headers.Get("request-id"))

    // Test extract
    newCtx, err := propagator.Extract(context.Background(), headers)
    assert.NoError(t, err)
    assert.Equal(t, "test-123", newCtx.Value("request-id"))
}
```

## Debugging

### Enable Logging

Add logging to your propagator:

```go
func (p *LoggingPropagator) Inject(ctx context.Context, headers http.Header) error {
    log.Printf("Injecting context: %v", ctx)
    err := p.delegate.Inject(ctx, headers)
    log.Printf("Injected headers: %v", headers)
    return err
}

func (p *LoggingPropagator) Extract(ctx context.Context, headers http.Header) (context.Context, error) {
    log.Printf("Extracting from headers: %v", headers)
    newCtx, err := p.delegate.Extract(ctx, headers)
    log.Printf("Extracted context: %v", newCtx)
    return newCtx, err
}
```

### Verify Propagation

Check that context values are propagating:

```go
type DebugActor struct{}

func (a *DebugActor) Receive(ctx *actor.ReceiveContext) {
    // Log all context values
    ctx.Logger().Infof("Request ID: %v", ctx.Context().Value("request-id"))
    ctx.Logger().Infof("User ID: %v", ctx.Context().Value("user-id"))
    ctx.Logger().Infof("Trace ID: %v", ctx.Context().Value("trace-id"))
}
```

### Tracing UI

Use Jaeger or Zipkin UI to visualize traces:

```bash
# Start Jaeger all-in-one
docker run -d --name jaeger \
  -p 5775:5775/udp \
  -p 6831:6831/udp \
  -p 6832:6832/udp \
  -p 5778:5778 \
  -p 16686:16686 \
  -p 14268:14268 \
  -p 14250:14250 \
  jaegertracing/all-in-one:latest

# Access UI at http://localhost:16686
```

## Troubleshooting

### Context Values Not Propagating

**Symptoms:**

- Context values are nil on remote actors
- Trace spans are disconnected

**Common Causes:**

1. Propagator not configured on both client and server
2. Context key mismatch (different keys on inject vs extract)
3. Metadata not being serialized correctly for TCP transport

**Solutions:**

- Verify `WithContextPropagator()` is set on both remoting configs
- Add logging to verify inject/extract are called
- Check the header data structure contains expected values before TCP serialization

### Performance Degradation

**Symptoms:**

- Increased message latency
- High CPU usage

**Common Causes:**

- Heavy computation in Inject/Extract
- Large metadata payloads (increases TCP message size)
- Too many context values being propagated

**Solutions:**

- Profile Inject/Extract methods
- Reduce number of propagated values
- Optimize serialization/deserialization for TCP transport

### Memory Leaks

**Symptoms:**

- Growing memory usage over time
- Context objects not being garbage collected

**Common Causes:**

- Storing references to contexts
- Not using context correctly (mutating instead of deriving)

**Solutions:**

- Never store contexts; always pass as function parameters
- Use `context.WithValue()` to derive new contexts
- Ensure proper context cancellation

## Next Steps

- [Remoting Overview](overview.md): Learn remoting fundamentals
- [TLS Configuration](tls.md): Secure your remoting with TLS
- [Observability](../observability/metrics.md): Monitor distributed traces
