# Dependencies

Dependency injection allows you to attach runtime dependencies to actors at spawn time. This is particularly useful for testing, configuration, and resource management.

## What are Dependencies?

**Dependencies** are runtime values that actors need to function:
- Database connections
- HTTP clients
- Configuration objects
- External services
- Caches and stores
- Test mocks

Instead of creating these in `PreStart` or using global variables, dependencies can be **injected** when spawning the actor.

## Why Use Dependency Injection?

Benefits include:
- **Testability**: Easy to inject mocks and test doubles
- **Flexibility**: Different configurations per environment
- **Isolation**: No global state or singletons
- **Explicit dependencies**: Clear what each actor needs
- **Resource management**: Shared resources across actors

## Basic Usage

### Define Dependencies

Dependencies are passed as variadic arguments when spawning:

```go
type DatabaseActor struct {
    db *sql.DB
}

func (a *DatabaseActor) PreStart(ctx *actor.Context) error {
    // Access dependency from context
    deps := ctx.Dependencies()
    if len(deps) > 0 {
        if db, ok := deps[0].(*sql.DB); ok {
            a.db = db
        }
    }
    return nil
}
```

### Inject Dependencies

Pass dependencies when spawning:

```go
db, _ := sql.Open("postgres", connectionString)

pid, err := actorSystem.Spawn(ctx, "database-actor", &DatabaseActor{},
    actor.WithDependencies(db))
```

## Comprehensive Example

```go
package main

import (
    "context"
    "database/sql"
    "net/http"
    
    "github.com/tochemey/goakt/v3/actor"
)

// Dependencies structure
type AppDependencies struct {
    DB         *sql.DB
    HTTPClient *http.Client
    Config     *Config
    Cache      *Cache
}

// Actor with dependencies
type UserActor struct {
    deps *AppDependencies
}

func (a *UserActor) PreStart(ctx *actor.Context) error {
    // Extract dependencies
    deps := ctx.Dependencies()
    if len(deps) > 0 {
        if appDeps, ok := deps[0].(*AppDependencies); ok {
            a.deps = appDeps
        } else {
            return fmt.Errorf("invalid dependencies type")
        }
    }
    
    return nil
}

func (a *UserActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *GetUser:
        // Use injected dependencies
        user, err := a.loadUser(msg.GetId())
        if err != nil {
            ctx.Err(err)
            return
        }
        ctx.Response(user)
        
    case *UpdateUser:
        if err := a.updateUser(msg); err != nil {
            ctx.Err(err)
            return
        }
        ctx.Response(&UserUpdated{})
    }
}

func (a *UserActor) loadUser(id string) (*User, error) {
    // Check cache first
    if cached, ok := a.deps.Cache.Get(id); ok {
        return cached.(*User), nil
    }
    
    // Query database
    var user User
    err := a.deps.DB.QueryRow(
        "SELECT id, name, email FROM users WHERE id = $1", id,
    ).Scan(&user.Id, &user.Name, &user.Email)
    
    if err != nil {
        return nil, err
    }
    
    // Cache the result
    a.deps.Cache.Set(id, &user)
    return &user, nil
}

func (a *UserActor) updateUser(msg *UpdateUser) error {
    _, err := a.deps.DB.Exec(
        "UPDATE users SET name = $1, email = $2 WHERE id = $3",
        msg.GetName(), msg.GetEmail(), msg.GetId(),
    )
    
    if err != nil {
        return err
    }
    
    // Invalidate cache
    a.deps.Cache.Delete(msg.GetId())
    return nil
}

func (a *UserActor) PostStop(ctx *actor.Context) error {
    // Dependencies are shared, don't close them here
    return nil
}

func main() {
    ctx := context.Background()
    
    // Initialize dependencies
    db, _ := sql.Open("postgres", connectionString)
    httpClient := &http.Client{Timeout: 10 * time.Second}
    config := &Config{MaxRetries: 3}
    cache := NewCache()
    
    deps := &AppDependencies{
        DB:         db,
        HTTPClient: httpClient,
        Config:     config,
        Cache:      cache,
    }
    
    // Create actor system
    actorSystem, _ := actor.NewActorSystem("MySystem",
        actor.WithPassivationStrategy(passivation.NewLongLivedStrategy()))
    actorSystem.Start(ctx)
    defer actorSystem.Stop(ctx)
    
    // Spawn actor with dependencies
    pid, _ := actorSystem.Spawn(ctx, "user-actor", &UserActor{},
        actor.WithDependencies(deps))
    
    // Use the actor
    response, _ := actor.Ask(ctx, pid, &GetUser{Id: "123"}, 5*time.Second)
    user := response.(*User)
    fmt.Printf("User: %s\n", user.Name)
}
```

## Multiple Dependencies

Pass multiple dependencies:

