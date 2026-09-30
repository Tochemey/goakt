# Issue 1411: a remote grain reply is decoded with the request's serializer

https://github.com/Tochemey/goakt/issues/1411

Two nodes. A grain on node 2 answers a CBOR request (a plain Go struct
registered with `remote.WithSerializables`) with a proto reply. The ask
works locally on node 2 and must work from node 1 as well.

## Actual (before the fix)

The client tried to decode the proto reply with the CBOR serializer it had
used for the request:

```
FAIL: remote ask: invalid message
remote: CBOR type not registered: testpb.Reply
```

## Expected (after the fix)

```
PASS: a CBOR request answered with a proto reply decodes on the other node
```

## Run

```bash
go run ./playground/issue-1411
```

`TestRemoteAskGrain_DecodesAReplyOfAnotherSerializer` in `actor` covers the
same case in the test suite.
