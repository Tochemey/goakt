# Issue 1409: grain send errors lose their sentinel across nodes

https://github.com/Tochemey/goakt/issues/1409

Two nodes. A grain with a mailbox of one lives on node 2. On its first
message it sends a request to a gate grain that never answers, standing in
for a database, and its `OnReceive` returns; with `StashNonReentrant`
reentrancy it takes no other message while that request is in flight, and a
second message fills its mailbox. Node 1 asks it and must get
`ErrMailboxFull`, as a local caller does, so it can back off instead of
failing.

## Actual (before the fix)

The owner answered with the error's text, so `errors.Is` had nothing to
match (and the owner logged the expected outcome at ERROR level):

```
FAIL: the full mailbox on node 2 does not come back as ErrMailboxFull: mailbox is full
```

## Expected (after the fix)

```
PASS: a full mailbox on the other node comes back as ErrMailboxFull
```

## Run

```bash
go run ./playground/issue-1409
```

`TestRemoteGrainSend_KeepsTheSentinelsAcrossNodes` in `actor` covers the
same case in the test suite; `TestGrainSendError` covers the mapping of
each sentinel to its code.
