# PipeTo Pattern

The PipeTo pattern allows actors to execute expensive or blocking operations in the background and receive the result as a regular message. This prevents blocking the actor's mailbox while maintaining the actor model's single-threaded message processing.

## Table of Contents

- 🤔 [What is PipeTo?](#what-is-pipeto)
- 💡 [When to Use PipeTo?](#when-to-use-pipeto)
- 🚀 [Basic Usage](#basic-usage)
- 💡 [Complete Example](#complete-example)
- ⚠️ [Error Handling](#error-handling)
- 🧩 [Common Patterns](#common-patterns)
- ✅ [Best Practices](#best-practices)
- 🔀 [PipeTo vs. Reentrancy](#pipeto-vs-reentrancy)
- ⚡ [Performance Considerations](#performance-considerations)
- 🧪 [Testing](#testing)
- 📋 [Summary](#summary)
- ➡️ [Next Steps](#next-steps)

---

## What is PipeTo?

**PipeTo** executes a function asynchronously and pipes the result back to an actor as a message. The function runs in a separate goroutine, and the actor continues processing other messages.

**Where it runs:** PipeTo is used from **message handling** — inside an actor’s `Receive`, on the `ReceiveContext` (`ctx`). You call `ctx.PipeTo(...)` or `ctx.PipeToName(...)` when processing a message.

**Target by PID or by name:**

- **PipeTo**(targetPID, func) — sends the result to an actor identified by **PID** (e.g. `ctx.Self()` or another actor’s PID).
- **PipeToName**(actorName, func) — sends the result to an actor identified by **name** (string); useful when you don’t have a PID or for location-transparent delivery in a cluster.

```go
// In Receive: target by PID
ctx.PipeTo(ctx.Self(), func() (proto.Message, error) {
    result := doExpensiveComputation()
    return &Result{Data: result}, nil
})

// Or another actor's PID
ctx.PipeTo(a.coordinatorPID, func() (proto.Message, error) { ... })

// In Receive: target by name
ctx.PipeToName("result-processor", func() (proto.Message, error) { ... })
```

**Key benefits:**

- **Non-blocking**: Actor continues processing messages
- **Background execution**: Work runs in separate goroutine
- **Actor model preserved**: Result delivered as message
- **Error handling**: Errors delivered as messages

## When to Use PipeTo?

Use PipeTo for:

- **Blocking I/O**: Database queries, HTTP requests, file operations
- **CPU-intensive work**: Complex calculations, data processing
- **External service calls**: Third-party APIs, remote services
- **Long-running operations**: Anything that would block the mailbox

**Don't use PipeTo for:**

- Fast, synchronous operations
- Operations that need immediate results
- Work that modifies actor state directly (use messages instead)

## Basic Usage

### PipeTo Self

Execute work and send result back to the current actor:

```go
func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *FetchData:
        // Execute in background
        ctx.PipeTo(ctx.Self(), func() (proto.Message, error) {
            // This runs in a separate goroutine
            data, err := a.fetchFromDatabase(msg.GetId())
            if err != nil {
                return nil, err
            }
            return &DataResult{Data: data}, nil
        })

    case *DataResult:
        // Result delivered as regular message
        a.handleData(msg)

    default:
        // Continue processing other messages
        // while background work executes
    }
}
```

### PipeTo Another Actor

Send result to a different actor:

```go
func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *ProcessTask:
        // Execute work and send result to coordinator
        ctx.PipeTo(a.coordinatorPID, func() (proto.Message, error) {
            result := a.processTask(msg)
            return &TaskResult{
                TaskId: msg.GetId(),
                Result: result,
            }, nil
        })
    }
}
```

### PipeToName

Send result to an actor by name:

```go
ctx.PipeToName("result-processor", func() (proto.Message, error) {
    result := doWork()
    return &WorkResult{Data: result}, nil
})
```

## Complete Example

```go
package main

import (
    "context"
    "database/sql"
    "encoding/json"
    "fmt"
    "net/http"
    "time"

    "github.com/tochemey/goakt/v3/actor"
)

// User service actor that handles user operations
type UserServiceActor struct {
    db         *sql.DB
    httpClient *http.Client
}

func (a *UserServiceActor) PreStart(ctx *actor.Context) error {
    db, err := sql.Open("postgres", connectionString)
    if err != nil {
        return err
    }
    a.db = db

    a.httpClient = &http.Client{
        Timeout: 10 * time.Second,
    }

    return nil
}

func (a *UserServiceActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *GetUser:
        // Fetch user from database (blocking I/O)
        ctx.PipeTo(ctx.Self(), func() (proto.Message, error) {
            var user User
            err := a.db.QueryRow(
                "SELECT id, name, email FROM users WHERE id = $1",
                msg.GetId(),
            ).Scan(&user.Id, &user.Name, &user.Email)

            if err != nil {
                return nil, fmt.Errorf("user not found: %w", err)
            }

            return &UserResult{User: &user}, nil
        })

        ctx.Response(&QueryAccepted{QueryId: "get-user"})

    case *UserResult:
        // User fetched, send to original requester
        ctx.Logger().Info("User fetched", "user_id", msg.User.Id)
        ctx.Response(msg.User)

    case *EnrichUserData:
        // Fetch additional data from external API
        ctx.PipeTo(ctx.Self(), func() (proto.Message, error) {
            // Call external API (blocking HTTP request)
            resp, err := a.httpClient.Get(
                fmt.Sprintf("https://api.example.com/users/%s/details",
                    msg.GetUserId()),
            )
            if err != nil {
                return nil, err
            }
            defer resp.Body.Close()

            var enrichedData EnrichedUserData
            if err := json.NewDecoder(resp.Body).Decode(&enrichedData); err != nil {
                return nil, err
            }

            return &EnrichedDataResult{Data: &enrichedData}, nil
        })

    case *EnrichedDataResult:
        // External data fetched
        ctx.Logger().Info("User data enriched")
        ctx.Response(msg.Data)

    case *ComputeUserMetrics:
        // CPU-intensive calculation
        ctx.PipeTo(ctx.Self(), func() (proto.Message, error) {
            // Complex calculation
            metrics := a.calculateUserMetrics(msg.GetUserId())
            return &MetricsResult{Metrics: metrics}, nil
        })

    case *MetricsResult:
        // Metrics computed
        ctx.Response(msg.Metrics)

    case *GetStatus:
        // Actor can still respond to queries
        // while background work is running
        ctx.Response(&Status{
            State: "running",
            PendingTasks: a.getPendingTaskCount(),
        })
    }
}

func (a *UserServiceActor) calculateUserMetrics(userId string) *Metrics {
    // Simulate expensive computation
    time.Sleep(2 * time.Second)
    return &Metrics{
        UserId:      userId,
        Score:       95.5,
        Engagement:  0.87,
        LastActive:  time.Now(),
    }
}

func (a *UserServiceActor) PostStop(ctx *actor.Context) error {
    if a.db != nil {
        return a.db.Close()
    }
    return nil
}

func main() {
    ctx := context.Background()
    actorSystem, _ := actor.NewActorSystem("UserSystem",
        actor.WithPassivationStrategy(passivation.NewLongLivedStrategy()))
    actorSystem.Start(ctx)
    defer actorSystem.Stop(ctx)

    // Spawn user service actor
    userServicePID, _ := actorSystem.Spawn(ctx, "user-service",
        &UserServiceActor{})

    // Request user data (non-blocking)
    actor.Tell(ctx, userServicePID, &GetUser{Id: "user123"})

    // Request metrics (non-blocking)
    actor.Tell(ctx, userServicePID, &ComputeUserMetrics{UserId: "user123"})

    // Check status while work is running
    time.Sleep(100 * time.Millisecond)
    response, _ := actor.Ask(ctx, userServicePID, &GetStatus{}, time.Second)
    status := response.(*Status)
    fmt.Printf("Status: %s\n", status.State)

    // Wait for results
    time.Sleep(3 * time.Second)
}
```

## Error Handling

Errors from PipeTo functions are delivered to the actor:

```go
func (a *MyActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *FetchData:
        ctx.PipeTo(ctx.Self(), func() (proto.Message, error) {
            data, err := a.fetchData(msg.GetId())
            if err != nil {
                // Return error - will be logged by supervisor
                return nil, fmt.Errorf("fetch failed: %w", err)
            }
            return &DataResult{Data: data}, nil
        })

    case *DataResult:
        a.handleSuccess(msg)
    }
}
```

If the function returns an error, it's handled by the actor's supervisor according to configured directives.

## Common Patterns

### Pattern 1: Database Query

```go
func (a *DatabaseActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *Query:
        ctx.PipeTo(ctx.Self(), func() (proto.Message, error) {
            rows, err := a.db.Query(msg.GetSql())
            if err != nil {
                return nil, err
            }
            defer rows.Close()

            results := a.parseRows(rows)
            return &QueryResult{Rows: results}, nil
        })

    case *QueryResult:
        ctx.Response(msg)
    }
}
```

### Pattern 2: HTTP Request

```go
func (a *APIActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *FetchFromAPI:
        ctx.PipeTo(ctx.Self(), func() (proto.Message, error) {
            req, _ := http.NewRequest("GET", msg.GetUrl(), nil)
            resp, err := a.httpClient.Do(req)
            if err != nil {
                return nil, err
            }
            defer resp.Body.Close()

            body, err := io.ReadAll(resp.Body)
            if err != nil {
                return nil, err
            }

            return &APIResponse{
                StatusCode: resp.StatusCode,
                Body:       body,
            }, nil
        })

    case *APIResponse:
        ctx.Response(msg)
    }
}
```

### Pattern 3: File Operations

```go
func (a *FileActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *ReadFile:
        ctx.PipeTo(ctx.Self(), func() (proto.Message, error) {
            data, err := os.ReadFile(msg.GetPath())
            if err != nil {
                return nil, err
            }

            return &FileContent{
                Path:    msg.GetPath(),
                Content: data,
            }, nil
        })

    case *FileContent:
        a.processFileContent(msg)
    }
}
```

### Pattern 4: Complex Computation

```go
func (a *ComputeActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *ComputePrimes:
        ctx.PipeTo(ctx.Self(), func() (proto.Message, error) {
            // CPU-intensive calculation
            primes := a.calculatePrimes(msg.GetMax())
            return &PrimeResult{Primes: primes}, nil
        })

    case *PrimeResult:
        ctx.Response(msg)
    }
}
```

### Pattern 5: Multiple Async Operations

```go
func (a *AggregatorActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *AggregateData:
        // Start multiple async operations
        ctx.PipeTo(ctx.Self(), func() (proto.Message, error) {
            data1 := a.fetchFromSource1()
            return &Source1Result{Data: data1}, nil
        })

        ctx.PipeTo(ctx.Self(), func() (proto.Message, error) {
            data2 := a.fetchFromSource2()
            return &Source2Result{Data: data2}, nil
        })

        ctx.PipeTo(ctx.Self(), func() (proto.Message, error) {
            data3 := a.fetchFromSource3()
            return &Source3Result{Data: data3}, nil
        })

    case *Source1Result:
        a.results["source1"] = msg.Data
        a.checkComplete(ctx)

    case *Source2Result:
        a.results["source2"] = msg.Data
        a.checkComplete(ctx)

    case *Source3Result:
        a.results["source3"] = msg.Data
        a.checkComplete(ctx)
    }
}

func (a *AggregatorActor) checkComplete(ctx *actor.ReceiveContext) {
    if len(a.results) == 3 {
        // All results collected
        ctx.Response(&AggregatedResult{Data: a.results})
        a.results = make(map[string]interface{})
    }
}
```

### Pattern 6: Retry with Backoff

```go
func (a *RetryActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *FetchWithRetry:
        a.attemptFetch(ctx, msg, 0)

    case *RetryAttempt:
        a.attemptFetch(ctx, msg.Original, msg.Attempt)
    }
}

func (a *RetryActor) attemptFetch(ctx *actor.ReceiveContext,
    msg *FetchWithRetry, attempt int) {

    ctx.PipeTo(ctx.Self(), func() (proto.Message, error) {
        data, err := a.fetch(msg.GetUrl())
        if err != nil {
            if attempt < 3 {
                // Schedule retry
                delay := time.Duration(1<<attempt) * time.Second
                _ = ctx.ActorSystem().ScheduleOnce(ctx.Context(),
                    &RetryAttempt{Original: msg, Attempt: attempt + 1},
                    ctx.Self(),
                    delay)
                return nil, nil // Don't treat as error
            }
            return nil, err
        }
        return &FetchResult{Data: data}, nil
    })
}
```

## Best Practices

### Do's ✅

1. **Use for blocking operations**
2. **Return proper proto.Message types**
3. **Handle errors in PipeTo function**
4. **Keep PipeTo functions focused**
5. **Use appropriate message types for results**

```go
// Good: Clear error handling
ctx.PipeTo(ctx.Self(), func() (proto.Message, error) {
    data, err := a.fetchData()
    if err != nil {
        return nil, fmt.Errorf("fetch failed: %w", err)
    }
    return &DataResult{Data: data}, nil
})
```

### Don'ts ❌

1. **Don't access actor state directly in PipeTo function**
2. **Don't return nil message on success**
3. **Don't use for fast operations**
4. **Don't ignore errors**
5. **Don't block actor thread**

```go
// Bad: Accessing actor state in PipeTo function
ctx.PipeTo(ctx.Self(), func() (proto.Message, error) {
    // ❌ Unsafe! Multiple goroutines accessing actor state
    a.counter++
    return &Result{}, nil
})

// Good: Pass data as parameters
counter := a.counter
ctx.PipeTo(ctx.Self(), func() (proto.Message, error) {
    // ✅ Safe: Use local copy
    result := doWork(counter)
    return &Result{Data: result}, nil
})
```

## PipeTo vs. Reentrancy

### PipeTo

- Execute **any** function in background
- Function runs in **separate goroutine**
- Result delivered as **message**
- Actor continues processing immediately
- Use for **blocking I/O**, **CPU work**, **external calls**

### Reentrancy

- Send **async request** to another actor
- Request handled by **actor system**
- Response via **continuation callback**
- Actor continues processing with reentrancy enabled
- Use for **actor-to-actor** async communication

```go
// PipeTo: Background execution
ctx.PipeTo(ctx.Self(), func() (proto.Message, error) {
    // Any work: I/O, computation, etc.
    return doWork(), nil
})

// Reentrancy: Actor request
request := ctx.Request(otherActor, &Query{})
request.Then(func(response proto.Message, err error) {
    // Handle actor response
})
```

## Performance Considerations

- **Goroutine overhead**: Each PipeTo creates a goroutine
- **Don't overuse**: For fast operations, use regular message processing
- **Pool if needed**: For very high throughput, consider worker pools
- **Monitor goroutines**: Use pprof to track goroutine count

## Testing

```go
func TestPipeTo(t *testing.T) {
    ctx := context.Background()
    system, _ := actor.NewActorSystem("test",
        actor.WithPassivationStrategy(passivation.NewLongLivedStrategy()))
    system.Start(ctx)
    defer system.Stop(ctx)

    pid, _ := system.Spawn(ctx, "test", &TestActor{})

    // Send message that triggers PipeTo
    actor.Tell(ctx, pid, &FetchData{Id: "123"})

    // Wait for background work to complete
    time.Sleep(500 * time.Millisecond)

    // Verify result
    response, _ := actor.Ask(ctx, pid, &GetResult{}, time.Second)
    result := response.(*DataResult)
    assert.NotNil(t, result.Data)
}
```

## Summary

- **PipeTo** executes work in background
- **Non-blocking**: Actor continues processing
- **Result as message**: Preserves actor model
- **Use for**: Blocking I/O, CPU work, external calls
- **PipeTo** vs. **Reentrancy**: Different use cases
- **Error handling**: Errors delivered to actor
- **Testing**: Account for async execution

## Next Steps

- **[Reentrancy](reentrancy.md)**: Async actor-to-actor requests
- **[Messaging](messaging.md)**: Message patterns
- **[Message Scheduling](message_scheduling.md)**: Delayed execution
- **[Behaviors](behaviours.md)**: State machines with async work
