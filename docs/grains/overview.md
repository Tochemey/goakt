# Grains Overview

Grains are GoAkt's implementation of the **Virtual Actor pattern**, providing lightweight, stateful, single-threaded compute units that are automatically activated, deactivated, and distributed across the cluster.

## Table of Contents

- 🤔 [What are Grains?](#what-are-grains)
- 📐 [Core Concepts](#core-concepts)
- 📝 [Registration](#registration)
- 🚀 [Grain Activation](#grain-activation)
- ⚙️ [Configuration Options](#configuration-options)
- 🌐 [Cluster Activation Strategies](#cluster-activation-strategies)
- 💡 [Complete Example](#complete-example)
- 💾 [State Management](#state-management)
- ✅ [Best Practices](#best-practices)
- 🔌 [GrainContext API](#graincontext-api)
- 🔧 [Troubleshooting](#troubleshooting)
- ➡️ [Next Steps](#next-steps)

---

## What are Grains?

Grains in GoAkt are:

- **Virtual Actors**: Actors that exist logically but are only instantiated in memory when needed
- **Location Transparent**: Can be addressed by ID regardless of their physical location
- **Automatically Managed**: The runtime handles activation, deactivation, and placement
- **Single-Threaded**: Process one message at a time, simplifying state management
- **Distributed**: Can be seamlessly activated on any node in a cluster

### Grains vs Regular Actors

| Feature               | Regular Actors                   | Grains                                   |
|-----------------------|----------------------------------|------------------------------------------|
| **Lifecycle**         | Manual spawn/stop                | Automatic activation/deactivation        |
| **Identity**          | PID (process ID)                 | GrainIdentity (kind + name)              |
| **Location**          | Fixed after spawn                | Transparent, can move                    |
| **Passivation**       | Manual or configured             | Automatic after idle timeout             |
| **Cluster Placement** | Manual or via spawn options      | Automatic via activation strategies      |
| **State Management**  | In-memory only                   | Designed for persistence patterns        |
| **Best For**          | Long-lived services, supervisors | Entity-based domains, stateful workflows |

## Core Concepts

### Grain Interface

All grains must implement the `Grain` interface:

```go
type Grain interface {
    // OnActivate is called when the grain is loaded into memory
    OnActivate(ctx context.Context, props *GrainProps) error

    // OnReceive handles incoming messages
    OnReceive(ctx *GrainContext)

    // OnDeactivate is called before the grain is removed from memory
    OnDeactivate(ctx context.Context, props *GrainProps) error
}
```

### Grain Identity

Each grain instance is uniquely identified by a `GrainIdentity`:

```go
type GrainIdentity struct {
    kind string // Fully qualified type name (e.g., "main.UserGrain")
    name string // Unique instance identifier (e.g., "user-123")
}
```

**Format**: `kind:name` (e.g., `main.UserGrain:user-123`)

**Key Properties**:

- **Stable**: Identity persists across activations
- **Unique**: No two active grain instances share the same identity
- **Routable**: Used for message routing and location lookup

### Grain Lifecycle

```
┌──────────────┐
│  Not Active  │
└──────┬───────┘
       │ First message arrives
       ▼
┌──────────────┐
│  OnActivate  │──► Initialization, load state
└──────┬───────┘
       │ Success
       ▼
┌──────────────┐
│  OnReceive   │◄─► Process messages
└──────┬───────┘
       │ Idle timeout or explicit deactivation
       ▼
┌──────────────┐
│ OnDeactivate │──► Persist state, cleanup
└──────┬───────┘
       │
       ▼
┌──────────────┐
│  Not Active  │
└──────────────┘
```

#### OnActivate

Called when the grain is first loaded into memory:

- **Purpose**: Initialize state, load from persistence, allocate resources
- **Timing**: Before the first message is processed
- **Return**: Non-nil error prevents activation; grain remains inactive

```go
func (g *UserGrain) OnActivate(ctx context.Context, props *GrainProps) error {
    // Load user data from database
    user, err := g.db.LoadUser(ctx, props.Identity().Name())
    if err != nil {
        return err
    }

    g.user = user
    return nil
}
```

#### OnReceive

Called for each incoming message:

- **Purpose**: Process messages and update grain state
- **Guarantee**: Only one message processed at a time (single-threaded)
- **Context**: Access to message, sender, identity, and system via `GrainContext`

```go
func (g *UserGrain) OnReceive(ctx *GrainContext) {
    switch msg := ctx.Message().(type) {
    case *UpdateName:
        g.user.Name = msg.NewName
        ctx.Response(&UpdateSuccess{})
    case *GetUser:
        ctx.Response(g.user)
    default:
        ctx.Unhandled()
    }
}
```

#### OnDeactivate

Called before the grain is removed from memory:

- **Purpose**: Persist state, release resources, cleanup
- **Timing**: After idle timeout or explicit deactivation
- **Return**: Non-nil error is logged but doesn't prevent deactivation

```go
func (g *UserGrain) OnDeactivate(ctx context.Context, props *GrainProps) error {
    // Persist current state
    return g.db.SaveUser(ctx, g.user)
}
```

## Registration

Before grains can be activated, their types must be registered with the actor system:

```go
import "github.com/tochemey/goakt/v3/actor"

// Register grain type
err := system.RegisterGrainKind(ctx, &UserGrain{})
if err != nil {
    log.Fatal(err)
}
```

**Registration**:

- Associates the grain's type name with the actor system
- Enables the system to instantiate grains on demand
- Must be done on **all nodes** that may host the grain
- Typically done during application startup

**Deregistration**:

```go
err := system.DeregisterGrainKind(ctx, &UserGrain{})
```

Prevents new activations but doesn't stop existing instances.

## Grain Activation

Grains are activated automatically when first accessed:

### Using Actor System

Obtain a grain identity with `GrainIdentity` (activation happens on demand when you send messages). Pass a factory that returns the grain instance and optional `GrainOption`s:

```go
// Resolve or activate grain; options apply when the grain is first activated
identity, err := system.GrainIdentity(ctx, "user-123", func(ctx context.Context) (actor.Grain, error) {
    return &UserGrain{}, nil
}, actor.WithGrainDeactivateAfter(5*time.Minute))
if err != nil {
    log.Fatal(err)
}

// Send message (activates grain if not already active)
err = system.TellGrain(ctx, identity, &UpdateName{NewName: "Alice"})

// Request-response
response, err := system.AskGrain(ctx, identity, &GetUser{}, 5*time.Second)
```

### Using Client (External Applications)

```go
import "github.com/tochemey/goakt/v3/client"

// Create client
nodes := []client.Node{
    client.NewNode("localhost", 3321),
}
c, err := client.New(ctx, nodes)
defer c.Close()

// Build grain request (activation happens on the server when you send)
grainRequest := &remote.GrainRequest{
    Kind: "main.UserGrain",
    Name: "user-123",
}

// Send message (client picks a node)
err = c.TellGrain(ctx, grainRequest, &UpdateName{})

// Request-response
response, err := c.AskGrain(ctx, grainRequest, &GetUser{}, 5*time.Second)
```

### Remote Activation

```go
import "github.com/tochemey/goakt/v3/remote"

remoting := remote.NewRemoting()
defer remoting.Close()

grainRequest := &remote.GrainRequest{
    Kind: "main.UserGrain",
    Name: "user-123",
}

// Explicitly activate
err := remoting.RemoteActivateGrain(ctx, "node1", 3321, grainRequest)

// Tell (activates if needed)
err = remoting.RemoteTellGrain(ctx, "node1", 3321, grainRequest, &UpdateName{})

// Ask (activates if needed)
response, err := remoting.RemoteAskGrain(ctx, "node1", 3321, grainRequest, &GetUser{}, 5*time.Second)
```

## Configuration Options

Grains support extensive configuration via functional options:

### Passivation

Control when inactive grains are deactivated:

```go
// Deactivate after 5 minutes of inactivity
actor.WithGrainDeactivateAfter(5 * time.Minute)

// Never deactivate (long-lived grain)
actor.WithLongLivedGrain()
```

**Default**: 2 minutes of inactivity

### Initialization

Configure grain activation behavior:

```go
// Maximum retries for OnActivate
actor.WithGrainInitMaxRetries(10) // Default: 5

// Timeout for OnActivate
actor.WithGrainInitTimeout(3 * time.Second) // Default: 1 second
```

### Dependencies

Inject dependencies into grains:

```go
type Database struct {
    conn *sql.DB
}

func (d *Database) ID() string { return "database" }

db := &Database{conn: dbConn}

// Inject dependencies
actor.WithGrainDependencies(db, cache, logger)
```

Access in grain:

```go
func (g *UserGrain) OnActivate(ctx context.Context, props *GrainProps) error {
    for _, dep := range props.Dependencies() {
        if dep.ID() == "database" {
            g.db = dep.(*Database)
        }
    }
    return nil
}
```

### Mailbox Capacity

Control grain mailbox size:

```go
// Bounded mailbox (backpressure)
actor.WithGrainMailboxCapacity(1000)

// Unbounded mailbox (default)
// Omit option or use capacity <= 0
```

When mailbox is full, `TellGrain` returns `ErrMailboxFull`.

## Cluster Activation Strategies

In cluster mode, grains can be automatically placed across nodes:

### Round Robin

Distributes grains evenly across nodes:

```go
actor.WithActivationStrategy(actor.RoundRobinActivation)
```

**Use case**: Balanced load distribution

### Random

Places grains randomly across nodes:

```go
actor.WithActivationStrategy(actor.RandomActivation)
```

**Use case**: Quick distribution, stateless workloads

### Local

Forces activation on the current node:

```go
actor.WithActivationStrategy(actor.LocalActivation)
```

**Use case**: Node affinity, local resource access

**Default strategy**

### Least Load

Activates on the node with lowest current load:

```go
actor.WithActivationStrategy(actor.LeastLoadActivation)
```

**Use case**: Optimal resource utilization

**Note**: Requires load metrics; may add overhead

### Role-Based Activation

Restrict grain activation to nodes with specific roles:

```go
// Cluster configuration
clusterConfig := actor.NewClusterConfig().
    WithRoles("payments", "processing")

// Grain configuration
actor.WithActivationRole("payments")
```

Only nodes advertising the `"payments"` role can host this grain.

### Relocation Control

Disable automatic relocation in cluster mode:

```go
actor.WithGrainDisableRelocation()
```

**Important**:

- Grain stays on its current node even if topology changes
- If node fails, grain is lost (not migrated)
- State must be persisted externally for recovery

## Complete Example

### Define a Grain

```go
package main

import (
    "context"
    "fmt"

    "github.com/tochemey/goakt/v3/actor"
    "google.golang.org/protobuf/proto"
)

// UserGrain manages user state
type UserGrain struct {
    user  *User
    db    Database
    cache Cache
}

// OnActivate loads user from database
func (g *UserGrain) OnActivate(ctx context.Context, props *actor.GrainProps) error {
    userID := props.Identity().Name()

    // Inject dependencies
    for _, dep := range props.Dependencies() {
        switch dep.ID() {
        case "database":
            g.db = dep.(Database)
        case "cache":
            g.cache = dep.(Cache)
        }
    }

    // Load from cache first
    if user, ok := g.cache.Get(userID); ok {
        g.user = user.(*User)
        return nil
    }

    // Load from database
    user, err := g.db.LoadUser(ctx, userID)
    if err != nil {
        return fmt.Errorf("failed to load user: %w", err)
    }

    g.user = user
    return nil
}

// OnReceive processes messages
func (g *UserGrain) OnReceive(ctx *actor.GrainContext) {
    switch msg := ctx.Message().(type) {
    case *UpdateName:
        g.user.Name = msg.NewName
        g.user.UpdatedAt = time.Now()
        ctx.Response(&UpdateSuccess{UserId: g.user.ID})

    case *UpdateEmail:
        if !isValidEmail(msg.NewEmail) {
            ctx.Err(errors.New("invalid email"))
            return
        }
        g.user.Email = msg.NewEmail
        g.user.UpdatedAt = time.Now()
        ctx.Response(&UpdateSuccess{UserId: g.user.ID})

    case *GetUser:
        ctx.Response(&UserResponse{User: g.user})

    case *DeleteUser:
        g.user.Deleted = true
        ctx.Response(&DeleteSuccess{UserId: g.user.ID})

    default:
        ctx.Unhandled()
    }
}

// OnDeactivate persists user state
func (g *UserGrain) OnDeactivate(ctx context.Context, props *actor.GrainProps) error {
    if g.user.Deleted {
        return g.db.DeleteUser(ctx, g.user.ID)
    }

    // Save to database
    if err := g.db.SaveUser(ctx, g.user); err != nil {
        return err
    }

    // Update cache
    g.cache.Set(g.user.ID, g.user, 10*time.Minute)
    return nil
}
```

### Register and Use

```go
func main() {
    ctx := context.Background()

    // Create dependencies
    db := NewDatabase()
    cache := NewCache()

    // Create actor system
    system, err := actor.NewActorSystem(
        "user-service",
        actor.WithRemote("0.0.0.0", 3321),
    )
    if err != nil {
        log.Fatal(err)
    }

    if err := system.Start(ctx); err != nil {
        log.Fatal(err)
    }
    defer system.Stop(ctx)

    // Register grain type
    err = system.RegisterGrainKind(ctx, &UserGrain{})
    if err != nil {
        log.Fatal(err)
    }

    // Resolve grain identity (activation happens on first use; options apply then)
    identity, err := system.GrainIdentity(ctx, "user-123", func(ctx context.Context) (actor.Grain, error) {
        return &UserGrain{}, nil
    }, actor.WithGrainDeactivateAfter(5*time.Minute), actor.WithGrainDependencies(db, cache), actor.WithGrainInitMaxRetries(3))
    if err != nil {
        log.Fatal(err)
    }

    // Send messages
    err = system.TellGrain(ctx, identity, &UpdateName{NewName: "Alice"})
    if err != nil {
        log.Fatal(err)
    }

    // Request-response
    response, err := system.AskGrain(
        ctx,
        identity,
        &GetUser{},
        5*time.Second,
    )
    if err != nil {
        log.Fatal(err)
    }

    userResp := response.(*UserResponse)
    fmt.Printf("User: %+v\n", userResp.User)
}
```

## State Management

### Stateless Grains

Grains without persistent state:

```go
type CalculatorGrain struct{}

func (g *CalculatorGrain) OnActivate(ctx context.Context, props *actor.GrainProps) error {
    return nil // No state to load
}

func (g *CalculatorGrain) OnReceive(ctx *actor.GrainContext) {
    switch msg := ctx.Message().(type) {
    case *Add:
        ctx.Response(&Result{Value: msg.A + msg.B})
    case *Multiply:
        ctx.Response(&Result{Value: msg.A * msg.B})
    }
}

func (g *CalculatorGrain) OnDeactivate(ctx context.Context, props *actor.GrainProps) error {
    return nil // No state to persist
}
```

### Stateful Grains

Grains with persistent state patterns:

#### Event Sourcing

```go
type AccountGrain struct {
    balance float64
    version int64
    events  []Event
}

func (g *AccountGrain) OnActivate(ctx context.Context, props *actor.GrainProps) error {
    // Replay events
    events, err := eventStore.LoadEvents(ctx, props.Identity().Name())
    if err != nil {
        return err
    }

    for _, event := range events {
        g.applyEvent(event)
    }
    return nil
}

func (g *AccountGrain) OnReceive(ctx *actor.GrainContext) {
    switch msg := ctx.Message().(type) {
    case *Deposit:
        event := &DepositedEvent{Amount: msg.Amount}
        g.events = append(g.events, event)
        g.applyEvent(event)
        ctx.Response(&Success{})
    }
}

func (g *AccountGrain) OnDeactivate(ctx context.Context, props *actor.GrainProps) error {
    // Persist new events
    return eventStore.SaveEvents(ctx, props.Identity().Name(), g.events)
}
```

#### Snapshot Pattern

```go
func (g *UserGrain) OnActivate(ctx context.Context, props *actor.GrainProps) error {
    // Load latest snapshot
    snapshot, err := snapshotStore.LoadSnapshot(ctx, props.Identity().Name())
    if err == nil {
        g.user = snapshot.User
        g.version = snapshot.Version
    }
    return nil
}

func (g *UserGrain) OnDeactivate(ctx context.Context, props *actor.GrainProps) error {
    // Save snapshot
    snapshot := &Snapshot{
        User:    g.user,
        Version: g.version,
    }
    return snapshotStore.SaveSnapshot(ctx, props.Identity().Name(), snapshot)
}
```

## Best Practices

### Grain Design

1. **Keep grains focused**: One grain type per entity or aggregate
2. **Immutable messages**: Use protobuf messages for type safety
3. **Validate input**: Check message validity in `OnReceive`
4. **Handle errors gracefully**: Use `ctx.Err()` instead of panicking
5. **Avoid long-running operations**: Keep message handlers fast

### State Management

1. **Persist state in OnDeactivate**: Don't rely on in-memory state
2. **Handle activation failures**: Return errors from `OnActivate` for retry
3. **Use idempotent operations**: Messages may be redelivered
4. **Version state**: Detect concurrent modifications
5. **Consider eventual consistency**: Grains are distributed

### Performance

1. **Batch operations**: Group related updates when possible
2. **Cache hot data**: Reduce database roundtrips
3. **Use bounded mailboxes**: Apply backpressure under load
4. **Tune passivation**: Balance memory vs activation cost
5. **Monitor activation rate**: High rates may indicate issues

### Cluster Deployment

1. **Register on all nodes**: Ensure grain types are registered everywhere
2. **Choose appropriate strategy**: Match activation strategy to workload
3. **Use roles for affinity**: Colocate related grains via roles
4. **Test relocation**: Verify grains handle node failures
5. **Persist state externally**: Don't rely on in-memory state

## GrainContext API

The `GrainContext` provides access to:

```go
type GrainContext struct {
    // ...
}

// Context returns the underlying context.Context
func (gctx *GrainContext) Context() context.Context

// Self returns the grain's identity
func (gctx *GrainContext) Self() *GrainIdentity

// ActorSystem returns the managing actor system
func (gctx *GrainContext) ActorSystem() ActorSystem

// Message returns the current message
func (gctx *GrainContext) Message() proto.Message

// Response sends a response to the sender
func (gctx *GrainContext) Response(reply proto.Message)

// Err reports an error
func (gctx *GrainContext) Err(err error)

// Unhandled marks the message as unhandled
func (gctx *GrainContext) Unhandled()
```

## Troubleshooting

### Grain Fails to Activate

**Symptoms**:

- `OnActivate` returns error
- Grain never becomes active

**Solutions**:

- Check error message from `OnActivate`
- Verify dependencies are available
- Ensure database/external services are reachable
- Increase `WithGrainInitTimeout` if activation is slow
- Increase `WithGrainInitMaxRetries` for transient failures

### Grain Not Found

**Symptoms**:

- `grain kind not registered` error

**Solutions**:

- Call `RegisterGrainKind` on all nodes
- Verify registration happens before grain usage
- Check grain type name matches across nodes

### State Loss

**Symptoms**:

- Grain state resets after passivation
- Data lost after node restart

**Solutions**:

- Implement persistence in `OnDeactivate`
- Restore state in `OnActivate`
- Use external storage (database, key-value store)
- Test passivation behavior explicitly

### High Memory Usage

**Symptoms**:

- Many grains active simultaneously
- Memory grows over time

**Solutions**:

- Reduce `WithGrainDeactivateAfter` timeout
- Use bounded mailboxes to limit grain activation
- Monitor grain activation count
- Consider grain design (too fine-grained?)

### Activation Errors in Cluster

**Symptoms**:

- Grain activates on wrong node
- `no nodes with required role` error

**Solutions**:

- Verify all nodes registered the grain type
- Check role configuration matches
- Review activation strategy selection
- Ensure cluster is healthy and nodes are connected

## Next Steps

- [Usage Examples](usage.md): Practical grain usage patterns
- [Cluster Overview](../cluster/overview.md): Distributed grain activation
- [Testing](../testing/overview.md): Testing grains effectively
