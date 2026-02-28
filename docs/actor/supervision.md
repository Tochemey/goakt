---
title: Supervision
description: One-for-one and one-for-all strategies, directives, and retry.
sidebarTitle: "🛡️ Supervision"
---

## What is supervision?

Every actor has a parent. When a child fails (panics, returns an error from a handler), the parent's **supervisor**
decides what to do. This keeps failures localized and recoverable.

## Strategies

| Strategy      | Effect                                          |
|---------------|-------------------------------------------------|
| **OneForOne** | Only the failing child is affected              |
| **OneForAll** | All siblings are affected (e.g., all restarted) |

Choose OneForOne when failures are independent; OneForAll when siblings share critical state.

## Directives

| Directive    | Action                                        |
|--------------|-----------------------------------------------|
| **Resume**   | Ignore the failure; continue processing       |
| **Restart**  | Re-initialize the child (PreStart runs again) |
| **Stop**     | Terminate the child                           |
| **Escalate** | Pass the failure to the grandparent           |

Configure retry windows (max retries within a time period) to avoid infinite restart loops. After the window is
exceeded, the supervisor can escalate.

## Supervisor configuration

Supervisors are created with `supervisor.NewSupervisor(opts...)` and passed via `WithSupervisor` when spawning. Key
options:

| Option                              | Purpose                                  |
|-------------------------------------|------------------------------------------|
| `WithDirective(errType, directive)` | Map a specific error type to a directive |
| `WithAnyErrorDirective(directive)`  | Catch-all for any error                  |
| `WithRetry(maxRetries, timeout)`    | Bound restarts within a time window      |
| `WithStrategy(strategy)`            | `OneForOne` (default) or `OneForAll`     |

Directives: `Resume`, `Restart`, `Stop`, `Escalate`. When the retry window is exceeded, the supervisor escalates to the
grandparent.
