# Messaging

Messaging is the foundation of actor communication in GoAkt. Actors interact exclusively through asynchronous message passing using **protocol buffers** as the message format.

## Table of Contents

- 📦 [Message Types](#message-types)
- 📨 [Messaging Patterns](#messaging-patterns)
- ↩️ [Responding to Messages](#responding-to-messages)
- 📤 [Batch Messaging](#batch-messaging)
- 📋 [Message Context](#message-context)
- 🔀 [Actor-to-Actor Messaging](#actor-to-actor-messaging)
- 🌐 [Remote Messaging](#remote-messaging)
- ➡️ [Forwarding Messages](#forwarding-messages)
- ⏰ [Message Scheduling](#message-scheduling)
- 📬 [Request Pattern](#request-pattern)
- 🔗 [PipeTo Pattern](#pipeto-pattern)
- 📊 [Message Ordering Guarantees](#message-ordering-guarantees)
- ⚠️ [Error Handling](#error-handling)
- ✅ [Best Practices](#best-practices)
- 💡 [Complete Example](#complete-example)
- ➡️ [Next Steps](#next-steps)

---

## Message Types

All messages in GoAkt must be protocol buffer messages (`proto.Message`). This ensures:
- **Type safety**: Compile-time type checking
- **Serialization**: Efficient wire format for remote communication
- **Versioning**: Forward and backward compatibility
- **Language interoperability**: Protocol buffers work across languages

### Defining Messages

```protobuf
syntax = "proto3";

package myapp;

message Greet {
    string name = 1;
}

message Greeting {
    string message = 1;
    int32 count = 2;
}

message GetBalance {
}

message Balance {
    int64 amount = 1;
}
```

## Messaging Patterns

GoAkt provides two primary messaging patterns:

### 1. Tell (Fire-and-Forget)

Send a message asynchronously without waiting for a response.

```go
import (
    "context"
    "github.com/tochemey/goakt/v3/actor"
)

// Tell sends a message and returns immediately
err := actor.Tell(ctx, pid, &Greet{Name: "Alice"})
if err != nil {
    // Handle error (e.g., actor is dead)
}
```

**Characteristics:**
- **Non-blocking**: Returns immediately
- **No response**: Sender doesn't wait for reply
- **Fire-and-forget**: Message is queued in mailbox
- **Fast**: Minimal overhead
- **Use for**: Commands, notifications, events

**Example:**

```go
// Notify an actor without waiting
func notifyUser(ctx context.Context, pid *actor.PID) {
    notification := &Notification{
        Message: "Your order has shipped",
        Timestamp: time.Now().Unix(),
    }
    
    if err := actor.Tell(ctx, pid, notification); err != nil {
        log.Printf("Failed to notify: %v", err)
    }
}
```

### 2. Ask (Request-Response)

Send a message and wait for a response synchronously.

```go
// Ask sends a message and blocks until response or timeout
response, err := actor.Ask(ctx, pid, &GetBalance{}, 5*time.Second)
if err != nil {
    // Handle timeout or actor dead error
}

balance := response.(*Balance)
fmt.Printf("Balance: %d\n", balance.GetAmount())
```

**Characteristics:**
- **Blocking**: Waits for response
- **Timeout**: Must specify maximum wait time
- **Synchronous**: Returns response or error
- **Use for**: Queries, requests requiring replies

**Example:**

```go
func getAccountBalance(ctx context.Context, pid *actor.PID) (int64, error) {
    response, err := actor.Ask(ctx, pid, &GetBalance{}, 3*time.Second)
    if err != nil {
        return 0, fmt.Errorf("failed to get balance: %w", err)
    }
    
    balance := response.(*Balance)
    return balance.GetAmount(), nil
}
```

## Responding to Messages

When an actor receives a message via Ask, it should respond using `ctx.Response()`:

```go
func (a *BankAccount) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *GetBalance:
        // Respond to Ask request
        ctx.Response(&Balance{
            Amount: a.balance,
        })
        
    case *Deposit:
        a.balance += msg.GetAmount()
        // Send confirmation
        ctx.Response(&Balance{
            Amount: a.balance,
        })
    }
}
```

## Batch Messaging

For sending multiple messages efficiently:

### BatchTell

Send multiple messages asynchronously:

```go
messages := []proto.Message{
    &LogEntry{Level: "INFO", Message: "Started"},
    &LogEntry{Level: "DEBUG", Message: "Processing"},
    &LogEntry{Level: "INFO", Message: "Completed"},
}

err := actor.BatchTell(ctx, loggerPID, messages...)
```

**Note**: Messages are processed **one at a time** in order. This is a design choice to maintain the actor model principle of sequential message processing.

### BatchAsk

Send multiple messages and receive responses in order:

```go
queries := []proto.Message{
    &GetUser{Id: 1},
    &GetUser{Id: 2},
    &GetUser{Id: 3},
}

responses, err := actor.BatchAsk(ctx, userPID, 5*time.Second, queries...)
if err != nil {
    log.Fatal(err)
}

// Responses arrive in same order as queries
for response := range responses {
    user := response.(*User)
    fmt.Printf("User: %s\n", user.GetName())
}
```

## Message Context

The `ReceiveContext` provides rich context for each message:

### Accessing Message Information

```go
func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    // Get the message
    msg := ctx.Message()
    
    // Get sender (if available)
    sender := ctx.Sender()
    if sender != nil {
        fmt.Printf("Message from: %s\n", sender.Name())
    }
    
    // Get sender address (works for local and remote)
    senderAddr := ctx.SenderAddress()
    
    // Get remote sender (if from another node)
    remoteSender := ctx.RemoteSender()
    
    // Get context for async operations
    context := ctx.Context()
    
    // Get logger
    logger := ctx.Logger()
    logger.Info("Processing message", "type", msg)
}
```

### Sender Information

```go
func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *Request:
        // Check if sender is known
        if ctx.Sender() == nil {
            ctx.Logger().Warn("No sender for request")
            return
        }
        
        // Reply to sender
        ctx.Tell(ctx.Sender(), &Response{
            Data: a.processRequest(msg),
        })
    }
}
```

## Actor-to-Actor Messaging

Within an actor's `Receive` method, you can send messages to other actors:

### Tell from Actor

```go
func (a *Coordinator) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *DistributeWork:
        // Send work to multiple workers
        for _, workerPID := range a.workers {
            ctx.Tell(workerPID, &WorkItem{
                Id:   msg.GetId(),
                Data: msg.GetData(),
            })
        }
    }
}
```

### Ask from Actor

```go
func (a *Manager) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *ProcessOrder:
        // Query inventory actor (Ask returns only response; check for nil on error)
        response := ctx.Ask(a.inventoryPID,
            &CheckStock{ProductId: msg.GetProductId()},
            2*time.Second)
        if response == nil {
            return
        }
        stock := response.(*StockLevel)
        if stock.GetQuantity() > 0 {
            // Process order
            ctx.Tell(a.orderPID, &CreateOrder{
                ProductId: msg.GetProductId(),
                Quantity:  msg.GetQuantity(),
            })
        }
    }
}
```

## Remote Messaging

GoAkt supports transparent remote messaging across nodes:

### Remote Tell

```go
// Resolve remote actor address, then send (from inside an actor)
addr := ctx.RemoteLookup("remote-host", 3000, "remote-actor")
if addr != nil {
    ctx.RemoteTell(addr, &Message{})
}
```

### Remote Ask

```go
// Query actor on remote node (returns *anypb.Any; unmarshal to your type)
addr := ctx.RemoteLookup("remote-host", 3000, "remote-actor")
if addr != nil {
    response := ctx.RemoteAsk(addr, &Query{}, 5*time.Second)
    if response != nil {
        // Unmarshal response to your proto type
    }
}
```

### Remote Lookup

```go
// From inside an actor: get address of remote actor by host, port, name
addr := ctx.RemoteLookup("remote-host", 3000, "actor-name")
if addr != nil {
    ctx.RemoteTell(addr, &Message{})
}

// From outside: use PID.RemoteLookup or system for discovery
addr, err := pid.RemoteLookup(ctx, "remote-host", 3000, "actor-name")
if err == nil && addr != nil {
    _ = pid.RemoteTell(ctx, addr, &Message{})
}
```

### SendSync and SendAsync

These methods send to an actor **by name**; the system resolves the target (local or remote). They are the **name-based** counterparts of **Tell** and **Ask**.

| Method                                    | Behavior                                                                                              | Use when                                                                   |
|-------------------------------------------|-------------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------|
| **SendAsync**(actorName, message)         | Fire-and-forget; does not block or expect a reply.                                                    | Like Tell, but target is an actor **name** (e.g. in cluster).              |
| **SendSync**(actorName, message, timeout) | Blocks until response or timeout; returns response (or nil). From Receive, errors are in `ctx.Err()`. | Like Ask, but target is an actor **name**. Receiver uses `ctx.Response()`. |

**From inside an actor (ReceiveContext):**

```go
ctx.SendAsync("worker-pool", &Job{Id: 1})           // no reply; check ctx.Err() for send failure
response := ctx.SendSync("service", &Query{}, 5*time.Second)  // blocks; check ctx.Err() and response == nil on failure
```

**From outside (PID as sender):** `pid.SendAsync(ctx, actorName, message)` returns `error`; `pid.SendSync(ctx, actorName, message, timeout)` returns `(proto.Message, error)`.

For PID-based Tell/Ask and error handling, see [Tell and Ask](#1-tell-fire-and-forget) and [Ask Errors](#ask-errors). For async request-response without blocking the sender, see [Request Pattern](#request-pattern) and [Reentrancy](reentrancy.md).

## Forwarding Messages

Forward a message to another actor while preserving the original sender:

```go
func (a *Router) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *RoutableMessage:
        // Select target based on routing logic
        target := a.selectWorker(msg.GetKey())
        
        // Forward preserves original sender
        ctx.Forward(target)
    }
}
```

### ForwardTo

Forward to an actor by name (location-transparent in cluster):

```go
func (a *Proxy) Receive(ctx *actor.ReceiveContext) {
    // Forward to backend by name
    ctx.ForwardTo("backend-actor")
}
// To forward to a specific PID (local), use ctx.Forward(a.backendPID)
```

## Message Scheduling

Schedule messages for future delivery:

### One-time Scheduled Message

```go
// Scheduling is on the actor system; from inside Receive:
err := ctx.ActorSystem().ScheduleOnce(ctx.Context(),
    &ReminderMessage{Text: "Don't forget!"},
    targetPID,
    5*time.Second,
)
```

### Repeated Scheduled Messages

```go
// Parameter order: ctx, message, pid, cronExpression
err := system.ScheduleWithCron(ctx, &HealthCheck{}, targetPID, "*/5 * * * * *")
```

See [Message Scheduling](message_scheduling.md) for more details.

## Request Pattern

The **request pattern** provides **async request-response**: the sender sends a request and gets the response later via a **continuation** (callback), without blocking the actor’s mailbox. This allows the actor to keep processing other messages while the request is in flight.

**Requirements:**

- The **sender actor** must be spawned with **reentrancy enabled**. Otherwise `Request` / `RequestName` fail (return `nil` and set `ctx.Err()`). See [Reentrancy](reentrancy.md) for modes, configuration, and behavior.
- The **receiver** responds with `ctx.Response(...)` just as for Ask; the runtime routes that response back to the sender and invokes the sender’s continuation.

**When to use:**

- You need a **reply** from another actor but do not want to **block** (unlike Ask).
- You want the actor to handle **other messages** while waiting (e.g. multiple pending requests, or queries while a long-running request is outstanding).

### Request

Send a request to a PID and handle the response in a continuation via the returned `RequestCall`:

```go
func (a *Client) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *FetchData:
        call := ctx.Request(a.serverPID, &DataQuery{Id: msg.GetId()})
        if call == nil {
            // Request failed (e.g. reentrancy disabled); check ctx.Err()
            return
        }
        call.Then(func(response proto.Message, err error) {
            if err != nil {
                ctx.Logger().Error("Request failed", "error", err)
                return
            }
            dataResp := response.(*DataResponse)
            a.handleData(dataResp)
        })
        // Actor can process more messages; response is handled in Then()
    }
}
```

The receiver uses `ctx.Response()` as with Ask:

```go
case *DataQuery:
    ctx.Response(&DataResponse{Data: a.loadData(msg.GetId())})
```

### RequestName

Same as `Request`, but the target is identified by **actor name** (resolved at call time; works with cluster/remoting):

```go
call := ctx.RequestName("data-server", &DataQuery{Id: 123})
if call != nil {
    call.Then(func(response proto.Message, err error) {
        // handle response or err
    })
}
```

For reentrancy modes (AllowReentrant, StashNonReentrant), timeouts, and full examples, see **[Reentrancy](reentrancy.md)**.

## PipeTo Pattern

Execute async work and pipe the result back to an actor:

```go
func (a *Worker) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *HeavyTask:
        // Execute expensive operation in background
        ctx.PipeTo(a.resultHandler, func() (proto.Message, error) {
            // Long-running operation
            result := doExpensiveComputation(msg.GetData())
            return &TaskResult{Data: result}, nil
        })
    }
}
```

See [PipeTo Pattern](pipeto.md) for more details.

## Message Ordering Guarantees

GoAkt provides the following ordering guarantees:

### Per-Actor Ordering

Messages sent to the **same actor** are processed **in order**:

```go
// These messages will be processed in order
actor.Tell(ctx, pid, &Message1{})
actor.Tell(ctx, pid, &Message2{})
actor.Tell(ctx, pid, &Message3{})
// Actor receives: Message1 → Message2 → Message3
```

### No Global Ordering

Messages from **different senders** to the **same actor** have no guaranteed order:

```go
// From sender A
actor.Tell(ctx, pid, &MessageA{})

// From sender B (concurrent)
actor.Tell(ctx, pid, &MessageB{})

// Order is non-deterministic: could be A→B or B→A
```

### Point-to-Point Ordering

Messages between **two specific actors** maintain order:

```go
// In ActorA
ctx.Tell(actorB, &Msg1{})
ctx.Tell(actorB, &Msg2{})
// ActorB receives in order: Msg1 → Msg2
```

## Error Handling

### Tell Errors

`Tell` returns an error if:
- Actor is dead or not running
- Mailbox is full (bounded mailbox)
- System is shutting down

```go
err := actor.Tell(ctx, pid, &Message{})
if err != nil {
    if errors.Is(err, actor.ErrDead) {
        log.Println("Actor is dead")
    }
}
```

### Ask Errors

`Ask` returns an error if:
- Actor is dead or not running
- Request times out
- Context is cancelled

```go
response, err := actor.Ask(ctx, pid, &Query{}, 5*time.Second)
if err != nil {
    if errors.Is(err, actor.ErrRequestTimeout) {
        log.Println("Request timed out")
    }
}
```

## Best Practices

### Message Design

✅ **Do:**
- Use immutable protocol buffer messages
- Keep messages small and focused
- Include all necessary data in message
- Version your messages properly
- Use specific message types

❌ **Don't:**
- Share mutable state via messages
- Send large payloads (consider batching)
- Reuse message instances
- Use generic "Any" messages

### Messaging Patterns

✅ **Do:**
- Use Tell for fire-and-forget
- Use Ask for queries
- Set reasonable timeouts for Ask
- Handle errors from messaging calls
- Use Request/PipeTo for async patterns

❌ **Don't:**
- Block indefinitely with Ask
- Ignore errors from Tell
- Create request-response loops
- Use Ask in hot paths (prefer Tell)

### Performance Tips

1. **Prefer Tell over Ask**: Tell is faster and doesn't block
2. **Batch when possible**: Use BatchTell for multiple messages
3. **Set appropriate timeouts**: Don't use excessive timeout values
4. **Use bounded mailboxes**: Prevent memory issues
5. **Monitor dead letters**: Track undeliverable messages

## Complete Example

```go
package main

import (
    "context"
    "fmt"
    "time"
    
    "github.com/tochemey/goakt/v3/actor"
)

// OrderProcessor handles order processing
type OrderProcessor struct {
    inventoryPID *actor.PID
    paymentPID   *actor.PID
}

func (a *OrderProcessor) PreStart(ctx *actor.Context) error {
    // Lookup dependent actors
    var err error
    a.inventoryPID, err = ctx.ActorOf("inventory")
    if err != nil {
        return err
    }
    
    a.paymentPID, err = ctx.ActorOf("payment")
    if err != nil {
        return err
    }
    
    return nil
}

func (a *OrderProcessor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *CreateOrder:
        a.handleCreateOrder(ctx, msg)
        
    case *StockReserved:
        a.handleStockReserved(ctx, msg)
        
    case *PaymentCompleted:
        a.handlePaymentCompleted(ctx, msg)
        
    case *OrderFailed:
        ctx.Response(&OrderResult{
            Success: false,
            Message: msg.GetReason(),
        })
    }
}

func (a *OrderProcessor) handleCreateOrder(ctx *actor.ReceiveContext, msg *CreateOrder) {
    // 1. Check inventory (Ask pattern; returns only response, nil on error)
    response := ctx.Ask(a.inventoryPID,
        &CheckStock{ProductId: msg.GetProductId()},
        2*time.Second)
    if response == nil {
        ctx.Response(&OrderResult{Success: false})
        return
    }
    
    stock := response.(*StockLevel)
    if stock.GetQuantity() < msg.GetQuantity() {
        ctx.Response(&OrderResult{
            Success: false,
            Message: "Insufficient stock",
        })
        return
    }
    
    // 2. Reserve inventory (Tell pattern)
    ctx.Tell(a.inventoryPID, &ReserveStock{
        ProductId: msg.GetProductId(),
        Quantity:  msg.GetQuantity(),
        OrderId:   msg.GetOrderId(),
    })
}

func (a *OrderProcessor) handleStockReserved(ctx *actor.ReceiveContext, msg *StockReserved) {
    // 3. Process payment (Tell pattern)
    ctx.Tell(a.paymentPID, &ProcessPayment{
        OrderId: msg.GetOrderId(),
        Amount:  msg.GetAmount(),
    })
}

func (a *OrderProcessor) handlePaymentCompleted(ctx *actor.ReceiveContext, msg *PaymentCompleted) {
    // 4. Order completed successfully
    ctx.Response(&OrderResult{
        Success: true,
        OrderId: msg.GetOrderId(),
    })
}

func (a *OrderProcessor) PostStop(ctx *actor.Context) error {
    return nil
}
```

This example demonstrates:
- **Tell**: Fire-and-forget commands (reserve stock, process payment)
- **Ask**: Synchronous queries (check stock)
- **Message flow**: Multi-step order processing
- **Error handling**: Validation and error responses
- **Actor collaboration**: Multiple actors working together

## Next Steps

- **[Mailbox](mailbox.md)**: Understand message queuing and mailbox types
- **[Message Scheduling](message_scheduling.md)**: Schedule messages for future delivery
- **[PipeTo Pattern](pipeto.md)**: Execute async work and pipe results
- **[Reentrancy](reentrancy.md)**: Handle concurrent requests within actors
