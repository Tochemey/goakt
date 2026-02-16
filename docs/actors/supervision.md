# Supervision

Supervision is a core fault-tolerance mechanism in GoAkt that enables actors to handle failures gracefully. Supervisors monitor child actors and decide how to respond when they fail, creating self-healing systems.

## What is Supervision?

In the actor model, actors form a hierarchy where **parent actors supervise child actors**. When a child fails, the parent (supervisor) decides how to handle the failure based on configured rules.

This creates a "let it crash" philosophy where:

- Failures are isolated to specific actors
- Supervisors handle failures automatically
- Systems can self-heal without manual intervention
- Failures don't bring down the entire system

## Actor Hierarchy

Every actor system has a built-in supervision hierarchy:

```
ActorSystem
  └── RootGuardian
        ├── SystemGuardian (system actors)
        └── UserGuardian (user actors)
              ├── YourActor
              │     ├── ChildActor1
              │     └── ChildActor2
              └── AnotherActor
```

- **Parents supervise children**: Each actor supervises its direct children
- **Hierarchical failure handling**: Failures can be handled at different levels
- **Escalation**: Failures can escalate up the hierarchy if needed

## Supervision Strategies

Supervisors use strategies to determine which actors are affected by a failure.

### OneForOneStrategy (Default)

When a child fails, **only that child** is affected by the supervisor's directive.

```go
supervisor := supervisor.NewSupervisor(
    supervisor.WithStrategy(supervisor.OneForOneStrategy))
```

**Use when:**

- Children are independent
- One child's failure shouldn't affect others
- Default choice for most scenarios

**Example:**

```
Parent
  ├── Worker1 (fails) ← Only this actor is affected
  ├── Worker2 (continues running)
  └── Worker3 (continues running)
```

### OneForAllStrategy

When any child fails, **all siblings** are affected by the supervisor's directive.

```go
supervisor := supervisor.NewSupervisor(
    supervisor.WithStrategy(supervisor.OneForAllStrategy))
```

**Use when:**

- Children are interdependent
- Shared state across children
- Consistent state is crucial
- One failure compromises all children

**Example:**

```
Parent
  ├── Worker1 (fails)
  ├── Worker2 ← Also restarted
  └── Worker3 ← Also restarted
```

## Supervision Directives

Directives tell the supervisor what action to take when a child fails.

### StopDirective

Stop the failing actor permanently.

```go
supervisor := supervisor.NewSupervisor(
    supervisor.WithDirective(&MyError{}, supervisor.StopDirective))
```

**Behavior:**

- Actor is stopped immediately
- Resources are released
- Actor is removed from supervision
- No restart

**Use when:**

- Failure is irrecoverable
- Actor state is corrupted
- Continuing would cause more harm
- Resource cleanup is needed

### RestartDirective

Restart the failing actor with fresh state.

```go
supervisor := supervisor.NewSupervisor(
    supervisor.WithDirective(&MyError{}, supervisor.RestartDirective))
```

**Behavior:**

- Actor is stopped
- `PostStop` is called
- New instance is created
- `PreStart` is called
- Actor resumes with fresh state

**Use when:**

- Failure is transient
- Fresh state can recover
- Resource re-initialization helps
- Common for most failures

### ResumeDirective

Continue processing without restarting.

```go
supervisor := supervisor.NewSupervisor(
    supervisor.WithDirective(&MyError{}, supervisor.ResumeDirective))
```

**Behavior:**

- Error is logged
- Actor state is preserved
- Actor continues processing next message
- No restart

**Use when:**

- Failure is non-critical
- State is still valid
- Message can be skipped
- Restart would be too expensive

### EscalateDirective

Escalate failure to parent supervisor.

```go
supervisor := supervisor.NewSupervisor(
    supervisor.WithDirective(&MyError{}, supervisor.EscalateDirective))
```

**Behavior:**

- Failure is passed to parent
- Parent's supervisor handles it
- Can propagate up hierarchy
- Actor waits for parent's decision

**Use when:**

- Failure is too severe for this level
- Parent needs to coordinate response
- Multiple actors need coordinated restart
- Current supervisor can't handle it

## Configuring Supervision

### Basic Configuration

