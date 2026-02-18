# Reentrancy

Reentrancy allows actors to process new messages while waiting for async responses from other actors. This enables concurrent request handling without blocking the actor's mailbox.

## Table of Contents

- 🤔 [What is Reentrancy?](#what-is-reentrancy)
- 💡 [Why Use Reentrancy?](#why-use-reentrancy)
- 🔀 [Reentrancy Modes](#reentrancy-modes)
- 🚀 [Basic Usage](#basic-usage)
- 💡 [Example](#example)
- ⚙️ [Request Options](#request-options)
- ❌ [Request Cancellation](#request-cancellation)
- 🔄 [AllowAll vs. StashNonReentrant](#allowall-vs-stashnonreentrant)
- ✅ [Best Practices](#best-practices)
- 📋 [Summary](#summary)

---

## What is Reentrancy?

**Reentrancy** is the ability of an actor to process new messages while it is waiting for a response from another actor. In the default, non-reentrant model, an actor handles one message at a time: if it sends a request and waits for a reply, its mailbox is effectively blocked until that reply arrives. With reentrancy enabled, the actor can keep processing other messages and handle the reply later via a **continuation** (callback), so the mailbox stays responsive.

### The default: one message at a time

In the actor model, each actor has a mailbox and processes messages **sequentially**. That gives you a simple mental model and avoids races on the actor’s state. As long as the handler runs to completion without waiting on external replies, this works well. The problem appears when an actor needs to **ask another actor** for something: if it blocks until the response comes back, it cannot process any other message in the meantime. The actor (and its mailbox) is stuck until the response arrives or times out.

### What reentrancy changes

When reentrancy is **enabled**, the actor can issue an **async request** to another actor and register a **continuation**. The runtime does not block the actor’s message loop: as soon as the request is sent, the actor can move on to the next message. When the response (or error) arrives, the runtime invokes the continuation with that result. So the actor can have **multiple requests in flight** and still process new messages; responses are handled by their continuations, not by blocking in the middle of a handler.

### In other words

With reentrancy, an actor can:

- **Send async requests** to other actors (Request or RequestName)
- **Continue processing** new messages while waiting for responses
- **Handle responses** via continuations (callbacks) when they arrive
- **Avoid blocking** the mailbox on request–response round-trips

Without reentrancy, the actor processes messages strictly one at a time; if it waits for a reply, nothing else is processed until that reply (or timeout). With reentrancy, multiple pending requests can be outstanding and the actor remains responsive to new work.

### Trade-off

Reentrancy trades **strict single-threaded-by-message** safety for **better concurrency and responsiveness**. While a continuation is running, the actor’s state may have changed because other messages were processed in between. You must design your continuations with that in mind (e.g. avoid assuming state that might be stale, or use request correlation ids to match responses to the right logical operation). When used carefully, reentrancy lets you build responsive actors that coordinate with others without blocking the mailbox.

## Why Use Reentrancy?

Traditional actor model (no reentrancy):

```
Process Msg1 → Send request → BLOCK → Wait → Response → Process Msg2
                                ↑
                         Mailbox blocked!
```

With reentrancy:

```
Process Msg1 → Send async request → Process Msg2 → Process Msg3
                                          ↓
                                    Response arrives
                                          ↓
                                  Continuation executes
```

**Benefits:**

- **Higher throughput**: Don't block on async operations
- **Better resource utilization**: Process messages while waiting
- **Responsive actors**: Handle new messages immediately
- **Concurrent workflows**: Multiple requests in flight

## Reentrancy Modes

GoAkt provides three reentrancy modes:

### Off (Default)

No reentrancy support. Async requests not allowed.

```go
// Default behavior
pid, _ := actorSystem.Spawn(ctx, "actor", &MyActor{})
// Reentrancy is OFF by default
```

### AllowAll

Allow concurrent request handling. New messages processed while requests pending.

```go
pid, _ := actorSystem.Spawn(ctx, "actor", &MyActor{},
    actor.WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))
```

**Behavior:**

- Multiple async requests can be in flight
- New messages processed immediately
- Continuations run when responses arrive
- **No ordering guarantees** for completion

### StashNonReentrant

Process requests sequentially. Stash new messages until current request completes.

```go
pid, _ := actorSystem.Spawn(ctx, "actor", &MyActor{},
    actor.WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.StashNonReentrant))))
```

**Behavior:**

- One request at a time
- New messages stashed until request completes
- Continuations run when response arrives
- **Sequential processing** maintained

## Basic Usage

### Enable Reentrancy

```go
pid, err := actorSystem.Spawn(ctx, "my-actor", &MyActor{},
    actor.WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))
```

### Send Async Request

```go
func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *ProcessOrder:
        // Send async request
        requestCall := ctx.Request(a.inventoryPID, &CheckStock{
            ProductId: msg.GetProductId(),
        })

        // Register continuation
        requestCall.Then(func(response proto.Message, err error) {
            if err != nil {
                ctx.Logger().Error("Stock check failed", "error", err)
                return
            }

            stock := response.(*StockLevel)
            if stock.GetQuantity() > 0 {
                // Process order
                a.createOrder(msg)
            }
        })

        // Continue processing other messages
        // Don't block!
    }
}
```

## Example

```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/passivation"
    "github.com/tochemey/goakt/v3/reentrancy"
)

type OrderActor struct {
    inventoryPID *actor.PID
    paymentPID   *actor.PID
    pendingOrders map[string]*Order
}

func (a *OrderActor) PreStart(ctx *actor.Context) error {
    var err error
    a.inventoryPID, err = ctx.ActorOf("inventory")
    if err != nil {
        return err
    }

    a.paymentPID, err = ctx.ActorOf("payment")
    if err != nil {
        return err
    }

    a.pendingOrders = make(map[string]*Order)
    return nil
}

func (a *OrderActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *CreateOrder:
        a.handleCreateOrder(ctx, msg)

    case *GetOrderStatus:
        // Can handle queries while orders are processing!
        order, exists := a.pendingOrders[msg.GetOrderId()]
        if !exists {
            ctx.Response(&OrderNotFound{})
            return
        }
        ctx.Response(&OrderStatus{
            OrderId: msg.GetOrderId(),
            Status:  order.Status,
        })
    }
}

func (a *OrderActor) handleCreateOrder(ctx *actor.ReceiveContext, msg *CreateOrder) {
    orderId := msg.GetOrderId()

    // Create pending order
    a.pendingOrders[orderId] = &Order{
        Id:     orderId,
        Status: "checking_stock",
    }

    // Step 1: Check inventory (async)
    stockRequest := ctx.Request(a.inventoryPID, &CheckStock{
        ProductId: msg.GetProductId(),
        Quantity:  msg.GetQuantity(),
    }, actor.WithRequestTimeout(5*time.Second))

    stockRequest.Then(func(response proto.Message, err error) {
        if err != nil {
            ctx.Logger().Error("Stock check failed", "error", err)
            a.pendingOrders[orderId].Status = "failed"
            return
        }

        stock := response.(*StockLevel)
        if stock.GetQuantity() < msg.GetQuantity() {
            a.pendingOrders[orderId].Status = "insufficient_stock"
            return
        }

        // Step 2: Reserve stock
        a.pendingOrders[orderId].Status = "reserving_stock"

        reserveRequest := ctx.Request(a.inventoryPID, &ReserveStock{
            ProductId: msg.GetProductId(),
            Quantity:  msg.GetQuantity(),
            OrderId:   orderId,
        })

        reserveRequest.Then(func(response proto.Message, err error) {
            if err != nil {
                ctx.Logger().Error("Stock reservation failed", "error", err)
                a.pendingOrders[orderId].Status = "failed"
                return
            }

            // Step 3: Process payment (async)
            a.pendingOrders[orderId].Status = "processing_payment"

            paymentRequest := ctx.Request(a.paymentPID, &ProcessPayment{
                OrderId: orderId,
                Amount:  msg.GetAmount(),
            })

            paymentRequest.Then(func(response proto.Message, err error) {
                if err != nil {
                    ctx.Logger().Error("Payment failed", "error", err)
                    a.pendingOrders[orderId].Status = "payment_failed"

                    // Release stock
                    ctx.Tell(a.inventoryPID, &ReleaseStock{
                        ProductId: msg.GetProductId(),
                        Quantity:  msg.GetQuantity(),
                    })
                    return
                }

                // Order complete!
                a.pendingOrders[orderId].Status = "completed"
                ctx.Logger().Info("Order completed", "order_id", orderId)
            })
        })
    })
}

func (a *OrderActor) PostStop(ctx *actor.Context) error {
    return nil
}

func main() {
    ctx := context.Background()
    actorSystem, _ := actor.NewActorSystem("OrderSystem",
        actor.WithPassivationStrategy(passivation.NewLongLivedStrategy()))
    actorSystem.Start(ctx)
    defer actorSystem.Stop(ctx)

    // Spawn inventory actor
    inventoryPID, _ := actorSystem.Spawn(ctx, "inventory", &InventoryActor{})

    // Spawn payment actor
    paymentPID, _ := actorSystem.Spawn(ctx, "payment", &PaymentActor{})

    // Spawn order actor with reentrancy
    orderPID, _ := actorSystem.Spawn(ctx, "orders", &OrderActor{},
        actor.WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))

    // Create multiple orders concurrently
    actor.Tell(ctx, orderPID, &CreateOrder{
        OrderId:   "order1",
        ProductId: "product1",
        Quantity:  5,
        Amount:    100.00,
    })

    actor.Tell(ctx, orderPID, &CreateOrder{
        OrderId:   "order2",
        ProductId: "product2",
        Quantity:  3,
        Amount:    75.00,
    })

    // While orders are processing, query status
    // This works because of reentrancy!
    time.Sleep(100 * time.Millisecond)
    response, _ := actor.Ask(ctx, orderPID,
        &GetOrderStatus{OrderId: "order1"},
        time.Second)
    status := response.(*OrderStatus)
    fmt.Printf("Order status: %s\n", status.Status)
}
```

## Request Options

### Request Timeout

Set timeout for individual requests:

```go
import "github.com/tochemey/goakt/v3/errors"

request := ctx.Request(pid, &Query{},
    actor.WithRequestTimeout(5*time.Second))

request.Then(func(response proto.Message, err error) {
    if errors.Is(err, errors.ErrRequestTimeout) {
        ctx.Logger().Warn("Request timed out")
        return
    }
    // Handle response
})
```

### Per-Request Reentrancy Mode

Override reentrancy mode for specific requests:

```go
// Actor configured with AllowAll
// But this specific request should stash
request := ctx.Request(pid, &CriticalQuery{},
    actor.WithReentrancyMode(reentrancy.StashNonReentrant))

request.Then(func(response proto.Message, err error) {
    // Process response
})
```

## Request Cancellation

Cancel pending requests:

```go
func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *StartQuery:
        // send a query to the db actor using its pid
        request := ctx.Request(a.pid, &Query{})

        // Store request for cancellation
        a.pendingRequest = request

        request.Then(func(response proto.Message, err error) {
            if errors.Is(err, context.Canceled) {
                ctx.Logger().Info("Request was cancelled")
                return
            }
            // Handle response
        })

    case *CancelQuery:
        if a.pendingRequest != nil {
            a.pendingRequest.Cancel()
            a.pendingRequest = nil
        }
    }
}
```

## AllowAll vs. StashNonReentrant

### AllowAll

```go
// Multiple concurrent requests
pid, _ := actorSystem.Spawn(ctx, "actor", &MyActor{},
    actor.WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))

// Send 3 requests - all processed concurrently
// Responses may arrive in any order
```

**Use when:**

- Order of completion doesn't matter
- Higher throughput needed
- Independent requests
- Stateless operations

### StashNonReentrant

```go
// Sequential request processing
pid, _ := actorSystem.Spawn(ctx, "actor", &MyActor{},
    actor.WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.StashNonReentrant))))

// Send 3 requests - processed one at a time
// Requests 2 and 3 stashed until request 1 completes
```

**Use when:**

- Order matters
- Stateful operations
- Need sequential consistency
- Avoid race conditions

## Best Practices

### Do's ✅

1. **Handle errors in continuations**
2. **Set request timeouts**
3. **Cancel requests when no longer needed**
4. **Use appropriate reentrancy mode**
5. **Keep continuations short**

```go
import "github.com/tochemey/goakt/v3/errors"

// Good: Error handling and timeout
request := ctx.Request(pid, &Query{},
    actor.WithRequestTimeout(5*time.Second))

request.Then(func(response proto.Message, err error) {
    if err != nil {
        if errors.Is(err, errors.ErrRequestTimeout) {
            ctx.Logger().Warn("Timeout")
        } else {
            ctx.Logger().Error("Request failed", "error", err)
        }
        return
    }
    a.handleResponse(response)
})
```

### Don'ts ❌

1. **Don't block in continuations**
2. **Don't forget error handling**
3. **Don't use without timeouts**
4. **Don't access actor state unsafely**
5. **Don't leak request handles**

```go
// Bad: No error handling, no timeout
request := ctx.Request(pid, &Query{})
request.Then(func(response proto.Message, err error) {
    // ❌ No error check
    result := response.(*Result)
    a.process(result)
})
```

## Summary

- **Reentrancy** enables async request handling
- **AllowAll**: Concurrent requests
- **StashNonReentrant**: Sequential requests
- **Request/RequestName**: Send async requests
- **Then**: Register continuations
- **Cancel**: Cancel pending requests
- **WithRequestTimeout**: Set timeout per request
- **WithReentrancyMode**: Override mode per request
