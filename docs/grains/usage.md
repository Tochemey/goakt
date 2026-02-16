# Grain Usage Patterns

This guide demonstrates practical patterns for using grains in real-world applications.

## Basic Patterns

### Simple Entity Grain

Managing a single entity with CRUD operations:

```go
package main

import (
    "context"
    "time"

    "github.com/tochemey/goakt/v3/actor"
)

type Product struct {
    ID          string
    Name        string
    Description string
    Price       float64
    Stock       int
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

type ProductGrain struct {
    product *Product
    db      ProductRepository
}

func (g *ProductGrain) OnActivate(ctx context.Context, props *actor.GrainProps) error {
    productID := props.Identity().Name()

    // Inject database dependency
    for _, dep := range props.Dependencies() {
        if dep.ID() == "product-repo" {
            g.db = dep.(ProductRepository)
        }
    }

    // Load product
    product, err := g.db.GetProduct(ctx, productID)
    if err != nil {
        return err
    }

    g.product = product
    return nil
}

func (g *ProductGrain) OnReceive(ctx *actor.GrainContext) {
    switch msg := ctx.Message().(type) {
    case *GetProduct:
        ctx.Response(&ProductResponse{Product: g.product})

    case *UpdatePrice:
        g.product.Price = msg.NewPrice
        g.product.UpdatedAt = time.Now()
        ctx.Response(&Success{})

    case *UpdateStock:
        if g.product.Stock + msg.Delta < 0 {
            ctx.Err(errors.New("insufficient stock"))
            return
        }
        g.product.Stock += msg.Delta
        g.product.UpdatedAt = time.Now()
        ctx.Response(&StockUpdated{NewStock: g.product.Stock})

    case *UpdateDetails:
        g.product.Name = msg.Name
        g.product.Description = msg.Description
        g.product.UpdatedAt = time.Now()
        ctx.Response(&Success{})

    default:
        ctx.Unhandled()
    }
}

func (g *ProductGrain) OnDeactivate(ctx context.Context, props *actor.GrainProps) error {
    return g.db.SaveProduct(ctx, g.product)
}
```

**Usage**:

```go
// Register grain
system.RegisterGrainKind(ctx, &ProductGrain{})

// Access product
identity := actor.NewGrainIdentity(&ProductGrain{}, "product-123")

// Update price
err := system.TellGrain(ctx, identity, &UpdatePrice{NewPrice: 29.99})

// Get product
response, err := system.AskGrain(ctx, identity, &GetProduct{}, time.Second)
product := response.(*ProductResponse).Product
```

### Counter Grain

Simple stateful grain for counting:

```go
type CounterGrain struct {
    count int64
}

func (g *CounterGrain) OnActivate(ctx context.Context, props *actor.GrainProps) error {
    g.count = 0 // Start from zero or load from persistence
    return nil
}

func (g *CounterGrain) OnReceive(ctx *actor.GrainContext) {
    switch msg := ctx.Message().(type) {
    case *Increment:
        g.count += msg.Delta
        ctx.Response(&CountResponse{Count: g.count})

    case *Decrement:
        g.count -= msg.Delta
        ctx.Response(&CountResponse{Count: g.count})

    case *GetCount:
        ctx.Response(&CountResponse{Count: g.count})

    case *Reset:
        g.count = 0
        ctx.Response(&CountResponse{Count: g.count})

    default:
        ctx.Unhandled()
    }
}

func (g *CounterGrain) OnDeactivate(ctx context.Context, props *actor.GrainProps) error {
    // Optionally persist count
    return nil
}
```

## Advanced Patterns

### Aggregate Grain with Event Sourcing

