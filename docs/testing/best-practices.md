# Testing Best Practices

Best practices for writing reliable tests using the GoAkt `testkit` package.

## Table of Contents

- 🔄 [TestKit Lifecycle](#testkit-lifecycle)
- 🔍 [Probe Patterns](#probe-patterns)
- 🌾 [Grain Testing Patterns](#grain-testing-patterns)
- 🌐 [Multi-Node Testing Patterns](#multi-node-testing-patterns)
- 📁 [Test Organization](#test-organization)
- ⚠️ [Common Mistakes](#common-mistakes)
- 📋 [Summary](#summary)

---

## TestKit Lifecycle

### Always Clean Up

Use `t.Cleanup` to guarantee the test system shuts down, even on failure:

```go
func TestMyActor(t *testing.T) {
    ctx := context.TODO()
    kit := testkit.New(ctx, t)
    t.Cleanup(func() { kit.Shutdown(ctx) })

    probe := kit.NewProbe(ctx)
    defer probe.Stop()

    // ... test code
}
```

### One TestKit per Test

Create a fresh `TestKit` for each test to avoid shared state:

```go
// Good: Isolated tests
func TestBehaviorA(t *testing.T) {
    ctx := context.TODO()
    kit := testkit.New(ctx, t)
    t.Cleanup(func() { kit.Shutdown(ctx) })
    // ...
}

func TestBehaviorB(t *testing.T) {
    ctx := context.TODO()
    kit := testkit.New(ctx, t)
    t.Cleanup(func() { kit.Shutdown(ctx) })
    // ...
}
```

### Extract a Helper

If you repeat the same setup, extract a helper:

```go
func newKit(t *testing.T) (*testkit.TestKit, context.Context) {
    t.Helper()
    ctx := context.TODO()
    kit := testkit.New(ctx, t)
    t.Cleanup(func() { kit.Shutdown(ctx) })
    return kit, ctx
}

func TestActorA(t *testing.T) {
    kit, ctx := newKit(t)
    kit.Spawn(ctx, "actor", &MyActor{})
    // ...
}
```

## Probe Patterns

### Always Stop Probes

Stop the probe when done to release resources:

```go
probe := kit.NewProbe(ctx)
defer probe.Stop()
```

### Always Assert No Trailing Messages

After the expected messages, assert no unexpected messages remain:

```go
probe.Send("actor", new(testpb.TestPing))
probe.ExpectMessage(new(testpb.TestPong))
probe.ExpectNoMessage() // Verify nothing unexpected
```

### Use the Right Send Method

| Scenario         | Method                               | Actor Replies Via               |
|------------------|--------------------------------------|---------------------------------|
| Fire-and-forget  | `probe.Send(name, msg)`              | `ctx.Tell(ctx.Sender(), reply)` |
| Request-response | `probe.SendSync(name, msg, timeout)` | `ctx.Response(reply)`           |

```go
// Tell pattern: actor replies to sender
probe.Send("actor", new(testpb.TestPing))
probe.ExpectMessage(new(testpb.TestPong))

// Ask pattern: actor uses ctx.Response()
probe.SendSync("actor", new(testpb.TestReply), time.Second)
probe.ExpectMessage(new(testpb.Reply))
```

### Use Timeouts for Slow Actors

When an actor performs async or delayed work, use `Within` variants:

```go
// Actor takes ~1s to respond
probe.Send("slow-actor", &testpb.TestWait{Duration: uint64(time.Second)})
probe.ExpectMessageWithin(2*time.Second, new(testpb.TestPong))
```

### Use ExpectMessageOfType for Partial Matching

When you only care about the message type, not exact field values:

```go
probe.Send("actor", new(testpb.TestPing))
probe.ExpectMessageOfType(new(testpb.TestPong))
```

## Grain Testing Patterns

### Use GrainIdentity with a Factory

Always create grain identities through the testkit:

```go
identity := kit.GrainIdentity(ctx, "my-grain",
    func(ctx context.Context) (actor.Grain, error) {
        return &MyGrain{}, nil
    },
)
```

### Test Grain Passivation

Use `WithGrainDeactivateAfter` and `ExpectTerminated`. Activate the grain first (e.g. send a message), then assert it terminates after the passivation timeout:

```go
identity := kit.GrainIdentity(ctx, "temp-grain",
    func(ctx context.Context) (actor.Grain, error) {
        return &MyGrain{}, nil
    },
    actor.WithGrainDeactivateAfter(200*time.Millisecond),
)

probe := kit.NewGrainProbe(ctx)
defer probe.Stop()

// Activate grain, then wait for passivation
probe.Send(identity, new(testpb.TestPing))
probe.ExpectNoResponse()
probe.ExpectTerminated(identity, time.Second)
```

### Tell vs Ask for Grains

```go
probe := kit.NewGrainProbe(ctx)

// Tell: fire-and-forget, no response expected
probe.Send(identity, new(testpb.TestPing))
probe.ExpectNoResponse()

// Ask: expects a response
probe.SendSync(identity, new(testpb.TestReply), time.Second)
probe.ExpectResponse(new(testpb.Reply))
```

## Multi-Node Testing Patterns

### Setup and Teardown

```go
func TestCluster(t *testing.T) {
    ctx := context.Background()

    multi := testkit.NewMultiNodes(t, log.DiscardLogger, []actor.Actor{&MyGrain{}}, nil)
    multi.Start()
    t.Cleanup(multi.Stop)

    node1 := multi.StartNode(ctx, "node-1")
    node2 := multi.StartNode(ctx, "node-2")

    // Test cross-node communication
    node1.Spawn(ctx, "echo", &EchoActor{})
    probe := node2.SpawnProbe(ctx)
    defer probe.Stop()

    probe.Send("echo", new(testpb.TestPing))
    probe.ExpectMessage(new(testpb.TestPong))
}
```

### Use SenderAddress in Cluster Tests

In multi-node tests, `Sender()` may return nil for remote actors. Use `SenderAddress()` instead:

```go
probe := node2.SpawnProbe(ctx)
probe.Send("remote-actor", msg)
probe.ExpectAnyMessage()

addr := probe.SenderAddress()
// addr is always populated, even for remote senders
```

## Test Organization

### Use Table-Driven Tests

```go
func TestActorResponses(t *testing.T) {
    tests := []struct {
        name     string
        input    proto.Message
        expected proto.Message
    }{
        {
            name:     "ping returns pong",
            input:    new(testpb.TestPing),
            expected: new(testpb.TestPong),
        },
        {
            name:     "reply returns reply",
            input:    new(testpb.TestReply),
            expected: new(testpb.Reply),
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            ctx := context.TODO()
            kit := testkit.New(ctx, t)
            t.Cleanup(func() { kit.Shutdown(ctx) })

            kit.Spawn(ctx, "actor", &MyActor{})

            probe := kit.NewProbe(ctx)
            defer probe.Stop()

            probe.Send("actor", tt.input)
            probe.ExpectMessage(tt.expected)
            probe.ExpectNoMessage()
        })
    }
}
```

### Use Subtests

Group related tests using `t.Run`:

```go
func TestPingActor(t *testing.T) {
    t.Run("responds to ping", func(t *testing.T) {
        kit, ctx := newKit(t)
        kit.Spawn(ctx, "pinger", &PingActor{})
        probe := kit.NewProbe(ctx)
        defer probe.Stop()

        probe.Send("pinger", new(testpb.TestPing))
        probe.ExpectMessage(new(testpb.TestPong))
        probe.ExpectNoMessage()
    })

    t.Run("responds to sync request", func(t *testing.T) {
        kit, ctx := newKit(t)
        kit.Spawn(ctx, "pinger", &PingActor{})
        probe := kit.NewProbe(ctx)
        defer probe.Stop()

        probe.SendSync("pinger", new(testpb.TestReply), time.Second)
        probe.ExpectMessage(new(testpb.Reply))
        probe.ExpectNoMessage()
    })

    t.Run("can be terminated", func(t *testing.T) {
        kit, ctx := newKit(t)
        kit.Spawn(ctx, "pinger", &PingActor{})
        probe := kit.NewProbe(ctx)
        defer probe.Stop()

        probe.WatchNamed("pinger")
        probe.Send("pinger", new(goaktpb.PoisonPill))
        probe.ExpectTerminated("pinger")
    })
}
```

## Common Mistakes

### Forgetting to Stop the Probe

```go
// Bad: probe resources leak
probe := kit.NewProbe(ctx)
probe.Send("actor", msg)
probe.ExpectMessage(expected)

// Good: always stop
probe := kit.NewProbe(ctx)
defer probe.Stop()
```

### Forgetting to Shut Down the TestKit

```go
// Bad: actor system leaks
kit := testkit.New(ctx, t)

// Good: use t.Cleanup
kit := testkit.New(ctx, t)
t.Cleanup(func() { kit.Shutdown(ctx) })
```

### Using Send When the Actor Uses ctx.Response()

If the actor replies via `ctx.Response(...)`, you need `SendSync`, not `Send`:

```go
// If actor does: ctx.Response(new(testpb.Reply))
// Bad: probe.Send won't capture the response
probe.Send("actor", new(testpb.TestReply))

// Good: use SendSync
probe.SendSync("actor", new(testpb.TestReply), time.Second)
probe.ExpectMessage(new(testpb.Reply))
```

### Using SendSync When the Actor Uses ctx.Tell()

If the actor replies via `ctx.Tell(ctx.Sender(), reply)`, use `Send`:

```go
// If actor does: ctx.Tell(ctx.Sender(), new(testpb.TestPong))
// Good: use Send
probe.Send("actor", new(testpb.TestPing))
probe.ExpectMessage(new(testpb.TestPong))
```

### Not Asserting ExpectNoMessage

Always finish assertion sequences with `ExpectNoMessage()` to catch unexpected messages:

```go
// Bad: might miss unexpected extra messages
probe.Send("actor", msg)
probe.ExpectMessage(expected)

// Good: verify no stray messages
probe.Send("actor", msg)
probe.ExpectMessage(expected)
probe.ExpectNoMessage()
```

## Summary

| Do                                             | Don't                                |
|------------------------------------------------|--------------------------------------|
| Use `t.Cleanup` for shutdown                   | Forget to shut down TestKit          |
| Stop probes with `defer probe.Stop()`          | Leave probes running                 |
| Use `ExpectNoMessage()` after assertions       | Skip trailing message checks         |
| Use `SendSync` for `ctx.Response()` actors     | Mix up Send and SendSync             |
| Use `ExpectMessageWithin` etc. for slow actors | Use fixed `time.Sleep`               |
| Create fresh TestKit per test                  | Share TestKit across tests           |
| Use table-driven tests for multiple cases      | Duplicate boilerplate                |
| Use `SenderAddress()` in cluster tests         | Rely on `Sender()` for remote actors |
