# TestKit

The `testkit` package provides helpers for actor-level testing. It creates a throwaway actor system, probes for message assertions, and supports grain testing.

## TestKit

```go
    kit := testkit.New(ctx, t, testkit.WithLogging(log.DebugLevel), testkit.WithExtensions(ext))
    defer kit.Shutdown(ctx)
```

| Method                                                   | Purpose                                                               |
|----------------------------------------------------------|-----------------------------------------------------------------------|
| `ActorSystem()`                                          | Access the underlying actor system.                                   |
| `Spawn(ctx, name, actor, opts...)`                       | Spawn an actor. Fails the test on error.                              |
| `SpawnChild(ctx, childName, parentName, actor, opts...)` | Spawn a child of an existing actor.                                   |
| `Subscribe()`                                            | Create an event-stream subscriber. Auto-unsubscribes on test cleanup. |
| `Kill(ctx, name)`                                        | Stop an actor by name.                                                |
| `NewProbe(ctx)`                                          | Create a test probe.                                                  |
| `NewGrainProbe(ctx)`                                     | Create a grain test probe.                                            |
| `GrainIdentity(ctx, name, factory, opts...)`             | Create a grain identity.                                              |
| `Shutdown(ctx)`                                          | Stop the actor system.                                                |

### Options

| Option                   | Purpose                                   |
|--------------------------|-------------------------------------------|
| `WithLogging(level)`     | Enable logging at the given level.        |
| `WithExtensions(ext...)` | Register extensions with the test system. |

## Probe

A probe is a test actor that records messages. Use it to assert message delivery instead of sleeps or polling.

```go
    probe := kit.NewProbe(ctx)
    defer probe.Stop()
    kit.Spawn(ctx, "worker", NewWorkerActor())
    probe.Send("worker", msg)
    probe.ExpectMessage(expected)
```

### Probe interface

| Method                              | Purpose                                                                      |
|-------------------------------------|------------------------------------------------------------------------------|
| `ExpectMessage(msg)`                | Assert the next message exactly matches `msg`. Fails on timeout or mismatch. |
| `ExpectMessageWithin(d, msg)`       | Same with custom timeout.                                                    |
| `ExpectMessageOfType(msg)`          | Assert the next message has the same type as `msg`.                          |
| `ExpectMessageOfTypeWithin(d, msg)` | Same with custom timeout.                                                    |
| `ExpectNoMessage()`                 | Assert no message arrives within the default timeout.                        |
| `ExpectAnyMessage()`                | Wait for and return the next message.                                        |
| `ExpectAnyMessageWithin(d)`         | Same with custom timeout.                                                    |
| `ExpectTerminated(actorName)`       | Assert a `Terminated` message for the given actor.                           |
| `Send(actorName, msg)`              | Tell the named actor (fire-and-forget).                                      |
| `SendSync(actorName, msg, timeout)` | Ask the named actor; record the response in the probe.                       |
| `Sender()`                          | PID of the sender of the last received message.                              |
| `PID()`                             | PID of the probe itself (use as sender in tests).                            |
| `Watch(pid)`                        | Subscribe to termination of `pid`.                                           |
| `WatchNamed(actorName)`             | Subscribe to termination of the named actor.                                 |
| `Stop()`                            | Shutdown the probe. Call in cleanup.                                         |

Default timeout is 3 seconds. Probes work in both standalone and cluster mode.

## GrainProbe

Similar to Probe but for grains. Use `GrainIdentity` to create identities, then send and assert responses.

| Method                             | Purpose                                |
|------------------------------------|----------------------------------------|
| `ExpectResponse(msg)`              | Assert the next response matches.      |
| `ExpectResponseWithin(d, msg)`     | Same with custom timeout.              |
| `ExpectResponseOfType(msg)`        | Assert type match.                     |
| `ExpectNoResponse()`               | Assert no response within timeout.     |
| `ExpectAnyResponse()`              | Wait for and return the next response. |
| `ExpectTerminated(identity, d)`    | Assert grain termination.              |
| `Send(identity, msg)`              | Tell the grain.                        |
| `SendSync(identity, msg, timeout)` | Ask the grain; record response.        |
| `Stop()`                           | Shutdown the probe.                    |

## MultiNodes

For integration tests, `MultiNodes` creates an in-process multi-node cluster. See `testkit/multi_nodes.go` for usage. Requires cluster and remoting configuration.

## Usage pattern

```go
func TestWorker(t *testing.T) {
    ctx := context.Background()
    kit := testkit.New(ctx, t)
    defer kit.Shutdown(ctx)

    probe := kit.NewProbe(ctx)
    defer probe.Stop()

    kit.Spawn(ctx, "worker", NewWorkerActor())
    probe.Send("worker", &DoWork{ID: "1"})
    probe.ExpectMessage(&WorkDone{ID: "1"})
}
```
