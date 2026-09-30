# Issue 1400: the dispatcher worker pool is sized by GOMAXPROCS and cannot be configured

https://github.com/Tochemey/goakt/issues/1400

Every actor and grain turn runs on one shared dispatcher pool of
`max(GOMAXPROCS, 2)` workers. A grain whose `OnReceive` waits on I/O (a
database transaction, an RPC) holds a worker for the whole wait, so a node
handles at most GOMAXPROCS such turns at a time, whatever the number of
grains.

The sample runs 200 independent grains, each asked 5 times, with an
`OnReceive` that sleeps 10 ms as a stand-in for a database call, and all
1000 asks in flight at once. The work of one grain is 50 ms. It measures
the node with the default pool and with a pool of 256 workers.

## Actual (before the fix)

On two CPUs the node needs 1000 × 10 ms / 2, and there is no option to
change it (`WithDispatcherPoolSize` does not exist, so the sample does
not build against `main`; its first measurement is what `main` does):

```
GOMAXPROCS=2, 200 grains x 5 messages x 10ms = 50ms of work per grain
default pool:       5.867s (1000 messages / 5.867s = 170 msg/s)
```

## Expected (after the fix)

```
256 workers:        59ms (1000 messages / 59ms = 16823 msg/s)
PASS: with a sized pool, 200 independent grains finish in 1.2 times the work of one grain
```

## Run

```bash
GOMAXPROCS=2 go run ./playground/issue-1400
```

`GOMAXPROCS=2` stands for a small pod; with more CPUs the default pool is
larger and the first number shrinks in proportion, the second does not
change. `TestWithDispatcherPoolSize` in `actor` covers the option.
