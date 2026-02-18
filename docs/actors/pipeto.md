# PipeTo Pattern

The PipeTo pattern allows actors to execute expensive or blocking operations in the background and receive the result as a regular message. This prevents blocking the actor's mailbox while maintaining the actor model's single-threaded message processing.

## Table of Contents

- 🤔 [What is PipeTo?](#what-is-pipeto)
- 💡 [When to Use PipeTo?](#when-to-use-pipeto)
- 🚀 [Basic Usage](#basic-usage)
- 💡 [Complete Example (concept)](#complete-example-concept)
- ⚠️ [Error Handling](#error-handling)
- 🧩 [Common Patterns](#common-patterns)
- ✅ [Best Practices](#best-practices)
- 🔀 [PipeTo vs. Reentrancy](#pipeto-vs-reentrancy)
- ⚡ [Performance Considerations](#performance-considerations)
- 📋 [Summary](#summary)

---

## What is PipeTo?

**PipeTo** executes a function asynchronously and pipes the result back to an actor as a message. The function runs in a separate goroutine, and the actor continues processing other messages. **The function must return a protocol buffer message (proto.Message) on success**—that is what the target actor receives as a normal message. On failure, return an error.

**Where it runs:** Use **PipeTo** or **PipeToName** from inside an actor’s **Receive**, on the receive context. You pass a target (PID or actor name) and a function that returns **(proto.Message, error)**.

**Target by PID or by name:**

- **PipeTo** — you pass a target PID and a function. The function’s return value (or error) is sent as a message to that PID. Use the current actor’s PID (e.g. from the context) to send the result back to self, or pass another actor’s PID.
- **PipeToName** — same idea but you pass the recipient’s **name** (string). Use when you don’t have a PID or for location-transparent delivery in a cluster.

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

- **From inside Receive:** Call **PipeTo(targetPID, fn)** or **PipeToName(actorName, fn)**. The function returns **(proto.Message, error)** and runs in a **separate goroutine**. On success, the proto message is sent to the target as a normal message; the target handles it in a later **Receive**. On error, the **target** actor’s supervisor is invoked.
- **PipeTo Self** — Pass the current actor’s PID; the same actor receives the result. Handle the result type in a later message and respond to the original requester or update state.
- **PipeTo another actor** — Pass another PID to send the result to a coordinator, worker, etc.
- **PipeToName** — Pass the actor’s name when you don’t have a PID (e.g. in a cluster).

**Important:** Do not read or write actor state inside the function—it runs in another goroutine. Capture what you need in locals. On success return a proto message; on failure return an error so the target’s supervision is triggered.

## Complete Example (concept)

A typical flow: the actor receives a request (e.g. GetUser, ComputeMetrics). It calls **PipeTo** targeting Self and returns immediately (optionally sending a “request accepted” response). Inside the function you pass, do the blocking or CPU-heavy work (DB query, HTTP call, computation), then return a result message or an error. When the function completes, the **same actor** receives the result as a new message (e.g. *UserResult, *MetricsResult); in that branch, call **Response** to reply to the original requester. The actor can handle other messages (e.g. GetStatus) while background work is in flight. For full runnable examples see the GoAkt repo or [Messaging](messaging.md).

## Error Handling

If the function you pass to PipeTo returns a **non-nil error**, that error is treated as a **failure of the target actor** (the one that receives the result). The **target**’s supervisor is then invoked according to its directives (Restart, Stop, etc.). The target does not receive a success message when you return an error. Signal failure by returning an error from the function; signal success by returning a message. See [Supervisor](supervisor.md) for configuring directives.

## Common Patterns

### Database query

Run the query in the background; return a proto message with the rows. Handle the result in Receive and call Response.

```go
func (a *DatabaseActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *Query:
        sql := msg.GetSql()
        ctx.PipeTo(ctx.Self(), func() (proto.Message, error) {
            rows, err := a.db.Query(sql)
            if err != nil {
                return nil, err
            }
            defer rows.Close()
            results := parseRows(rows)
            return &QueryResult{Rows: results}, nil
        })
    case *QueryResult:
        ctx.Response(msg)
    default:
        ctx.Unhandled()
    }
}
```

### HTTP request

Perform the request in the background; return a proto message with status and body. Handle the result and respond.

```go
func (a *APIActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *FetchFromAPI:
        url := msg.GetUrl()
        ctx.PipeTo(ctx.Self(), func() (proto.Message, error) {
            resp, err := a.httpClient.Get(url)
            if err != nil {
                return nil, err
            }
            defer resp.Body.Close()
            body, _ := io.ReadAll(resp.Body)
            return &APIResponse{StatusCode: int32(resp.StatusCode), Body: body}, nil
        })
    case *APIResponse:
        ctx.Response(msg)
    default:
        ctx.Unhandled()
    }
}
```

### File read

Read the file in the background; return a proto message with the content. Handle the result in Receive.

```go
func (a *FileActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *ReadFile:
        path := msg.GetPath()
        ctx.PipeTo(ctx.Self(), func() (proto.Message, error) {
            data, err := os.ReadFile(path)
            if err != nil {
                return nil, err
            }
            return &FileContent{Path: path, Content: data}, nil
        })
    case *FileContent:
        a.processFileContent(msg)
        ctx.Response(&Done{})
    default:
        ctx.Unhandled()
    }
}
```

### CPU-heavy work

Run the computation in the background; return a proto message with the result. Handle the result and respond.

```go
func (a *ComputeActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *ComputePrimes:
        max := msg.GetMax()
        ctx.PipeTo(ctx.Self(), func() (proto.Message, error) {
            primes := a.calculatePrimes(max)
            return &PrimeResult{Primes: primes}, nil
        })
    case *PrimeResult:
        ctx.Response(msg)
    default:
        ctx.Unhandled()
    }
}
```

### Multiple async calls

Start several PipeTo calls from one message; collect results in actor state; when all have arrived, respond with the aggregated proto message.

```go
func (a *AggregatorActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *AggregateData:
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
        a.maybeRespondAggregated(ctx)
    case *Source2Result:
        a.results["source2"] = msg.Data
        a.maybeRespondAggregated(ctx)
    case *Source3Result:
        a.results["source3"] = msg.Data
        a.maybeRespondAggregated(ctx)
    default:
        ctx.Unhandled()
    }
}
```

### Retry with backoff

On error in the PipeTo function, either return the error (target fails) or schedule a delayed *RetryAttempt* and return success. Handle *RetryAttempt* in Receive and call PipeTo again.

```go
func (a *RetryActor) Receive(ctx *actor.ReceiveContext) {
    switch msg := ctx.Message().(type) {
    case *FetchWithRetry:
        a.attemptFetch(ctx, msg, 0)
    case *RetryAttempt:
        a.attemptFetch(ctx, msg.Original, msg.Attempt)
    case *FetchResult:
        ctx.Response(msg)
    default:
        ctx.Unhandled()
    }
}

func (a *RetryActor) attemptFetch(ctx *actor.ReceiveContext, msg *FetchWithRetry, attempt int) {
    url := msg.GetUrl()
    ctx.PipeTo(ctx.Self(), func() (proto.Message, error) {
        data, err := a.fetch(url)
        if err != nil {
            if attempt < 3 {
                delay := time.Duration(1<<attempt) * time.Second
                _ = ctx.ActorSystem().ScheduleOnce(ctx.Context(),
                    &RetryAttempt{Original: msg, Attempt: attempt + 1},
                    ctx.Self(), delay)
                return nil, nil
            }
            return nil, err
        }
        return &FetchResult{Data: data}, nil
    })
}
```

## Best Practices

### Do's ✅

Use **PipeTo** for blocking or CPU-heavy work; **return a proto message on success** and an error on failure; handle errors inside the function and let the target’s supervisor handle failures; keep the function focused; use clear result message types so the target can switch on them.

### Don'ts ❌

Do **not** read or write actor fields inside the function you pass—it runs in another goroutine. Capture needed data in local variables and use those in the function. Don’t return a nil message on success; don’t use PipeTo for very fast operations (use normal Receive); don’t ignore errors; don’t block the actor’s Receive loop (that’s what PipeTo avoids).

## PipeTo vs. Reentrancy

### PipeTo

- Execute **any** function in background
- Function runs in **separate goroutine**
- Result delivered as **message**
- Actor continues processing immediately
- Use for **blocking I/O**, **CPU work**, **external calls**

### Reentrancy

- Send an **async request** to **another actor**; response is delivered via a **continuation** (callback). The actor must be spawned with reentrancy enabled. Use for **actor-to-actor** async request-response when you don’t want to block. See [Reentrancy](reentrancy.md).

**Summary:** Use **PipeTo** for running arbitrary work (I/O, CPU) in a goroutine and getting the result as a message. Use **Reentrancy** (Request/RequestName) for async calls to other actors with a callback.

## Performance Considerations

- **Goroutine overhead**: Each PipeTo creates a goroutine
- **Don't overuse**: For fast operations, use regular message processing
- **Pool if needed**: For very high throughput, consider worker pools
- **Monitor goroutines**: Use pprof to track goroutine count

## Summary

- **PipeTo** executes work in background
- **Result must be a proto message**: The function returns (proto.Message, error); on success the target receives that message
- **Non-blocking**: Actor continues processing
- **Result as message**: Preserves actor model
- **Use for**: Blocking I/O, CPU work, external calls
- **PipeTo** vs. **Reentrancy**: Different use cases
- **Error handling**: Errors delivered to actor
- **Testing**: Account for async execution
