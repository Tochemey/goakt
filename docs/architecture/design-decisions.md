# Design Decisions

## Why `any` instead of `proto.Message` (v4)?

v4 allows any type as actor messages. Benefits:

- **Flexibility** — Plain Go structs, protobuf, or custom types
- **Simplicity** — No mandatory .proto definitions for simple cases
- **CBOR** — Efficient serialization for arbitrary Go types

ProtoSerializer remains the default for protobuf messages. CBOR and custom serializers extend the set of supported
types.

## Why a custom TCP frame protocol?

GoAkt uses length-prefixed binary frames over TCP instead of gRPC:

- **Low overhead** — No HTTP/2, HPACK, or stream multiplexing
- **Control** — Connection pools, compression, buffer pooling tuned for actor traffic
- **Fewer dependencies** — Leaner than gRPC

## Why Olric for cluster state?

Cluster state (actor/grain placement) needs replication. Olric provides:

- **Embedded** — No external database
- **Distributed hash map** — Configurable quorum for consistency
- **Memberlist** — Same membership layer as the cluster

## Why a tree-based actor hierarchy?

Mirrors Erlang/OTP and Akka:

- **Lifecycle ordering** — Stopping a parent stops descendants first (depth-first)
- **Scoped supervision** — Parent defines failure policy for children
- **Namespacing** — Addresses reflect tree path; no name collisions

## Why separate Actor and Grain?

- **Actors** — Explicit spawn/stop; caller controls lifecycle. Best for services and infrastructure.
- **Grains** — Identity-addressed; framework manages activation and passivation. Best for entity-per-identity patterns.

Both share the same runtime; choose based on use case.
