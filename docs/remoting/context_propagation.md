# Context Propagation

Context propagation lets cross-cutting metadata (trace IDs, correlation IDs, auth tokens) cross remoting boundaries over TCP. Essential for distributed tracing and request tracking.

## Table of Contents

- 📖 [Overview](#overview)
- 💡 [Why It Matters](#why-it-matters)
- 🔄 [How It Works](#how-it-works)
- 🔌 [ContextPropagator Interface](#contextpropagator-interface)
- 🚀 [Basic Implementation](#basic-implementation)
- ✅ [Best Practices](#best-practices)
- 🐛 [Debugging & Troubleshooting](#debugging--troubleshooting)

---

## Overview

- **Propagates context values** across remote actor boundaries via TCP
- **Integrates with tracing**: OpenTelemetry, Jaeger, Zipkin
- **Supports custom metadata**: Auth, correlation IDs, feature flags
- **Transparent to actors**: Injected on send, extracted on receive; use `ctx.Context()` in `Receive`
- **Works with remoting and clustering**

**Note:** Transport is **TCP**, not HTTP. `net/http.Header` is used only as a string-keyed carrier for metadata; communication uses GoAkt’s TCP protocol.

## Why It Matters

**Preserving intent across hops**  
Without a propagator, each remote hop starts with a “fresh” context: deadlines and cancellations are lost, logs lose correlation IDs, and downstream services can’t make auth decisions based on upstream user info. ContextPropagator gives GoAkt a framework hook to carry that metadata from inbound HTTP → actors, between cluster nodes, and back out over HTTP, so `ReceiveContext.Context()` and `GrainContext.Context()` see a single logical request context end-to-end.

**Decoupling headers from logic**  
Environments encode metadata differently (X-Request-ID vs X-Correlation-ID, W3C traceparent vs B3, different auth schemes). Baking these into GoAkt would lock in tracing/auth strategies and tangle protocol details with application code. Instead, GoAkt depends only on the abstract contract: Inject (“from context.Context to headers”) and Extract (“from headers to a derived context.Context”). Each deployment supplies its own ContextPropagator that matches its header conventions and infrastructure.

**Safety & observability**  
Propagation can leak secrets, inflate headers, or break traces if misused. Centralising it in ContextPropagator gives a single, auditable place to decide which context keys may leave the process, cap header sizes, validate IDs (logging or failing when invalid), and enforce consistent header naming. This is far safer and more observable than ad-hoc header handling scattered across handlers and actors.

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

```go
type SimplePropagator struct{ keys []string }

func (p *SimplePropagator) Inject(ctx context.Context, headers http.Header) error {
    for _, key := range p.keys {
        if v := ctx.Value(key); v != nil {
            if s, ok := v.(string); ok {
                headers.Set(key, s)
            }
        }
    }
    return nil
}

func (p *SimplePropagator) Extract(ctx context.Context, headers http.Header) (context.Context, error) {
    for _, key := range p.keys {
        if v := headers.Get(key); v != "" {
            ctx = context.WithValue(ctx, key, v)
        }
    }
    return ctx, nil
}

// Wire: server
cfg := remote.NewConfig("0.0.0.0", 3321, remote.WithContextPropagator(&SimplePropagator{keys: []string{"request-id", "user-id"}}))

// Send: ctx with values is passed to Inject
ctx = context.WithValue(ctx, "request-id", "req-1")
system.NoSender().RemoteTell(ctx, addr, msg)

// Receive: in actor
requestID := ctx.Context().Value("request-id")
```

## Best Practices

- **Lightweight:** Propagate only small, necessary metadata.
- **Validate on extract:** Sanitize and validate values; use allowlists for header keys and size limits.
- **Typed context keys:** Use custom types for keys to avoid collisions (e.g. `type contextKey string`).
- **Security:** Don’t log sensitive data; validate tokens on extract; limit metadata size.
- **Performance:** Inject/Extract run per message; avoid heavy work and large payloads.

## Debugging & Troubleshooting

- **Verify propagation:** Log in the actor (`ctx.Context().Value("request-id")` etc.) or wrap your propagator with a delegating one that logs Inject/Extract inputs and outputs.
- **Not propagating:** Ensure the same (or compatible) propagator is configured on both sides; check key names and that the context passed to send actually contains the values.
- **Performance:** Profile Inject/Extract; reduce number and size of propagated values.
- **Memory:** Don’t store context references; derive with `context.WithValue` and pass as parameters.

