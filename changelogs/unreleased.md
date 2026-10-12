# Unreleased

## ⚠️ Behavior Changes

- **`ActorSystem.Partition` reports failures as errors instead of partition `0`** ([#1456](https://github.com/Tochemey/goakt/issues/1456), see `playground/issue-1456`). The signature is now `Partition(ctx, actorName) (uint64, error)`. It returns the registry partition that holds the actor's record, `ErrActorNotFound` when no actor is registered under the name, `ErrClusterDisabled` outside cluster mode and `ErrActorSystemNotStarted` on a stopped actor system. Before, all of these returned `0`, which is also a valid partition. Callers must pass a context and handle the error.

## 🔧 Fixes

- **Each clustered actor system places registry keys with its own partition hasher** ([Tochemey/olric#53](https://github.com/Tochemey/olric/issues/53)). The cluster engine stored its key hash in a process-wide variable set by the first engine started, so a second clustered actor system in the same process with a different `ClusterConfig.WithPartitionHasher` placed and looked up registry records with the first system's hasher, while its peers in other processes used the configured one. Every system now uses its own hasher.
  - No action is needed when every actor system in a process uses the same partition hasher, or a process runs a single actor system.
  - A process that runs clustered actor systems with different partition hashers now places their keys differently. Do not roll the upgrade across such a cluster: stop the whole cluster and start it on the new version.

## Dependencies

- **Upgraded `github.com/tochemey/olric` to `v0.3.23-0.20261012205706-90e6c2c7a656`** ([Tochemey/olric#53](https://github.com/Tochemey/olric/issues/53)). It brings the per-instance key hashing above.
