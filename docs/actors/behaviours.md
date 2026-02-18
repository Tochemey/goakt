# Behaviors

Behaviors enable actors to dynamically change their message processing logic at runtime. This is a powerful pattern for implementing state machines, workflows, and context-aware message handling.

## Table of Contents

- 🤔 [What are Behaviors?](#what-are-behaviors)
- 💡 [Why Use Behaviors?](#why-use-behaviors)
- 🛠️ [Behavior Operations](#behavior-operations)
- 🔀 [State Machine Pattern](#state-machine-pattern)
- 📚 [Stacked Behaviors](#stacked-behaviors)
- 🔄 [Behavior with State Transitions](#behavior-with-state-transitions)
- ✅ [Behavior Best Practices](#behavior-best-practices)

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
