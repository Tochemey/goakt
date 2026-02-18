# Behaviors

Behaviors enable actors to dynamically change their message processing logic at runtime. This is a powerful pattern for implementing state machines, workflows, and context-aware message handling.

## Table of Contents

- 🤔 [What are Behaviors?](#what-are-behaviors)
- 💡 [Why Use Behaviors?](#why-use-behaviors)
- 🛠️ [Behavior Operations](#behavior-operations)
- 🔐 [Basic Example: Authentication](#basic-example-authentication)
- 🔀 [State Machine Pattern](#state-machine-pattern)
- 📚 [Stacked Behaviors](#stacked-behaviors)
- 🔄 [Behavior with State Transitions](#behavior-with-state-transitions)
- ✅ [Behavior Best Practices](#behavior-best-practices)
- 🧩 [Common Patterns](#common-patterns)

---

## What are Behaviors?

A **behavior** is a function that defines how an actor processes messages:

```go
type Behavior func(ctx *ReceiveContext)
```

Instead of having a single fixed `Receive` method, actors can switch between different behaviors dynamically based on their current state or context.

## Why Use Behaviors?

Behaviors are useful for:

- **State machines**: Different states with different message handling
- **Workflows**: Multi-step processes with different stages
- **Protocol transitions**: Switching between communication protocols
- **Context-aware handling**: Processing messages differently based on context
- **Dynamic logic**: Runtime behavior changes without actor restart

## Behavior Operations

GoAkt provides four operations for managing behaviors:

### 1. Become

Replace the current behavior with a new one.

```go
ctx.Become(newBehavior)
```

- **Replaces** current behavior completely
- Previous behavior is **discarded**
- Use for **permanent state transitions**

### 2. BecomeStacked

Push a new behavior onto a stack, keeping the previous behavior.

```go
ctx.BecomeStacked(newBehavior)
```

- **Pushes** new behavior onto stack
- Previous behavior is **preserved**
- Use for **temporary state changes**
- Can return to previous behavior with `UnBecomeStacked`

### 3. UnBecome

Reset to the original actor's `Receive` method.

```go
ctx.UnBecome()
```

- **Discards** all behaviors
- Returns to actor's default `Receive` method
- Use to **reset** to initial state

### 4. UnBecomeStacked

Pop the current behavior from the stack, returning to the previous behavior.

```go
ctx.UnBecomeStacked()
```

- **Pops** top behavior from stack
- Returns to **previous** behavior
- Use to **exit** temporary state
- Does nothing if stack is empty

## Basic Example: Authentication (concept)

Use two behaviors: initial `Receive` handles **Login** (on success call **ctx.Become(authenticatedBehavior)** and respond; on failure respond with error). The **authenticatedBehavior** function handles **GetProfile**, **UpdateProfile**, and **Logout**; on **Logout** call **ctx.UnBecome()** to return to the default Receive. Store auth state (e.g. username) in the actor struct.

## State Machine Pattern

Behaviors are perfect for implementing finite state machines:

```go
type OrderActor struct {
    orderId    string
    totalAmount float64
    items      []string
}

func (a *OrderActor) PreStart(ctx *actor.Context) error {
    return nil
}

// State: Created
func (a *OrderActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *AddItem:
        a.items = append(a.items, msg.GetItem())
        a.totalAmount += msg.GetPrice()
        ctx.Response(&ItemAdded{})

    case *Checkout:
        if len(a.items) == 0 {
            ctx.Response(&CheckoutFailed{Reason: "No items"})
            return
        }

        // Transition to PendingPayment state
        ctx.Become(a.pendingPaymentBehavior)
        ctx.Response(&CheckoutStarted{Amount: a.totalAmount})

    case *Cancel:
        // Transition to Cancelled state
        ctx.Become(a.cancelledBehavior)
        ctx.Response(&OrderCancelled{})
    }
}

// State: PendingPayment
func (a *OrderActor) pendingPaymentBehavior(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *PaymentReceived:
        if msg.GetAmount() >= a.totalAmount {
            // Transition to Paid state
            ctx.Become(a.paidBehavior)
            ctx.Response(&PaymentAccepted{})
        } else {
            ctx.Response(&PaymentRejected{
                Reason: "Insufficient amount",
            })
        }

    case *Cancel:
        // Transition to Cancelled state
        ctx.Become(a.cancelledBehavior)
        ctx.Response(&OrderCancelled{})

    case *PaymentTimeout:
        // Transition to Cancelled state
        ctx.Become(a.cancelledBehavior)
        ctx.Response(&OrderCancelled{Reason: "Payment timeout"})
    }
}

// State: Paid
func (a *OrderActor) paidBehavior(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *Ship:
        // Transition to Shipped state
        ctx.Become(a.shippedBehavior)
        ctx.Response(&OrderShipped{})

    case *Refund:
        // Process refund and return to Created state
        ctx.UnBecome()
        ctx.Response(&RefundProcessed{})
    }
}

// State: Shipped
func (a *OrderActor) shippedBehavior(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *Deliver:
        // Transition to Delivered (terminal state)
        ctx.Become(a.deliveredBehavior)
        ctx.Response(&OrderDelivered{})

    case *GetStatus:
        ctx.Response(&OrderStatus{State: "Shipped"})
    }
}

// State: Delivered (terminal)
func (a *OrderActor) deliveredBehavior(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *GetStatus:
        ctx.Response(&OrderStatus{State: "Delivered"})

    default:
        ctx.Response(&Error{
            Message: "Order already delivered",
        })
    }
}

// State: Cancelled (terminal)
func (a *OrderActor) cancelledBehavior(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *GetStatus:
        ctx.Response(&OrderStatus{State: "Cancelled"})

    default:
        ctx.Response(&Error{
            Message: "Order is cancelled",
        })
    }
}

func (a *OrderActor) PostStop(ctx *actor.Context) error {
    return nil
}
```

## Stacked Behaviors

Use `BecomeStacked` for temporary behavior changes that you'll revert:

```go
type WorkerActor struct {
    normalPriority  int
    emergencyMode   bool
}

func (a *WorkerActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *EmergencyAlert:
        // Enter emergency mode temporarily
        ctx.BecomeStacked(a.emergencyBehavior)
        ctx.Response(&EmergencyModeActivated{})

    case *WorkItem:
        a.processNormalWork(msg)
        ctx.Response(&WorkComplete{})
    }
}

func (a *WorkerActor) emergencyBehavior(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *UrgentTask:
        a.processUrgentWork(msg)
        ctx.Response(&UrgentWorkComplete{})

    case *EmergencyResolved:
        // Return to normal behavior
        ctx.UnBecomeStacked()
        ctx.Response(&EmergencyModeDeactivated{})

    case *WorkItem:
        // Defer normal work in emergency mode
        ctx.Stash()

    default:
        ctx.Unhandled()
    }
}
```

## Behavior with State Transitions

Track state explicitly for better debugging:

```go
type ConnectionActor struct {
    state      string
    connection *Connection
}

func (a *ConnectionActor) Receive(ctx *actor.ReceiveContext) {
    a.state = "Disconnected"

    switch msg := ctx.Message().(type) {
    case *Connect:
        conn, err := dial(msg.GetAddress())
        if err != nil {
            ctx.Response(&ConnectFailed{Reason: err.Error()})
            return
        }

        a.connection = conn
        ctx.Become(a.connectedBehavior)
        ctx.Response(&Connected{})
    }
}

func (a *ConnectionActor) connectedBehavior(ctx *actor.ReceiveContext) {
    a.state = "Connected"

    switch msg := ctx.Message().(type) {
    case *SendData:
        if err := a.connection.Send(msg.GetData()); err != nil {
            // Connection error, return to disconnected
            ctx.UnBecome()
            ctx.Response(&SendFailed{Reason: err.Error()})
            return
        }
        ctx.Response(&DataSent{})

    case *Disconnect:
        a.connection.Close()
        ctx.UnBecome()
        ctx.Response(&Disconnected{})

    case *GetStatus:
        ctx.Response(&Status{State: a.state})
    }
}
```

## Behavior Best Practices

### Do's ✅

1. **Keep behaviors focused**: Each behavior should handle a specific state
2. **Use descriptive names**: `authenticatedBehavior`, not `behavior1`
3. **Handle state cleanup**: Clean up resources when transitioning
4. **Log state changes**: Log for debugging and monitoring
5. **Document transitions**: Comment which states can transition where

```go
func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *Start:
        ctx.Logger().Info("Transitioning to active state")
        ctx.Become(a.activeBehavior)
    }
}
```

### Don'ts ❌

1. **Don't nest behaviors deeply**: Keep behavior stack shallow
2. **Don't lose state**: Save important state before transitioning
3. **Don't forget terminal states**: Handle end-of-lifecycle states
4. **Don't create behavior loops**: Ensure state machines terminate
5. **Don't share behaviors**: Each actor instance should have its own behaviors

## Common Patterns

### 1. Protocol Negotiation

```go
type ProtocolActor struct {
    version int
}

func (a *ProtocolActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *NegotiateProtocol:
        switch msg.GetVersion() {
        case 1:
            ctx.Become(a.protocolV1Behavior)
        case 2:
            ctx.Become(a.protocolV2Behavior)
        default:
            ctx.Response(&Error{Message: "Unsupported protocol"})
        }
    }
}

func (a *ProtocolActor) protocolV1Behavior(ctx *actor.ReceiveContext) {
    // Handle v1 protocol messages
}

func (a *ProtocolActor) protocolV2Behavior(ctx *actor.ReceiveContext) {
    // Handle v2 protocol messages
}
```

### 2. Retry Logic with State

```go
type RetryActor struct {
    attempts int
    maxRetries int
}

func (a *RetryActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *ExecuteTask:
        err := a.executeTask(msg)
        if err != nil {
            a.attempts = 1
            ctx.BecomeStacked(a.retryBehavior)
            // Schedule retry
            _ = ctx.ActorSystem().ScheduleOnce(ctx.Context(), &RetryTask{}, ctx.Self(), time.Second)
        } else {
            ctx.Response(&TaskComplete{})
        }
    }
}

func (a *RetryActor) retryBehavior(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *RetryTask:
        if a.attempts >= a.maxRetries {
            ctx.UnBecomeStacked()
            ctx.Response(&TaskFailed{Reason: "Max retries exceeded"})
            return
        }

        err := a.executeTask(msg)
        if err != nil {
            a.attempts++
            // Schedule next retry
            backoff := time.Duration(a.attempts) * time.Second
            _ = ctx.ActorSystem().ScheduleOnce(ctx.Context(), &RetryTask{}, ctx.Self(), backoff)
        } else {
            a.attempts = 0
            ctx.UnBecomeStacked()
            ctx.Response(&TaskComplete{})
        }
    }
}
```

### 3. Throttling with Behavior

```go
type ThrottledActor struct {
    requestCount int
    limit        int
}

func (a *ThrottledActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *Request:
        a.requestCount++

        if a.requestCount >= a.limit {
            // Switch to throttled behavior
            ctx.Become(a.throttledBehavior)
            // Schedule reset
            _ = ctx.ActorSystem().ScheduleOnce(ctx.Context(), &ResetThrottle{}, ctx.Self(), time.Minute)
        }

        a.handleRequest(msg)
        ctx.Response(&RequestHandled{})
    }
}

func (a *ThrottledActor) throttledBehavior(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *Request:
        ctx.Response(&ThrottledError{
            Message: "Rate limit exceeded",
        })

    case *ResetThrottle:
        a.requestCount = 0
        ctx.UnBecome()
    }
}
```

### 4. Multi-Step Workflow

```go
type WorkflowActor struct {
    data map[string]interface{}
}

func (a *WorkflowActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *StartWorkflow:
        a.data = make(map[string]interface{})
        a.data["input"] = msg.GetInput()

        // Step 1: Validation
        ctx.Become(a.validationStep)
        ctx.Tell(ctx.Self(), &ValidateInput{})
    }
}

func (a *WorkflowActor) validationStep(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *ValidateInput:
        if a.validate() {
            // Step 2: Processing
            ctx.Become(a.processingStep)
            ctx.Tell(ctx.Self(), &ProcessData{})
        } else {
            ctx.Response(&WorkflowFailed{Step: "validation"})
            ctx.UnBecome()
        }
    }
}

func (a *WorkflowActor) processingStep(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *ProcessData:
        result := a.process()
        a.data["result"] = result

        // Step 3: Finalization
        ctx.Become(a.finalizationStep)
        ctx.Tell(ctx.Self(), &Finalize{})
    }
}

func (a *WorkflowActor) finalizationStep(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *Finalize:
        a.finalize()
        ctx.Response(&WorkflowComplete{
            Result: a.data["result"],
        })
        ctx.UnBecome()
    }
}
```

### Logging State Transitions

```go
func (a *MyActor) transitionTo(ctx *actor.ReceiveContext,
    newState string, behavior actor.Behavior) {
    ctx.Logger().Info("State transition",
        "from", a.currentState,
        "to", newState)

    a.currentState = newState
    ctx.Become(behavior)
}
```