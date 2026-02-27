# Clustered Mode

## Overview

In clustered mode, multiple nodes form a cluster. Actors are location-transparent: you send to a PID, and the framework
routes to the correct node. Discovery backends (Consul, etcd, Kubernetes, NATS, mDNS, static) tell the cluster how to
find peers.

## When to use

- High availability and horizontal scaling
- Actor distribution across machines
- Cluster [singletons](../actor/singletons.md) and relocation

## Key components

- **Discovery** — Pluggable provider for peer discovery
- **Cluster registry** — Distributed map (Olric) for actor/grain placement
- **Remoting** — TCP-based message transport between nodes

## Configuration

Configure the actor system with `WithCluster(clusterConfig)` and a discovery provider. Configure remoting with
`WithRemote(config)`. The cluster joins membership, and actors can be looked up via `ActorOf` across nodes. See [Service Discovery](service-discovery.md) for provider options.

## Relocation

When a node leaves, the leader relocates its actors and grains to remaining nodes. Singleton actors move to the leader;
others are distributed. Actors can opt out of relocation via spawn options. See [Relocation](../actor/relocation.md) for
the full flow, configuration, and relocatability requirements.
