# Reentrancy

Reentrancy allows actors to process new messages while waiting for async responses from other actors. This enables concurrent request handling without blocking the actor's mailbox.

## What is Reentrancy?

**Reentrancy** is the ability of an actor to:

- Send async requests to other actors
- Continue processing new messages while waiting for responses
- Handle responses via continuations (callbacks)
- Avoid blocking the mailbox

Without reentrancy, actors process messages **one at a time** sequentially. With reentrancy, actors can have **multiple pending requests** and continue processing while waiting for responses.

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

### AllowReentrant

Allow concurrent request handling. New messages processed while requests pending.

```go
pid, _ := actorSystem.Spawn(ctx, "actor", &MyActor{},
    actor.WithReentrancyEnabled(reentrancy.AllowReentrant))
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
    actor.WithReentrancyEnabled(reentrancy.StashNonReentrant))
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
    actor.WithReentrancyEnabled(reentrancy.AllowReentrant))
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

## Complete Example

```go
package main

import (
    "context"
    "time"

    "github.com/tochemey/goakt/v3/actor"
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
        actor.WithPassivationDisabled())
    actorSystem.Start(ctx)
    defer actorSystem.Stop(ctx)

    // Spawn inventory actor
    inventoryPID, _ := actorSystem.Spawn(ctx, "inventory", &InventoryActor{})

    // Spawn payment actor
    paymentPID, _ := actorSystem.Spawn(ctx, "payment", &PaymentActor{})

    // Spawn order actor with reentrancy
    orderPID, _ := actorSystem.Spawn(ctx, "orders", &OrderActor{},
        actor.WithReentrancyEnabled(reentrancy.AllowReentrant))

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
request := ctx.Request(targetPID, &Query{},
    actor.WithRequestTimeout(5*time.Second))

