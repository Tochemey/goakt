# Milestone 2 Implementation Plan: Duplex Dial-First Remoting

**Issue:** [#1301](https://github.com/Tochemey/goakt/issues/1301)
**Authoritative scope:** [REMOTING_REFACTOR_MILESTONES.md](REMOTING_REFACTOR_MILESTONES.md) (Milestone 2)
**Design rationale:** [REMOTING_REFACTOR_DESIGN.md](REMOTING_REFACTOR_DESIGN.md)
**Depends on:** Milestone 1 (frame, handshake, duplex, transport, sniff, protocol pin) — landed inert

**Overview:** Implement Milestone 2 end-to-end: DATA/REPLY envelopes, per-connection correlation, duplex dial-first with legacy fallback, server dispatch off the read loop, and migration of every remoteclient call site — as one reviewable diff (no commit until approved).

## Scope

From the milestones guide, Milestone 2 delivers:

- DATA/REPLY/ERROR envelope codecs
- Async correlation (pending table, waiters)
- Byte-capped writer queue with vectored writes
- Server dispatch off the read loop
- remoteclient migration (Tell, Ask, batches, all control RPCs)
- Dial-side fallback + per-peer protocol cache
- Switchover drain when a peer's cached protocol changes

**Out of scope:** lanes / deadline enforcement (M3), chunking / large FramePool rework (M4), compression tables (M5), vtprotobuf (M6), credit window beyond the byte-capped queue (M7), coalescer deletion.

**Known interim state:** Until Milestone 3, one duplex connection carries control, user, and ask traffic for a peer. Milestone 2 delivers transport parity plus multiplexing, not control-lane isolation — do not benchmark control-latency isolation against it.

## Architecture

```mermaid
sequenceDiagram
  participant Caller
  participant PeerMgr as peerManager
  participant Duplex as duplexConn
  participant Server as handleDuplexConn
  participant WP as WorkerPool
  participant Actor as mailbox_or_handler

  Caller->>PeerMgr: Tell_Ask_or_RPC
  alt cache_legacy_or_pin_legacy
    PeerMgr->>PeerMgr: legacy SendProto
  else duplex_or_auto
    PeerMgr->>Duplex: Dial_HELLO_if_needed
    Duplex-->>PeerMgr: HELLO_ACK_or_fallback
    PeerMgr->>Duplex: Submit DATA plus waiter
    Duplex->>Server: frame
    alt expectsReply
      Server->>WP: dispatch
      WP->>Actor: ask_or_control
      Actor-->>WP: result
      WP->>Duplex: Submit REPLY_or_ERROR
      Duplex-->>Caller: complete waiter
    else tell
      Server->>Actor: inline mailbox enqueue
    end
  end
```

### Wire payload rules (fixed in this milestone)

| Traffic     | Frame  | Envelope                                                                                        | Correlation / flags                                                                                   |
|-------------|--------|-------------------------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------|
| User tell   | `DATA` | `senderRef \| receiverRef \| typeRef \| serializerID \| [meta] \| payload`                      | corr `0`, no `expectsReply`                                                                           |
| User ask    | `DATA` | same as tell                                                                                    | nonzero corr, `expectsReply`; response is `REPLY` with `typeRef \| serializerID \| [meta] \| payload` |
| Control RPC | `DATA` | empty sender/receiver refs; `typeRef` = protobuf full name; `serializerID=0`; raw proto payload | nonzero corr, `expectsReply`; response is `REPLY` (or `ERROR` for transport/handler failure)          |

- Refs encode as inline literals only at this milestone: `0` + uvarint length + bytes. A nonzero table ID can only come from a misbehaving peer: at negotiated revision 1 a table ref is a capability violation per the wire reference, answered with a connection-scoped `ERROR` then close (tables arrive in Milestone 5).
- Metadata is present only when `FrameFlagHasMetadata` is set; binary blob reuses `internal/net/metadata.go` (including remaining-deadline rebasing).
- Serializer IDs: `0` internal proto (raw bytes + typeRef), `1` public proto frame, `2` JSON, `3` CBOR, `255` custom (self-describing payload, typeRef `0`).
- Application-level `internalpb.Error` responses keep today’s semantics so `checkProtoError` still works when the reply body is that type.
- Connection-scoped `ERROR` frames use correlation `0` (Milestone 1 wire amendment); request-scoped `ERROR` echoes the failed request's correlation ID.
- Ask deadline: the flattened envelope has no legacy `timeout` field. Asks always set `FrameFlagHasMetadata` and stamp the remaining deadline into the metadata blob; the server derives its wait bound from the rebased deadline in context and falls back to the system's default ask timeout when absent. Control RPCs stamp the deadline the same way.
- Lane byte: every Milestone 2 frame carries the control lane byte, matching the single negotiated control-role connection, so Milestone 3 receivers that validate the lane byte against the connection role accept Milestone 2 peers unchanged. Per-role lane bytes arrive with Milestone 3.

## Layered deliverables

### 1. Envelope codec

**Files:** `internal/net/envelope.go`, `internal/net/envelope_test.go`

- Structs: `DataEnvelope`, `ReplyEnvelope`
- Functions: `encodeDataEnvelope` / `decodeDataEnvelope`, `encodeReplyEnvelope` / `decodeReplyEnvelope`
- Adversarial tests: truncated refs, oversize lengths, unknown serializer ID, flag/metadata mismatch, round-trip happy path

### 2. Correlation and duplex hardening

**Files:** `internal/net/duplex.go`, new `internal/net/pending.go` (+ tests), `internal/net/transport.go`

- Per-connection `atomic.Uint64` correlation counter and `xsync.Map[uint64,*waiter]` with pooled 1-buffer channels (same pattern as `actor/pools.go` / `internal/pendingasks`)
- Peer-facing duplex API: `Ask(ctx, frame) (Frame, error)`, `Tell(ctx, frame) error`
  - Timeout removes the waiter; late `REPLY`/`ERROR` dropped on table miss
- Map `ErrDuplexBackpressure` → `errors.ErrRemoteSendBackpressure` at the remoteclient boundary
- Writer queue cap = negotiated `InitialCredits` (default **16 MiB** via `remote.DefaultInitialCredits` / net constant used in HELLO)
- No-deadline bound: when the caller's context carries no deadline, `Submit` blocks at most `remote.Config` `writeTimeout` before returning backpressure (design decision 2; this specific bound is Milestone 2 even though general deadline enforcement is Milestone 3)
- `WriteFrames`: drain with `net.Buffers` (header + payload per frame; multi-frame batch when the writer has several ready)

### 3. Server duplex dispatch

**Files:** `internal/net/proto_server.go`, `actor/remote_server.go`

Replace the PING-only `handleDuplexConn` loop with:

- `PING` → `PONG` (unchanged)
- `DATA` without `expectsReply` → decode → user-tell callback **inline on the read loop** (reuse / extract shared logic from `deliverRemoteTellMessage`)
- `DATA` with `expectsReply` → `WorkerPool` task:
  - if `typeRef` matches a registered `ProtoHandler` → control RPC path (existing handlers), enqueue `REPLY` / `ERROR`
  - else → actor-ask path extracted from `remoteAskHandler` for a **single** envelope, enqueue `REPLY` / `ERROR`
- Transport failures (decode, unknown receiver before dispatch, panic) → request- or connection-scoped `ERROR` then continue/close per wire rules
- Register duplex user callbacks from `startRemoteServer`
- HELLO local params: advertise `SystemName`, `MaxFrameSize`, `MaxMessageSize` (still equal to `MaxFrameSize` until chunking lands in Milestone 4), and `InitialCredits` from config (no magic numbers)

### 4. Peer dialer, protocol cache, fallback, drain

**Files (new under `internal/remoteclient/`):** `protocol_cache.go`, `peer.go` and/or `duplex_peer.go` (+ tests)

- Per-peer (`host:port`) cached protocol `{unknown, duplex, legacy}`; clear entry on reconnect
- One long-lived duplex connection per peer; lazy dial via `TCPTransport` + `performHello`; TLS/compression after HELLO as in Milestone 1
- Dial policy from `ProtocolPin`:
  - `auto`: duplex-first; EOF/reset before `HELLO_ACK` → mark legacy, retry once on legacy `SendProto`, cache legacy
  - `legacy`: legacy only
  - `duplex`: duplex only (no fallback)
- **Switchover drain:** when a reconnect changes the cached protocol, wait for in-flight legacy sends to that peer to finish before opening/using duplex (order-preserving)
- Central helpers: `sendControl`, `sendTell`, `sendAsk` — all `client.go` call sites route through these instead of bare `SendProto` when not forced legacy
- Coalescer: **unchanged**; only used when the peer is legacy (or pin is legacy). Duplex tells skip the coalescer (enqueue success returns nil)

### 5. remoteclient migration

**Files:** `internal/remoteclient/client.go`, actor-system client construction

- Every `SendProto` call site (Tell, Ask, batches, control RPCs, grains) uses the helpers above
- `RemoteBatchAsk`: N correlated frames; collect by correlation ID; return responses in **request order**
- `RemoteBatchTell`: N tell frames on the duplex connection
- Propagate `ProtocolPin` into remoteclient construction from the actor system (new client option mirroring config)
- Fire-and-forget duplex failures after enqueue: dead-letter/event path analogous to the coalesced failure handler (do not change Tell’s return-after-enqueue contract)

### 6. Config and docs

- `remote/config.go`: `DefaultInitialCredits` (16 MiB); HELLO uses it. Prefer constant + handshake field for Milestone 2; add `WithInitialCredits` only if wiring requires a public knob
- `docs/advanced/remoting.mdx`: deferred by maintainer decision; the dial-behavior documentation (`auto` dials duplex-first with legacy fallback and caching) lands in one pass once the milestone work settles, not with this diff
- Tick Milestone 2 acceptance boxes in `REMOTING_REFACTOR_MILESTONES.md` only at handoff after review

## Tests (focused, no parallel)

| Area               | Coverage                                                                                                                         |
|--------------------|----------------------------------------------------------------------------------------------------------------------------------|
| `envelope_test.go` | round-trip, adversarial decode                                                                                                   |
| pending / duplex   | concurrent asks out of order; timeout clears table; late reply dropped; backpressure → `ErrRemoteSendBackpressure`               |
| ProtoServer duplex | tell ordering; ask multiplexing (slow actor does not block fast ask p99 on same connection); control RPC round-trip; ERROR paths |
| remoteclient       | fallback to legacy-only listener + cache; switchover drain order; batch ask order; pin legacy / duplex / auto; every migrated control RPC has a targeted duplex round-trip test |
| Actor integration  | mixed pin in one process (duplex↔duplex and duplex↔legacy) for tell/ask + one control RPC                                        |

**Acceptance criteria (from milestones guide — tick after review):**

- [x] The slow-actor test proves ask multiplexing: p99 of the fast ask is unaffected by the slow one, same connection.
- [x] All remoting operations (tell, ask, batches, all control RPCs, grain and topic traffic) round-trip over the new protocol in tests.
- [x] Mixed-version test: a new node exchanges full traffic with a legacy-pinned node via fallback, and with a new node via the duplex path, in the same process space.
- [x] Per-peer memory is bounded: the writer queue rejects beyond its byte cap; asserted by test.
- [x] Benchstat against Milestone 1 shows no small-message throughput regression on the legacy path.

Benchmark: benchstat small-message **legacy** path vs Milestone 1 baseline; record as a comment on #1301 after approval (no permanent bench scaffolding unless it earns a place).

## Implementation order

1. Envelope + tests
2. Pending/correlation + vectored `WriteFrames` + duplex API
3. Server dispatch + actor wiring
4. Peer manager + cache + fallback + drain
5. Migrate all remoteclient call sites
6. Integration tests + docs + milestones checklist (unticked until review)

## File map (expected touch list)

| Area             | Path                                                                              |
|------------------|-----------------------------------------------------------------------------------|
| Envelope         | `internal/net/envelope.go`, `envelope_test.go`                                    |
| Correlation      | `internal/net/pending.go`, `pending_test.go`; extend `duplex.go`                  |
| Vectored write   | `internal/net/transport.go`                                                       |
| Server dispatch  | `internal/net/proto_server.go`, `proto_server_test.go`                            |
| Actor wiring     | `actor/remote_server.go`                                                          |
| Peer / cache     | `internal/remoteclient/protocol_cache.go`, `peer.go` / `duplex_peer.go` (+ tests) |
| Client migration | `internal/remoteclient/client.go`                                                 |
| Config           | `remote/config.go` (`DefaultInitialCredits`)                                      |
| Docs             | `docs/advanced/remoting.mdx`                                                      |
| Untouched        | `internal/remoteclient/coalescer.go` (legacy only)                                |

## Handoff

1. Present the full diff for maintainer approval.
2. Do **not** commit until approved.
3. After approval: exactly one semantic `feat:` commit referencing #1301.
4. Optionally comment benchstat numbers on #1301 (needs write approval).
5. Mark Milestone 2 acceptance boxes in `REMOTING_REFACTOR_MILESTONES.md` complete before starting Milestone 3.