```go
type OrderGrain struct {
    orderID    string
    items      []OrderItem
    status     OrderStatus
    totalPrice float64
    version    int64
    events     []Event
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

    // Load and replay events
    events, err := g.eventStore.LoadEvents(ctx, g.orderID)
    if err != nil {
        return err
    }

    for _, event := range events {
        g.applyEvent(event)
    }

    return nil
}

func (g *OrderGrain) OnReceive(ctx *actor.GrainContext) {
    switch msg := ctx.Message().(type) {
    case *AddItem:
        if g.status != OrderStatusOpen {
            ctx.Err(errors.New("order is not open"))
            return
        }

        event := &ItemAddedEvent{
            ItemID:   msg.ItemID,
            Quantity: msg.Quantity,
            Price:    msg.Price,
        }
        g.events = append(g.events, event)
        g.applyEvent(event)
        ctx.Response(&Success{})

    case *RemoveItem:
        if g.status != OrderStatusOpen {
            ctx.Err(errors.New("order is not open"))
            return
        }

        event := &ItemRemovedEvent{ItemID: msg.ItemID}
        g.events = append(g.events, event)
        g.applyEvent(event)
        ctx.Response(&Success{})

    case *SubmitOrder:
        if g.status != OrderStatusOpen {
            ctx.Err(errors.New("order already submitted"))
            return
        }

        if len(g.items) == 0 {
            ctx.Err(errors.New("cannot submit empty order"))
            return
        }

        event := &OrderSubmittedEvent{
            Timestamp: time.Now(),
        }
        g.events = append(g.events, event)
        g.applyEvent(event)
        ctx.Response(&OrderSubmitted{OrderID: g.orderID, Total: g.totalPrice})

    case *GetOrder:
        ctx.Response(&OrderResponse{
            OrderID:    g.orderID,
            Items:      g.items,
            Status:     g.status,
            TotalPrice: g.totalPrice,
        })

    default:
        ctx.Unhandled()
    }
}

func (g *OrderGrain) OnDeactivate(ctx context.Context, props *actor.GrainProps) error {
    // Persist all new events
    if len(g.events) > 0 {
        return g.eventStore.SaveEvents(ctx, g.orderID, g.events, g.version)
    }
    return nil
}

func (g *OrderGrain) applyEvent(event Event) {
    switch e := event.(type) {
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

### Saga Coordinator Grain

Coordinating distributed transactions:

```go
type PaymentSagaGrain struct {
    sagaID      string
    state       SagaState
    steps       []SagaStep
    currentStep int
}

func (g *PaymentSagaGrain) OnActivate(ctx context.Context, props *actor.GrainProps) error {
    g.sagaID = props.Identity().Name()
    g.state = SagaStateNew
    g.steps = []SagaStep{
        {Name: "reserve-funds", Status: StepStatusPending},
        {Name: "validate-inventory", Status: StepStatusPending},
        {Name: "create-shipment", Status: StepStatusPending},
        {Name: "charge-payment", Status: StepStatusPending},
    }
    return nil
}

func (g *PaymentSagaGrain) OnReceive(ctx *actor.GrainContext) {
    switch msg := ctx.Message().(type) {
    case *StartSaga:
        if g.state != SagaStateNew {
            ctx.Err(errors.New("saga already started"))
            return
        }
        g.state = SagaStateRunning
        g.executeNextStep(ctx)

    case *StepCompleted:
        if g.currentStep >= len(g.steps) {
            ctx.Err(errors.New("no pending steps"))
            return
        }

        g.steps[g.currentStep].Status = StepStatusCompleted
        g.currentStep++

        if g.currentStep >= len(g.steps) {
            g.state = SagaStateCompleted
            ctx.Response(&SagaCompleted{SagaID: g.sagaID})
        } else {
            g.executeNextStep(ctx)
        }

    case *StepFailed:
        g.steps[g.currentStep].Status = StepStatusFailed
        g.state = SagaStateFailed
        g.compensate(ctx)

    case *GetSagaStatus:
        ctx.Response(&SagaStatusResponse{
            SagaID:      g.sagaID,
            State:       g.state,
            CurrentStep: g.currentStep,
            Steps:       g.steps,
        })

    default:
        ctx.Unhandled()
    }
}

func (g *PaymentSagaGrain) OnDeactivate(ctx context.Context, props *actor.GrainProps) error {
    // Persist saga state
    return nil
}

func (g *PaymentSagaGrain) executeNextStep(ctx *actor.GrainContext) {
    step := g.steps[g.currentStep]
    // Send command to appropriate service actor
    // The service will respond with StepCompleted or StepFailed
}

func (g *PaymentSagaGrain) compensate(ctx *actor.GrainContext) {
    // Execute compensation logic for completed steps in reverse order
    for i := g.currentStep - 1; i >= 0; i-- {
        if g.steps[i].Status == StepStatusCompleted {
            // Compensate this step
        }
    }
}
```

### Session Grain

Managing user sessions:

```go
type SessionGrain struct {
    sessionID   string
    userID      string
    createdAt   time.Time
    lastAccess  time.Time
    data        map[string]interface{}
    maxLifetime time.Duration
}

func (g *SessionGrain) OnActivate(ctx context.Context, props *actor.GrainProps) error {
    g.sessionID = props.Identity().Name()
    g.data = make(map[string]interface{})
    g.maxLifetime = 30 * time.Minute

    // Load session from storage (e.g., Redis)
    // ...

    return nil
}

