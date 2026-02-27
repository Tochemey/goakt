# Serialization

## Overview

Messages crossing process boundaries must be serialized. GoAkt v4 supports pluggable serializers. The default is
**ProtoSerializer** for `proto.Message`. **CBORSerializer** supports arbitrary Go types.

## ProtoSerializer (default)

Protobuf messages use the default serializer automatically. No registration needed for `proto.Message` types.

## CBOR for plain Go structs

For plain Go structs, use `remote.WithSerializers(new(MyMessage), remote.NewCBORSerializer())` when creating the remote
config. Types are registered automatically in the type registry. Both sender and receiver must register the same types
via `WithSerializers`; for receive-only types, register them the same way — the type is auto-registered for
deserialization.

### How Go types are magically serialized

When you pass a concrete Go type to `WithSerializers` with `CBORSerializer`, the type is **automatically registered**
in a global type registry. There is no separate registration step — no `RegisterSerializableTypes` or similar.

**One line does it all:**

```go
cfg := remote.NewConfig("0.0.0.0", 9000,
    remote.WithSerializers(new(MyMessage), remote.NewCBORSerializer()),
)
```

Under the hood:

1. **On config build:** When `WithSerializers(new(MyMessage), remote.NewCBORSerializer())` is applied, GoAkt detects
   that the serializer is `CBORSerializer` and the type is a concrete non-proto struct. It registers `MyMessage` in a
   global type registry keyed by the type's reflected name.

2. **On serialize:** When a message is sent, `CBORSerializer` looks up the type name in the registry, encodes the
   value as CBOR, and prepends a self-describing frame (total length, type name length, type name, payload). The
   receiver can reconstruct the exact Go type from the type name.

3. **On deserialize:** When bytes arrive, the frame header is parsed, the type name is extracted, and the registry is
   consulted to resolve the concrete Go type. A new instance is allocated and the CBOR payload is unmarshaled into it.

Both sender and receiver must register the same types via `WithSerializers`. For types you only receive (never send),
register them the same way — the type is auto-registered for deserialization. Proto message types are excluded from
this registry; they use protobuf's own type resolution.

## The Serializer interface

Custom serializers must implement:

```go
type Serializer interface {
    Serialize(message any) ([]byte, error)
    Deserialize(data []byte) (any, error)
}
```

| Method          | Purpose                                                                                                                                                   |
|-----------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------|
| **Serialize**   | Encode a message into bytes. The encoding must be **self-describing** so the receiver can reconstruct the concrete type without out-of-band coordination. |
| **Deserialize** | Decode bytes back into the original Go value. The dynamic type must match what was passed to Serialize.                                                   |

Implementations must be safe for concurrent use. Register via `WithSerializers(msgType, serializer)` on the remote
config.

## Wire format

Frames use: total length, type name length, type name, payload. Both client and server must use compatible serializers
and compression.
