# Issue 1444: a NodeLeft handled after Stop panics on a nil cluster store

https://github.com/Tochemey/goakt/issues/1444

A node handles a `NodeLeft` on its cluster events goroutine. When the node
stops while it is handling one, `Stop` does not wait for the handler: it
resets the system, which clears the cluster store, and the handler then reads
that store and panics. The panic is on a goroutine GoAkt starts, so it ends
the process.

The sample runs a three-node cluster. Node 3's logger holds the goroutine that
logs node 3's `detected node left event` line for node 1. The `NodeLeft`
handler logs that line after it checked that the system is running, so holding
it puts the handler in the window the issue describes every time:

1. node 1 stops; node 3 starts handling its departure and is held
2. node 3 stops; `Stop` returns while the handler is still held
3. the handler is released and carries on against the stopped system

In production the window is opened by a slow logger or by the scheduler: a
peer leaves while the node finishes its own `Stop`, as in a rolling restart.

## Actual (before the fix)

```
node 3 is handling the departure of node 1
node 3 stopped while handling the departure of node 1
panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: segmentation violation code=0x2 addr=0x20 pc=0x105f6fab8]

goroutine 558 [running]:
github.com/tochemey/goakt/v4/actor.(*actorSystem).handleNodeLeftEvent(0x64477e430008, 0x64477cbeee70)
	actor/actor_system.go:4079 +0x958
github.com/tochemey/goakt/v4/actor.(*actorSystem).handleClusterEvent(0x64477e430008, 0x64477cbeee70)
	actor/actor_system.go:3922 +0x4e8
github.com/tochemey/goakt/v4/actor.(*actorSystem).clusterEventsLoop(...)
	actor/actor_system.go:3888
created by github.com/tochemey/goakt/v4/actor.(*actorSystem).startCluster in goroutine 1
	actor/actor_system.go:3390 +0x234
exit status 2
```

## Expected (after the fix)

```
node 3 is handling the departure of node 1
node 3 stopped while handling the departure of node 1
PASS: node 3 handled the departure of node 1 after its Stop returned without crashing
```

## Run

```bash
go run ./playground/issue-1444
```
