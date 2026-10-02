# Issue 1411: a remote reply is decoded with the request's serializer

https://github.com/Tochemey/goakt/issues/1411

The remoting client decoded the reply to an ask with the serializer it picked
for the request. A grain or an actor that answers a CBOR request (a plain Go
struct registered with `remote.WithSerializables`) with a proto reply, or a
proto request with a CBOR reply, could not be asked from another node, although
the same ask worked locally.

The sample runs a three-node cluster. A grain and an actor live on node 3 and
answer every request with a reply of the other serializer. Node 1 and node 2
each ask both of them with a CBOR request and with a proto request, and check
that each reply decodes as what the target sent.

## Actual (before the fix)

```
BUG: node 1, grain, CBOR request, proto reply: invalid message
remote: CBOR type not registered: testpb.Reply
BUG: node 1, grain, proto request, CBOR reply: invalid message
remote: unknown or unregistered proto message type
proto: not found
BUG: node 1, actor, CBOR request, proto reply: remote: CBOR type not registered: testpb.Reply
BUG: node 1, actor, proto request, CBOR reply: remote: unknown or unregistered proto message type
proto: not found
BUG: node 2, grain, CBOR request, proto reply: invalid message
remote: CBOR type not registered: testpb.Reply
BUG: node 2, grain, proto request, CBOR reply: invalid message
remote: unknown or unregistered proto message type
proto: not found
BUG: node 2, actor, CBOR request, proto reply: remote: CBOR type not registered: testpb.Reply
BUG: node 2, actor, proto request, CBOR reply: remote: unknown or unregistered proto message type
proto: not found
FAIL: 8 of 8 replies of another serializer did not decode on the other nodes
exit status 1
```

## Expected (after the fix)

```
OK: node 1, grain, CBOR request, proto reply
OK: node 1, grain, proto request, CBOR reply
OK: node 1, actor, CBOR request, proto reply
OK: node 1, actor, proto request, CBOR reply
OK: node 2, grain, CBOR request, proto reply
OK: node 2, grain, proto request, CBOR reply
OK: node 2, actor, CBOR request, proto reply
OK: node 2, actor, proto request, CBOR reply
PASS: a reply of another serializer than the request decodes on the other nodes
```

## Run

```bash
go run ./playground/issue-1411
```