func (g *SessionGrain) OnReceive(ctx *actor.GrainContext) {
    // Check session expiration
    if time.Since(g.lastAccess) > g.maxLifetime {
        ctx.Err(errors.New("session expired"))
        return
    }

    g.lastAccess = time.Now()

    switch msg := ctx.Message().(type) {
    case *SetSessionData:
        g.data[msg.Key] = msg.Value
        ctx.Response(&Success{})

    case *GetSessionData:
        value, ok := g.data[msg.Key]
        if !ok {
            ctx.Err(errors.New("key not found"))
            return
        }
        ctx.Response(&SessionDataResponse{Value: value})

    case *DeleteSessionData:
        delete(g.data, msg.Key)
        ctx.Response(&Success{})

    case *GetSession:
        ctx.Response(&SessionResponse{
            SessionID:  g.sessionID,
            UserID:     g.userID,
            CreatedAt:  g.createdAt,
            LastAccess: g.lastAccess,
            Data:       g.data,
        })

    case *InvalidateSession:
        // Clear all data
        g.data = make(map[string]interface{})
        ctx.Response(&Success{})

    default:
        ctx.Unhandled()
    }
}

func (g *SessionGrain) OnDeactivate(ctx context.Context, props *actor.GrainProps) error {
    // Persist session to storage
    return nil
}
```

## Integration Patterns

### Grain with HTTP API

```go
package main

import (
    "encoding/json"
    "net/http"
    "time"

    "github.com/tochemey/goakt/v3/actor"
)

type UserAPI struct {
    system actor.ActorSystem
}

func (api *UserAPI) GetUser(w http.ResponseWriter, r *http.Request) {
    userID := r.URL.Query().Get("id")
    if userID == "" {
        http.Error(w, "missing user ID", http.StatusBadRequest)
        return
    }

    // Create grain identity
    identity := actor.NewGrainIdentity(&UserGrain{}, userID)

    // Query grain
    response, err := api.system.AskGrain(
        r.Context(),
        identity,
        &GetUser{},
        5*time.Second,
    )
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    userResp := response.(*UserResponse)
    json.NewEncoder(w).Encode(userResp.User)
}

func (api *UserAPI) UpdateUser(w http.ResponseWriter, r *http.Request) {
    var req UpdateUserRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    identity := actor.NewGrainIdentity(&UserGrain{}, req.UserID)

    err := api.system.TellGrain(
        r.Context(),
        identity,
        &UpdateName{NewName: req.Name},
    )
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    w.WriteHeader(http.StatusOK)
}

func main() {
    // Setup actor system
    system, _ := actor.NewActorSystem("api", actor.WithRemote("0.0.0.0", 3321))
    system.Start(context.Background())
    defer system.Stop(context.Background())

    // Register grains
    system.RegisterGrainKind(context.Background(), &UserGrain{})

    // Setup HTTP handlers
    api := &UserAPI{system: system}
    http.HandleFunc("/user", api.GetUser)
    http.HandleFunc("/user/update", api.UpdateUser)

    http.ListenAndServe(":8080", nil)
}
```

### Grain with Message Queue

```go
package main

import (
    "context"
    "encoding/json"

    "github.com/segmentio/kafka-go"
    "github.com/tochemey/goakt/v3/actor"
)

type OrderProcessor struct {
    system actor.ActorSystem
    reader *kafka.Reader
}

func (p *OrderProcessor) Start(ctx context.Context) error {
    p.reader = kafka.NewReader(kafka.ReaderConfig{
        Brokers: []string{"localhost:9092"},
        Topic:   "orders",
        GroupID: "order-processor",
    })

    for {
        select {
        case <-ctx.Done():
            return p.reader.Close()
        default:
            msg, err := p.reader.ReadMessage(ctx)
            if err != nil {
                continue
            }

            var orderEvent OrderEvent
            if err := json.Unmarshal(msg.Value, &orderEvent); err != nil {
                continue
            }

            // Route to order grain
            identity := actor.NewGrainIdentity(&OrderGrain{}, orderEvent.OrderID)
            p.system.TellGrain(ctx, identity, &orderEvent)
        }
    }
}
```

### Grain with Scheduled Tasks

```go
type ReminderGrain struct {
    reminders []Reminder
}

func (g *ReminderGrain) OnActivate(ctx context.Context, props *actor.GrainProps) error {
    // Load reminders from storage
    for _, reminder := range g.reminders {
        if reminder.Status == ReminderStatusPending {
            g.scheduleReminder(ctx, props.ActorSystem(), props.Identity(), reminder)
        }
    }
    return nil
}

