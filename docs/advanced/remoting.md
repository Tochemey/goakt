# Remoting

## Overview

The remoting layer enables actors on different nodes to exchange messages over TCP. It is configured via `WithRemote` (
passing `remote.Config`). From inside the actor system, you interact through `ActorOf` and messaging. For external callers (CLI, API servers, batch jobs), use the [Client](client.md) package to connect to the cluster without running an actor system.

## Configuration

Create a `remote.Config` with `remote.NewConfig(bindAddr, bindPort, opts...)`. The bind address should be a concrete
IP (e.g., `127.0.0.1` or `0.0.0.0`), not a hostname. Pass it to the actor system via `actor.WithRemote(cfg)`.

| Option                  | Purpose                            |
|-------------------------|------------------------------------|
| `WithCompression`       | None, Gzip, Zstd (default), Brotli |
| `WithMaxFrameSize`      | Max frame size (default 16MB)      |
| `WithWriteTimeout`      | Connection write timeout           |
| `WithReadIdleTimeout`   | Health check / ping interval       |
| `WithSerializers`       | Custom message serialization       |
| `WithContextPropagator` | Propagate trace IDs, auth tokens   |

## Transport

- Length-prefixed TCP frames
- Optional TLS
- Compression: None, Gzip, Zstd (default), Brotli

## Messaging across nodes

Once remoting is configured, `ActorOf(ctx, name)` returns a `*PID` whether the actor is local or remote. Use `Tell` and
`Ask` as usual; the framework routes remote messages automatically. No code changes are needed for remote vs local.

## Context propagation

When messages cross node boundaries, request-scoped metadata (trace IDs, auth tokens, correlation IDs) must travel with them. Implement `remote.ContextPropagator` and pass it via `WithContextPropagator` on the remote config.

### The ContextPropagator interface

```go
type ContextPropagator interface {
    Inject(ctx context.Context, headers http.Header) error
    Extract(ctx context.Context, headers http.Header) (context.Context, error)
}
```

| Method      | Purpose                                                                                                                    |
|-------------|----------------------------------------------------------------------------------------------------------------------------|
| **Inject**  | Write context values into headers for outgoing requests. Called before sending a remote message.                           |
| **Extract** | Read headers from an incoming request and return a context with propagated values. Called when receiving a remote message. |

The carrier is `http.Header` (string-keyed map)—the transport is TCP, not HTTP. The receiving actor's `ReceiveContext.Context()` or `GrainContext.Context()` will contain the propagated values.

### Implementation notes

- Be stateless and safe for concurrent use
- Use stable, well-known header keys
- Avoid leaking sensitive data unless required
- Validate inputs to guard against injection or oversized metadata
