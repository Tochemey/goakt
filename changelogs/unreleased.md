# Unreleased

## ⚡ Performance

- **Smaller idle-actor memory footprint.** A spawned, started, idle actor now holds about 1,268 B of live heap, down from about 2,124 B, roughly 40 percent less, with fewer heap objects each (18 to 13). At one million idle actors on a node that is roughly 1.18 GB of live heap instead of 1.98 GB, and long-lived actors (`WithLongLived`) drop from about 1,966 B to about 1,110 B, making dense deployments of many light actors cheaper to keep resident. The savings come from:
  - Moving spawn settings most actors never set (reliable-delivery configuration, durable queues, singleton spec, role, metrics identity) off the PID into a companion allocated only when one of them is used.
  - Looking the logger and event stream up from the actor system on demand instead of copying them onto every actor.
  - Replacing each actor's system-message mailbox with a sentinel-free inline queue, so an idle actor holds no control-plane node.
  - Embedding the default user mailbox in the PID rather than in a separate heap object.
  - Holding each actor's watchers in a small slice instead of a map, which for the usual two watchers cost a header and a bucket group plus a copy of each watcher's ID.
  - Making the actor path a thin view over its address instead of a struct that re-copied the host, port, name, system, and cached strings the address already holds.

  Actor behavior is unchanged: one message at a time, system messages ahead of user messages, and send order preserved. Throughput is unchanged and `Tell` remains allocation-free. Custom mailboxes set with `WithMailbox` are unaffected.

## 🔧 Fixes

- **Cluster membership events follow routing table convergence** ([#1331](https://github.com/Tochemey/goakt/issues/1331)). `NodeLeft` and `NodeJoined` are now published once the cluster's routing table has converged on the membership change, using the member set olric announces with each convergence, instead of waiting for the single rebalance epoch started for the change. A departure whose epoch was superseded by a later routing table push used to surface only through the 30s fallback timer, and a join in the same situation could go unreported until the next join. A node that joins and departs, or departs and restarts, before the table converges is announced in the order the converged member set implies. A member that departs, restarts at the same address and departs again is announced each time, where the second departure was previously lost. A join whose convergence never comes is announced after the same 30s bound as a departure, and that wait is cancelled as soon as the event is announced or the actor system stops.

- **A transient cluster cleanup failure no longer shuts down a healthy node** ([#1337](https://github.com/Tochemey/goakt/issues/1337)). When removing a dead actor's record from the cluster registry failed, DeathWatch escalated the error to the system guardian, which stopped the whole ActorSystem. A transient registry error during a membership transition could therefore turn one node's departure into a cascading outage. DeathWatch now logs the failure, keeps running, and retries the removal up to five times with exponential backoff through the system scheduler. A record still stuck after the retries is reported with an error log, and no retry is attempted when the cluster engine is stopped or the system is stopping. Any other DeathWatch failure still escalates as before.

- **Actors and grains carry their role constraint through relocation and remote activation** ([#1334](https://github.com/Tochemey/goakt/issues/1334)). Actors spawned with `WithRole` and grains activated with `WithActivationRole` lost their role when recreated on another node, because `SpawnOn`, the grain cluster record, and the remote activation request did not carry it. All three paths now transport the role, and eager grain relocation honors it: a role-constrained eager grain is reassigned to the least-loaded surviving node advertising its role, and reported in `RelocationFailed` when no surviving node qualifies. Lazy grains stay unconstrained during relocation; their role is re-applied at the next activation.

## 📦 Dependencies

- Updated Go to v1.26.6 (`go.mod` and the build Dockerfile).