```go
package main

import (
    "context"
    "fmt"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/supervisor"
)

func main() {
    ctx := context.Background()

    // Create actor system
    actorSystem, _ := actor.NewActorSystem("MySystem",
        actor.WithPassivationStrategy(passivation.NewLongLivedStrategy()))
    actorSystem.Start(ctx)
    defer actorSystem.Stop(ctx)

    // Define supervisor
    supervisorSpec := supervisor.NewSupervisor(
        supervisor.WithStrategy(supervisor.OneForOneStrategy),
        supervisor.WithDirective(&ValidationError{}, supervisor.ResumeDirective),
        supervisor.WithDirective(&DatabaseError{}, supervisor.RestartDirective),
        supervisor.WithDirective(&FatalError{}, supervisor.StopDirective),
    )

    // Spawn actor with supervision
    pid, _ := actorSystem.Spawn(ctx, "worker", &WorkerActor{},
        actor.WithSupervisorDirective(supervisorSpec))
}
```

### Error-Specific Directives

Map different errors to different directives:

```go
// Define custom errors
type ValidationError struct{ error }
type NetworkError struct{ error }
type DatabaseError struct{ error }
type FatalError struct{ error }

// Configure supervisor
supervisorSpec := supervisor.NewSupervisor(
    // Validation errors: just log and continue
    supervisor.WithDirective(&ValidationError{}, supervisor.ResumeDirective),

    // Network errors: restart to re-establish connection
    supervisor.WithDirective(&NetworkError{}, supervisor.RestartDirective),

    // Database errors: restart with backoff
    supervisor.WithDirective(&DatabaseError{}, supervisor.RestartDirective),

    // Fatal errors: stop permanently
    supervisor.WithDirective(&FatalError{}, supervisor.StopDirective),
)
```

### Any Error Directive

Apply the same directive to all errors:

```go
// Restart on any error
supervisorSpec := supervisor.NewSupervisor(
    supervisor.WithAnyErrorDirective(supervisor.RestartDirective))
```

**Note:** `WithAnyErrorDirective` overrides all error-specific directives.

### Retry Configuration

Configure restart retry limits:

```go
supervisorSpec := supervisor.NewSupervisor(
    supervisor.WithDirective(&DatabaseError{}, supervisor.RestartDirective),
    supervisor.WithRetry(3, 30*time.Second))
```

**Parameters:**

- `maxRetries`: Maximum restart attempts within window
- `timeout`: Time window for counting retries

**Behavior:**

- Restarts are counted within the time window
- If max retries exceeded, actor is stopped
- Window resets after timeout period

**Example:**

```
Time: 0s   - Actor fails, restart (count: 1)
Time: 10s  - Actor fails, restart (count: 2)
Time: 20s  - Actor fails, restart (count: 3)
Time: 25s  - Actor fails, exceeds max retries → STOP

Time: 40s  - Window resets (30s elapsed from first failure)
Time: 40s  - Actor fails, restart (count: 1)
```

## How Failures are Reported

Actors can fail in several ways:

### 1. PreStart Error

```go
func (a *MyActor) PreStart(ctx *actor.Context) error {
    conn, err := connectDatabase()
    if err != nil {
        return &DatabaseError{err} // Supervisor handles this
    }
    a.conn = conn
    return nil
}
```

### 2. Panic in Receive

```go
func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *ProcessData:
        // Panic is caught and handled by supervisor
        panic("unexpected error")
    }
}
```

### 3. Context Error

```go
func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *ProcessData:
        if err := a.validate(msg); err != nil {
            ctx.Err(&ValidationError{err}) // Reported to supervisor
            return
        }
    }
}
```

## Complete Example

