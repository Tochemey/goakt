# Unreleased

## ✨ Features

- **Message scheduling for grains** ([#1372](https://github.com/Tochemey/goakt/issues/1372)). `ScheduleGrainOnce`, `ScheduleGrain` and `ScheduleGrainWithCron` deliver a message to a grain once after a delay, at a fixed interval, or on a cron expression. Delivery is a one-way `TellGrain`: it reaches the grain wherever it lives and activates it again if it has passivated. Grain schedules share references, `CancelSchedule`, `PauseSchedule`, `ResumeSchedule`, metrics and cluster rules with actor schedules, and `ListSchedules` reports them through the new `ScheduleInfo.Grain` field. They outlive the grain's activation and, like actor schedules, are not persisted.

- **One-way tell for grains** ([#1370](https://github.com/Tochemey/goakt/issues/1370)). `TellGrain` on the actor system and on `GrainContext` accepts `actor.WithOneWay()`, and `client.TellGrain` accepts `client.WithOneWay()`. The call returns as soon as the message is enqueued; errors before the enqueue are still returned, and a handler failure or panic is recorded as a deadletter. Without the option the call keeps waiting for the acknowledgement. During a rolling upgrade, older nodes ignore the new `one_way` field and acknowledge after processing, as before.

- **mDNS discovery accepts a logger** ([#1368](https://github.com/Tochemey/goakt/issues/1368)). `mdns.NewDiscovery` takes `mdns.WithLogger`, which routes the provider's and the library's output to your logger. The default is the discard logger.

## 🔧 Fixes

- **Actor addresses carry the full path** ([#1379](https://github.com/Tochemey/goakt/issues/1379)). An address now renders every ancestor of the actor, `goakt://system@host:port/grand/parent/name`, and parses it back, as the documentation describes. Before, only the immediate parent was written, so two actors nested under parents with the same name shared one address and the second `SpawnChild` returned the first actor instead of creating one. Top-level actors and direct children keep the addresses they had. Large-message destination patterns now see the full path, one `*` per segment. During a rolling upgrade, nodes on the previous version reject the addresses of actors nested two or more levels deep until they are upgraded.

- **Same-named children no longer overwrite or delete each other's records** ([#1380](https://github.com/Tochemey/goakt/issues/1380)). Two children with the same name under different parents used to share one cluster registry record and one local name entry: the second spawn replaced the first, and stopping either deleted the record of the other while it was still running. Registry records are now keyed by the qualified actor name, the names of its ancestors and its own joined by `/` (`p1/kid`), and the local name index keeps every actor with a name. Top-level actors keep the name-based lookups they had. A child is found by its qualified name from any node (`ActorOf(ctx, "p1/kid")`) and by its bare name on its own node, which returns the most recently spawned child with that name. Looking a child up by its bare name from another node now returns not found. Spawning a top-level actor with the name of a running child now creates the actor instead of returning the child, and `Reinstate` of a child now finds that child. During a rolling upgrade, a child's record is keyed by the node that hosts it: by its bare name on a node running the previous version, by its qualified name on an upgraded node. Until every node is upgraded, a child is found from another node only under the key its host wrote; top-level actors are unaffected.

- **etcd discovery validates `Timeout`**. A zero `Timeout` is now rejected at validation instead of failing every request at runtime, in line with `DialTimeout` and `TTL`.

- **A wildcard bind address listens on every interface** ([#1374](https://github.com/Tochemey/goakt/issues/1374)). `remote.NewConfig("0.0.0.0", port)` used to bind the remoting server to one guessed private IP, so connections through loopback or any other interface were refused, and start failed on a host with no private or public address. The remoting server now listens on the wildcard as configured, `::` is accepted and serves IPv4 and IPv6 peers, and a host with no other address advertises loopback. What peers are told is unchanged: `ActorSystem.Host()`, actor addresses and cluster membership keep advertising the first private IP, and cluster gossip and the registry keep binding it. Bind to a concrete IP to listen on one interface only.

## ⚠️ Behavior Changes

- **mDNS discovery rebuilt on `hashicorp/mdns`** ([#1368](https://github.com/Tochemey/goakt/issues/1368)). Each node registers its own instance name and carries the cluster name in its TXT record; configuration is unchanged. Nodes on the old and the new provider do not discover each other, so upgrade an mDNS cluster with a full stop and restart rather than a rolling upgrade. Peers learn one IPv4 address and, with `IPv6` set, one IPv6 address per node; IPv6-only networks are not supported.

- **Sorted results where the order used to be unspecified**. Kubernetes, mDNS and DNS-SD peer addresses (deduplicated), `Peer.Roles` and `ActorSystem.Grains` now come back sorted.

- **Retry delays follow a new curve**. The retries behind spawn, initialization, routee restart, relocation, reliable delivery and NATS discovery now run on `cenkalti/backoff`: delays double with up to fifty percent jitter within the same bounds. Attempt counts, defaults and returned errors are unchanged.

## 🗑️ Deprecations

- **Kubernetes discovery: `RemotingPortName` and `PeersPortName`**. Both fields are deprecated and ignored; peers learn these ports from the node metadata exchanged on membership. Existing configurations keep working.

## 📚 Documentation

- **Every built-in discovery provider is documented**: how it registers and discovers, each configuration field with its default, and a complete example, on the service discovery page.

## ⬆️ Dependencies

- **Four modules removed**: `github.com/deckarep/golang-set/v2`, `github.com/flowchartsman/retry` (replaced by an internal package on `github.com/cenkalti/backoff/v7`), `github.com/grandcat/zeroconf` (replaced by `github.com/hashicorp/mdns` v1.0.7, [#1368](https://github.com/Tochemey/goakt/issues/1368)) and `github.com/kapetan-io/tackle`.