```go
db, _ := sql.Open("postgres", connectionString)
cache := NewCache()
logger := log.New()

pid, err := actorSystem.Spawn(ctx, "my-actor", &MyActor{},
    actor.WithDependencies(db, cache, logger))
```

Access in actor:

```go
func (a *MyActor) PreStart(ctx *actor.Context) error {
    deps := ctx.Dependencies()
    
    if len(deps) >= 3 {
        a.db = deps[0].(*sql.DB)
        a.cache = deps[1].(*Cache)
        a.logger = deps[2].(log.Logger)
    }
    
    return nil
}
```

## Testing with Dependencies

### Mock Dependencies

```go
// Mock database
type MockDB struct {
    users map[string]*User
}

func (m *MockDB) QueryRow(query string, args ...interface{}) *sql.Row {
    // Return mock data
}

func (m *MockDB) Exec(query string, args ...interface{}) (sql.Result, error) {
    // Mock execution
    return nil, nil
}

// Test
func TestUserActor(t *testing.T) {
    ctx := context.Background()
    system, _ := actor.NewActorSystem("test",
        actor.WithPassivationStrategy(passivation.NewLongLivedStrategy()))
    system.Start(ctx)
    defer system.Stop(ctx)
    
    // Create mock dependencies
    mockDB := &MockDB{
        users: map[string]*User{
            "123": {Id: "123", Name: "Test User"},
        },
    }
    
    mockDeps := &AppDependencies{
        DB:    mockDB,
        Cache: NewCache(),
    }
    
    // Spawn with mock dependencies
    pid, _ := system.Spawn(ctx, "user", &UserActor{},
        actor.WithDependencies(mockDeps))
    
    // Test
    response, _ := actor.Ask(ctx, pid, &GetUser{Id: "123"}, time.Second)
    user := response.(*User)
    
    assert.Equal(t, "Test User", user.Name)
}
```

### Test Doubles

```go
// Test double for external service
type FakePaymentService struct {
    shouldFail bool
}

func (f *FakePaymentService) ProcessPayment(amount float64) error {
    if f.shouldFail {
        return errors.New("payment failed")
    }
    return nil
}

// Test
func TestPaymentActor(t *testing.T) {
    ctx := context.Background()
    system, _ := actor.NewActorSystem("test",
        actor.WithPassivationStrategy(passivation.NewLongLivedStrategy()))
    system.Start(ctx)
    defer system.Stop(ctx)
    
    // Success case
    fakeService := &FakePaymentService{shouldFail: false}
    pid, _ := system.Spawn(ctx, "payment", &PaymentActor{},
        actor.WithDependencies(fakeService))
    
    response, _ := actor.Ask(ctx, pid, &ProcessPayment{Amount: 100}, time.Second)
    result := response.(*PaymentResult)
    assert.True(t, result.Success)
    
    // Failure case
    fakeService.shouldFail = true
    response, _ = actor.Ask(ctx, pid, &ProcessPayment{Amount: 100}, time.Second)
    result = response.(*PaymentResult)
    assert.False(t, result.Success)
}
```

## Patterns

### Pattern 1: Shared Resource Pool

```go
type ConnectionPool struct {
    connections []*Connection
    mu          sync.Mutex
}

func (p *ConnectionPool) Get() *Connection {
    p.mu.Lock()
    defer p.mu.Unlock()
    // Get connection from pool
}

func (p *ConnectionPool) Return(conn *Connection) {
    p.mu.Lock()
    defer p.mu.Unlock()
    // Return connection to pool
}

// Multiple actors share the same pool
pool := NewConnectionPool(10)

worker1, _ := actorSystem.Spawn(ctx, "worker1", &WorkerActor{},
    actor.WithDependencies(pool))

worker2, _ := actorSystem.Spawn(ctx, "worker2", &WorkerActor{},
    actor.WithDependencies(pool))
```

### Pattern 2: Configuration Injection

```go
type Config struct {
    MaxRetries    int
    Timeout       time.Duration
    RetryBackoff  time.Duration
}

type WorkerActor struct {
    config *Config
}

func (a *WorkerActor) PreStart(ctx *actor.Context) error {
    deps := ctx.Dependencies()
    if len(deps) > 0 {
        a.config = deps[0].(*Config)
    }
    return nil
}

// Different configurations per environment
devConfig := &Config{MaxRetries: 5, Timeout: 10*time.Second}
prodConfig := &Config{MaxRetries: 3, Timeout: 5*time.Second}

// Dev actor
devWorker, _ := actorSystem.Spawn(ctx, "dev-worker", &WorkerActor{},
    actor.WithDependencies(devConfig))

// Prod actor
prodWorker, _ := actorSystem.Spawn(ctx, "prod-worker", &WorkerActor{},
    actor.WithDependencies(prodConfig))
```

### Pattern 3: Service Locator

