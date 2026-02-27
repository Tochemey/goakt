# Relocation

**Relocation** is the automatic migration of actors and grains from a node that has left the cluster to the remaining live nodes. When a node shuts down **gracefully**, its relocatable actors and grains are recreated on other nodes so the cluster remains available.

> **Note:** Relocation requires a graceful shutdown. If a node crashes (kill -9, OOM, etc.), `preShutdown` and `persistPeerStateToPeers` never run, so peer state is not replicated and relocation does not occur. Actors and grains on a crashed node are lost.

## When relocation happens

Relocation is triggered when:

1. **A node leaves the cluster** — The cluster membership layer (Hashicorp Memberlist) detects the departure and emits a `NodeLeft` event.
2. **The departed node had relocatable actors or grains** — Only actors spawned with relocation enabled (the default) and grains without `WithGrainDisableRelocation` are eligible.

Relocation does **not** run in standalone mode; it requires cluster mode and remoting.

## Relocation flow

```
Node A (leaving)                    Cluster leader                    Remaining nodes
-------------                      --------------                    ---------------
1. Stop() called
2. preShutdown() builds
   PeerState (actors + grains)
3. shutdownCluster():
   persistPeerStateToPeers() -----> Up to 3 oldest peers store state (2-of-3 quorum)
   cleanupCluster() removes actors/grains from cluster map
   cluster.Stop() leaves membership
4. NodeLeft event emitted

                                   4. Leader receives NodeLeft
                                   5. Fetches PeerState from cluster store
                                   6. Enqueues Rebalance to relocator
                                   7. Relocator allocates actors/grains
                                      - Singletons -> leader
                                      - Others -> distributed across peers
                                   8. recreateLocally() / spawnRemoteActor()
                                      / recreateGrain() / activateRemoteGrain()
                                   9. RebalanceComplete
                                   10. Delete PeerState from store
```

### Key steps

1. **preShutdown** — When a node stops, it builds a `PeerState` snapshot of all relocatable actors and grains. Actors with `WithRelocationDisabled` and grains with `WithGrainDisableRelocation` are excluded.

2. **persistPeerStateToPeers** — Before leaving membership, the node replicates its `PeerState` to **up to 3 oldest** cluster peers via RPC (not all remaining peers). The implementation uses `selectOldestPeers(3)` to pick the three oldest nodes by `CreatedAt`; if fewer than 3 peers exist, it replicates to all. Replication returns successfully as soon as **2-of-3** peers acknowledge (quorum); remaining RPCs are cancelled. Each peer stores the state in its local cluster store (e.g. BoltDB). The oldest peers are chosen because leadership is determined by node age—the oldest nodes are most likely to remain or become leader when the departing node leaves, so the new leader will have the state when it handles `NodeLeft`.

3. **cleanupCluster** — The departing node removes its actors and grains from the cluster map (Olric) and, if leader, removes singleton kinds. This runs after persist and before leaving membership.

4. **NodeLeft** — The cluster emits a `NodeLeft` event. The **leader** node handles it: it fetches the departed node's `PeerState` from its local cluster store (the leader must have been one of the up-to-3 peers that received the state) and enqueues a `Rebalance` message for the relocator.

5. **Relocator** — A system actor receives `Rebalance` and:
   - Allocates actors: singletons go to the leader; non-singletons are distributed across leader + peers.
   - Allocates grains: same distribution.
   - For actors on the leader: `recreateLocally` removes the actor from the cluster map, then spawns it locally (or via `SpawnSingleton` for singletons).
   - For actors on peers: `spawnRemoteActor` removes from cluster map, then `RemoteSpawn` on the target peer.
   - For grains: `recreateGrain` or `activateRemoteGrain` on the target node.

6. **RebalanceComplete** — The relocator tells the system guardian, which marks relocation done and deletes the departed node's state from the cluster store.

## Actor allocation

| Actor type        | Destination                                 |
|-------------------|---------------------------------------------|
| **Singleton**     | Leader node only                            |
| **Non-singleton** | Distributed across leader + peers (chunked) |

Only **relocatable** actors are recreated. Actors with `WithRelocationDisabled` are skipped. System actors (e.g. dead letter, scheduler) are never relocated.

## Grain allocation

Grains are distributed similarly: remainder to the leader, then chunks to peers. Grains with `WithGrainDisableRelocation` are skipped.

## Configuration

### Disable relocation for an actor

Use `WithRelocationDisabled` when spawning:

```go
pid, err := system.Spawn(ctx, "my-actor", actor, actor.WithRelocationDisabled())
```

When the host node leaves, this actor is **not** recreated elsewhere. Use for:

- Node-local state or resources (e.g. local files, device handles)
- Actors that cannot be safely recreated without external state

### Disable relocation for a grain

```go
cfg := newGrainConfig(WithGrainDisableRelocation())
system.RegisterGrain(MyGrainType, factory, cfg)
```

If the hosting node leaves, the grain instance is lost. Re-addressing the grain by ID will create a new instance on another node; any in-memory state is gone. Persist state externally if you need recovery.

### Disable relocation system-wide

Use `WithoutRelocation()` when creating the actor system:

```go
system, err := actor.NewActorSystem("app",
    actor.WithCluster(config),
    actor.WithoutRelocation(),
)
```

When relocation is disabled:

- `preShutdown` skips building and persisting peer state
- No actors or grains are relocated when nodes leave
- Useful for development, testing, or when you manage placement yourself

## Child actors

**Child actors are not relocatable by default.** When a parent spawns a child via `SpawnChild`, the child gets `withRelocationDisabled()` implicitly. Children are tied to their parent's lifecycle; they are not independently relocated.

If a parent is relocated, its children are **not** relocated with it. They are recreated only as part of the parent's `PreStart` (or equivalent) on the new node, if the parent explicitly spawns them again.

## Relocatability requirements

For an actor to be relocated successfully:

- **Actor type** must be registered (reflection) so the relocator can instantiate it.
- **Dependencies** must implement `Dependency` (serializable). Pass via `WithDependencies(dep)`.
- **Supervisor**, **passivation**, **reentrancy**, **stashing**, **role** — these are encoded in the actor's serialized form and restored on the target node.

See [Extensions and Dependencies](../advanced/extensions-and-dependencies.md) for dependency serialization.

## During relocation

- The actor may **temporarily not exist**: it is removed from the cluster map before being recreated on the target node.
- Callers should retry or tolerate `ActorNotFound` / lookup failures during this window.
- The actor's logical identity (name, grain ID) is preserved; only the physical location (host:port) changes.
- Location transparency means you address by PID or grain ID; the framework routes to the new node once relocation completes.

## Check relocatability

| API                     | Purpose                                      |
|-------------------------|----------------------------------------------|
| `pid.IsRelocatable()`   | Returns whether the actor may be relocated.  |
| `pid.Path().HostPort()` | Current host:port; changes after relocation. |

## See also

- [Clustered Mode](../clustering/clustered.md) — Cluster setup and discovery
- [Singletons](singletons.md) — Cluster singletons and placement
- [Grains](../grains/overview.md) — Virtual actors and `WithGrainDisableRelocation`
- [Extensions and Dependencies](../advanced/extensions-and-dependencies.md) — Serializable dependencies for relocation
- [Coordinated Shutdown](../advanced/coordinated-shutdown.md) — Shutdown sequence and `preShutdown`
