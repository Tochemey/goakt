# Unreleased

## ✨ Features

- **mDNS discovery accepts a logger** ([#1368](https://github.com/Tochemey/goakt/issues/1368)). `mdns.NewDiscovery` takes an `mdns.WithLogger` option, mirroring the NATS provider. It routes the provider's own messages and the mDNS library's log output to your logger instead of standard error. The default is the discard logger.

## 🔧 Fixes

- **etcd discovery validates `Timeout`**. A zero `Timeout` used to pass validation and then fail every etcd request at runtime with an expired context. It is now rejected at validation, in line with `DialTimeout` and `TTL`.

## ⚠️ Behavior Changes

- **mDNS discovery rebuilt on `hashicorp/mdns`** ([#1368](https://github.com/Tochemey/goakt/issues/1368)). Every node now registers its own instance name, `ServiceName` plus a short random suffix, and carries the cluster name in its TXT record. The previous provider registered the same instance name on every node, which the mDNS specification forbids and which only worked by accident of the old client. Configuration is unchanged, and `Domain` accepts `local` with or without the trailing dot.

  Nodes on the previous provider and nodes on this one do not discover each other. Upgrade an mDNS cluster by stopping every node and starting them on the new version; a rolling upgrade would run two clusters with the same name until the last old node is replaced. Peers now learn one IPv4 address and, when `IPv6` is set, one IPv6 address per node instead of every address of a multi-homed host. Queries travel over IPv4 multicast, and IPv6 addresses come from the AAAA records in the answers, so IPv6-only networks are not supported.

- **Sorted results where the order used to be unspecified**. The Kubernetes, mDNS and DNS-SD providers return peer addresses sorted with duplicates removed, `Peer.Roles` comes back sorted, and `ActorSystem.Grains` returns identities in sorted order. Callers that relied on a random order are unaffected; callers that compared slices by position get a stable order.

- **Retry delays follow a new curve**. The retries behind spawn, actor and grain initialization, routee restart, relocation, reliable delivery and NATS discovery now run on `cenkalti/backoff`. Attempt counts, defaults, terminal errors and the error returned to callers are unchanged. The delay between attempts doubles with up to fifty percent jitter within the same bounds as before, instead of the previous full jitter scheme.

## 🗑️ Deprecations

- **Kubernetes discovery: `RemotingPortName` and `PeersPortName`**. The provider only ever read `DiscoveryPortName` to build peer addresses; peers learn each other's remoting and peers ports from the node metadata exchanged once they are members. Both fields are deprecated, no longer required by validation, and ignored. Configurations that set them keep working.

## 📚 Documentation

- **Every built-in discovery provider is documented**. The service discovery page now has a section per provider with how it registers and discovers, each configuration field with its default and whether it is required, and a complete example. It adds the Role a Kubernetes service account needs, the note that DNS-SD returns addresses without a port so every node must share one discovery port, and corrects the configuration example that called a `New` constructor no provider has.

## ⬆️ Dependencies

- **Four modules removed from the dependency graph**. `github.com/deckarep/golang-set/v2` is replaced by maps and sorted slices. `github.com/flowchartsman/retry`, unmaintained since December 2020, is replaced by an internal package with the same API on `github.com/cenkalti/backoff/v7`. `github.com/grandcat/zeroconf`, unmaintained since January 2023, is replaced by `github.com/hashicorp/mdns` v1.0.7 ([#1368](https://github.com/Tochemey/goakt/issues/1368)), which also drops the `github.com/cenkalti/backoff` v2 it pulled in. `github.com/kapetan-io/tackle`, used only by tests, is replaced by an internal loader for the TLS fixtures under `test/data/certs`.
