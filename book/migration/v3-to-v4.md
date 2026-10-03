# Migrating from v3 to v4

Verified against: `cf7a7c6d` and the uncommitted changes of branch `issue-1432` (2026-10-03): every v4 statement checked against the code

## Contents

- [What changed](#what-changed)
- [Quick reference](#quick-reference)
- [Breaking changes](#breaking-changes)
  - [1. Module path](#1-module-path)
  - [2. Messages are `any`](#2-messages-are-any)
  - [3. System messages moved to the `actor` package](#3-system-messages-moved-to-the-actor-package)
  - [4. `*PID` is the only actor reference](#4-pid-is-the-only-actor-reference)
  - [5. One lookup: `ActorOf`](#5-one-lookup-actorof)
  - [6. `Actors` replaces `ActorRefs`](#6-actors-replaces-actorrefs)
  - [7. Remote scheduling uses the ordinary scheduler](#7-remote-scheduling-uses-the-ordinary-scheduler)
  - [8. `GetPartition` is now `Partition`](#8-getpartition-is-now-partition)
  - [9. Addresses became paths](#9-addresses-became-paths)
  - [10. Remoting is configured, not constructed](#10-remoting-is-configured-not-constructed)
  - [11. The `Logger` interface](#11-the-logger-interface)
- [New in v4](#new-in-v4)
  - [Serializers for any Go type](#serializers-for-any-go-type)
  - [New error sentinels](#new-error-sentinels)
  - [`PID.Kind`](#pidkind)
  - [Logger implementations](#logger-implementations)
- [Migration checklist](#migration-checklist)

## What changed

v4 makes the API smaller and more uniform:

- **One actor reference.** `*PID` stands for local and remote actors alike.
- **One lookup.** `ActorOf` finds an actor on this node or anywhere in the cluster.
- **One scheduler.** The same `Schedule*` methods serve local and remote actors.
- **Any message type.** Messages are `any` instead of `proto.Message`. Protocol Buffers still work, and plain Go types can cross the network through the CBOR or JSON serializer.
- **Configured remoting.** You enable remoting with a `remote.Config`. The client the actor system uses to reach other nodes is internal.
- **Paths instead of addresses.** The `Path` interface replaces `*address.Address`.

## Quick reference

| v3 | v4 | Section |
|---|---|---|
| `github.com/tochemey/goakt/v3` | `github.com/tochemey/goakt/v4` | [1](#1-module-path) |
| `proto.Message` in handlers and call sites | `any` | [2](#2-messages-are-any) |
| `goaktpb.*` types | `actor.*` types, e.g. `actor.PostStart`, `actor.PoisonPill` | [3](#3-system-messages-moved-to-the-actor-package) |
| `ActorRef` | `*PID` | [4](#4-pid-is-the-only-actor-reference) |
| `ActorOf` returning `(addr, pid, err)` | `ActorOf` returning `(*PID, error)` | [5](#5-one-lookup-actorof) |
| `LocalActor`, `RemoteActor` | `ActorOf` | [5](#5-one-lookup-actorof) |
| `Actors()` and `ActorRefs(ctx, timeout)` | `Actors(ctx, timeout)` | [6](#6-actors-replaces-actorrefs) |
| `RemoteScheduleOnce`, `RemoteSchedule`, `RemoteScheduleWithCron` | `ScheduleOnce`, `Schedule`, `ScheduleWithCron` with a PID from `ActorOf` | [7](#7-remote-scheduling-uses-the-ordinary-scheduler) |
| `GetPartition` | `Partition` | [8](#8-getpartition-is-now-partition) |
| `pid.Address()` | `pid.Path()` | [9](#9-addresses-became-paths) |
| `ctx.SenderAddress()` | `ctx.Sender().Path()` | [9](#9-addresses-became-paths) |
| `ctx.ReceiverAddress()` | `ctx.Self().Path()` | [9](#9-addresses-became-paths) |
| `testkit.Probe.SenderAddress()` | `Probe.Sender()`, then `Path()` or `IsRemote()` | [9](#9-addresses-became-paths) |
| `address` package | no public package; use the `Path` interface | [9](#9-addresses-became-paths) |
| `remote.Remoting`, `remote.Client`, `remote.NewRemoting()`, `remote.NewClient()` | `actor.WithRemote(config)` and the PID methods; the `client` package outside an actor system | [10](#10-remoting-is-configured-not-constructed) |
| `WithRemoting` | `WithRemote` | [10](#10-remoting-is-configured-not-constructed) |
| Custom `Logger` implementations | Implement the context-aware methods, `LogLevel`, `Enabled`, `With`, `Flush` and `StdLogger` | [11](#11-the-logger-interface) |

## Breaking changes

### 1. Module path

The module is `github.com/tochemey/goakt/v4`. Change every import from `github.com/tochemey/goakt/v3/...` to `github.com/tochemey/goakt/v4/...` and run `go mod tidy`. v4 requires Go 1.26.

### 2. Messages are `any`

Every message-passing API of the actor system uses `any` instead of `proto.Message` for messages and replies:

- `actor.Tell`, `actor.Ask`, `PID.Tell`, `PID.Ask` and their batch forms.
- `ReceiveContext.Message`, `ReceiveContext.Response`, `ReceiveContext.Tell` and `ReceiveContext.Ask`.
- `PID.PipeTo` and `PID.PipeToName`: the task is `func() (any, error)`.
- `ScheduleOnce`, `Schedule` and `ScheduleWithCron`.
- `AskGrain` and `TellGrain`.

Messages between actors on the same node are passed as they are and never serialized. A message that crosses the network needs a serializer: Protocol Buffers messages have one by default, and other types must be registered (see [Serializers for any Go type](#serializers-for-any-go-type)).

The standalone `client` package is the exception. `client.Client.Tell` and `client.Client.TellGrain` still take a `proto.Message`. `client.Client.Ask` and `client.Client.AskGrain` take `any`.

**What to do:** replace `proto.Message` with `any` in handlers and at call sites. Protocol Buffers messages need no other change.

```go
package orders

import (
	"github.com/tochemey/goakt/v4/actor"
)

// PlaceOrder and OrderPlaced are plain Go types. Protocol Buffers messages work the same way.
type PlaceOrder struct{ ID string }
type OrderPlaced struct{ ID string }

type OrderActor struct{}

var _ actor.Actor = (*OrderActor)(nil)

func (a *OrderActor) PreStart(*actor.Context) error { return nil }

func (a *OrderActor) Receive(ctx *actor.ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *actor.PostStart:
		ctx.Logger().Info("order actor started")
	case *PlaceOrder:
		ctx.Response(&OrderPlaced{ID: msg.ID})
	default:
		ctx.Unhandled()
	}
}

func (a *OrderActor) PostStop(*actor.Context) error { return nil }
```

### 3. System messages moved to the `actor` package

The `goaktpb` package is gone. System messages are plain Go structs in the `actor` package and arrive as pointers:

| v3 | v4 |
|---|---|
| `goaktpb.PostStart` | `actor.PostStart` |
| `goaktpb.PoisonPill` | `actor.PoisonPill` |
| `goaktpb.Terminated` | `actor.Terminated` |
| `goaktpb.Deadletter` | `actor.Deadletter` |
| `goaktpb.NoMessage` | `actor.NoMessage` |
| `goaktpb.ActorStarted` | `actor.ActorStarted` |
| `goaktpb.ActorStopped` | `actor.ActorStopped` |
| `goaktpb.ActorPassivated` | `actor.ActorPassivated` |
| `goaktpb.ActorChildCreated` | `actor.ActorChildCreated` |
| `goaktpb.ActorRestarted` | `actor.ActorRestarted` |
| `goaktpb.ActorSuspended` | `actor.ActorSuspended` |
| `goaktpb.ActorReinstated` | `actor.ActorReinstated` |
| `goaktpb.NodeJoined` | `actor.NodeJoined` |
| `goaktpb.NodeLeft` | `actor.NodeLeft` |

Messages with data keep it in unexported fields. Build them with their constructors, such as `actor.NewDeadletter(sender, receiver, message, sendTime, reason)` or `actor.NewTerminated(actorPath)`, and read them through accessor methods, such as `Terminated.ActorPath()` or `Deadletter.Reason()`. Actors in these messages are identified by `actor.Path` values and nodes by address strings. Timestamps are `time.Time` instead of `*timestamppb.Timestamp`. Messages without data, such as `PostStart` and `PoisonPill`, are empty structs: send `new(actor.PoisonPill)`.

```go
package watch

import (
	"github.com/tochemey/goakt/v4/actor"
)

type Watcher struct{}

func (w *Watcher) PreStart(*actor.Context) error { return nil }

func (w *Watcher) Receive(ctx *actor.ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *actor.Terminated:
		ctx.Logger().Infof("%s stopped at %s", msg.ActorPath().String(), msg.TerminatedAt())
	default:
		ctx.Unhandled()
	}
}

func (w *Watcher) PostStop(*actor.Context) error { return nil }
```

### 4. `*PID` is the only actor reference

`ActorRef` is gone. `*PID` is the reference for local and remote actors alike. Methods that returned `ActorRef` values now return `*PID` values. A remote PID is a lightweight handle: `Tell`, `Ask` and the scheduler route it through remoting. When location matters, ask the PID with `pid.IsLocal()` or `pid.IsRemote()`. Most methods work on a remote PID by asking its node: `Stop`, `Shutdown`, `Restart`, `Reinstate`, `SpawnChild`, `Child`, `Children` and `Parent` all do. A few make sense only locally and return `errors.ErrNotLocal` for a remote PID: `ReinstateNamed`, `SendAsync`, `SendSync`, `PipeTo`, `PipeToName` and `DiscoverActor`. `Watch` and `UnWatch` called on a remote PID do nothing; to watch a remote actor, call them on a local PID with the remote PID as argument.

**What to do:** replace `ActorRef` with `*PID`.

### 5. One lookup: `ActorOf`

`LocalActor` and `RemoteActor` are gone, and `ActorOf` has a new signature:

```go
ActorOf(ctx context.Context, actorName string) (*PID, error)
```

| Case | v3 result | v4 result |
|---|---|---|
| Actor found on this node | `(addr, pid, nil)` | `(pid, nil)` with a local PID |
| Actor found on another node of the cluster | `(addr, nil, nil)` | `(pid, nil)` with a remote PID |
| Actor not found | `(nil, nil, err)` | `(nil, err)`, where `err` wraps `errors.ErrActorNotFound`; with remoting enabled and no cluster, `err` is `errors.ErrMethodCallNotAllowed` |

To reach an actor by name on a given node without a cluster, use `PID.RemoteLookup(ctx, host, port, name)` from a running actor, or `ReceiveContext.RemoteLookup(host, port, name)` inside `Receive`.

```go
package orders

import (
	"context"
	"errors"
	"fmt"

	"github.com/tochemey/goakt/v4/actor"
	gerrors "github.com/tochemey/goakt/v4/errors"
)

func placeOrder(ctx context.Context, system actor.ActorSystem) error {
	pid, err := system.ActorOf(ctx, "orders")
	if err != nil {
		if errors.Is(err, gerrors.ErrActorNotFound) {
			return fmt.Errorf("orders actor is not running: %w", err)
		}
		return err
	}

	if pid.IsRemote() {
		fmt.Printf("orders runs on %s\n", pid.Path().HostPort())
	}

	return actor.Tell(ctx, pid, &PlaceOrder{ID: "42"})
}
```

### 6. `Actors` replaces `ActorRefs`

`Actors` and `ActorRefs` are merged into one method:

```go
// v3
Actors() []*PID
ActorRefs(ctx, timeout) ([]ActorRef, error)

// v4
Actors(ctx context.Context, timeout time.Duration) ([]*PID, error)
```

Local actors come back as local PIDs. In cluster mode, actors on other nodes come back as remote PIDs. `timeout` bounds the cluster scan and is ignored outside cluster mode. The cluster scan has a cost, so do not call `Actors` on a hot path.

### 7. Remote scheduling uses the ordinary scheduler

`RemoteScheduleOnce`, `RemoteSchedule` and `RemoteScheduleWithCron` are gone. `ScheduleOnce`, `Schedule` and `ScheduleWithCron` accept a remote PID and deliver through remoting. They return `errors.ErrRemotingDisabled` when the PID is remote and remoting is not enabled.

In cluster mode, `ScheduleWithCron` requires `actor.WithReference`: without it the call fails with `errors.ErrScheduleReferenceRequired`. The reference is the key that makes each tick deliver once across the cluster.

```go
package reports

import (
	"context"
	"time"

	"github.com/tochemey/goakt/v4/actor"
)

type Tick struct{}

func scheduleTick(ctx context.Context, system actor.ActorSystem) error {
	pid, err := system.ActorOf(ctx, "reporter")
	if err != nil {
		return err
	}

	return system.Schedule(ctx, &Tick{}, pid, time.Minute, actor.WithReference("reporter-tick"))
}
```

### 8. `GetPartition` is now `Partition`

Replace `system.GetPartition(name)` with `system.Partition(name)`. It returns the partition as a `uint64`.

### 9. Addresses became paths

`pid.Address()` is replaced by `pid.Path()`, which returns the `actor.Path` interface. A path gives the actor's `Host`, `Port`, `HostPort`, `Name`, `QualifiedName`, `System`, `Parent` and `String`, and compares with `Equals`. You cannot implement `Path` yourself. The `address` package is internal and has no public replacement.

`Path()` returns `nil` on a nil PID.

The address accessors of `ReceiveContext` and of the test probe are gone too:

- `ctx.SenderAddress()` becomes `ctx.Sender().Path()`.
- `ctx.ReceiverAddress()` becomes `ctx.Self().Path()`.
- `testkit.Probe.SenderAddress()` becomes `Probe.Sender()`, then `Path()` or `IsRemote()` on the PID it returns. `Probe.Sender()` is `nil` until an `Expect` method of the probe has taken a message.

When a message has no sender, `ctx.Sender()` returns the actor system's no-sender PID, not `nil`: compare it with `ctx.ActorSystem().NoSender()`. A message from another node has a remote PID as its sender.

```go
package audit

import (
	"github.com/tochemey/goakt/v4/actor"
)

type Auditor struct{}

func (a *Auditor) PreStart(*actor.Context) error { return nil }

func (a *Auditor) Receive(ctx *actor.ReceiveContext) {
	sender := ctx.Sender()
	if sender.Equals(ctx.ActorSystem().NoSender()) {
		ctx.Logger().Info("message without a sender")
		return
	}

	ctx.Logger().Infof("%s (remote: %t) sent a message to %s",
		sender.Path().String(), sender.IsRemote(), ctx.Self().Path().String())
}

func (a *Auditor) PostStop(*actor.Context) error { return nil }
```

### 10. Remoting is configured, not constructed

`remote.Remoting`, `remote.Client`, `remote.NewRemoting()` and `remote.NewClient()` are gone. The client the actor system uses to reach other nodes is internal. The `remote` package now holds configuration and protocol types: `Config`, its options, `Serializer`, `Compression` and `ContextPropagator`.

**What to do:**

- Replace `WithRemoting` with `actor.WithRemote(remote.NewConfig(bindAddr, bindPort, opts...))`.
- Message remote actors through PIDs: get one with `ActorOf`, `PID.RemoteLookup` or `ReceiveContext.RemoteLookup`, then use `Tell` and `Ask` as for a local actor. `PID.RemoteStop` and `PID.RemoteReSpawn` stop and restart an actor on a given node.
- To spawn an actor on a given node, pass `actor.WithHostAndPort(host, port)` to `Spawn`. The target node must have registered the actor type with `system.Register(ctx, actor)`. `Spawn` forwards these options to that node: `WithRelocationDisabled`, `WithDependencies`, `WithStashing`, `WithPassivationStrategy`, `WithReentrancy`, `WithSupervisor`, `WithRole` and `WithInitTimeout`, and, in cluster mode, the reliable-delivery options such as `AsReliableProducer` and `AsReliableConsumer`.
- From a program that does not run an actor system, use the `client` package: `client.New(ctx, nodes)` with nodes from `client.NewNode(address, client.WithRemoteConfig(config))`.
- Configure TLS with `remote.WithTLS` on the remote config. `actor.WithTLS` is deprecated, and when both are set the remote config wins.

The main options of `remote.NewConfig`:

| Option | Purpose | Default |
|---|---|---|
| `WithTLS` | Secures remoting and, in cluster mode, the cluster transports. | none |
| `WithCompression` | `NoCompression`, `GzipCompression`, `ZstdCompression` or `BrotliCompression`. Configure every node with the same algorithm. | `NoCompression` |
| `WithSerializables`, `WithJSONSerializables`, `WithSerializers` | Serializers for message types that are not Protocol Buffers. | `ProtoSerializer` for every `proto.Message` |
| `WithContextPropagator` | Carries request metadata, such as trace IDs or auth tokens, across nodes. | none |
| `WithMaxFrameSize` | Largest single wire frame, from 16 KiB to 16 MiB. | 16 MiB |
| `WithMaxMessageSize` | Largest whole message; may exceed the frame size. | 16 MiB |
| `WithWriteTimeout` | Closes a connection that cannot write for this long. | 10 seconds |
| `WithReadIdleTimeout` | Sends a liveness ping when nothing has been received for this long. | 10 seconds |
| `WithDialTimeout` | How long a connection attempt to a peer may take. | 5 seconds |

```go
package main

import (
	"context"
	"os"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
)

type PlaceOrder struct{ ID string }
type OrderPlaced struct{ ID string }

func main() {
	ctx := context.Background()

	config := remote.NewConfig("127.0.0.1", 3321,
		remote.WithCompression(remote.ZstdCompression),
		remote.WithSerializables(new(PlaceOrder), new(OrderPlaced)),
	)

	system, err := actor.NewActorSystem("orders",
		actor.WithLogger(log.NewSlog(log.InfoLevel, os.Stdout)),
		actor.WithRemote(config),
	)
	if err != nil {
		panic(err)
	}

	if err := system.Start(ctx); err != nil {
		panic(err)
	}

	_ = system.Stop(ctx)
}
```

### 11. The `Logger` interface

The `log.Logger` interface has new methods. A custom implementation must add:

- `InfoContext`, `InfofContext`, `WarnContext`, `WarnfContext`, `ErrorContext`, `ErrorfContext`, `DebugContext` and `DebugfContext`, which take a `context.Context` first so trace data can reach the log.
- `LogLevel() Level`: the minimum level the logger writes.
- `Enabled(level Level) bool`: whether a level is written; check it before expensive work.
- `With(keyValues ...any) Logger`: a logger that adds the given fields to every entry. Keys and values alternate, and keys are strings.
- `Flush() error`: writes buffered entries.
- `StdLogger() *log.Logger`: a standard library logger for dependencies that need one.

The interface has no `Fatal`, `Panic` or `LogOutput` methods: a library must not stop the process. `log.Zap` and `log.Slog` still have them, so call them on the concrete type if you need them.

**What to do:** add the missing methods and check with `var _ log.Logger = (*MyLogger)(nil)`. Use `log.DiscardLogger` where you need a logger that writes nothing, such as in tests.

## New in v4

### Serializers for any Go type

A message that crosses the network is encoded by the serializer registered for its type on the remote config:

- `ProtoSerializer` is registered for every `proto.Message` by default. Protocol Buffers users change nothing.
- `CBORSerializer` and `JSONSerializer` encode plain Go types. Register types with `remote.WithSerializables(new(MyMessage))` for CBOR or `remote.WithJSONSerializables(new(MyMessage))` for JSON. A typed nil interface pointer, such as `(*MyInterface)(nil)`, registers every type that implements the interface.
- A custom serializer implements `remote.Serializer` and is registered with `remote.WithSerializers(new(MyMessage), mySerializer)`.

Register the same types on every node that sends or receives them: a node cannot decode a type it has not registered.

### New error sentinels

- `errors.ErrRemotingDisabled`: a remote operation was attempted but remoting is not enabled.
- `errors.ErrNotLocal`: the operation needs a local PID but got a remote one.

### `PID.Kind`

`pid.Kind()` returns the type name of the actor behind the PID. For a remote PID it asks the actor's node, and returns an empty string when that fails.

### Logger implementations

- `log.NewSlog(level, writers...)`: a logger built on the standard library `slog` that writes JSON. `log.NewSlogFrom(logger, level)` wraps a `*slog.Logger` you already have.
- `log.NewZap(level, writers...)`: a logger built on Zap. Debug, info and warning entries written to files are buffered, so call `Flush` at shutdown.
- `log.DiscardLogger`: a logger that writes nothing.

Pass a logger to the actor system with `actor.WithLogger`.

## Migration checklist

- [ ] Change imports from `github.com/tochemey/goakt/v3` to `github.com/tochemey/goakt/v4`.
- [ ] Replace `proto.Message` with `any` in handlers and call sites.
- [ ] Replace `goaktpb.*` with `actor.*`; build messages with their constructors and read them with their accessors.
- [ ] Replace `ActorRef` with `*PID`.
- [ ] Replace `LocalActor` and `RemoteActor` with `ActorOf(ctx, name)`, and update callers of `ActorOf` to its two return values.
- [ ] Replace `ActorRefs` with `Actors(ctx, timeout)`.
- [ ] Replace `RemoteSchedule*` with `Schedule*` on a PID from `ActorOf`; give `ScheduleWithCron` a `WithReference` in cluster mode.
- [ ] Replace `GetPartition` with `Partition`.
- [ ] Replace `pid.Address()` with `pid.Path()`, and remove imports of the `address` package.
- [ ] Replace `ctx.SenderAddress()` and `ctx.ReceiverAddress()` with `ctx.Sender().Path()` and `ctx.Self().Path()`.
- [ ] Replace `Probe.SenderAddress()` with `Probe.Sender()` and `Path()`.
- [ ] Replace `WithRemoting` and any use of `remote.Remoting` or `remote.Client` with `WithRemote(config)`, PID methods, or the `client` package.
- [ ] Register serializers for every message type that crosses the network and is not Protocol Buffers.
- [ ] Add the new methods to custom `Logger` implementations.