func (g *ReminderGrain) OnReceive(ctx *actor.GrainContext) {
    switch msg := ctx.Message().(type) {
    case *AddReminder:
        reminder := Reminder{
            ID:        generateID(),
            Message:   msg.Message,
            ScheduleAt: msg.ScheduleAt,
            Status:    ReminderStatusPending,
        }
        g.reminders = append(g.reminders, reminder)
        g.scheduleReminder(ctx.Context(), ctx.ActorSystem(), ctx.Self(), reminder)
        ctx.Response(&ReminderAdded{ID: reminder.ID})

    case *ReminderTriggered:
        for i, reminder := range g.reminders {
            if reminder.ID == msg.ID {
                g.reminders[i].Status = ReminderStatusSent
                // Send actual reminder message
                break
            }
        }

    case *GetReminders:
        ctx.Response(&RemindersResponse{Reminders: g.reminders})

    default:
        ctx.Unhandled()
    }
}

func (g *ReminderGrain) scheduleReminder(ctx context.Context, sys actor.ActorSystem, identity *actor.GrainIdentity, reminder Reminder) {
    delay := time.Until(reminder.ScheduleAt)
    if delay < 0 {
        delay = 0
    }
    go func() {
        timer := time.NewTimer(delay)
        defer timer.Stop()
        select {
        case <-ctx.Done():
            return
        case <-timer.C:
            _ = sys.TellGrain(ctx, identity, &ReminderTriggered{ID: reminder.ID})
        }
    }()
}

func (g *ReminderGrain) OnDeactivate(ctx context.Context, props *actor.GrainProps) error {
    // Persist reminders
    return nil
}
```

## Testing Patterns

### Unit Testing Grains

```go
package main

import (
    "context"
    "testing"
    "time"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/testkit"
)

func TestUserGrain_UpdateName(t *testing.T) {
    ctx := context.Background()

    // Create test actor system
    system, err := actor.NewActorSystem(
        "test",
        actor.WithLogger(log.DiscardLogger),
    )
    require.NoError(t, err)

    err = system.Start(ctx)
    require.NoError(t, err)
    defer system.Stop(ctx)

    // Register grain
    err = system.RegisterGrainKind(ctx, &UserGrain{})
    require.NoError(t, err)

    // Create grain probe
    probe := testkit.NewGrainProbe(t)
    identity := actor.NewGrainIdentity(&UserGrain{}, "test-user")

    // Activate grain
    err = system.ActivateGrain(ctx, identity)
    require.NoError(t, err)

    // Send message
    err = system.TellGrain(ctx, identity, &UpdateName{NewName: "Alice"})
    require.NoError(t, err)

    // Query grain
    response, err := system.AskGrain(ctx, identity, &GetUser{}, time.Second)
    require.NoError(t, err)

    userResp := response.(*UserResponse)
    assert.Equal(t, "Alice", userResp.User.Name)
}

func TestUserGrain_InvalidEmail(t *testing.T) {
    ctx := context.Background()

    system, _ := actor.NewActorSystem("test")
    system.Start(ctx)
    defer system.Stop(ctx)

    system.RegisterGrainKind(ctx, &UserGrain{})

    identity := actor.NewGrainIdentity(&UserGrain{}, "test-user")
    system.ActivateGrain(ctx, identity)

    // Send invalid email
    _, err := system.AskGrain(
        ctx,
        identity,
        &UpdateEmail{NewEmail: "invalid"},
        time.Second,
    )

    assert.Error(t, err)
    assert.Contains(t, err.Error(), "invalid email")
}
```

### Integration Testing

```go
func TestOrderGrain_CompleteFlow(t *testing.T) {
    ctx := context.Background()

    // Setup dependencies
    eventStore := NewMockEventStore()

    // Create actor system
    system, _ := actor.NewActorSystem("test")
    system.Start(ctx)
    defer system.Stop(ctx)

    // Register grain with dependencies
    system.RegisterGrainKind(ctx, &OrderGrain{})

    identity := actor.NewGrainIdentity(&OrderGrain{}, "order-123")

    // Activate with dependencies
    err := system.ActivateGrain(
        ctx,
        identity,
        actor.WithGrainDependencies(eventStore),
    )
    require.NoError(t, err)

    // Add items
    system.TellGrain(ctx, identity, &AddItem{ItemID: "item-1", Quantity: 2, Price: 10.00})
    system.TellGrain(ctx, identity, &AddItem{ItemID: "item-2", Quantity: 1, Price: 25.00})

    // Submit order
    response, err := system.AskGrain(ctx, identity, &SubmitOrder{}, time.Second)
    require.NoError(t, err)

    submitted := response.(*OrderSubmitted)
    assert.Equal(t, "order-123", submitted.OrderID)
    assert.Equal(t, 45.00, submitted.Total)

    // Verify events were persisted
    events, _ := eventStore.LoadEvents(ctx, "order-123")
    assert.Len(t, events, 3) // 2 ItemAdded + 1 OrderSubmitted
}
```

## Performance Optimization

### Batching Updates

```go
type InventoryGrain struct {
    items map[string]int
    batch []InventoryUpdate
}

