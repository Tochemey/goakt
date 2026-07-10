# Issue 1252: remoting poisoned by an expired Start context

Starting a remoting-enabled system with a bounded context (`context.WithTimeout` around `Start`, the pattern DI frameworks hand to OnStart hooks) used to store that context as the remoting server's long-lived base context. Once the startup budget elapsed, every inbound handler context was born already `DeadlineExceeded`, so every remote operation against the node failed with `context deadline exceeded` / `request timed out` until the process restarted - while the process itself looked healthy.

## Expected vs actual

- **Expected**: the server's lifetime is governed by `Stop()`; a startup context expiring after startup completed has no effect on a running node.
- **Actual (before the fix)**: all remote lookups/asks against the node fail permanently once the startup context expires.

## Run

```bash
go run ./playground/issue-1252
```

Prints `OK: remoting survives an expired Start context` with the fix (`startRemoteServer` detaches the server base context via `context.WithoutCancel`); before the fix it exits with a `REPRO (broken)` fatal on the first remote call.