request.Then(func(response proto.Message, err error) {
    if err == actor.ErrRequestTimeout {
        ctx.Logger().Warn("Request timed out")
        return
    }
    // Handle response
})
```

### Per-Request Reentrancy Mode

Override reentrancy mode for specific requests:

```go
// Actor configured with AllowReentrant
// But this specific request should stash
request := ctx.Request(targetPID, &CriticalQuery{},
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
        request := ctx.Request(a.dbPID, &Query{})

        // Store request for cancellation
        a.pendingRequest = request

        request.Then(func(response proto.Message, err error) {
            if err == context.Canceled {
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

## Patterns

### Pattern 1: Parallel Requests

Send multiple requests in parallel:

```go
func (a *AggregatorActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *AggregateData:
        results := make(map[string]*Result)
        pending := 3

        // Request from service 1
        req1 := ctx.Request(a.service1PID, &Query{})
        req1.Then(func(response proto.Message, err error) {
            if err == nil {
                results["service1"] = response.(*Result)
            }
            pending--
            if pending == 0 {
                a.sendAggregatedResults(ctx, results)
            }
        })

        // Request from service 2
        req2 := ctx.Request(a.service2PID, &Query{})
        req2.Then(func(response proto.Message, err error) {
            if err == nil {
                results["service2"] = response.(*Result)
            }
            pending--
            if pending == 0 {
                a.sendAggregatedResults(ctx, results)
            }
        })

        // Request from service 3
        req3 := ctx.Request(a.service3PID, &Query{})
        req3.Then(func(response proto.Message, err error) {
            if err == nil {
                results["service3"] = response.(*Result)
            }
            pending--
            if pending == 0 {
                a.sendAggregatedResults(ctx, results)
            }
        })
    }
}
```

### Pattern 2: Sequential Chain

Chain requests sequentially:

```go
func (a *ChainActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *ProcessWorkflow:
        // Step 1
        req1 := ctx.Request(a.step1PID, &Step1{Data: msg.GetData()})
        req1.Then(func(response proto.Message, err error) {
            if err != nil {
                ctx.Logger().Error("Step 1 failed", "error", err)
                return
            }

            result1 := response.(*Step1Result)

            // Step 2
            req2 := ctx.Request(a.step2PID, &Step2{
                Data: result1.GetData(),
            })
            req2.Then(func(response proto.Message, err error) {
                if err != nil {
                    ctx.Logger().Error("Step 2 failed", "error", err)
                    return
                }

                result2 := response.(*Step2Result)

                // Step 3
                req3 := ctx.Request(a.step3PID, &Step3{
                    Data: result2.GetData(),
                })
                req3.Then(func(response proto.Message, err error) {
                    if err != nil {
                        ctx.Logger().Error("Step 3 failed", "error", err)
                        return
                    }

                    ctx.Logger().Info("Workflow completed")
                })
            })
        })
    }
}
```

### Pattern 3: Request with Retry

Implement retry logic with reentrancy:

```go
func (a *RetryActor) sendWithRetry(ctx *actor.ReceiveContext,
    targetPID *actor.PID, msg proto.Message, attempts int) {

    request := ctx.Request(targetPID, msg)

    request.Then(func(response proto.Message, err error) {
        if err != nil && attempts > 0 {
            ctx.Logger().Warn("Request failed, retrying",
                "attempts_left", attempts)

            // Retry after delay
            ctx.ScheduleOnce(time.Second, ctx.Self(), &RetryMessage{
                TargetPID: targetPID,
                Message:   msg,
                Attempts:  attempts - 1,
            })
            return
        }

        if err != nil {
            ctx.Logger().Error("Request failed after retries")
            return
        }

        // Success
        a.handleResponse(response)
    })
}
```

## AllowReentrant vs. StashNonReentrant

### AllowReentrant

```go
// Multiple concurrent requests
pid, _ := actorSystem.Spawn(ctx, "actor", &MyActor{},
    actor.WithReentrancyEnabled(reentrancy.AllowReentrant))

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
    actor.WithReentrancyEnabled(reentrancy.StashNonReentrant))

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
// Good: Error handling and timeout
request := ctx.Request(targetPID, &Query{},
    actor.WithRequestTimeout(5*time.Second))

request.Then(func(response proto.Message, err error) {
    if err != nil {
        if err == actor.ErrRequestTimeout {
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
request := ctx.Request(targetPID, &Query{})
request.Then(func(response proto.Message, err error) {
    // ❌ No error check
    result := response.(*Result)
    a.process(result)
})
```

## Testing

```go
func TestReentrantActor(t *testing.T) {
    ctx := context.Background()
    system, _ := actor.NewActorSystem("test",
        actor.WithPassivationDisabled())
    system.Start(ctx)
    defer system.Stop(ctx)

    // Spawn with reentrancy
    pid, _ := system.Spawn(ctx, "test", &TestActor{},
        actor.WithReentrancyEnabled(reentrancy.AllowReentrant))

    // Send multiple requests
    for i := 0; i < 5; i++ {
        actor.Tell(ctx, pid, &ProcessRequest{Id: i})
    }

    // Wait for processing
    time.Sleep(time.Second)

    // Verify all requests processed
    response, _ := actor.Ask(ctx, pid, &GetProcessedCount{}, time.Second)
    count := response.(*ProcessedCount)
    assert.Equal(t, 5, count.Value)
}
```

## Summary

- **Reentrancy** enables async request handling
- **AllowReentrant**: Concurrent requests
- **StashNonReentrant**: Sequential requests
- **Request/RequestName**: Send async requests
- **Then**: Register continuations
- **Cancel**: Cancel pending requests
- **WithRequestTimeout**: Set timeout per request
- **WithReentrancyMode**: Override mode per request

## Next Steps

- **[PipeTo Pattern](pipeto.md)**: Execute background work
- **[Messaging](messaging.md)**: Synchronous vs. asynchronous
- **[Behaviors](behaviours.md)**: State machines with reentrancy
- **[Stashing](stashing.md)**: Message deferral patterns
