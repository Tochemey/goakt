# Supervision

Supervision is a core fault-tolerance mechanism in GoAkt that enables actors to handle failures gracefully. Supervisors monitor child actors and decide how to respond when they fail, creating self-healing systems.

## Table of Contents

- 🤔 [What is Supervision?](#what-is-supervision)
- 🌳 [Actor Hierarchy](#actor-hierarchy)
- 🛡️ [Supervision Strategies](#supervision-strategies)
- 📋 [Supervision Directives](#supervision-directives)
- ⚙️ [Configuring Supervision](#configuring-supervision)
- 📢 [How Failures are Reported](#how-failures-are-reported)
- 💡 [Complete Example (concept)](#complete-example-concept)
- 🧩 [Supervision Patterns](#supervision-patterns)
- ✅ [Best Practices](#best-practices)
- 📊 [Monitoring Failures](#monitoring-failures)
- 📋 [Summary](#summary)

---

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

When a child fails, **only that child** is affected by the supervisor's directive. Siblings keep running. Use `WithStrategy(supervisor.OneForOneStrategy)` when creating the supervisor (or rely on the default).

**Use when:** children are independent, one failure shouldn't affect others. This is the default and the right choice for most scenarios (e.g. worker pools).

```
Parent
  ├── Worker1 (fails) ← Only this actor is affected
  ├── Worker2 (continues running)
  └── Worker3 (continues running)
```

### OneForAllStrategy

When any child fails, **all siblings** are affected: the supervisor's directive (Restart, Stop, etc.) is applied to the failing child and all its siblings. Use `WithStrategy(supervisor.OneForAllStrategy)` when configuring the supervisor.

**Use when:** children are interdependent, share state, or must stay in a consistent state—so one failure implies all should be restarted or stopped together (e.g. auth + cache + DB coordinators).

```
Parent
  ├── Worker1 (fails)
  ├── Worker2 ← Also restarted
  └── Worker3 ← Also restarted
```

## Supervision Directives

Directives tell the supervisor what action to take when a child fails.

Directives are set per error type with `WithDirective(&YourError{}, supervisor.XDirective)`. You can also use `WithAnyErrorDirective` to apply one directive to all errors.

### StopDirective

Stop the failing actor permanently: it is shut down, resources released, and removed from supervision with no restart. Use when the failure is irrecoverable, state is corrupted, or you need guaranteed cleanup.

### RestartDirective

Restart the failing actor with fresh state: the actor is stopped, `PostStop` runs, a new instance is created, `PreStart` runs, then the actor continues. Use for transient failures where re-initialization (e.g. reconnecting to a DB) fixes the issue. This is the most common choice.

### ResumeDirective

Do not restart: the error is logged, state is kept, and the actor continues with the next message. Use when the failure is non-critical, state is still valid, and the message can be skipped without affecting correctness.

### EscalateDirective

Do not handle the failure at this level: the runtime sends a **PanicSignal** to the parent. The parent can then handle it in `Receive` or report a failure (e.g. with `ctx.Err`) so that the parent's supervisor decides (Restart, Stop, etc.); the grandparent executes that directive on the parent. Use when the failure is too severe for this level or the parent must coordinate the response.

## Configuring Supervision

Create a supervisor with `supervisor.NewSupervisor(...)` and pass it when spawning: `actorSystem.Spawn(ctx, "name", &MyActor{}, actor.WithSupervisorDirective(supervisorSpec))`. The spec combines strategy, error-to-directive mapping, and optional retry limits.

**Error-specific directives:** Map each error type to a directive with `WithDirective(&YourError{}, supervisor.RestartDirective)` (and similarly for Stop, Resume, Escalate). Use custom error types (e.g. `type ValidationError struct{ error }`) so the supervisor can match them. You can combine several `WithDirective` calls for different errors (e.g. Resume for validation, Restart for DB errors, Stop for fatal).

**Any-error directive:** `WithAnyErrorDirective(supervisor.RestartDirective)` applies one directive to all errors. It overrides any error-specific directives you set.

**Retry limits:** `WithRetry(maxRetries, timeout)` limits how many restarts are allowed within the given time window. Restarts are counted in that window; if the limit is exceeded, the actor is stopped. After the timeout elapses, the window resets. Use this to avoid infinite restart loops (e.g. `WithRetry(5, time.Minute)`).

## How Failures are Reported

The supervisor only runs when the actor **reports a failure**. Three ways to do that:

1. **PreStart:** Return a non-nil error (e.g. `return &DatabaseError{err}`). The supervisor receives it and applies the directive for that error type.
2. **Panic in Receive:** Any panic is caught, wrapped, and sent to the supervisor. Use sparingly; prefer reporting via context when you can.
3. **Context error in Receive:** Call `ctx.Err(yourError)` and return. The error type is matched against the supervisor's directives. **Prefer this over panic** when you know the error: it is explicit and avoids stack unwinding.

Use custom error types that match the directives you configured (e.g. `ValidationError` → Resume, `DatabaseError` → Restart).

## Complete Example (concept)

A typical use case is a **database actor**: in `PreStart` you open a connection and return an error on failure so the supervisor can Restart. In `Receive`, on validation failure call `ctx.Err(&ValidationError{err})` (Resume), on DB failure call `ctx.Err(&DatabaseError{err})` (Restart), and in `PostStop` close the connection. Spawn the actor with a supervisor that maps those error types to Resume, Restart, and optionally Restart with `WithRetry(5, time.Minute)`. The Configuring section and the patterns below give you everything needed to wire this up; for full runnable code, see the GoAkt examples or tests.

## Supervision Patterns

### Pattern 1: Worker Pool with One-For-One

Use **OneForOne** so that when one worker fails, only that worker is restarted and the rest keep processing. In the parent's `PreStart`, create a single supervisor spec (e.g. Restart with retry) and spawn each worker with `ctx.Spawn(..., actor.WithSupervisorDirective(supervisorSpec))`. Store the child PIDs if you need to route work. No code changes are needed in the workers beyond reporting failures (e.g. `ctx.Err`) or panicking; the parent's supervisor handles them per child.

### Pattern 2: Coordinated Services with One-For-All

Use **OneForAll** when several actors (e.g. auth, cache, database) must stay in sync: if one fails, all are restarted so state is consistent. In the coordinator's `PreStart`, create one supervisor spec with `OneForAllStrategy` and a suitable directive (e.g. Restart), then spawn each service with the same spec via `WithSupervisorDirective`. If any child fails, the supervisor applies the directive to all siblings. Good for small, tightly coupled service groups.

### Pattern 3: Escalation Chain

Use a **parent–child hierarchy** so that some failures are handled at the level where they occur and others are escalated to the parent's supervisor. Each level has its own supervisor spec; when a child's directive for an error is **Escalate**, the runtime does not apply that directive itself. Instead, it sends a **PanicSignal** to the parent. The parent then decides what to do: either handle the signal in `Receive` (e.g. stop the child) or **report a failure** so that the parent's own supervisor is consulted. Reporting a failure is what makes the escalation chain work: the parent's supervisor chooses a directive (Restart, Stop, etc.), and the *grandparent* executes it on the parent (e.g. restarts or stops the parent actor).

**Recommendation:** Prefer **`ctx.Err(err)`** over panic to report that failure. Both cause the parent's supervisor to run, but `ctx.Err` is explicit, easier to reason about, and avoids stack unwinding. In `Receive`, when you handle `*goaktpb.PanicSignal`, call `ctx.Err(yourTypedError)` with an error type that matches one of the parent's directive rules so the right action (Restart, Stop, Escalate again, etc.) is chosen.

**How to implement it:** Build a chain of actors (e.g. Level1 → Level2 → Level3). Each actor gets its own supervisor via `WithSupervisorDirective` when spawned; the leaf handles minor errors with Resume and escalates others, the middle tier restarts on some errors and escalates critical ones, and the root might stop on any error. In each parent's `Receive`, handle `*goaktpb.PanicSignal` by calling `ctx.Err(...)` with the appropriate error type (or panic as a fallback). The error type must match the parent's supervisor so the correct directive is applied. When the parent is restarted by its parent, its `PreStart` runs again and can recreate child actors, so the subtree is rebuilt.

**Summary of flow:**

| Level  | Error type | Who decides  | What happens                                                      |
|--------|------------|--------------|-------------------------------------------------------------------|
| Leaf   | Minor      | Leaf's sup   | Resume (handled locally)                                          |
| Leaf   | Major      | Parent's sup | Parent's parent restarts parent; parent's PreStart recreates leaf |
| Middle | Critical   | Root's sup   | Root stops; entire subtree is torn down                           |

## Best Practices

### Do's ✅

1. **Use appropriate strategies:** OneForOne when children are independent, OneForAll when they are interdependent or must stay consistent.
2. **Map errors to directives:** Use custom error types and `WithDirective` so each kind of failure gets the right action (Resume, Restart, Stop, Escalate).
3. **Set retry limits:** Use `WithRetry(maxRetries, window)` to avoid infinite restart loops.
4. **Clean up in PostStop:** Release connections, file handles, and other resources so restarts and stops are safe.
5. **Prefer `ctx.Err` over panic:** When you know the error type, report it with `ctx.Err(yourError)` so the supervisor can choose the right directive; reserve panic for unexpected cases.
6. **Test failure scenarios:** Trigger PreStart errors, Receive panics, and `ctx.Err` in tests to confirm your supervision config behaves as intended.

### Don'ts ❌

1. **Don't ignore errors**: Always configure supervision
2. **Don't use Resume for state corruption**: Restart instead
3. **Don't restart without limits**: Set max retries
4. **Don't forget PostStop**: Resources must be cleaned up
5. **Don't use OneForAll unnecessarily**: Restart only what's needed

## Monitoring Failures

### Dead Letter Monitoring

Failed messages may end up in dead letters. Subscribe to the events stream and handle `*goaktpb.Deadletter` payloads (see [Events Stream](../events_stream/overview.md) and [Mailbox](mailbox.md#dead-letters)).

### Metrics

GoAkt exposes supervision metrics via OpenTelemetry:

- `actor_restart_total`: Total actor restarts
- `actor_stop_total`: Total actor stops
- `actor_failure_total`: Total failures

## Summary

- **Supervision** enables fault-tolerant actor systems
- **Strategies** determine which actors are affected (OneForOne vs OneForAll)
- **Directives** determine how to handle failures (Stop, Restart, Resume, Escalate)
- **Error mapping** allows different errors to be handled differently
- **Retry limits** prevent infinite restart loops
- **Parent-child hierarchy** enables escalation and coordination
