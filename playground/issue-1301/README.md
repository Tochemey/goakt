# issue-1301 — duplex remoting throughput

Living sample for [#1301](https://github.com/Tochemey/goakt/issues/1301): measure the multiplexed duplex remoting engine on a topology adapted from the [goakt-remoting](https://github.com/Tochemey/goakt-examples/tree/main/goakt-remoting) ping/pong example.

Both nodes run in one process (playground-friendly). Remoting is pinned to `ProtocolPinDuplex` with `NoCompression` so the number reflects the new path, not legacy coalescing or compression.

## Run

```bash
# Fire-and-forget blast (default): 20 senders for 10s
go run ./playground/issue-1301

# Tune blast
go run ./playground/issue-1301 -duration=10s -senders=20

# Compare legacy unary path
go run ./playground/issue-1301 -pin=legacy

# RTT ping/pong (closer to the original example shape)
go run ./playground/issue-1301 -mode=pingpong -rounds=100000

# Warmed-lane goroutine decomposition (goroutine budget plan cell)
go run ./playground/issue-1301 -mode=lanes

# Lane-sharded coalescing scaling (blast across many receivers)
go run ./playground/issue-1301 -lanes=4 -receivers=8

# End-to-end residency bound (credit window sweep; 0 keeps the 16MiB default)
go run ./playground/issue-1301 -mode=stalledrecv -window=2097152

# Goroutine budget plan cells
go run ./playground/issue-1301 -mode=ask            # ask throughput + p50/p99
go run ./playground/issue-1301 -mode=isolation      # slow-actor isolation
go run ./playground/issue-1301 -mode=largesmall     # 1 MiB transfers + small-ask p99
go run ./playground/issue-1301 -mode=controllatency # RemoteLookup p99 under blast
go run ./playground/issue-1301 -mode=stalledrecv    # stalled consumer memory/backpressure
go run ./playground/issue-1301 -pin=auto -pongpin=legacy  # mixed-version auto-pin fallback
```

## What it prints

**blast** (primary throughput gate)

- `sent` / `received` / `errors`
- `delivered/sent` — should be ≈ 1.0 when the engine keeps up
- `throughput` — receiver-observed msgs/sec over `-duration`
- `-lanes=N` sets `remote.WithOrdinaryLanes` (default 1); `-receivers=N`
  spawns N pong actors so receiver hashing spreads traffic across the lane
  shards (single-receiver traffic stays on one shard by design)

**pingpong**

- round-trips/sec and remoting messages/sec (2 tells per round-trip)

## Notes

- Discard logger is intentional; Debug logging would dominate the result.
- Blast uses `PID.Tell` to a `RemoteLookup` target so traffic rides the actor system’s remoting client (same path production `Tell` uses).
- For a larger fan-out harness see `benchmark/remote_tell_throughput_test.go`.
