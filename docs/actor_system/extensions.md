# Extensions

Extensions provide a powerful mechanism to augment the actor system with custom or domain-specific capabilities. They enable pluggable functionality that can be shared across all actors in the system.

## Table of Contents

- 🤔 [What are Extensions?](#what-are-extensions)
- 💡 [Why Use Extensions?](#why-use-extensions)
- 🛠️ [Creating an Extension](#creating-an-extension)
- 🔧 [Registering Extensions](#registering-extensions)
- 📋 [Accessing Extensions](#accessing-extensions)
- 🎯 [Common Use Cases](#common-use-cases)
- 🔐 [Dependencies vs Extensions](#dependencies-vs-extensions)
- ✅ [Best Practices](#best-practices)
- ⚠️ [Important Considerations](#important-considerations)
- 📋 [Summary](#summary)

---

## What are Extensions?

An **Extension** is a pluggable component that extends the ActorSystem's core functionality with custom or domain-specific behavior. Extensions are registered at system initialization and become accessible from any actor's context.

**Key characteristics:**

- **System-wide availability**: Accessible from all actors
- **Simple interface**: Only requires an `ID()` method
- **Type flexibility**: Can be any type (services, clients, utilities)
- **Lifecycle**: Managed externally (not by the ActorSystem)
- **Thread-safe access**: Must handle concurrent access from multiple actors

Think of extensions as shared services or utilities that all actors can use.

## Why Use Extensions?

Extensions are useful for:

- **Shared services**: Database clients, API clients, service registries
- **Cross-cutting concerns**: Metrics, tracing, logging
- **Event sourcing**: State persistence engines
- **Custom functionality**: Domain-specific services unique to your application
- **Avoiding duplication**: Share common logic across actors
- **Dependency injection**: Provide testable dependencies to actors

## Creating an Extension

### The Extension Interface

The `Extension` interface is simple:

```go
package extension

type Extension interface {
    // ID returns the unique identifier for the extension
    ID() string
}
```

**ID Requirements:**

- Maximum 255 characters
- Must start with alphanumeric character `[a-zA-Z0-9]`
- Can contain alphanumeric characters, hyphens `-`, or underscores `_`
- Must be unique within the ActorSystem

### State Store Extension Example

```go
package myapp

import (
    "context"
    "database/sql"
    "github.com/tochemey/goakt/v3/extension"
)

// EventStore is an extension for event sourcing
type EventStore struct {
    db *sql.DB
}

var _ extension.Extension = (*EventStore)(nil)

func NewEventStore(db *sql.DB) *EventStore {
    return &EventStore{db: db}
}

func (e *EventStore) ID() string {
    return "event-store"
}

// SaveEvent persists an event to the database
func (e *EventStore) SaveEvent(ctx context.Context, persistenceID string, event interface{}) error {
    // Implementation...
    return nil
}

// LoadEvents retrieves all events for a given persistence ID
func (e *EventStore) LoadEvents(ctx context.Context, persistenceID string) ([]interface{}, error) {
    // Implementation...
    return nil, nil
}
```

### API Client Extension Example

```go
package myapp

import (
    "context"
    "net/http"
    "github.com/tochemey/goakt/v3/extension"
)

// PaymentClient is an extension for payment processing
type PaymentClient struct {
    baseURL string
    apiKey  string
    client  *http.Client
}

var _ extension.Extension = (*PaymentClient)(nil)

func NewPaymentClient(baseURL, apiKey string) *PaymentClient {
    return &PaymentClient{
        baseURL: baseURL,
        apiKey:  apiKey,
        client:  &http.Client{},
    }
}

func (p *PaymentClient) ID() string {
    return "payment-client"
}

func (p *PaymentClient) ProcessPayment(ctx context.Context, amount float64, currency string) error {
    // Implementation...
    return nil
}

func (p *PaymentClient) RefundPayment(ctx context.Context, transactionID string) error {
    // Implementation...
    return nil
}
```

## Registering Extensions

### Single Extension

Register extensions using `WithExtensions()` when creating the ActorSystem:

```go
import (
    "github.com/tochemey/goakt/v3/actor"
)

func main() {
    ctx := context.Background()
    
    // Create extension
    metrics := NewMetricsCollector()
    
    // Create actor system with extension
    actorSystem, err := actor.NewActorSystem("MyApp",
        actor.WithExtensions(metrics))
    
    if err != nil {
        log.Fatal(err)
    }
    
    if err := actorSystem.Start(ctx); err != nil {
        log.Fatal(err)
    }
    
    // Extensions are now available to all actors
}
```

### Multiple Extensions

Register multiple extensions at once:

```go
metrics := NewMetricsCollector()
eventStore := NewEventStore(db)
paymentClient := NewPaymentClient("https://api.payment.com", "api-key")

actorSystem, err := actor.NewActorSystem("MyApp",
    actor.WithExtensions(
        metrics,
        eventStore,
        paymentClient))
```

### Validations

The ActorSystem validates extension IDs during initialization:

```go
// ❌ Invalid: ID too long (> 255 characters)
type BadExtension struct{}
func (b *BadExtension) ID() string {
    return strings.Repeat("a", 300)
}

// ❌ Invalid: ID contains invalid characters
type BadExtension struct{}
func (b *BadExtension) ID() string {
    return "$omeN@me"
}

// ✅ Valid: Meets all requirements
type GoodExtension struct{}
func (g *GoodExtension) ID() string {
    return "good-extension"
}
```

## Accessing Extensions

- **From PreStart / PostStop:** Use **ctx.Extension(id)** or **ctx.Extensions()** on the actor context to read extensions during initialization or cleanup.
- **From Receive:** Use **ctx.Extension(id)** on the receive context to get an extension while handling messages. Type-assert to your concrete type (e.g. `*MetricsCollector`).

### From Actor Context (`PreStart`, `PostStop`)

Access extensions during actor initialization:

```go
type MyActor struct {
    metrics    *MetricsCollector
    eventStore *EventStore
}

func (a *MyActor) PreStart(ctx *actor.Context) error {
    // Get specific extension by ID
    a.metrics = ctx.Extension("metrics-collector").(*MetricsCollector)
    a.eventStore = ctx.Extension("event-store").(*EventStore)
    
    // Or get all extensions
    allExtensions := ctx.Extensions()
    
    return nil
}
```

### From ReceiveContext

Access extensions during message processing:

```go
func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *IncrementCounter:
        metrics := ctx.Extension("metrics-collector").(*MetricsCollector)
        metrics.Increment(msg.GetName())
        ctx.Response(&CounterIncremented{})
        
    case *ProcessPayment:
        client := ctx.Extension("payment-client").(*PaymentClient)
        err := client.ProcessPayment(ctx.Context(), msg.GetAmount(), msg.GetCurrency())
        if err != nil {
            ctx.Response(&PaymentFailed{Reason: err.Error()})
            return
        }
        ctx.Response(&PaymentSuccess{})
    }
}
```

### From GrainContext

Access extensions in grains:

```go
type MyGrain struct {
    persistenceID string
    stateStore    *EventStore
}

func (g *MyGrain) PreStart(ctx *actor.GrainContext) error {
    g.persistenceID = ctx.Name()
    g.stateStore = ctx.Extension("event-store").(*EventStore)
    
    // Load initial state
    events, err := g.stateStore.LoadEvents(ctx.Context(), g.persistenceID)
    if err != nil {
        return err
    }
    
    // Replay events...
    return nil
}
```

### Safe Type Assertions

Always check type assertions to avoid panics:

```go
func (a *MyActor) PreStart(ctx *actor.Context) error {
    ext := ctx.Extension("metrics-collector")
    if ext == nil {
        return fmt.Errorf("metrics-collector extension not found")
    }
    
    metrics, ok := ext.(*MetricsCollector)
    if !ok {
        return fmt.Errorf("extension is not MetricsCollector type")
    }
    
    a.metrics = metrics
    return nil
}
```

## Common Use Cases

### 1. Database Client Extension

```go
type DatabaseClient struct {
    db *sql.DB
}

func NewDatabaseClient(connString string) (*DatabaseClient, error) {
    db, err := sql.Open("postgres", connString)
    if err != nil {
        return nil, err
    }
    return &DatabaseClient{db: db}, nil
}

func (d *DatabaseClient) ID() string {
    return "database-client"
}

func (d *DatabaseClient) Query(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
    return d.db.QueryContext(ctx, query, args...)
}

func (d *DatabaseClient) Close() error {
    return d.db.Close()
}

// Usage in actor
type UserActor struct {
    db *DatabaseClient
}

func (u *UserActor) PreStart(ctx *actor.Context) error {
    u.db = ctx.Extension("database-client").(*DatabaseClient)
    return nil
}

func (u *UserActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *GetUser:
        rows, err := u.db.Query(ctx.Context(), "SELECT * FROM users WHERE id = $1", msg.GetId())
        if err != nil {
            ctx.Response(&Error{Message: err.Error()})
            return
        }
        defer rows.Close()
        
        // Process results...
        ctx.Response(&User{})
    }
}
```


### 2. Configuration Extension

```go
type ConfigExtension struct {
    config map[string]string
}

func NewConfigExtension(configPath string) (*ConfigExtension, error) {
    config, err := loadConfig(configPath)
    if err != nil {
        return nil, err
    }
    return &ConfigExtension{config: config}, nil
}

func (c *ConfigExtension) ID() string {
    return "config"
}

func (c *ConfigExtension) Get(key string) string {
    return c.config[key]
}

func (c *ConfigExtension) GetInt(key string) int {
    // Parse and return int...
    return 0
}
```

### 4. Cache Extension

```go
import "github.com/go-redis/redis/v8"

type CacheExtension struct {
    client *redis.Client
}

func NewCacheExtension(addr string) *CacheExtension {
    return &CacheExtension{
        client: redis.NewClient(&redis.Options{Addr: addr}),
    }
}

func (c *CacheExtension) ID() string {
    return "cache"
}

func (c *CacheExtension) Get(ctx context.Context, key string) (string, error) {
    return c.client.Get(ctx, key).Result()
}

func (c *CacheExtension) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
    return c.client.Set(ctx, key, value, ttl).Err()
}
```

## Dependencies vs Extensions

GoAkt provides two related concepts: **Extensions** and **Dependencies**.

### Extensions

- **Purpose**: Shared services/utilities for all actors
- **Lifecycle**: Managed externally (you create and maintain them)
- **Serialization**: Not required
- **Access**: Via `Context.Extension(id)`
- **Use cases**: Database clients, API clients, metrics, tracing

```go
type Extension interface {
    ID() string
}
```

### Dependencies

- **Purpose**: Actor-specific external dependencies
- **Lifecycle**: Managed by ActorSystem
- **Serialization**: **Required** (must implement `BinaryMarshaler`/`BinaryUnmarshaler`)
- **Access**: Via `ActorSystem.Inject()` and actor retrieval
- **Use cases**: Persistent state, relocatable resources

```go
type Dependency interface {
    Serializable
    ID() string
}

type Serializable interface {
    encoding.BinaryMarshaler
    encoding.BinaryUnmarshaler
}
```

### When to Use Which?

**Use Extensions when:**

- Service is stateless or maintains its own state
- Shared across many/all actors
- Does not need to be serialized
- Lifecycle managed by your application
- Examples: Database clients, API clients, config providers

**Use Dependencies when:**

- Resource is actor-specific
- Needs to survive actor relocation (clustering)
- Must be serialized/deserialized
- Lifecycle managed by ActorSystem
- Examples: Actor state stores, persistent connections

### Example Comparison

```go
// Extension: Shared database client
type DatabaseClient struct {
    db *sql.DB
}

func (d *DatabaseClient) ID() string {
    return "database-client"
}

// Registered with: actor.WithExtensions(dbClient)
// Accessed via: ctx.Extension("database-client")

// ---

// Dependency: Actor-specific state
type ActorState struct {
    data map[string]string
}

func (a *ActorState) ID() string {
    return "actor-state"
}

func (a *ActorState) MarshalBinary() ([]byte, error) {
    // Serialize state...
    return json.Marshal(a.data)
}

func (a *ActorState) UnmarshalBinary(data []byte) error {
    // Deserialize state...
    return json.Unmarshal(data, &a.data)
}

// Registered with: actorSystem.Inject(actorState)
```

## Best Practices

### Do's ✅

1. **Use meaningful IDs**: Choose descriptive, unique identifiers
2. **Thread-safe implementations**: Extensions are accessed concurrently
3. **Initialize before registration**: Set up resources before adding to system
4. **Document your extensions**: Explain purpose and usage
5. **Handle cleanup externally**: ActorSystem doesn't manage extension lifecycle
6. **Validate extension presence**: Check if extension exists before using

```go
// Good: Thread-safe, clear ID, well-documented
type MetricsCollector struct {
    counters map[string]int64
    mu       sync.RWMutex
}

func (m *MetricsCollector) ID() string {
    return "metrics-collector"
}

func (m *MetricsCollector) Increment(name string) {
    m.mu.Lock()
    defer m.mu.Unlock()
    m.counters[name]++
}
```

### Don'ts ❌

1. **Don't use invalid IDs**: Follow naming rules
2. **Don't skip thread-safety**: Multiple actors access extensions concurrently
3. **Don't forget to register**: Extensions must be registered before system start
4. **Don't assume presence**: Always check if extension exists
5. **Don't share mutable state unsafely**: Use proper synchronization

```go
// Bad: Not thread-safe
type BadExtension struct {
    counter int // ❌ No synchronization
}

func (b *BadExtension) Increment() {
    b.counter++ // ❌ Race condition!
}

// Bad: Invalid ID
func (b *BadExtension) ID() string {
    return "$bad-name!" // ❌ Invalid characters
}

// Bad: Unsafe type assertion
func (a *MyActor) PreStart(ctx *actor.Context) error {
    // ❌ Panics if extension not found or wrong type
    a.ext = ctx.Extension("my-ext").(*MyExtension)
    return nil
}

// Good: Safe type assertion
func (a *MyActor) PreStart(ctx *actor.Context) error {
    ext := ctx.Extension("my-ext")
    if ext == nil {
        return fmt.Errorf("my-ext not found")
    }
    
    myExt, ok := ext.(*MyExtension)
    if !ok {
        return fmt.Errorf("wrong extension type")
    }
    
    a.ext = myExt
    return nil
}
```

## Important Considerations

### Lifecycle Management

The ActorSystem does **not** manage extension lifecycle:

```go
func main() {
    ctx := context.Background()
    
    // You create and manage the extension
    dbClient, err := NewDatabaseClient("postgres://...")
    if err != nil {
        log.Fatal(err)
    }
    
    actorSystem, _ := actor.NewActorSystem("MyApp",
        actor.WithExtensions(dbClient))
    
    actorSystem.Start(ctx)
    
    // Do work...
    
    // You must clean up the extension
    actorSystem.Stop(ctx)
    dbClient.Close() // ← Your responsibility
}
```

### Thread Safety

Extensions are accessed by multiple actors concurrently:

```go
// ✅ Thread-safe
type SafeExtension struct {
    data map[string]string
    mu   sync.RWMutex
}

func (s *SafeExtension) Get(key string) string {
    s.mu.RLock()
    defer s.mu.RUnlock()
    return s.data[key]
}

func (s *SafeExtension) Set(key, value string) {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.data[key] = value
}
```

### Error Handling

Extensions can return errors; actors should handle them:

```go
type MyExtension struct {
    client *http.Client
}

func (m *MyExtension) FetchData(ctx context.Context) ([]byte, error) {
    // Can return errors
    resp, err := m.client.Get("https://api.example.com/data")
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    return io.ReadAll(resp.Body)
}

// Actor handles extension errors
func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    ext := ctx.Extension("my-ext").(*MyExtension)
    
    data, err := ext.FetchData(ctx.Context())
    if err != nil {
        ctx.Response(&FetchFailed{Reason: err.Error()})
        return
    }
    
    ctx.Response(&FetchSuccess{Data: data})
}
```

## Summary

- **Extensions** provide pluggable functionality for the ActorSystem
- **Simple interface**: Only requires an `ID()` method
- **Register** with `WithExtensions()` during system creation
- **Access** via `Context.Extension(id)` from any actor
- **Thread-safe**: Extensions must handle concurrent access
- **Use cases**: Database clients, API clients, metrics, tracing, configuration
- **Lifecycle**: Managed by your application, not the ActorSystem
- **Alternative**: Use Dependencies for serializable, actor-specific resources
- **Best practices**: Use meaningful IDs, ensure thread-safety, validate presence
