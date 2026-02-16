# Actor Overview

## What is an Actor?

An actor is a lightweight, concurrent, and isolated unit of computation that encapsulates both **state** and **behavior**. Actors communicate exclusively through asynchronous message passing, making them ideal for building concurrent and distributed systems.

In GoAkt, actors are the fundamental building blocks for creating reactive, fault-tolerant applications. Each actor:

- Processes one message at a time (ensuring thread safety)
- Maintains its own private state
- Communicates only via messages
- Has a unique identity (PID - Process ID)
- Can be supervised for fault tolerance

## The Actor Model

The actor model is a conceptual model for handling concurrent computation. When an actor receives a message, it can:

1. **Send messages** to other actors
2. **Create new actors** (spawn child actors)
3. **Designate behavior** for the next message (change state/behavior)

This simple set of rules enables powerful patterns for building scalable, fault-tolerant systems.

## Actor Interface

Every actor in GoAkt must implement the `Actor` interface:

```go
type Actor interface {
    PreStart(ctx *Context) error
    Receive(ctx *ReceiveContext)
    PostStop(ctx *Context) error
}
```

### Lifecycle Hooks

#### PreStart

Called once before the actor starts processing messages. Use this to:

- Initialize dependencies (database clients, caches, connections)
- Recover persistent state
- Set up resources

If `PreStart` returns an error, the actor won't start and the supervisor will handle the failure.

```go
func (a *MyActor) PreStart(ctx *Context) error {
    // Initialize database connection
    db, err := connectDB()
    if err != nil {
        return err
    }
    a.db = db
    return nil
}
```

#### Receive

The heart of the actor's behavior. All messages sent to the actor are processed here.

```go
func (a *MyActor) Receive(ctx *ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *Ping:
        ctx.Response(&Pong{})
    case *DoWork:
        result := a.doWork(msg)
        ctx.Response(result)
    default:
        ctx.Unhandled()
    }
}
```

#### PostStop

Called when the actor is shutting down. Use this to:

- Release resources (connections, files, goroutines)
- Flush logs or metrics
- Notify other systems of termination

```go
func (a *MyActor) PostStop(ctx *Context) error {
    // Close database connection
    if a.db != nil {
        return a.db.Close()
    }
    return nil
}
```

## Creating Actors

### Basic Actor

```go
package main

import (
    "github.com/tochemey/goakt/v3/actor"
)

type GreeterActor struct {
    greetings int
}

func (a *GreeterActor) PreStart(ctx *actor.Context) error {
    a.greetings = 0
    return nil
}

func (a *GreeterActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *Greet:
        a.greetings++
        ctx.Response(&Greeting{
            Message: "Hello, " + msg.GetName(),
            Count:   int32(a.greetings),
        })
    }
}

func (a *GreeterActor) PostStop(ctx *actor.Context) error {
    return nil
}
```

## Spawning Actors

Actors are spawned within an `ActorSystem`:

```go
// Create actor system
actorSystem, err := actor.NewActorSystem("MySystem",
    actor.WithPassivationDisabled())
if err != nil {
    panic(err)
}

// Start the system
if err := actorSystem.Start(ctx); err != nil {
    panic(err)
}

// Spawn an actor
pid, err := actorSystem.Spawn(ctx, "greeter", &GreeterActor{})
if err != nil {
    panic(err)
}
```

### Spawn Options

You can customize actor behavior with spawn options:

```go
pid, err := actorSystem.Spawn(ctx, "greeter", &GreeterActor{},
    actor.WithPassivationAfter(5*time.Minute),
    actor.WithMailbox(actor.NewBoundedMailbox(1000)),
    actor.WithSupervisorDirective(actor.NewStopDirective()),
)
```

## Actor Identity (PID)

When you spawn an actor, you receive a **PID** (Process ID) - a unique reference to that actor.

```go
type PID struct {
    // Contains actor system reference, address, and metadata
}
```

### PID Operations

```go
// Check if actor is running
if pid.IsRunning() {
    // Actor is alive and can receive messages
}

// Get actor's name
name := pid.Name()

// Get actor's address
addr := pid.Address()

// Stop the actor
err := pid.Shutdown(ctx)
```

## Actor Hierarchy

Actors in GoAkt are organized in a hierarchy:

```
ActorSystem
  └── RootGuardian
        ├── SystemGuardian (system actors)
        └── UserGuardian (user actors)
              ├── Actor1
              │     ├── ChildActor1
              │     └── ChildActor2
              └── Actor2
```

### Parent-Child Relationships

- **Parents supervise children**: If a child fails, the parent decides how to handle it
- **Children are stopped when parent stops**: Ensures clean shutdown
- **Hierarchical naming**: Children inherit their parent's path

### Spawning Child Actors

```go
func (a *MyActor) Receive(ctx *ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *CreateChild:
        // Spawn a child actor
        child, err := ctx.Spawn("child-worker", &ChildActor{})
        if err != nil {
            ctx.Err(err)
            return
        }
        // Child is supervised by this actor
    }
}
```

## Actor State Management

Actors encapsulate state that is:

- **Private**: Only accessible within the actor
- **Thread-safe**: Messages processed one at a time
- **Mutable**: State can change between messages

### Best Practices

