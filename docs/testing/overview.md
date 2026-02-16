# Testing Actors

GoAkt ships with a dedicated testing package (`github.com/tochemey/goakt/v3/testkit`) that provides a structured way to test actors and grains. The testkit creates a lightweight actor system behind the scenes and offers **Probes** — special test actors that can send messages, receive responses, and make assertions.

## Table of Contents

- ⚡ [Quick reference](#quick-reference)
- 🧪 [TestKit](#testkit)
- 🔍 [Probe](#probe)
- 🌾 [GrainProbe](#grainprobe)
- 🌐 [Multi-Node Testing](#multi-node-testing)
- 💡 [Complete Examples](#complete-examples)
- ➡️ [Next Steps](#next-steps)

---

## Quick reference

| Goal            | API                                                                                                                                                                                                 |
|-----------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Create test env | `kit := testkit.New(ctx, t)` then `t.Cleanup(func() { kit.Shutdown(ctx) })`                                                                                                                         |
| Spawn actor     | `kit.Spawn(ctx, "name", &MyActor{})` (no return; use `kit.ActorSystem().LocalActor("name")` if you need the PID)                                                                                    |
| Probe (actors)  | `probe := kit.NewProbe(ctx)` then `defer probe.Stop()`; `probe.Send("name", msg)` / `probe.SendSync("name", msg, timeout)`; `probe.ExpectMessage(msg)` / `probe.ExpectMessageWithin(duration, msg)` |
| Grain identity  | `identity := kit.GrainIdentity(ctx, "id", func(ctx context.Context) (actor.Grain, error) { return &MyGrain{}, nil })`                                                                               |
| Grain probe     | `probe := kit.NewGrainProbe(ctx)`; `probe.Send(identity, msg)` / `probe.SendSync(identity, msg, timeout)`; `probe.ExpectResponse(msg)` / `probe.ExpectTerminated(identity, duration)`               |
| Event stream    | `sub := kit.Subscribe()` (auto-unsubscribed on test end)                                                                                                                                            |

## TestKit

The `TestKit` is the entry point for all actor testing. It manages an internal actor system and provides helpers for spawning actors, creating probes, and shutting down cleanly.

### Creating a TestKit

```go
import (
    "context"
    "testing"

    "github.com/tochemey/goakt/v3/testkit"
)

func TestMyActor(t *testing.T) {
    ctx := context.TODO()

    // Create the testkit (starts an internal actor system)
    kit := testkit.New(ctx, t)

    // Always shut down when done
    t.Cleanup(func() {
        kit.Shutdown(ctx)
    })
}
```

### TestKit Options

Configure the testkit with options:

```go
import "github.com/tochemey/goakt/v3/log"

// Enable logging at a specific level
kit := testkit.New(ctx, t, testkit.WithLogging(log.ErrorLevel))

// Add extensions
kit := testkit.New(ctx, t, testkit.WithExtensions(myExtension))
```

| Option                | Description                                         |
|-----------------------|-----------------------------------------------------|
| `WithLogging(level)`  | Sets the logger level for the internal actor system |
| `WithExtensions(...)` | Registers extensions in the actor system            |

### TestKit API

| Method                                                   | Description                                   |
|----------------------------------------------------------|-----------------------------------------------|
| `New(ctx, t, opts...)`                                   | Creates and starts a new TestKit              |
| `ActorSystem()`                                          | Returns the underlying actor system           |
| `Spawn(ctx, name, actor, opts...)`                       | Spawns an actor in the test system            |
| `SpawnChild(ctx, childName, parentName, actor, opts...)` | Spawns a child actor under an existing parent |
| `NewProbe(ctx)`                                          | Creates a Probe for testing actors            |
| `NewGrainProbe(ctx)`                                     | Creates a GrainProbe for testing grains       |
| `GrainIdentity(ctx, name, factory, opts...)`             | Creates a grain identity for testing          |
| `Subscribe()`                                            | Subscribes to the actor system event stream   |
| `Kill(ctx, name)`                                        | Stops an actor by name                        |
| `Shutdown(ctx)`                                          | Stops the entire test actor system            |

## Probe

A `Probe` is a special test actor that intercepts messages. It can send messages to actors under test, then assert what messages come back. This is the primary tool for verifying actor behavior.

### Creating a Probe

```go
func TestWithProbe(t *testing.T) {
    ctx := context.TODO()
    kit := testkit.New(ctx, t)
    t.Cleanup(func() { kit.Shutdown(ctx) })

    // Spawn the actor under test
    kit.Spawn(ctx, "my-actor", &MyActor{})

    // Create a probe
    probe := kit.NewProbe(ctx)
    defer probe.Stop()

    // Use the probe to test the actor...
}
```

### Sending Messages

#### Fire-and-Forget (Tell)

Use `Send` to deliver a message to an actor without expecting a direct reply. The actor under test should reply to its sender (the probe) via `ctx.Tell(ctx.Sender(), response)`.

```go
// Send a fire-and-forget message
probe.Send("my-actor", new(testpb.TestPing))

// The actor receives TestPing, and replies with TestPong to the probe
probe.ExpectMessage(new(testpb.TestPong))
```

#### Request-Response (Ask)

Use `SendSync` to send a message and wait for a synchronous response via `ctx.Response(...)` inside the actor.

```go
// Send and wait for response
probe.SendSync("my-actor", new(testpb.TestReply), time.Second)

// Assert the response
probe.ExpectMessage(new(testpb.Reply))
```

### Asserting Messages

#### ExpectMessage

Asserts the next received message exactly matches a given protobuf message:

```go
probe.Send("pinger", new(testpb.TestPing))
probe.ExpectMessage(new(testpb.TestPong))
```

#### ExpectMessageWithin

Same as `ExpectMessage` but with an explicit timeout:

```go
probe.Send("slow-actor", &testpb.TestWait{Duration: uint64(time.Second)})
probe.ExpectMessageWithin(2*time.Second, new(testpb.TestPong))
```

#### ExpectAnyMessage

Asserts that any message is received (returns it for further inspection):

```go
probe.Send("pinger", new(testpb.TestPing))
actual := probe.ExpectAnyMessage()
// Inspect 'actual' further if needed
```

#### ExpectAnyMessageWithin

Same as `ExpectAnyMessage` but with an explicit timeout:

```go
actual := probe.ExpectAnyMessageWithin(5 * time.Second)
```

#### ExpectMessageOfType

Asserts the next message is of a specific protobuf type (ignores field values):

```go
probe.Send("pinger", new(testpb.TestPing))
probe.ExpectMessageOfType(new(testpb.TestPong))
```

#### ExpectMessageOfTypeWithin

Same as `ExpectMessageOfType` but with an explicit timeout:

```go
probe.ExpectMessageOfTypeWithin(2*time.Second, new(testpb.TestPong))
```

#### ExpectNoMessage

Asserts that no message is received within the default timeout window. Use this to confirm an actor is idle:

```go
probe.Send("pinger", new(testpb.TestPing))
probe.ExpectMessage(new(testpb.TestPong))

// No further messages expected
probe.ExpectNoMessage()
```

### Watching Actors

A probe can watch an actor and receive a `Terminated` message when the actor stops.

#### Watch by Name

```go
kit.Spawn(ctx, "worker", &WorkerActor{})

probe := kit.NewProbe(ctx)
defer probe.Stop()

// Watch the actor
probe.WatchNamed("worker")

// Kill the actor
probe.Send("worker", new(goaktpb.PoisonPill))

// Assert it terminated
probe.ExpectTerminated("worker")
```

#### Watch by PID

```go
pid, _ := kit.ActorSystem().Spawn(ctx, "worker", &WorkerActor{})

probe := kit.NewProbe(ctx)
defer probe.Stop()

// Watch the actor by PID
probe.Watch(pid)

// Shutdown the actor
pid.Shutdown(ctx)

// Assert it terminated
probe.ExpectTerminated(pid.Name())
```

### Inspecting the Sender

After receiving a message, retrieve the sender:

```go
probe.Send("pinger", new(testpb.TestPing))
probe.ExpectMessage(new(testpb.TestPong))

// Get the sender of the last received message
sender := probe.Sender()
require.Equal(t, "pinger", sender.Name())
```

For multi-node scenarios, use `SenderAddress()`:

```go
addr := probe.SenderAddress()
```

### Probe API Reference

| Method                                         | Description                            |
|------------------------------------------------|----------------------------------------|
| `Send(actorName, message)`                     | Sends a fire-and-forget message (Tell) |
| `SendSync(actorName, message, timeout)`        | Sends a request-response message (Ask) |
| `ExpectMessage(message)`                       | Asserts exact message match            |
| `ExpectMessageWithin(duration, message)`       | Asserts exact match within timeout     |
| `ExpectAnyMessage()`                           | Waits for and returns any message      |
| `ExpectAnyMessageWithin(duration)`             | Waits for any message within timeout   |
| `ExpectMessageOfType(message)`                 | Asserts message type match             |
| `ExpectMessageOfTypeWithin(duration, message)` | Asserts type match within timeout      |
| `ExpectNoMessage()`                            | Asserts no message received            |
| `ExpectTerminated(actorName)`                  | Asserts actor terminated               |
| `WatchNamed(actorName)`                        | Watches actor by name                  |
| `Watch(pid)`                                   | Watches actor by PID                   |
| `Sender()`                                     | Returns sender PID of last message     |
| `SenderAddress()`                              | Returns sender address of last message |
| `PID()`                                        | Returns the probe's own PID            |
| `Stop()`                                       | Stops the probe                        |

## GrainProbe

The `GrainProbe` is purpose-built for testing grains (virtual actors). It interacts with grains through their identity rather than by actor name.

### Creating a GrainProbe

```go
func TestMyGrain(t *testing.T) {
    ctx := context.TODO()
    kit := testkit.New(ctx, t)
    t.Cleanup(func() { kit.Shutdown(ctx) })

    // Create a grain identity
    identity := kit.GrainIdentity(ctx, "my-grain", func(ctx context.Context) (actor.Grain, error) {
        return &MyGrain{}, nil
    })

    // Create a grain probe
    probe := kit.NewGrainProbe(ctx)

    // Use the probe to test the grain...
}
```

### Sending Messages to Grains

#### Fire-and-Forget (Tell)

```go
// Send a fire-and-forget message to the grain
probe.Send(identity, new(testpb.TestPing))

// No response expected for Tell
probe.ExpectNoResponse()
```

#### Request-Response (Ask)

```go
// Send a request and wait for response
probe.SendSync(identity, new(testpb.TestReply), time.Second)

// Assert the response
probe.ExpectResponse(new(testpb.Reply))
```

### Asserting Grain Responses

#### ExpectResponse

Asserts the response exactly matches:

```go
probe.SendSync(identity, new(testpb.TestReply), time.Second)
probe.ExpectResponse(new(testpb.Reply))
```

#### ExpectResponseWithin

Same with an explicit timeout:

```go
probe.SendSync(identity, &testpb.TestWait{Duration: uint64(time.Second)}, 2*time.Second)
probe.ExpectResponseWithin(3*time.Second, new(testpb.Reply))
```

#### ExpectAnyResponse / ExpectAnyResponseWithin

Asserts any response is received:

```go
probe.SendSync(identity, new(testpb.TestReply), time.Second)
response := probe.ExpectAnyResponse()
```

#### ExpectResponseOfType / ExpectResponseOfTypeWithin

Asserts the response type matches:

```go
probe.SendSync(identity, new(testpb.TestReply), time.Second)
probe.ExpectResponseOfType(new(testpb.Reply))
```

#### ExpectNoResponse

Asserts no response is received:

```go
probe.Send(identity, new(testpb.TestPing))
probe.ExpectNoResponse()
```

#### ExpectTerminated

Asserts that a grain has been deactivated:

```go
// Create grain with short passivation timeout
identity := kit.GrainIdentity(ctx, "short-lived-grain",
    func(ctx context.Context) (actor.Grain, error) {
        return &MyGrain{}, nil
    },
    actor.WithGrainDeactivateAfter(200*time.Millisecond),
)

// Wait and assert deactivation
probe.ExpectTerminated(identity, time.Second)
```

### GrainProbe API Reference

| Method                                          | Description                              |
|-------------------------------------------------|------------------------------------------|
| `Send(identity, message)`                       | Sends a fire-and-forget message (Tell)   |
| `SendSync(identity, message, timeout)`          | Sends a request-response message (Ask)   |
| `ExpectResponse(message)`                       | Asserts exact response match             |
| `ExpectResponseWithin(duration, message)`       | Asserts exact match within timeout       |
| `ExpectAnyResponse()`                           | Waits for and returns any response       |
| `ExpectAnyResponseWithin(duration)`             | Waits for any response within timeout    |
| `ExpectResponseOfType(message)`                 | Asserts response type match              |
| `ExpectResponseOfTypeWithin(duration, message)` | Asserts type match within timeout        |
| `ExpectNoResponse()`                            | Asserts no response received             |
| `ExpectTerminated(identity, duration)`          | Asserts grain deactivated within timeout |

## Multi-Node Testing

For testing cluster scenarios, the testkit provides `MultiNodes` and `TestNode`. These spin up multiple clustered actor systems using an embedded NATS server for discovery.

### Setting Up Multi-Node Tests

```go
import (
    "context"
    "testing"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/log"
    "github.com/tochemey/goakt/v3/testkit"
)

func TestClusterScenario(t *testing.T) {
    ctx := context.Background()

    // Define grain kinds to register across the cluster
    kinds := []actor.Actor{&MyGrainKind{}}

    // Create multi-node environment
    multi := testkit.NewMultiNodes(t, log.DiscardLogger, kinds, nil)
    multi.Start()
    t.Cleanup(multi.Stop)

    // Start cluster nodes
    node1 := multi.StartNode(ctx, "node-1")
    node2 := multi.StartNode(ctx, "node-2")

    // Spawn actors on different nodes
    node1.Spawn(ctx, "actor-on-node1", &MyActor{})
    node2.Spawn(ctx, "actor-on-node2", &MyActor{})

    // Use probes to test cross-node communication
    probe := node1.SpawnProbe(ctx)
    defer probe.Stop()

    probe.Send("actor-on-node2", new(testpb.TestPing))
    probe.ExpectMessage(new(testpb.TestPong))
}
```

### TestNode API

Each `TestNode` provides the same convenience methods as the `TestKit`:

| Method                                       | Description                       |
|----------------------------------------------|-----------------------------------|
| `NodeName()`                                 | Returns the node's name           |
| `Spawn(ctx, name, actor, opts...)`           | Spawns an actor on this node      |
| `SpawnSingleton(ctx, name, actor)`           | Spawns a cluster singleton        |
| `SpawnProbe(ctx)`                            | Creates a Probe on this node      |
| `SpawnGrainProbe(ctx)`                       | Creates a GrainProbe on this node |
| `GrainIdentity(ctx, name, factory, opts...)` | Creates a grain identity          |
| `Subscribe()`                                | Subscribes to the event stream    |
| `Kill(ctx, name)`                            | Stops an actor by name            |

### MultiNodes API

| Method                                        | Description                                        |
|-----------------------------------------------|----------------------------------------------------|
| `NewMultiNodes(t, logger, kinds, extensions)` | Creates the multi-node environment                 |
| `Start()`                                     | Starts the embedded NATS server and infrastructure |
| `StartNode(ctx, name)`                        | Starts a new cluster node                          |
| `Stop()`                                      | Shuts down all nodes, discovery, and NATS server   |

### Testing Singletons

```go
func TestClusterSingleton(t *testing.T) {
    ctx := context.Background()

    multi := testkit.NewMultiNodes(t, log.DiscardLogger, nil, nil)
    multi.Start()
    t.Cleanup(multi.Stop)

    node1 := multi.StartNode(ctx, "node-1")
    node2 := multi.StartNode(ctx, "node-2")

    // Spawn singleton on node1 (will be placed on the oldest node)
    node1.SpawnSingleton(ctx, "coordinator", &CoordinatorActor{})

    // Test from node2 via probe
    probe := node2.SpawnProbe(ctx)
    defer probe.Stop()

    probe.Send("coordinator", new(testpb.TestPing))
    probe.ExpectMessage(new(testpb.TestPong))
}
```

### Testing Grains Across Nodes

```go
func TestGrainsAcrossNodes(t *testing.T) {
    ctx := context.Background()

    kinds := []actor.Actor{&MyGrain{}}
    multi := testkit.NewMultiNodes(t, log.DiscardLogger, kinds, nil)
    multi.Start()
    t.Cleanup(multi.Stop)

    node1 := multi.StartNode(ctx, "node-1")
    node2 := multi.StartNode(ctx, "node-2")

    // Create grain identity on node1
    identity := node1.GrainIdentity(ctx, "test-grain",
        func(ctx context.Context) (actor.Grain, error) {
            return &MyGrain{}, nil
        },
    )

    // Test grain from node2
    probe := node2.SpawnGrainProbe(ctx)
    probe.SendSync(identity, new(testpb.TestReply), time.Second)
    probe.ExpectResponse(new(testpb.Reply))
}
```

## Complete Examples

### Testing a Ping-Pong Actor

```go
// Define the actor
type PingActor struct{}

func (a *PingActor) PreStart(_ *actor.Context) error  { return nil }
func (a *PingActor) PostStop(_ *actor.Context) error   { return nil }

func (a *PingActor) Receive(ctx *actor.ReceiveContext) {
    switch ctx.Message().(type) {
    case *testpb.TestPing:
        ctx.Tell(ctx.Sender(), new(testpb.TestPong))
    case *testpb.TestReply:
        ctx.Response(new(testpb.Reply))
    }
}

// Test with probe
func TestPingActor(t *testing.T) {
    ctx := context.TODO()
    kit := testkit.New(ctx, t)
    t.Cleanup(func() { kit.Shutdown(ctx) })

    kit.Spawn(ctx, "pinger", &PingActor{})

    probe := kit.NewProbe(ctx)
    defer probe.Stop()

    // Test Tell pattern
    probe.Send("pinger", new(testpb.TestPing))
    probe.ExpectMessage(new(testpb.TestPong))
    probe.ExpectNoMessage()

    // Test Ask pattern
    probe.SendSync("pinger", new(testpb.TestReply), time.Second)
    probe.ExpectMessage(new(testpb.Reply))
    probe.ExpectNoMessage()

    // Verify sender
    probe.Send("pinger", new(testpb.TestPing))
    probe.ExpectMessage(new(testpb.TestPong))
    require.Equal(t, "pinger", probe.Sender().Name())
}
```

### Testing Actor Termination

```go
func TestActorTermination(t *testing.T) {
    ctx := context.TODO()
    kit := testkit.New(ctx, t)
    t.Cleanup(func() { kit.Shutdown(ctx) })

    kit.Spawn(ctx, "worker", &WorkerActor{})

    probe := kit.NewProbe(ctx)
    defer probe.Stop()

    // Watch the actor
    probe.WatchNamed("worker")

    // Send PoisonPill to terminate
    probe.Send("worker", new(goaktpb.PoisonPill))

    // Assert termination
    probe.ExpectTerminated("worker")
    probe.ExpectNoMessage()
}
```

### Testing Parent-Child Actors

```go
func TestParentChildActors(t *testing.T) {
    ctx := context.TODO()
    kit := testkit.New(ctx, t)
    t.Cleanup(func() { kit.Shutdown(ctx) })

    // Spawn parent
    kit.Spawn(ctx, "parent", &ParentActor{})

    // Spawn child under parent
    kit.SpawnChild(ctx, "child", "parent", &ChildActor{})

    probe := kit.NewProbe(ctx)
    defer probe.Stop()

    // Test child actor
    probe.SendSync("child", new(testpb.TestReply), time.Second)
    probe.ExpectMessage(new(testpb.Reply))
    probe.ExpectNoMessage()
}
```

### Testing a Grain

```go
func TestCounterGrain(t *testing.T) {
    ctx := context.TODO()
    kit := testkit.New(ctx, t)
    t.Cleanup(func() { kit.Shutdown(ctx) })

    // Create grain identity with factory
    identity := kit.GrainIdentity(ctx, "counter",
        func(ctx context.Context) (actor.Grain, error) {
            return &CounterGrain{}, nil
        },
    )

    // Create grain probe
    probe := kit.NewGrainProbe(ctx)

    // Test request-response
    probe.SendSync(identity, new(testpb.TestReply), time.Second)
    probe.ExpectResponse(new(testpb.Reply))
    probe.ExpectNoResponse()
}
```

### Testing Grain Deactivation

```go
func TestGrainDeactivation(t *testing.T) {
    ctx := context.TODO()
    kit := testkit.New(ctx, t)
    t.Cleanup(func() { kit.Shutdown(ctx) })

    probe := kit.NewGrainProbe(ctx)

    // Create grain with short passivation timeout
    identity := kit.GrainIdentity(ctx, "ephemeral",
        func(ctx context.Context) (actor.Grain, error) {
            return &MyGrain{}, nil
        },
        actor.WithGrainDeactivateAfter(200*time.Millisecond),
    )

    // Wait for passivation and assert deactivation
    probe.ExpectTerminated(identity, time.Second)
}
```

### Testing with Event Stream

```go
func TestEventStream(t *testing.T) {
    ctx := context.TODO()
    kit := testkit.New(ctx, t)
    t.Cleanup(func() { kit.Shutdown(ctx) })

    // Subscribe to events (auto-unsubscribes on test cleanup)
    subscriber := kit.Subscribe()
    require.NotNil(t, subscriber)

    // Spawn an actor (generates events)
    kit.Spawn(ctx, "test-actor", &MyActor{})

    // Poll for events
    time.Sleep(100 * time.Millisecond)
    for event := range subscriber.Iterator() {
        // Check for ActorStarted event
        if started, ok := event.Payload().(*goaktpb.ActorStarted); ok {
            t.Logf("Actor started: %s", started.GetAddress())
        }
    }
}
```

## Next Steps

- [Best Practices](best-practices.md): Testing conventions and tips
- [Actors Overview](../actors/overview.md): Understanding actor fundamentals
- [Grains Overview](../grains/overview.md): Understanding virtual actors
