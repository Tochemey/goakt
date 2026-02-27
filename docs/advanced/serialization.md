# Serialization

## Overview

Messages crossing process boundaries must be serialized. GoAkt v4 supports pluggable serializers. The default is *
*ProtoSerializer** for `proto.Message`. **CBORSerializer** supports arbitrary Go types.

## ProtoSerializer (default)

Protobuf messages use the default serializer automatically. No registration needed for `proto.Message` types.

## CBOR for plain Go structs

For plain Go structs, use `remote.WithSerializers(new(MyMessage), remote.NewCBORSerializer())` when creating the remote
config. Both sender and receiver must register the same types. For receive-only types, use
`remote.RegisterSerializableTypes(new(MyMessage))` before creating the config.

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
