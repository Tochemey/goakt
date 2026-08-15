# Issue 1312: Stashed Ask Replies Dropped After Unstash

Living sample for [#1312](https://github.com/Tochemey/goakt/issues/1312), replies to stashed Asks silently dropped after `Unstash` under concurrent load.

## Scenario

A single `stasher` actor defers every incoming Ask exactly once:

- On a first-seen `command` it calls `Stash()` and sends itself a `flush`.
- On `flush` it grants one reply slot and calls `Unstash()`.
- On a command released by a flush it answers with `Response`.

Every `Stash` pairs with exactly one flush, and every flush grants exactly one reply slot, so replies and commands stay in one-to-one correspondence no matter how messages interleave. The driver fires 40 rounds of 5 concurrent Asks; a timeout can only mean `Response` dropped the reply.

## Expected vs actual

Expected: every Ask receives its reply, and `Response` on an unstashed delivery behaves the same as on a direct delivery.

Actual before the fix (v4.5.0): under concurrent load, more than half of the Asks timed out. `stash` and `unstash` copy the current message into a pooled `ReceiveContext` through `cloneContext()`, which bypasses `build()` and therefore never cleared the `responseClosed` late-reply guard. A context recycled after a completed Ask came back with the guard already tripped, so the CAS in `Response()` failed and the reply was dropped. A second defect compounded it: the Ask callers stored `responseClosed` on the pooled context after handing it to the mailbox, a write that could land on a context already rebuilt for an unrelated Ask and drop that request's reply too.

## Run

```bash
go run ./playground/issue-1312
```

On fixed code the sample prints:

```
OK: all 200 asks received their reply after stash and unstash
```

On broken code it prints one line per lost reply and exits non-zero:

```
round 2 command 3: request timed out
...
FAIL: 129/200 asks lost or mismatched their reply
```