```go
package main

import (
    "context"
    "errors"
    "fmt"
    "time"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/supervisor"
)

// Custom errors
type ValidationError struct{ error }
type NetworkError struct{ error }
type DatabaseError struct{ error }

// DatabaseActor manages database connections
type DatabaseActor struct {
    conn         *DBConnection
    retryCount   int
}

func (a *DatabaseActor) PreStart(ctx *actor.Context) error {
    conn, err := connectDatabase()
    if err != nil {
        // This error will be handled by supervisor
        return &DatabaseError{err}
    }

    a.conn = conn
    a.retryCount = 0
    ctx.ActorSystem().Logger().Info("Database connection established")
    return nil
}

func (a *DatabaseActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *Query:
        // Validate query
        if err := a.validateQuery(msg); err != nil {
            // Non-critical error, just log and continue
            ctx.Err(&ValidationError{err})
            ctx.Response(&QueryError{Reason: "Invalid query"})
            return
        }

        // Execute query
        result, err := a.conn.Execute(msg.GetSql())
        if err != nil {
            // Database error, supervisor will restart
            ctx.Err(&DatabaseError{err})
            return
        }

        ctx.Response(&QueryResult{Data: result})

    case *HealthCheck:
        if !a.conn.IsHealthy() {
            // Connection lost, trigger restart
            ctx.Err(&NetworkError{errors.New("connection lost")})
            return
        }
        ctx.Response(&Healthy{})
    }
}

func (a *DatabaseActor) PostStop(ctx *actor.Context) error {
    if a.conn != nil {
        ctx.ActorSystem().Logger().Info("Closing database connection")
        return a.conn.Close()
    }
    return nil
}

func (a *DatabaseActor) validateQuery(msg *Query) error {
    if msg.GetSql() == "" {
        return errors.New("empty query")
    }
    return nil
}

func main() {
    ctx := context.Background()

    // Create actor system
    actorSystem, err := actor.NewActorSystem("DBSystem",
        actor.WithPassivationStrategy(passivation.NewLongLivedStrategy()))
    if err != nil {
        panic(err)
    }

    if err := actorSystem.Start(ctx); err != nil {
        panic(err)
    }
    defer actorSystem.Stop(ctx)

    // Configure supervisor
    supervisorSpec := supervisor.NewSupervisor(
        // Strategy: only restart the failing actor
        supervisor.WithStrategy(supervisor.OneForOneStrategy),

        // Validation errors: resume (non-critical)
        supervisor.WithDirective(&ValidationError{}, supervisor.ResumeDirective),

        // Network errors: restart
        supervisor.WithDirective(&NetworkError{}, supervisor.RestartDirective),

        // Database errors: restart with retry limits
        supervisor.WithDirective(&DatabaseError{}, supervisor.RestartDirective),

        // Retry: max 5 times within 1 minute
        supervisor.WithRetry(5, time.Minute),
    )

    // Spawn supervised actor
    pid, err := actorSystem.Spawn(ctx, "database", &DatabaseActor{},
        actor.WithSupervisorDirective(supervisorSpec))
    if err != nil {
        panic(err)
    }

    // Use the actor
    response, err := actor.Ask(ctx, pid, &Query{Sql: "SELECT * FROM users"}, 5*time.Second)
    if err != nil {
        fmt.Printf("Query failed: %v\n", err)
        return
    }

    result := response.(*QueryResult)
    fmt.Printf("Query result: %v\n", result.Data)
}
```

## Supervision Patterns

### Pattern 1: Worker Pool with One-For-One

```go
type WorkerPool struct {
    workers []*actor.PID
}

func (p *WorkerPool) PreStart(ctx *actor.Context) error {
    // Spawn worker children with supervision
    supervisorSpec := supervisor.NewSupervisor(
        supervisor.WithStrategy(supervisor.OneForOneStrategy),
        supervisor.WithDirective(&WorkerError{}, supervisor.RestartDirective),
        supervisor.WithRetry(3, 30*time.Second),
    )

    for i := 0; i < 10; i++ {
        worker, err := ctx.Spawn(fmt.Sprintf("worker-%d", i), &Worker{},
            actor.WithSupervisorDirective(supervisorSpec))
        if err != nil {
            return err
        }
        p.workers = append(p.workers, worker)
    }

    return nil
}

// If one worker fails, only that worker is restarted
// Other workers continue processing
```

### Pattern 2: Coordinated Services with One-For-All

```go
type ServiceCoordinator struct {
    authService   *actor.PID
    cacheService  *actor.PID
    dbService     *actor.PID
}

func (c *ServiceCoordinator) PreStart(ctx *actor.Context) error {
    // All services must be consistent
    supervisorSpec := supervisor.NewSupervisor(
        supervisor.WithStrategy(supervisor.OneForAllStrategy),
        supervisor.WithAnyErrorDirective(supervisor.RestartDirective),
    )

    var err error
    c.authService, err = ctx.Spawn("auth", &AuthService{},
        actor.WithSupervisorDirective(supervisorSpec))
    if err != nil {
        return err
    }

    c.cacheService, err = ctx.Spawn("cache", &CacheService{},
        actor.WithSupervisorDirective(supervisorSpec))
    if err != nil {
        return err
    }

    c.dbService, err = ctx.Spawn("database", &DatabaseService{},
        actor.WithSupervisorDirective(supervisorSpec))
    if err != nil {
        return err
    }

    return nil
}

// If any service fails, all services are restarted
// Ensures consistent state across all services
```

### Pattern 3: Escalation Chain