```go
type ServiceLocator struct {
    services map[string]interface{}
}

func (s *ServiceLocator) Get(name string) interface{} {
    return s.services[name]
}

type MyActor struct {
    locator *ServiceLocator
}

func (a *MyActor) PreStart(ctx *actor.Context) error {
    deps := ctx.Dependencies()
    if len(deps) > 0 {
        a.locator = deps[0].(*ServiceLocator)
    }
    return nil
}

func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    // Lazy service lookup
    db := a.locator.Get("database").(*sql.DB)
    cache := a.locator.Get("cache").(*Cache)
    
    // Use services
}

// Usage
locator := &ServiceLocator{
    services: map[string]interface{}{
        "database": db,
        "cache":    cache,
        "logger":   logger,
    },
}

pid, _ := actorSystem.Spawn(ctx, "actor", &MyActor{},
    actor.WithDependencies(locator))
```

### Pattern 4: Factory Pattern

```go
type ActorFactory struct {
    db    *sql.DB
    cache *Cache
}

func (f *ActorFactory) CreateUserActor() *UserActor {
    return &UserActor{
        db:    f.db,
        cache: f.cache,
    }
}

func (f *ActorFactory) CreateOrderActor() *OrderActor {
    return &OrderActor{
        db:    f.db,
        cache: f.cache,
    }
}

// Usage
factory := &ActorFactory{
    db:    db,
    cache: cache,
}

userActor, _ := actorSystem.Spawn(ctx, "user", factory.CreateUserActor())
orderActor, _ := actorSystem.Spawn(ctx, "order", factory.CreateOrderActor())
```

## Best Practices

### Do's ✅

1. **Use dependency injection for external resources**
2. **Inject mocks for testing**
3. **Share expensive resources** (connection pools, caches)
4. **Use typed dependencies** with helper functions
5. **Document required dependencies**

```go
// Good: Typed dependency accessor
func (a *MyActor) PreStart(ctx *actor.Context) error {
    deps, err := extractDependencies(ctx.Dependencies())
    if err != nil {
        return err
    }
    a.deps = deps
    return nil
}

func extractDependencies(deps []interface{}) (*AppDependencies, error) {
    if len(deps) == 0 {
        return nil, errors.New("no dependencies provided")
    }
    
    appDeps, ok := deps[0].(*AppDependencies)
    if !ok {
        return nil, errors.New("invalid dependency type")
    }
    
    return appDeps, nil
}
```

### Don'ts ❌

1. **Don't use global variables** instead of injection
2. **Don't close shared resources** in PostStop
3. **Don't ignore type assertions**
4. **Don't over-inject** too many dependencies
5. **Don't create dependencies in actor** (defeats the purpose)

```go
// Bad: Global variables
var globalDB *sql.DB

func (a *BadActor) PreStart(ctx *actor.Context) error {
    a.db = globalDB // ❌ Don't use globals
    return nil
}

// Good: Dependency injection
func (a *GoodActor) PreStart(ctx *actor.Context) error {
    deps := ctx.Dependencies()
    a.db = deps[0].(*sql.DB) // ✅ Inject dependencies
    return nil
}
```

## Advanced: Builder Pattern

```go
type ActorBuilder struct {
    db         *sql.DB
    cache      *Cache
    httpClient *http.Client
    config     *Config
}

func NewActorBuilder() *ActorBuilder {
    return &ActorBuilder{}
}

func (b *ActorBuilder) WithDatabase(db *sql.DB) *ActorBuilder {
    b.db = db
    return b
}

func (b *ActorBuilder) WithCache(cache *Cache) *ActorBuilder {
    b.cache = cache
    return b
}

func (b *ActorBuilder) WithHTTPClient(client *http.Client) *ActorBuilder {
    b.httpClient = client
    return b
}

func (b *ActorBuilder) WithConfig(config *Config) *ActorBuilder {
    b.config = config
    return b
}

func (b *ActorBuilder) Build() *Dependencies {
    return &Dependencies{
        DB:         b.db,
        Cache:      b.cache,
        HTTPClient: b.httpClient,
        Config:     b.config,
    }
}

// Usage
deps := NewActorBuilder().
    WithDatabase(db).
    WithCache(cache).
    WithHTTPClient(httpClient).
    WithConfig(config).
    Build()

pid, _ := actorSystem.Spawn(ctx, "actor", &MyActor{},
    actor.WithDependencies(deps))
```

## Summary

- **Dependencies** provide runtime values to actors
- **Inject** using `WithDependencies()` at spawn time
- **Access** via `ctx.Dependencies()` in PreStart
- **Use for** databases, clients, configs, mocks
- **Benefits**: testability, flexibility, isolation
- **Share** expensive resources across actors
- **Don't close** shared dependencies in PostStop

## Next Steps

- **[Overview](overview.md)**: Actor lifecycle and PreStart
- **[Supervision](supervision.md)**: Fault tolerance with dependencies
- **[Passivation](passivation.md)**: Resource management
- **[Reentrancy](reentrancy.md)**: Concurrent request handling