func (g *InventoryGrain) OnReceive(ctx *actor.GrainContext) {
    switch msg := ctx.Message().(type) {
    case *UpdateInventory:
        // Accumulate updates
        g.batch = append(g.batch, InventoryUpdate{
            ItemID: msg.ItemID,
            Delta:  msg.Delta,
        })

        // Batch flush on size or timeout
        if len(g.batch) >= 100 {
            g.flushBatch(ctx.Context())
        }

        ctx.Response(&Success{})

    case *FlushBatch:
        g.flushBatch(ctx.Context())
    }
}

func (g *InventoryGrain) flushBatch(ctx context.Context) {
    if len(g.batch) == 0 {
        return
    }

    // Apply all updates to inventory
    for _, update := range g.batch {
        g.items[update.ItemID] += update.Delta
    }

    // Persist to database in one transaction
    // ...

    g.batch = g.batch[:0]
}
```

### Caching Pattern

```go
type CachedUserGrain struct {
    user      *User
    cacheHit  bool
    cache     Cache
    db        Database
}

func (g *CachedUserGrain) OnActivate(ctx context.Context, props *actor.GrainProps) error {
    userID := props.Identity().Name()

    // Try cache first
    if cached, ok := g.cache.Get(userID); ok {
        g.user = cached.(*User)
        g.cacheHit = true
        return nil
    }

    // Load from database
    user, err := g.db.LoadUser(ctx, userID)
    if err != nil {
        return err
    }

    g.user = user
    g.cacheHit = false

    // Populate cache
    g.cache.Set(userID, user, 10*time.Minute)
    return nil
}
```

## Best Practices Summary

1. **Keep grains small and focused**: One entity or aggregate per grain
2. **Persist state**: Use OnDeactivate to save state
3. **Handle failures**: Return errors from OnActivate for retry
4. **Use dependencies**: Inject external services via WithGrainDependencies
5. **Set appropriate timeouts**: Tune passivation based on access patterns
6. **Test thoroughly**: Unit test grain logic, integration test persistence
7. **Monitor metrics**: Track activation rate, latency, memory usage
8. **Use bounded mailboxes**: Apply backpressure under load
9. **Batch when possible**: Group related updates
10. **Cache hot data**: Reduce external service calls

## Common Pitfalls

### Don't Block in Message Handlers

```go
// BAD: Blocking operation in OnReceive
func (g *MyGrain) OnReceive(ctx *actor.GrainContext) {
    response, _ := http.Get("http://slow-api.com") // Blocks the grain!
    // ...
}

// GOOD: Use async patterns
func (g *MyGrain) OnReceive(ctx *actor.GrainContext) {
    go func() {
        response, err := http.Get("http://slow-api.com")
        // Send result back to grain
    }()
    ctx.Response(&RequestQueued{})
}
```

### Don't Retain GrainContext

```go
// BAD: Storing context reference
type MyGrain struct {
    lastContext *actor.GrainContext // DON'T DO THIS
}

func (g *MyGrain) OnReceive(ctx *actor.GrainContext) {
    g.lastContext = ctx // Invalid after method returns
}

// GOOD: Use context only within method
func (g *MyGrain) OnReceive(ctx *actor.GrainContext) {
    message := ctx.Message()
    // Use ctx within this method only
}
```

### Don't Forget Error Handling

```go
// BAD: No error handling
func (g *MyGrain) OnActivate(ctx context.Context, props *actor.GrainProps) error {
    g.data = loadFromDB() // What if this fails?
    return nil
}

// GOOD: Proper error handling
func (g *MyGrain) OnActivate(ctx context.Context, props *actor.GrainProps) error {
    data, err := loadFromDB()
    if err != nil {
        return fmt.Errorf("failed to load data: %w", err)
    }
    g.data = data
    return nil
}
```

## Next Steps

- [Grains Overview](overview.md): Understand grain fundamentals
- [Cluster Overview](../cluster/overview.md): Distributed grain activation
- [Testing Guide](../testing/overview.md): Testing strategies for grains
- [Best Practices](../testing/best-practices.md): General testing best practices
