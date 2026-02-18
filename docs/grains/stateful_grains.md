# Stateful Grains

Stateful grains are grains that maintain persistent state across activations and deactivations. This guide covers patterns, strategies, and best practices for managing state in GoAkt grains.

## Table of Contents

- 📖 [Overview](#overview)
- 🔄 [State Lifecycle](#state-lifecycle)
- 🛠️ [State Management Strategies](#state-management-strategies)
- 🔒 [Concurrency Control](#concurrency-control)
- ✅ [State Validation](#state-validation)
- ✅ [Best Practices](#best-practices)
- 🔧 [Troubleshooting](#troubleshooting)

---

## Overview

Stateful grains persist their state to external storage and restore it when reactivated. This enables:

- **Durability**: State survives grain passivation and node failures
- **Consistency**: Grains maintain a single source of truth
- **Scalability**: Grains can be deactivated to free memory without losing state
- **Recovery**: Failed operations can be retried or compensated

## State Lifecycle

```
┌────────────────┐
│  Storage       │
│  (Database,    │
│   Event Store, │
│   Cache, etc)  │
└────────┬───────┘
         │
         │ OnActivate: Load State
         ▼
┌────────────────┐
│  Grain Memory  │◄──┐
│  (Active)      │   │ OnReceive: Update State
└────────┬───────┘   │
         │           │
         └───────────┘
         │
         │ OnDeactivate: Persist State
         ▼
┌────────────────┐
│  Storage       │
└────────────────┘
```

## State Management Strategies

### 1. Direct State Persistence

The simplest pattern: load state on activation, save on deactivation.

```go
package main

import (
    "context"
    "fmt"

    "github.com/tochemey/goakt/v3/actor"
)

// UserGrain maintains user profile state
type UserGrain struct {
    // State
    user *UserProfile

    // Dependencies
    repository UserRepository
}

type UserProfile struct {
    ID        string
    Name      string
    Email     string
    CreatedAt time.Time
    UpdatedAt time.Time
    Version   int64 // For optimistic locking
}

func (g *UserGrain) OnActivate(ctx context.Context, props *actor.GrainProps) error {
    userID := props.Identity().Name()

    // Inject repository dependency
    for _, dep := range props.Dependencies() {
        if dep.ID() == "user-repository" {
            g.repository = dep.(UserRepository)
        }
    }

    // Load state from database
    user, err := g.repository.LoadUser(ctx, userID)
    if err != nil {
        return fmt.Errorf("failed to load user: %w", err)
    }

    g.user = user
    return nil
}

func (g *UserGrain) OnReceive(ctx *actor.GrainContext) {
    switch msg := ctx.Message().(type) {
    case *UpdateProfile:
        // Update in-memory state
        g.user.Name = msg.Name
        g.user.Email = msg.Email
        g.user.UpdatedAt = time.Now()
        g.user.Version++

        ctx.Response(&ProfileUpdated{
            UserID:  g.user.ID,
            Version: g.user.Version,
        })

    case *GetProfile:
        ctx.Response(&ProfileResponse{
            User: g.user,
        })

    default:
        ctx.Unhandled()
    }
}

func (g *UserGrain) OnDeactivate(ctx context.Context, props *actor.GrainProps) error {
    // Persist state to database
    return g.repository.SaveUser(ctx, g.user)
}
```

**Pros**:

- Simple to implement
- Works well for small state
- Easy to understand

**Cons**:

- State loss if grain crashes between updates
- No audit trail
- All state loaded/saved each time

### 2. Write-Through Cache Pattern

Update storage immediately on state changes for durability.

```go
type BankAccountGrain struct {
    accountID string
    balance   float64
    version   int64

    db    Database
    cache Cache
}

func (g *BankAccountGrain) OnActivate(ctx context.Context, props *actor.GrainProps) error {
    g.accountID = props.Identity().Name()

    // Inject dependencies
    for _, dep := range props.Dependencies() {
        switch dep.ID() {
        case "database":
            g.db = dep.(Database)
        case "cache":
            g.cache = dep.(Cache)
        }
    }

    // Try cache first (fast path)
    if cached, ok := g.cache.Get(g.accountID); ok {
        account := cached.(*BankAccount)
        g.balance = account.Balance
        g.version = account.Version
        return nil
    }

    // Load from database (slow path)
    account, err := g.db.LoadAccount(ctx, g.accountID)
    if err != nil {
        return err
    }

    g.balance = account.Balance
    g.version = account.Version

    // Populate cache
    g.cache.Set(g.accountID, account, 5*time.Minute)
    return nil
}

func (g *BankAccountGrain) OnReceive(ctx *actor.GrainContext) {
    switch msg := ctx.Message().(type) {
    case *Deposit:
        // Validate
        if msg.Amount <= 0 {
            ctx.Err(errors.New("invalid amount"))
            return
        }

        // Update in-memory state
        g.balance += msg.Amount
        g.version++

        // Persist immediately (write-through)
        account := &BankAccount{
            ID:      g.accountID,
            Balance: g.balance,
            Version: g.version,
        }

        if err := g.db.SaveAccount(ctx.Context(), account); err != nil {
            // Rollback in-memory state
            g.balance -= msg.Amount
            g.version--
            ctx.Err(fmt.Errorf("failed to persist: %w", err))
            return
        }

        // Update cache
        g.cache.Set(g.accountID, account, 5*time.Minute)

        ctx.Response(&DepositSuccess{
            AccountID:  g.accountID,
            NewBalance: g.balance,
            Version:    g.version,
        })

    case *Withdraw:
        if msg.Amount <= 0 {
            ctx.Err(errors.New("invalid amount"))
            return
        }

        if g.balance < msg.Amount {
            ctx.Err(errors.New("insufficient funds"))
            return
        }

        oldBalance := g.balance
        oldVersion := g.version

        g.balance -= msg.Amount
        g.version++

        account := &BankAccount{
            ID:      g.accountID,
            Balance: g.balance,
            Version: g.version,
        }

        if err := g.db.SaveAccount(ctx.Context(), account); err != nil {
            g.balance = oldBalance
            g.version = oldVersion
            ctx.Err(err)
            return
        }

        g.cache.Set(g.accountID, account, 5*time.Minute)
        ctx.Response(&WithdrawSuccess{NewBalance: g.balance})

    case *GetBalance:
        ctx.Response(&BalanceResponse{
            AccountID: g.accountID,
            Balance:   g.balance,
            Version:   g.version,
        })

    default:
        ctx.Unhandled()
    }
}

func (g *BankAccountGrain) OnDeactivate(ctx context.Context, props *actor.GrainProps) error {
    // State already persisted, just cleanup
    return nil
}
```

**Pros**:

- Immediate durability
- State safe even if grain crashes
- Cache improves read performance

**Cons**:

- Higher write latency
- More database load
- May impact throughput

### 3. Event Sourcing Pattern

Store state as a sequence of events instead of current state.

```go
type OrderGrain struct {
    orderID string

    // Current state (derived from events)
    items      []OrderItem
    status     OrderStatus
    totalPrice float64
    createdAt  time.Time

    // Event tracking
    version    int64
    events     []Event // Uncommitted events

    eventStore EventStore
}

func (g *OrderGrain) OnActivate(ctx context.Context, props *actor.GrainProps) error {
    g.orderID = props.Identity().Name()

    // Inject event store
    for _, dep := range props.Dependencies() {
        if dep.ID() == "event-store" {
            g.eventStore = dep.(EventStore)
        }
    }

    // Load all events for this order
    events, err := g.eventStore.LoadEvents(ctx, g.orderID)
    if err != nil {
        return err
    }

    // Replay events to rebuild state
    for _, event := range events {
        g.applyEvent(event)
    }

    g.events = make([]Event, 0) // Clear uncommitted events
    return nil
}

func (g *OrderGrain) OnReceive(ctx *actor.GrainContext) {
    switch msg := ctx.Message().(type) {
    case *CreateOrder:
        if g.status != OrderStatusEmpty {
            ctx.Err(errors.New("order already created"))
            return
        }

        // Create event
        event := &OrderCreatedEvent{
            OrderID:   g.orderID,
            CustomerID: msg.CustomerID,
            CreatedAt: time.Now(),
        }

        // Apply to in-memory state
        g.applyEvent(event)

        // Store in uncommitted events
        g.events = append(g.events, event)

        ctx.Response(&OrderCreated{OrderID: g.orderID})

    case *AddItem:
        if g.status != OrderStatusOpen {
            ctx.Err(errors.New("cannot add item to closed order"))
            return
        }

        event := &ItemAddedEvent{
            OrderID:  g.orderID,
            ItemID:   msg.ItemID,
            Quantity: msg.Quantity,
            Price:    msg.Price,
        }

        g.applyEvent(event)
        g.events = append(g.events, event)

        ctx.Response(&ItemAdded{
            OrderID:    g.orderID,
            ItemID:     msg.ItemID,
            TotalPrice: g.totalPrice,
        })

    case *RemoveItem:
        if g.status != OrderStatusOpen {
            ctx.Err(errors.New("cannot remove item from closed order"))
            return
        }

        // Find item
        found := false
        for _, item := range g.items {
            if item.ItemID == msg.ItemID {
                found = true
                break
            }
        }

        if !found {
            ctx.Err(errors.New("item not found"))
            return
        }

        event := &ItemRemovedEvent{
            OrderID: g.orderID,
            ItemID:  msg.ItemID,
        }

        g.applyEvent(event)
        g.events = append(g.events, event)

        ctx.Response(&ItemRemoved{OrderID: g.orderID})

    case *SubmitOrder:
        if g.status != OrderStatusOpen {
            ctx.Err(errors.New("order not open"))
            return
        }

        if len(g.items) == 0 {
            ctx.Err(errors.New("cannot submit empty order"))
            return
        }

        event := &OrderSubmittedEvent{
            OrderID:     g.orderID,
            SubmittedAt: time.Now(),
            TotalPrice:  g.totalPrice,
        }

        g.applyEvent(event)
        g.events = append(g.events, event)

        ctx.Response(&OrderSubmitted{
            OrderID:    g.orderID,
            TotalPrice: g.totalPrice,
        })

    case *GetOrder:
        ctx.Response(&OrderResponse{
            OrderID:    g.orderID,
            Items:      g.items,
            Status:     g.status,
            TotalPrice: g.totalPrice,
            Version:    g.version,
        })

    default:
        ctx.Unhandled()
    }
}

func (g *OrderGrain) OnDeactivate(ctx context.Context, props *actor.GrainProps) error {
    // Persist all uncommitted events
    if len(g.events) > 0 {
        err := g.eventStore.SaveEvents(ctx, g.orderID, g.events, g.version-int64(len(g.events)))
        if err != nil {
            return fmt.Errorf("failed to persist events: %w", err)
        }
    }
    return nil
}

// applyEvent applies an event to the grain's state
func (g *OrderGrain) applyEvent(event Event) {
    switch e := event.(type) {
    case *OrderCreatedEvent:
        g.status = OrderStatusOpen
        g.createdAt = e.CreatedAt
        g.items = make([]OrderItem, 0)
        g.totalPrice = 0
        g.version++

    case *ItemAddedEvent:
        g.items = append(g.items, OrderItem{
            ItemID:   e.ItemID,
            Quantity: e.Quantity,
            Price:    e.Price,
        })
        g.totalPrice += e.Price * float64(e.Quantity)
        g.version++

    case *ItemRemovedEvent:
        for i, item := range g.items {
            if item.ItemID == e.ItemID {
                g.totalPrice -= item.Price * float64(item.Quantity)
                g.items = append(g.items[:i], g.items[i+1:]...)
                break
            }
        }
        g.version++

    case *OrderSubmittedEvent:
        g.status = OrderStatusSubmitted
        g.version++
    }
}
```

**Pros**:

- Complete audit trail
- Can replay history
- Time travel debugging
- CQRS-friendly

**Cons**:

- More complex implementation
- Replay can be slow for many events
- Requires event store

### 4. Snapshot Pattern

Combine event sourcing with periodic snapshots for performance.

```go
type InventoryGrain struct {
    productID string

    // Current state
    stock     int
    reserved  int
    version   int64

    // Event tracking
    events          []Event
    lastSnapshotVer int64

    eventStore    EventStore
    snapshotStore SnapshotStore
}

func (g *InventoryGrain) OnActivate(ctx context.Context, props *actor.GrainProps) error {
    g.productID = props.Identity().Name()

    // Inject stores
    for _, dep := range props.Dependencies() {
        switch dep.ID() {
        case "event-store":
            g.eventStore = dep.(EventStore)
        case "snapshot-store":
            g.snapshotStore = dep.(SnapshotStore)
        }
    }

    // Load latest snapshot (fast)
    snapshot, err := g.snapshotStore.LoadSnapshot(ctx, g.productID)
    if err == nil {
        g.stock = snapshot.Stock
        g.reserved = snapshot.Reserved
        g.version = snapshot.Version
        g.lastSnapshotVer = snapshot.Version
    }

    // Load events since snapshot (incremental)
    events, err := g.eventStore.LoadEventsSince(ctx, g.productID, g.version)
    if err != nil {
        return err
    }

    // Replay only recent events
    for _, event := range events {
        g.applyEvent(event)
    }

    g.events = make([]Event, 0)
    return nil
}

func (g *InventoryGrain) OnReceive(ctx *actor.GrainContext) {
    switch msg := ctx.Message().(type) {
    case *AddStock:
        event := &StockAddedEvent{
            ProductID: g.productID,
            Quantity:  msg.Quantity,
            Timestamp: time.Now(),
        }

        g.applyEvent(event)
        g.events = append(g.events, event)

        ctx.Response(&StockUpdated{
            ProductID: g.productID,
            Stock:     g.stock,
            Version:   g.version,
        })

    case *ReserveStock:
        if g.stock-g.reserved < msg.Quantity {
            ctx.Err(errors.New("insufficient stock"))
            return
        }

        event := &StockReservedEvent{
            ProductID: g.productID,
            Quantity:  msg.Quantity,
            Timestamp: time.Now(),
        }

        g.applyEvent(event)
        g.events = append(g.events, event)

        ctx.Response(&StockReserved{
            ProductID: g.productID,
            Reserved:  g.reserved,
        })

    case *GetStock:
        ctx.Response(&StockResponse{
            ProductID: g.productID,
            Stock:     g.stock,
            Reserved:  g.reserved,
            Available: g.stock - g.reserved,
            Version:   g.version,
        })

    default:
        ctx.Unhandled()
    }
}

func (g *InventoryGrain) OnDeactivate(ctx context.Context, props *actor.GrainProps) error {
    // Persist uncommitted events
    if len(g.events) > 0 {
        err := g.eventStore.SaveEvents(ctx, g.productID, g.events, g.lastSnapshotVer)
        if err != nil {
            return err
        }
    }

    // Create snapshot if enough events accumulated (e.g., every 100 events)
    if g.version-g.lastSnapshotVer >= 100 {
        snapshot := &InventorySnapshot{
            ProductID: g.productID,
            Stock:     g.stock,
            Reserved:  g.reserved,
            Version:   g.version,
            Timestamp: time.Now(),
        }

        if err := g.snapshotStore.SaveSnapshot(ctx, g.productID, snapshot); err != nil {
            // Log but don't fail deactivation
            return nil
        }

        g.lastSnapshotVer = g.version
    }

    return nil
}

func (g *InventoryGrain) applyEvent(event Event) {
    switch e := event.(type) {
    case *StockAddedEvent:
        g.stock += e.Quantity
        g.version++

    case *StockReservedEvent:
        g.reserved += e.Quantity
        g.version++

    case *ReservationReleasedEvent:
        g.reserved -= e.Quantity
        g.version++
    }
}
```

**Pros**:

- Fast activation (load snapshot + recent events)
- Full audit trail preserved
- Balances performance and history

**Cons**:

- More storage space
- Snapshot management complexity
- Need to manage snapshot frequency

## Concurrency Control

### Optimistic Locking

Use version numbers to detect concurrent modifications:

```go
type DocumentGrain struct {
    docID    string
    content  string
    version  int64

    repository DocumentRepository
}

func (g *DocumentGrain) OnReceate(ctx *actor.GrainContext) {
    switch msg := ctx.Message().(type) {
    case *UpdateDocument:
        // Validate version (optimistic lock)
        if msg.ExpectedVersion != g.version {
            ctx.Err(&ConcurrencyError{
                Message:         "document was modified by another request",
                CurrentVersion:  g.version,
                ExpectedVersion: msg.ExpectedVersion,
            })
            return
        }

        // Update content
        g.content = msg.Content
        g.version++

        // Persist with version check
        err := g.repository.SaveDocument(ctx.Context(), &Document{
            ID:      g.docID,
            Content: g.content,
            Version: g.version,
        }, msg.ExpectedVersion)

        if err != nil {
            // Rollback
            g.content = msg.OldContent
            g.version--
            ctx.Err(err)
            return
        }

        ctx.Response(&DocumentUpdated{
            DocID:   g.docID,
            Version: g.version,
        })
    }
}
```

### Idempotency

Ensure operations can be safely retried:

```go
type PaymentGrain struct {
    paymentID        string
    status           PaymentStatus
    amount           float64
    processedOps     map[string]bool // Track processed operation IDs

    paymentGateway PaymentGateway
}

func (g *PaymentGrain) OnReceive(ctx *actor.GrainContext) {
    switch msg := ctx.Message().(type) {
    case *ProcessPayment:
        // Check idempotency key
        if g.processedOps[msg.IdempotencyKey] {
            // Already processed, return cached result
            ctx.Response(&PaymentProcessed{
                PaymentID: g.paymentID,
                Status:    g.status,
            })
            return
        }

        // Process payment
        result, err := g.paymentGateway.Charge(ctx.Context(), g.amount)
        if err != nil {
            ctx.Err(err)
            return
        }

        // Update state
        g.status = PaymentStatusCompleted
        g.processedOps[msg.IdempotencyKey] = true

        ctx.Response(&PaymentProcessed{
            PaymentID:     g.paymentID,
            Status:        g.status,
            TransactionID: result.TransactionID,
        })
    }
}
```

## State Validation

### Input Validation

```go
func (g *UserGrain) OnReceive(ctx *actor.GrainContext) {
    switch msg := ctx.Message().(type) {
    case *UpdateEmail:
        // Validate email format
        if !isValidEmail(msg.Email) {
            ctx.Err(errors.New("invalid email format"))
            return
        }

        // Check for duplicates
        exists, err := g.repository.EmailExists(ctx.Context(), msg.Email)
        if err != nil {
            ctx.Err(err)
            return
        }

        if exists {
            ctx.Err(errors.New("email already in use"))
            return
        }

        g.user.Email = msg.Email
        ctx.Response(&EmailUpdated{})
    }
}
```

### State Invariants

```go
type ShoppingCartGrain struct {
    cartID string
    items  []CartItem
    maxItems int // Business rule: max 50 items
}

func (g *ShoppingCartGrain) OnReceive(ctx *actor.GrainContext) {
    switch msg := ctx.Message().(type) {
    case *AddToCart:
        // Enforce invariant
        if len(g.items) >= g.maxItems {
            ctx.Err(errors.New("cart is full"))
            return
        }

        // Check quantity limits
        totalQuantity := 0
        for _, item := range g.items {
            totalQuantity += item.Quantity
        }

        if totalQuantity+msg.Quantity > 100 {
            ctx.Err(errors.New("quantity limit exceeded"))
            return
        }

        g.items = append(g.items, CartItem{
            ProductID: msg.ProductID,
            Quantity:  msg.Quantity,
        })

        ctx.Response(&ItemAdded{})
    }
}
```

## Best Practices

### 1. Choose the Right Strategy

- **Simple CRUD**: Direct state persistence
- **High-value transactions**: Write-through cache
- **Audit requirements**: Event sourcing
- **Large state**: Snapshot pattern
- **Frequent access**: Caching layers

### 2. Handle Activation Failures

```go
func (g *UserGrain) OnActivate(ctx context.Context, props *actor.GrainProps) error {
    user, err := g.repository.LoadUser(ctx, props.Identity().Name())
    if err != nil {
        if errors.Is(err, ErrNotFound) {
            // User doesn't exist - create default
            g.user = &User{
                ID:        props.Identity().Name(),
                CreatedAt: time.Now(),
            }
            return nil
        }
        // Other errors - fail activation for retry
        return fmt.Errorf("failed to load user: %w", err)
    }

    g.user = user
    return nil
}
```

### 3. Minimize State Size

```go
// Bad: Loading entire history
type BadGrain struct {
    allTransactions []Transaction // Could be millions!
}

// Good: Load only what's needed
type GoodGrain struct {
    balance         float64
    recentTxs       []Transaction // Last 100 only
}
```

### 4. Use Transactions

```go
func (g *TransferGrain) OnDeactivate(ctx context.Context, props *actor.GrainProps) error {
    // Use database transaction
    tx, err := g.db.BeginTx(ctx)
    if err != nil {
        return err
    }
    defer tx.Rollback()

    // Save multiple related entities atomically
    if err := tx.SaveTransfer(g.transfer); err != nil {
        return err
    }

    if err := tx.SaveAuditLog(g.auditLog); err != nil {
        return err
    }

    return tx.Commit()
}
```

### 5. Implement Soft Deletes

```go
type UserGrain struct {
    user *User
}

type User struct {
    ID        string
    Name      string
    DeletedAt *time.Time // nil = active
}

func (g *UserGrain) OnReceive(ctx *actor.GrainContext) {
    switch msg := ctx.Message().(type) {
    case *DeleteUser:
        // Soft delete
        now := time.Now()
        g.user.DeletedAt = &now

        ctx.Response(&UserDeleted{UserID: g.user.ID})

    case *GetUser:
        if g.user.DeletedAt != nil {
            ctx.Err(errors.New("user not found"))
            return
        }
        ctx.Response(&UserResponse{User: g.user})
    }
}
```

## Troubleshooting

### State Not Persisting

**Symptoms**: State resets after passivation

**Solutions**:

- Verify `OnDeactivate` is being called
- Check database write errors
- Ensure dependencies are injected
- Test persistence layer independently

### Slow Activation

**Symptoms**: Long activation times

**Solutions**:

- Use snapshot pattern for event-sourced grains
- Implement caching layer
- Optimize database queries (indexes, connection pooling)
- Consider pagination for large state

### Concurrent Modification

**Symptoms**: Lost updates, stale data

**Solutions**:

- Implement optimistic locking with version numbers
- Use database-level locking for critical sections
- Implement idempotency keys
- Add retry logic with exponential backoff

### Memory Leaks

**Symptoms**: Growing memory usage in grains

**Solutions**:

- Limit in-memory history (e.g., keep only recent events)
- Clear uncommitted events after persistence
- Implement snapshot compaction
- Set appropriate passivation timeouts