1. **Keep fields private** (unexported)
2. **Initialize in PreStart**
3. **Modify only in Receive**
4. **Clean up in PostStop**

```go
type CounterActor struct {
    // Private state
    count int
    history []int
}

func (a *CounterActor) Receive(ctx *ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *Increment:
        a.count += int(msg.GetValue())
        a.history = append(a.history, a.count)
        ctx.Response(&Count{Value: int32(a.count)})
    case *GetHistory:
        ctx.Response(&History{Values: a.history})
    }
}
```

## Immutability Requirement

Actor structs **must be immutable** from the outside:

- All fields should be unexported (private)
- Initialization occurs in `PreStart`, not via constructors
- This ensures thread safety and proper supervision

❌ **Bad**:

```go
type Actor struct {
    DB *sql.DB  // Exported field
}

actor := &Actor{DB: myDB}  // External initialization
```

✅ **Good**:

```go
type Actor struct {
    db *sql.DB  // Unexported field
}

func (a *Actor) PreStart(ctx *Context) error {
    db, err := connectDB()
    if err != nil {
        return err
    }
    a.db = db
    return nil
}
```

## Message Processing

Actors process messages **one at a time** from their mailbox:

1. Message arrives and is queued in mailbox
2. Actor dequeues message
3. `Receive` is called with message context
4. Message is processed
5. Next message is dequeued

This sequential processing eliminates the need for locks and makes actors thread-safe by design.

## Location Transparency

Actors can be local or remote, but you interact with them the same way:

```go
// Local actor
local, _ := actorSystem.Spawn(ctx, "local", &MyActor{})

// Remote actor (on another node)
remote, _ := actorSystem.RemoteLookup(ctx, "remote-host", 3000, "remote")

// Same API for both
actor.Tell(ctx, local, &Message{})
actor.Tell(ctx, remote, &Message{})
```

## Actor Best Practices

### Do's ✅

- Keep actors small and focused (single responsibility)
- Use immutable messages (protocol buffers)
- Handle all message types explicitly
- Use supervision for fault tolerance
- Clean up resources in `PostStop`
- Use typed messages with protocol buffers

### Don'ts ❌

- Don't share state between actors (use messages instead)
- Don't make synchronous calls to external systems in `Receive` (use `PipeTo` instead)
- Don't block indefinitely in message handlers
- Don't expose actor internals via exported fields
- Don't create actors in constructors (use `PreStart`)

## Error Handling

Actors handle errors through:

1. **Returning errors from PreStart**: Prevents actor from starting
2. **Supervision**: Parent actors handle child failures
3. **ctx.Err()**: Report non-fatal errors during message processing
4. **Panic recovery**: Runtime catches panics and restarts actors

```go
func (a *MyActor) Receive(ctx *ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *ProcessData:
        if err := a.validateData(msg.Data); err != nil {
            ctx.Err(err)
            return
        }
        // Process valid data
    }
}
```

## Next Steps

Now that you understand the actor fundamentals, explore:

- **[Messaging](messaging.md)**: Learn about Tell, Ask, and communication patterns
- **[Mailbox](mailbox.md)**: Understand message queuing and mailbox types
- **[Behaviors](behaviours.md)**: Dynamic behavior changes for state machines
- **[Supervision](supervision.md)**: Fault tolerance and error handling
- **[Stashing](stashing.md)**: Defer message processing
- **[Passivation](passivation.md)**: Automatic resource management

## Example: Complete Actor

```go
package main

import (
    "context"
    "fmt"

    "github.com/tochemey/goakt/v3/actor"
)

// BankAccountActor manages a bank account
type BankAccountActor struct {
    balance  int64
    holder   string
    transactions []string
}

func (a *BankAccountActor) PreStart(ctx *actor.Context) error {
    a.balance = 0
    a.transactions = make([]string, 0)
    ctx.Logger().Info("Bank account opened")
    return nil
}

func (a *BankAccountActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *Deposit:
        a.balance += msg.GetAmount()
        a.transactions = append(a.transactions,
            fmt.Sprintf("Deposit: %d", msg.GetAmount()))
        ctx.Response(&Balance{Amount: a.balance})

    case *Withdraw:
        if msg.GetAmount() > a.balance {
            ctx.Err(fmt.Errorf("insufficient funds"))
            return
        }
        a.balance -= msg.GetAmount()
        a.transactions = append(a.transactions,
            fmt.Sprintf("Withdrawal: %d", msg.GetAmount()))
        ctx.Response(&Balance{Amount: a.balance})

    case *GetBalance:
        ctx.Response(&Balance{Amount: a.balance})

    case *GetTransactions:
        ctx.Response(&Transactions{Items: a.transactions})

    default:
        ctx.Unhandled()
    }
}

func (a *BankAccountActor) PostStop(ctx *actor.Context) error {
    ctx.Logger().Info("Bank account closed",
        "final_balance", a.balance,
        "transactions", len(a.transactions))
    return nil
}
```

This example demonstrates:

- Private state management (`balance`, `holder`, `transactions`)
- Initialization in `PreStart`
- Message pattern matching in `Receive`
- Error handling with `ctx.Err()`
- Resource cleanup in `PostStop`
- Responding to requests with `ctx.Response()`
