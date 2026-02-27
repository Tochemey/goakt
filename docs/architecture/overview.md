# Architecture Overview

## Bird's eye view

GoAkt is a framework for building **concurrent, distributed, and fault-tolerant systems** in Go using the actor model.
Every unit of computation is an **actor**—a lightweight, isolated entity that communicates exclusively through messages.
The [Actor System](../concepts/actor-system.md) is the top-level runtime that hosts actors and orchestrates messaging, clustering, and lifecycle.

## Three deployment modes

| Mode                 | Description                                                                                                 |
|----------------------|-------------------------------------------------------------------------------------------------------------|
| **Standalone**       | Single process. Actors communicate in-process.                                                              |
| **Clustered**        | Multiple nodes. Discovery via Consul, etcd, Kubernetes, NATS, mDNS, or static. Location-transparent actors. |
| **Multi-Datacenter** | Multiple clusters across DCs. Pluggable control plane (NATS JetStream or etcd). DC-aware placement.         |

## Message flow

```
Sender                    Transport                    Receiver
------                    ---------                    --------
[Actor/Client]            [local / TCP]                [Target Actor]
     |                                                      |
     |-- Tell(message) ---------------------------------->  |
     |                                                      | enqueue -> mailbox
     |                                                      | dequeue -> Receive(ctx)
     |                                                      |
     |-- Ask(message, timeout) --------------------------> |
     | <--------------------------- response -------------- |
```

For remote messages, the remoting layer serializes the payload over a custom TCP frame protocol with optional
compression.

## Actor hierarchy

```
/ (root guardian)
|-- /system     <- internal actors (dead letter, scheduler, topic actors...)
+-- /user       <- all user-defined actors
    |-- /user/orderService
    |   |-- /user/orderService/inventory
    |   +-- /user/orderService/payment
    +-- /user/reportService
```

When a parent stops, all children stop first (depth-first). A parent supervises its children.

## Cluster architecture

```
+------------------------------------------------------------------+
| GoAkt Cluster Node                                               |
|                                                                  |
| +--------------+   +---------------+   +-------------------+     |
| | ActorSystem  |-->| Cluster       |-->| Discovery         |     |
| |              |   | (Olric DMap)  |   | (K8s/Consul/...)  |     |
| +------+-------+   +---------------+   +-------------------+     |
|        |                                                         |
| +------v-------+   +---------------+                             |
| | Remoting     |-->| TCP Server    | <-- connections from peers  |
| | (TCP/TLS)    |   | (frame proto) |                             |
| +--------------+   +---------------+                             |
+------------------------------------------------------------------+
```

Cluster state is stored in Olric (distributed hash map). Node membership uses Hashicorp Memberlist.
See [Code Map](code-map.md) for package layout.
