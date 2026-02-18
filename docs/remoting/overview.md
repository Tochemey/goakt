# Remoting Overview

Remoting enables actors on different actor systems to communicate across network boundaries. It provides the foundation for distributed actor systems and is a prerequisite for clustering.

## Table of Contents

- ⚡ [Quick reference](#quick-reference)
- 🤔 [What is Remoting?](#what-is-remoting)
- 🏗️ [Architecture](#architecture)
- ⚙️ [Enabling Remoting](#enabling-remoting)
- 📤 [Remote Operations](#remote-operations)
- 🔀 [Remoting vs Clustering](#remoting-vs-clustering)
- ⚡ [Performance Tuning](#performance-tuning)
- ⚠️ [Error Handling](#error-handling)
- ✅ [Best Practices](#best-practices)
- 🔧 [Troubleshooting](#troubleshooting)

---

## Quick reference

| Goal                             | API                                                                                       |
|----------------------------------|-------------------------------------------------------------------------------------------|
| Enable remoting                  | `actor.WithRemote`                                                                        |
| Remote Tell (from outside actor) | `system.NoSender().RemoteTell`                                                            |
| Remote Ask (from outside)        | `system.NoSender().RemoteAsk`                                                             |
| From inside actor                | `ctx.RemoteTell` / `ctx.RemoteAsk`                                                        |
| Build remote address             | `address.New`                                                                             |
| Lookup remote actor              | `system.NoSender().RemoteLookup`                                                          |
| Grains (cluster)                 | `system.GrainIdentity` then `TellGrain` / `AskGrain` (no host/port; works across cluster) |

## What is Remoting?

Remoting in GoAkt allows:

- **Remote actor communication**: Send messages to actors on different nodes
- **Actor lifecycle management**: Spawn, stop, and restart actors remotely
- **Actor discovery**: Look up actors running on remote nodes
- **Clustering foundation**: Required for cluster mode operations
- **Cross-datacenter messaging**: Communicate with actors in different datacenters
- **Secure communication**: Optional TLS encryption for production deployments

## Architecture

Remoting uses a custom TCP-based protocol built on Protocol Buffers:

```
Actor System A (Node 1)              Actor System B (Node 2)
┌─────────────────────┐             ┌─────────────────────┐
│  Actor A            │             │  Actor B            │
│    ↓                │             │    ↑                │
│  Remoting Client    │   TCP/TLS   │  Remoting Server    │
│    └────────────────┼─────────────►──────────────┘     │
└─────────────────────┘             └─────────────────────┘
```

### Components

1. **Remoting Server**: Listens for incoming remote requests (runs within the actor system)
2. **Remoting Client**: Connects to remote servers to send requests
3. **Protocol Buffers**: Message serialization format
4. **Connection Pool**: Reusable TCP connections for performance
5. **Compression**: Optional payload compression (Zstd, Gzip, Brotli)
6. **TLS**: Optional encryption layer

## Enabling Remoting

### Basic Configuration

Enable remoting by creating a config with host and port, then passing it to the actor system:

```go
import (
    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/remote"
)

remotingConfig := remote.NewConfig("localhost", 3321)
system, err := actor.NewActorSystem(
    "my-system",
    actor.WithRemote(remotingConfig),
)
if err != nil {
    log.Fatal(err)
}
```

This automatically:

- Starts a remoting server listening on `localhost:3321`
- Creates a remoting client for outbound connections
- Uses default settings (Zstd compression, no TLS, 16MB max frame size)

### Advanced Configuration

Use `remote.Config` for more control:

```go
import (
    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/remote"
)

remotingConfig := remote.NewConfig(
    "0.0.0.0",  // Bind address
    3321,        // Port
    remote.WithMaxFrameSize(32 * 1024 * 1024),      // 32MB frames
    remote.WithWriteTimeout(15 * time.Second),       // Write timeout
    remote.WithReadIdleTimeout(10 * time.Second),    // Read idle timeout
    remote.WithCompression(remote.ZstdCompression),  // Compression
)

system, err := actor.NewActorSystem(
    "my-system",
    actor.WithRemote(remotingConfig),
)
```

### Configuration Options

#### WithMaxFrameSize

Set the maximum size for a single message frame:

```go
remote.WithMaxFrameSize(32 * 1024 * 1024) // 32MB
```

- **Range**: 16KB to 16MB
- **Default**: 16MB
- **Use case**: Increase for large messages; decrease to limit memory usage

#### WithWriteTimeout

Set the timeout for writing data to connections:

```go
remote.WithWriteTimeout(15 * time.Second)
```

- **Default**: 10 seconds
- **Use case**: Adjust based on network latency and payload size

#### WithReadIdleTimeout

Set the timeout for health checks on idle connections:

```go
remote.WithReadIdleTimeout(10 * time.Second)
```

- **Default**: 10 seconds
- **Behavior**: Sends PING frames to detect broken connections
- **Use case**: Decrease for faster failure detection; increase for high-latency networks

#### WithCompression

Set the compression algorithm:

```go
remote.WithCompression(remote.ZstdCompression)
```

**Available algorithms:**

| Algorithm                   | Pros                           | Cons                  | Use Case                       |
|-----------------------------|--------------------------------|-----------------------|--------------------------------|
| `ZstdCompression` (default) | Fast, excellent ratio, low CPU | Requires zstd library | Recommended for all workloads  |
| `GzipCompression`           | Universal, battle-tested       | Slower than Zstd      | Legacy compatibility           |
| `BrotliCompression`         | Best compression ratio         | Slower compression    | Bandwidth-constrained networks |
| `NoCompression`             | Zero CPU overhead              | High bandwidth usage  | Fast networks, CPU bottlenecks |

#### WithContextPropagator

Enable context value propagation for distributed tracing:

```go
remote.WithContextPropagator(myPropagator)
```

See [Context Propagation](context_propagation.md) for details.

## Remote Operations

### Remote Messaging

Send messages to actors on remote nodes. **From inside an actor** use the receive context; **from outside** (e.g. main) use the system's `NoSender()` PID:

#### RemoteTell (Fire-and-Forget)

**From inside an actor:**
```go
ctx.RemoteTell(addr, message)
```

**From outside (e.g. main):**
```go
import "github.com/tochemey/goakt/v3/address"

addr := address.New("actor-name", "remote-system", "remote-host", 3321)
message := &MyRequest{Data: "payload"}
err := system.NoSender().RemoteTell(ctx, addr, message)
if err != nil {
    log.Fatal(err)
}
```

**Characteristics:**

- Asynchronous (no response)
- Fast (one-way communication)
- No delivery guarantee at application level
- Returns when RPC completes

#### RemoteAsk (Request-Response)

**From inside an actor:** `response := ctx.RemoteAsk(addr, message, timeout)` (returns `*anypb.Any`; check for nil and `ctx.Err()`).

**From outside:**
```go
addr := address.New("actor-name", "remote-system", "remote-host", 3321)
response, err := system.NoSender().RemoteAsk(ctx, addr, &MyRequest{Data: "query"}, 5*time.Second)
if err != nil {
    log.Fatal(err)
}
// response is *anypb.Any; use response.UnmarshalNew() or type assert after unpacking
myResponse, err := response.UnmarshalNew()
```

**Characteristics:**

- Synchronous (blocks until response)
- Returns first response received
- Timeout controls server-side processing window
- Context cancellation/deadline also applies

#### RemoteBatchTell / RemoteBatchAsk

Batch operations are available on **PID**: `pid.RemoteBatchTell(ctx, to, messages)` and `pid.RemoteBatchAsk(ctx, to, messages, timeout)`. Use `system.NoSender()` when calling from outside an actor. From inside an actor use `ctx.RemoteBatchTell(addr, messages)` and `ctx.RemoteBatchAsk(addr, messages, timeout)`.

### Remote Actor Management

#### RemoteLookup

Find an actor on a remote node. **From inside an actor:** `addr := ctx.RemoteLookup("remote-host", 3321, "actor-name")` (check for nil). **From outside:**
```go
addr, err := system.NoSender().RemoteLookup(ctx, "remote-host", 3321, "actor-name")
if err != nil {
    log.Fatal(err)
}
if addr == nil || addr.Equals(address.NoSender()) {
    log.Println("Actor not found")
} else {
    log.Printf("Actor found at: %s\n", addr.String())
}
```

#### RemoteSpawn

Create an actor on a remote node. Use a **PID** (e.g. `system.NoSender()` when outside an actor):

```go
err := system.NoSender().RemoteSpawn(ctx, "remote-host", 3321, "remote-worker", "WorkerActor")
```

**Note:** The actor kind must be registered on the remote node.

#### RemoteStop

Stop an actor on a remote node:

```go
err := system.NoSender().RemoteStop(ctx, "remote-host", 3321, "actor-name")
```

**ReSpawn** and **Reinstate** are also available on PID when needed.

### Remote Grain Operations

#### Activating grains

Grains are activated on demand when you call `system.AskGrain` or `system.TellGrain` with a `GrainIdentity`; no separate "RemoteActivateGrain" call is needed from application code.

#### TellGrain / AskGrain

For grains, use the actor system with a **GrainIdentity** (works across cluster; no host/port needed). Obtain the identity via `GrainIdentity` with a factory; activation happens on demand when you send:

```go
identity, err := system.GrainIdentity(ctx, "user-123", func(ctx context.Context) (actor.Grain, error) {
    return &UserGrain{}, nil
})
if err != nil {
    log.Fatal(err)
}
err = system.TellGrain(ctx, identity, &UpdateUserRequest{Name: "John"})
// request-response:
response, err := system.AskGrain(ctx, identity, &GetUserRequest{}, 5*time.Second)
```

## Remoting vs Clustering

| Feature              | Remoting                           | Clustering                          |
|----------------------|------------------------------------|-------------------------------------|
| **Purpose**          | Point-to-point communication       | Distributed actor management        |
| **Discovery**        | Manual (hardcoded addresses)       | Automatic (discovery provider)      |
| **Actor lookup**     | Must know exact address            | Transparent lookup by name          |
| **Fault tolerance**  | No automatic recovery              | Automatic actor relocation          |
| **Load balancing**   | Manual                             | Automatic placement strategies      |
| **Setup complexity** | Simple                             | Moderate                            |
| **Use case**         | Few known nodes, simple topologies | Dynamic clusters, high availability |

**When to use remoting alone:**

- Small deployments with fixed nodes
- Testing and development
- Simple remote actor scenarios
- When you need fine-grained control

**When to use clustering:**

- Dynamic node topology
- Automatic failover required
- Load distribution across many nodes
- Production deployments needing high availability

## Performance Tuning

### Connection Pooling

The remoting client pools connections per endpoint:

```go
// Create remoting with larger pool
remoting := remote.NewRemoting(
    remote.WithRemotingMaxIdleConns(16), // 16 connections per endpoint
)
```

**Defaults:**

- 8 connections per endpoint
- 30s idle timeout
- 5s dial timeout
- 15s TCP keep-alive

**When to adjust:**

- Increase pool size for high-concurrency workloads
- Decrease idle timeout to reclaim resources faster
- Increase dial timeout for slow networks

### Compression

Choose compression based on your workload:

```go
// Server-side (actor system)
remotingConfig := remote.NewConfig(
    "0.0.0.0",
    3321,
    remote.WithCompression(remote.ZstdCompression),
)

// Client-side (if using standalone remoting client)
remoting := remote.NewRemoting(
    remote.WithRemotingCompression(remote.ZstdCompression),
)
```

**Recommendations:**

- **ZstdCompression** (default): Best overall choice (50-70% bandwidth reduction with low CPU)
- **NoCompression**: Only if CPU is extremely limited or network is very fast
- **BrotliCompression**: Best ratio for bandwidth-constrained networks (slower CPU)
- **GzipCompression**: Legacy/compatibility scenarios

**Both client and server must use the same compression algorithm.**

### Frame Size

Adjust maximum frame size based on your message sizes:

```go
remotingConfig := remote.NewConfig(
    "0.0.0.0",
    3321,
    remote.WithMaxFrameSize(32 * 1024 * 1024), // 32MB
)
```

- **Default**: 16MB
- **Range**: 16KB to 16MB
- **Trade-off**: Larger frames allow bigger messages but increase memory usage

### Timeouts

Configure timeouts to match network characteristics:

```go
remotingConfig := remote.NewConfig(
    "0.0.0.0",
    3321,
    remote.WithWriteTimeout(15 * time.Second),
    remote.WithReadIdleTimeout(10 * time.Second),
)
```

**WriteTimeout**: How long to wait when writing data to a connection
**ReadIdleTimeout**: How long before sending PING health checks

## Error Handling

### Remote Operation Errors

```go
err := system.NoSender().RemoteTell(ctx, addr, message)
if err != nil {
    switch {
    case errors.Is(err, gerrors.ErrInvalidMessage):
        // Message couldn't be serialized
    case errors.Is(err, gerrors.NewErrActorNotFound("actor-name")):
        // Actor doesn't exist on remote node
    case errors.Is(err, context.DeadlineExceeded):
        // Timeout or context cancelled
    default:
        // Network errors, connection failures, etc.
    }
}
```

### Common Error Codes

When errors originate from the remote system, they carry error codes:

- **NotFound**: Actor doesn't exist
- **DeadlineExceeded**: Operation timed out
- **Unavailable**: Remote node not reachable
- **ResourceExhausted**: Remote node overloaded or resource limits hit
- **FailedPrecondition**: Operation precondition not met (e.g., actor already exists)

## Best Practices

### Configuration

1. **Use 0.0.0.0 for bind address**: Allows automatic IP resolution in containerized environments
2. **Match compression on all nodes**: Ensure client and server use the same algorithm
3. **Set appropriate frame size**: Based on your largest expected message
4. **Configure timeouts for your network**: WAN requires longer timeouts than LAN

### Messaging

1. **Prefer Tell over Ask**: One-way messaging is faster and more resilient
2. **Set reasonable timeouts**: Ask operations should timeout before clients give up
3. **Use batch operations**: Reduce network overhead when sending multiple messages
4. **Handle all errors**: Network is unreliable; always check for errors

### Operations

1. **Cache addresses**: Looking up addresses repeatedly is inefficient
2. **Use connection pooling**: Reuse remoting clients rather than creating new ones
3. **Monitor metrics**: Track message latency, error rates, and connection pool usage
4. **Enable TLS in production**: Secure communication between nodes

### Development vs Production

**Development:**

```go
// import "github.com/tochemey/goakt/v3/remote"
system, err := actor.NewActorSystem(
    "dev-system",
    actor.WithRemote(remote.NewConfig("localhost", 3321)),
)
```

**Production:**

```go
remotingConfig := remote.NewConfig(
    "0.0.0.0",
    3321,
    remote.WithMaxFrameSize(32 * 1024 * 1024),
    remote.WithWriteTimeout(15 * time.Second),
    remote.WithReadIdleTimeout(10 * time.Second),
    remote.WithCompression(remote.ZstdCompression),
)

system, err := actor.NewActorSystem(
    "prod-system",
    actor.WithRemote(remotingConfig),
    actor.WithTLS(&tls.Info{
        ClientConfig: clientTLSConfig,
        ServerConfig: serverTLSConfig,
    }),
)
```

## Troubleshooting

### Connection Refused

**Symptoms:**

- `connection refused` errors
- Cannot reach remote actors

**Solutions:**

- Verify remote node is running and remoting is enabled
- Check firewall rules allow connections on remoting port
- Confirm correct host and port in addresses
- Check network connectivity between nodes

### Frame Size Exceeded

**Symptoms:**

- `frame size exceeded` errors
- Large messages fail to send

**Solutions:**

- Increase `WithMaxFrameSize()` on both client and server
- Split large messages into smaller chunks
- Compress payloads before sending
- Review message design (avoid large embedded data)

### Compression Mismatch

**Symptoms:**

- Unreadable frames
- Deserialization errors
- Connection failures

**Solutions:**

- Ensure both nodes use the same compression algorithm
- Check `WithCompression()` configuration matches
- Verify all cluster nodes have consistent remoting config

### Timeout Errors

**Symptoms:**

- `Ask` operations timing out
- `DeadlineExceeded` errors

**Solutions:**

- Increase timeout duration
- Check network latency (use ping/traceroute)
- Verify remote actor is responsive (not blocked)
- Monitor remote node resource usage

### TLS Handshake Failures

**Symptoms:**

- TLS handshake errors
- Certificate verification failures

**Solutions:**

- Verify certificates are valid and not expired
- Ensure both nodes use the same root CA
- Check `ClientConfig` and `ServerConfig` are properly set
- Review TLS version compatibility

See [TLS Configuration](tls.md) for detailed TLS setup.