```go
type Level1Actor struct{}
type Level2Actor struct{}
type Level3Actor struct{}

// Level 3 (bottom)
supervisorL3 := supervisor.NewSupervisor(
    supervisor.WithDirective(&MinorError{}, supervisor.ResumeDirective),
    supervisor.WithDirective(&MajorError{}, supervisor.EscalateDirective))

// Level 2 (middle)
supervisorL2 := supervisor.NewSupervisor(
    supervisor.WithDirective(&MajorError{}, supervisor.RestartDirective),
    supervisor.WithDirective(&CriticalError{}, supervisor.EscalateDirective))

// Level 1 (top)
supervisorL1 := supervisor.NewSupervisor(
    supervisor.WithAnyErrorDirective(supervisor.StopDirective))

// Minor errors: handled at level 3
// Major errors: escalate to level 2, get restarted
// Critical errors: escalate to level 1, get stopped
```

## Best Practices

### Do's ✅

1. **Use appropriate strategies**: OneForOne for independence, OneForAll for dependencies
2. **Map errors to directives**: Different errors should have different handling
3. **Set retry limits**: Prevent infinite restart loops
4. **Clean up in PostStop**: Release resources properly
5. **Log failures**: Use logger to track failures
6. **Test failure scenarios**: Ensure supervision works as expected

```go
supervisorSpec := supervisor.NewSupervisor(
    supervisor.WithStrategy(supervisor.OneForOneStrategy),
    supervisor.WithDirective(&TransientError{}, supervisor.RestartDirective),
    supervisor.WithDirective(&FatalError{}, supervisor.StopDirective),
    supervisor.WithRetry(5, time.Minute))
```

### Don'ts ❌

1. **Don't ignore errors**: Always configure supervision
2. **Don't use Resume for state corruption**: Restart instead
3. **Don't restart without limits**: Set max retries
4. **Don't forget PostStop**: Resources must be cleaned up
5. **Don't use OneForAll unnecessarily**: Restart only what's needed

## Monitoring Failures

### Logging Failures

```go
func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *ProcessData:
        if err := a.process(msg); err != nil {
            ctx.Logger().Error("Processing failed",
                "error", err,
                "message", msg)
            ctx.Err(err)
            return
        }
    }
}
```

### Dead Letter Monitoring

Failed messages may end up in dead letters:

```go
// Subscribe to dead letters
deadLetterHandler, _ := actorSystem.Spawn(ctx, "dead-letter-handler",
    &DeadLetterHandler{})
actorSystem.Subscribe(actor.DeadLetterTopic, deadLetterHandler)
```

### Metrics

GoAkt exposes supervision metrics via OpenTelemetry:

- `actor_restart_total`: Total actor restarts
- `actor_stop_total`: Total actor stops
- `actor_failure_total`: Total failures

## Testing Supervision

```go
func TestSupervision(t *testing.T) {
    ctx := context.Background()
    system, _ := actor.NewActorSystem("test",
        actor.WithPassivationStrategy(passivation.NewLongLivedStrategy()))
    system.Start(ctx)
    defer system.Stop(ctx)

    // Configure supervisor to restart on error
    supervisorSpec := supervisor.NewSupervisor(
        supervisor.WithDirective(&TestError{}, supervisor.RestartDirective))

    // Spawn supervised actor
    pid, _ := system.Spawn(ctx, "test-actor", &TestActor{},
        actor.WithSupervisorDirective(supervisorSpec))

    // Send message that causes error
    _, err := actor.Ask(ctx, pid, &CauseError{}, time.Second)

    // Actor should restart and continue working
    time.Sleep(100 * time.Millisecond)

    // Verify actor is still running
    assert.True(t, pid.IsRunning())

    // Send normal message
    response, err := actor.Ask(ctx, pid, &NormalMessage{}, time.Second)
    assert.NoError(t, err)
    assert.NotNil(t, response)
}
```

## Summary

- **Supervision** enables fault-tolerant actor systems
- **Strategies** determine which actors are affected (OneForOne vs OneForAll)
- **Directives** determine how to handle failures (Stop, Restart, Resume, Escalate)
- **Error mapping** allows different errors to be handled differently
- **Retry limits** prevent infinite restart loops
- **Parent-child hierarchy** enables escalation and coordination

## Next Steps

- **[Behaviors](behaviours.md)**: Combine supervision with dynamic behaviors
- **[Passivation](passivation.md)**: Automatic actor lifecycle management
- **[Dependencies](dependencies.md)**: Inject dependencies for testing
- **[Stashing](stashing.md)**: Defer messages during restart
