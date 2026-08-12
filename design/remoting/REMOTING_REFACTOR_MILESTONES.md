# Remoting Refactor: Milestone Implementation Guide

**Overview:** Implementation specification for the seven milestones of issue [#1301](https://github.com/Tochemey/goakt/issues/1301), detailed enough for any developer to pick up a milestone and land it without reading the discussion history. The issue body is the authoritative scope; [REMOTING_REFACTOR_DESIGN.md](REMOTING_REFACTOR_DESIGN.md) holds the full rationale. Each milestone below states its scope, deliverables, normative behavior, tests, and acceptance criteria.

## Wire reference (normative)

### Frame header (16 bytes, all integers big-endian)

| Offset | Size | Field         | Notes                                                                                                                                                                                                                                                                                          |
|--------|------|---------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| 0      | 1    | `version`     | `0x02`. Doubles as the protocol discriminator against legacy traffic.                                                                                                                                                                                                                          |
| 1      | 1    | `type`        | See frame types below.                                                                                                                                                                                                                                                                         |
| 2      | 1    | `flags`       | Bit 0 `hasMetadata`, bit 1 `expectsReply`, bit 2 `firstChunk`, bit 3 `lastChunk`. Bits 4 to 7 reserved, must be zero; receivers reject nonzero reserved bits.                                                                                                                                  |
| 3      | 1    | `lane`        | The sender's lane role and index for validation and diagnostics: `0` control, `1..N` ordinary, `0xFF` large. Receivers validate it against the connection's negotiated role.                                                                                                                   |
| 4      | 4    | `length`      | Payload bytes following the header. Bounded by negotiated `maxFrameSize`.                                                                                                                                                                                                                      |
| 8      | 8    | `correlation` | `0` means none. Required nonzero for `REPLY`, `CHUNK`, and any `DATA` with `expectsReply`. Connection-scoped `ERROR` frames (protocol violations, handshake failures with no originating request) use correlation `0`; request-scoped `ERROR` frames echo the failed request's correlation ID. |

Frame types: `0x01 HELLO`, `0x02 HELLO_ACK`, `0x03 DATA`, `0x04 REPLY`, `0x05 ERROR`, `0x06 CHUNK`, `0x07 CREDIT`, `0x08 TABLE`, `0x09 PING`, `0x0A PONG`.

### Handshake

`HELLO` and `HELLO_ACK` payloads are one protobuf message (`internalpb.Hello`, new file `protos/internal/handshake.proto`) so the handshake stays extensible without wire surgery:

```
Hello {
  uint32 revision            // capability revision, see below
  string system_name
  string host
  uint32 port
  LaneRole lane_role         // CONTROL, ORDINARY, LARGE
  uint32 lane_index
  Compression compression    // NONE, GZIP, ZSTD, BROTLI
  uint32 max_frame_size
  uint64 max_message_size
  uint64 initial_credits
  uint32 max_concurrent_large_transfers
}
```

Rules: the dialer sends `HELLO` first; the acceptor answers `HELLO_ACK`. Effective limits are the pairwise minimum. Compression is negotiated: the acceptor answers with the codec it will use, `NONE` if it does not support the dialer's proposal, and both sides wrap the connection only after the handshake completes. A `version` mismatch is answered with an `ERROR` frame then a clean close.

`revision` gates optional capabilities: `1` = milestone 2 baseline (DATA, REPLY, ERROR, PING, PONG), `2` = chunking (milestone 4), `3` = compression tables (milestone 5), `4` = credits (milestone 7). A sender may use a capability only when `min(local, peer)` revision allows it. Revisions are cumulative. A received frame that requires a capability above the negotiated revision, or carries an unknown frame type, is a protocol violation, not a recoverable request failure: the receiver answers with `ERROR` and closes the connection, since it indicates a broken or mismatched peer. One deliberate carve-out (milestone 7): an inbound `CREDIT` frame below revision 4 is silently discarded instead of treated as a violation, so a buggy peer cannot kill an otherwise healthy revision-3 session with a spurious grant.

### DATA envelope (hand-parsed, fixed from milestone 2)

```
senderRef | receiverRef | typeRef | serializerID (1 byte) | [metaLen (4B) metadata] | payload
```

Each ref is a uvarint: a table ID, or `0` followed by a uvarint length and that many bytes of inline literal. Until milestone 5 senders always use inline literals; the layout does not change when tables arrive. `serializerID`: `0` internal protobuf codec (payload is raw proto bytes, `typeRef` carries the type), `1` public protobuf frame, `2` JSON, `3` CBOR, `255` custom (`typeRef` is `0`; payload is the serializer's verbatim self-describing bytes, resolved by the existing dispatch). Metadata is present only when `hasMetadata` is set and reuses the existing binary blob from `internal/net/metadata.go`, including the remaining-deadline rebasing.

`REPLY` envelope: `typeRef | serializerID | [metaLen metadata] | payload`. `ERROR` payload: `internalpb.Error` proto bytes. `CREDIT` payload: uvarint grant bytes. `TABLE` payload: kind byte (`0` actor path, `1` type name), uvarint ID, uvarint length, literal bytes. `PING`/`PONG`: empty payload, correlation echoed. `CHUNK`: see milestone 4.

### Defaults

| Constant                      | Value                                                                                                     | Where declared          |
|-------------------------------|-----------------------------------------------------------------------------------------------------------|-------------------------|
| `ChunkSize`                   | 256 KiB                                                                                                   | `remote/config.go`      |
| Large threshold               | logical frame size > `ChunkSize`                                                                          | derived, not a knob     |
| `MaxMessageSize`              | 16 MiB (raisable)                                                                                         | `remote/config.go`      |
| `MaxFrameSize`                | 16 MiB (unchanged; the chunk-sized receive bound is deferred to the legacy-path removal, see Milestone 4) | `remote/config.go`      |
| `CreditWindow` (HELLO `initial_credits`) | 16 MiB                                                                                         | `remote/config.go` (`DefaultCreditWindow` / `WithCreditWindow`, Milestone 7) |
| `MaxConcurrentLargeTransfers` | 4                                                                                                         | `remote/config.go`      |
| Table capacity                | 8192 entries per kind per connection                                                                      | `internal/net` constant |
| Outbound queue cap            | `CreditWindow` bytes                                                                                      | derived                 |
| `OrdinaryLanes`               | 1                                                                                                         | `remote/config.go`      |

---

## Milestone 1: frame protocol, handshake, duplex connections, transport abstraction

**Scope.** All new-protocol plumbing as inert, fully tested code: frame codec, handshake, duplex connection type, `Transport`/`FramedConn` interfaces, the dual-protocol listener sniff, and the protocol pin knob. Nothing dials the new protocol yet.

**Out of scope.** DATA handling, remoteclient wiring, correlation, lanes.

**Deliverables.**

* `internal/net/frame.go` + `frame_test.go`: header constants, `Frame` struct, `encodeFrameHeader`/`decodeFrameHeader`, flag helpers, validation (reserved bits, type range, length bound).
* `protos/internal/handshake.proto`, generated into `internalpb` (`make protogen`, `make proto-lint`, `make proto-format`).
* `internal/net/handshake.go` + test: `performHello` (dialer side), `acceptHello` (acceptor side), pairwise-minimum negotiation, compression selection, version-mismatch `ERROR`.
* `internal/net/transport.go` + test: `Transport`, `FramedConn`, `Acceptor` interfaces and the TCP implementation wrapping `net.Conn` with the frame codec.
* `internal/net/duplex.go` + test: `duplexConn` owning one reader goroutine, one writer goroutine, a byte-bounded outbound queue (admission and shutdown only in this milestone), clean start/stop with goroutine leak checks.
* Listener sniff in `TCPServer.serveConn` (`internal/net/tcp_server.go`): after the TLS wrap and before compression wrappers, peek exactly one byte. `0x02` routes to the new-protocol acceptor; anything else replays the byte (via a prepend reader) into the legacy path unchanged. The new-protocol acceptor applies negotiated compression after the handshake; the legacy path applies configured wrappers exactly as today.
* Protocol pin: `remote.Config` gains the pin knob (`auto`, `legacy`, `duplex`; default `auto`). In this milestone `auto` still dials legacy.

**Behavior notes.** The sniff is impossible to implement correctly after compression wrappers attach; restructure `serveConn` accordingly and keep the legacy byte stream byte-identical. Brotli-compressed legacy peers are indistinguishable by sniff; this is documented, not solved: such deployments pin the protocol.

**Tests.** Header round-trips plus adversarial decode (truncated, bad version, bad type, reserved bits, oversize length); handshake negotiation minima, codec agreement, and version mismatch producing `ERROR` then close; sniff routing for first bytes `0x00`, `0x01` (legacy frames), `0x1f` (legacy gzip), `0x28` (legacy zstd), `0x02` (new), each asserting the byte is not lost; duplex start/stop with no goroutine leaks (`goleak` is already in go.mod's test deps if present, otherwise a manual runtime.NumGoroutine guard); transport interface conformance for the TCP implementation.

**Acceptance criteria.**

* [x] Two in-process endpoints complete HELLO/HELLO_ACK over the TCP transport, negotiate pairwise-minimum limits and a compression codec, and exchange PING/PONG.
* [x] Version mismatch yields an in-band `ERROR` frame and a clean close, asserted by test.
* [x] All legacy remoting tests pass unmodified; the sniff test proves legacy bytes reach the legacy path intact for every legacy first-byte class.
* [x] No public API change beyond the protocol pin knob. `make lint` clean; proto targets clean.

## Milestone 2: asynchronous correlation and dispatch rework

**Scope.** The new protocol becomes able to carry all remoting traffic, and `auto` flips to dial it first. DATA/REPLY/ERROR envelopes, pending-request correlation, byte-capped writer queue, server dispatch off the read loop, migration of every remoteclient call site, dial-side fallback with per-peer protocol caching.

**Deliverables.**

* `internal/net/envelope.go` + test: hand-written encoder/decoder for the DATA and REPLY envelopes exactly as specified in the wire reference. Inline literals only; table IDs decode but never encode at this milestone.
* Correlation: per-connection atomic `uint64` counter and a pending table (`xsync.Map[uint64, *waiter]`); waiters reuse the pooled response-channel pattern from `actor/actor_system.go`. Timeout removes the waiter and the late reply is dropped by a table miss, asserted by test.
* Writer queue: byte-counted admission capped at `InitialCredits` bytes; `submit` blocks on a full queue until the context deadline (or `remote.Config` write timeout when the caller has none) then returns `errors.ErrRemoteSendBackpressure`; the writer drains with `net.Buffers` vectored writes.
* Server dispatch in the new-protocol acceptor: frames without `expectsReply` resolve the receiver and enqueue to the mailbox inline on the read loop (bounded-mailbox rejection keeps existing dead-letter semantics); frames with `expectsReply` dispatch to the existing `WorkerPool`, and completions enqueue `REPLY` frames on the connection's writer queue. Transport-level failures (unknown receiver before dispatch, decode failure, panic) answer with `ERROR`; application-level errors keep today's in-band `internalpb.Error` payload semantics so `checkProtoError` logic ports over unchanged.
* remoteclient migration: a request/response helper over correlation replaces `SendProto` for new-protocol peers at all call sites (`RemoteTell`, `RemoteAsk`, batches, and every control RPC). One duplex connection per peer at this milestone; lanes arrive in milestone 3. `RemoteBatchAsk` matches each reply to its request by correlation ID internally and returns the response slice in request order regardless of arrival order, removing the positional-matching caveat of the legacy batch RPC.
* Dial fallback: EOF or reset before `HELLO_ACK` marks the peer legacy in a per-peer cache; the send is retried once through the legacy path; the cache entry clears on the next reconnect to that peer. `auto` now means duplex-first with this fallback.
* Switchover drain, landing here with the fallback it belongs to: when a peer's cached protocol changes at reconnect, in-flight legacy sends to that peer drain before the duplex path opens, so the transition cannot reorder messages.
* Fire-and-forget semantics on the new path: enqueue success returns nil; transport failures dead-letter with an event. The coalescer stays untouched and serves legacy peers only; do not delete it here (see ground rule 3 and design decision 5, staged deletion with the legacy path in the next major).

**Known interim state.** Until milestone 3, one duplex connection carries control, user, and ask traffic for a peer, so control messages share the queue with user traffic exactly as they do today. Milestone 2 delivers transport parity plus multiplexing, not the end-state isolation architecture; do not benchmark control-latency isolation against it.

**Tests.** Concurrent asks on one connection with replies resolved out of order; ask timeout leaves the pending table empty; a deliberately slow actor does not delay a concurrent fast ask on the same connection (the head-of-line regression test); writer queue blocks then returns `ErrRemoteSendBackpressure` at the byte cap; sequential tells from one sender to one receiver arrive in order; every migrated control RPC has a targeted round-trip test over the new path; fallback dialing against a legacy-only listener lands the message via the legacy path and caches the peer; the switchover drain test interleaves a protocol change at reconnect with a message stream and asserts order.

**Acceptance criteria.**

* [x] The slow-actor test proves ask multiplexing: p99 of the fast ask is unaffected by the slow one, same connection.
* [x] All remoting operations (tell, ask, batches, all control RPCs, grain and topic traffic) round-trip over the new protocol in tests.
* [x] Mixed-version test: a new node exchanges full traffic with a legacy-pinned node via fallback, and with a new node via the duplex path, in the same process space.
* [x] Per-peer memory is bounded: the writer queue rejects beyond its byte cap; asserted by test.
* [x] Benchstat against milestone 1 shows no small-message throughput regression on the legacy path.

## Milestone 3: lanes and deadline enforcement

**Scope.** The per-peer connection set becomes role-separated: one control lane, `OrdinaryLanes` ordinary lanes, one large lane. Deadlines and liveness make every path bounded.

**Deliverables.**

* Lane manager in `internal/remoteclient`: per-peer set of duplex connections keyed by role and index, dialed lazily on first use, each performing its own handshake with `lane_role`/`lane_index` set. Reconnect with backoff per lane; on connection loss, pending waiters fail with a transport error.
* Routing: control message types (watch, unwatch, spawn, stop, reinstate, cluster RPCs, liveness) map statically to the control lane, with one planned exception: `RelocateBatch`, `PersistPeerState`, and `GetState` payloads move to the large lane once chunking exists in milestone 4, because they can be arbitrarily large and would otherwise recreate control-lane head-of-line blocking under rebalancing. Until milestone 4 they stay whole on the control lane, as today, since `MaxFrameSize` still admits them. User tells and asks hash the receiver path (FNV-1a) modulo `OrdinaryLanes`; receivers matching `LargeMessageDestinations` route to the large lane. The route decision is computed once per receiver and cached (the cache slot moves onto the compression-table entry in milestone 5).
* `LargeMessageDestinations` in `remote.Config`: glob-style patterns over actor paths, matcher unit-tested independently. At this milestone the large lane carries whole frames only (chunking is milestone 4), so listed destinations are still bounded by `MaxFrameSize`; document this as transitional.
* Deadlines: `writeTimeout` enforced on every write; `readIdleTimeout` armed per read with PING on idle and close after two missed PONGs. Both fields in `remote.Config` become live and validated.
* Peer lifecycle teardown: peer-down cluster events (member left or removed) and system shutdown tear down the peer's entire lane set: pending waiters fail with a transport error, route and protocol caches drop, and no reconnect is attempted until new traffic targets the peer again.

**Tests.** Lane assignment is stable per receiver and respects `OrdinaryLanes`; the isolation test saturates an ordinary lane with large frames and asserts a watch notification on the control lane is delivered within its normal latency budget; per-pair FIFO holds under `OrdinaryLanes > 1`; a black-holed peer (accept then read nothing) trips the write deadline instead of hanging; missed PONGs close and reconnect; waiters error on lane loss; a peer-down cluster event tears down every lane to that peer and fails pending waiters, asserted by test.

**Acceptance criteria.**

* [x] Control-lane isolation test passes: system traffic latency is independent of ordinary-lane saturation.
* [x] No send or read path can block indefinitely: every blocking call is bounded by a deadline, a context, or liveness, verified by the black-hole test.
* [x] Per-pair FIFO verified at `OrdinaryLanes = 1` and `4`.
* [x] `writeTimeout` and `readIdleTimeout` are live config with validation and documented defaults.

## Milestone 4: chunked large messages and frame pool rework

**Scope.** Logical frames above `ChunkSize` and up to `MaxMessageSize`, chunked on the large lane for listed destinations and in place on the ordinary lane otherwise (`MaxFrameSize` stays 16 MiB, see the Config deliverable). Requires peer `revision >= 2`.

**Deliverables.**

* Chunk sender: a logical frame exceeding `ChunkSize` is serialized once, then emitted as `CHUNK` frames of `ChunkSize`. The group correlation reuses the logical frame's correlation when present (ask requests and replies); only correlation-less tells allocate a fresh one from the connection counter. `firstChunk` carries the uvarint total size before the slice, `lastChunk` marks the final frame. Payload prefix of the first chunk is the logical 16-byte header plus envelope, so reassembly yields a standard frame.
* Reassembler per connection: map keyed by correlation; buffer allocated once at `firstChunk` from the declared total (rejected with `ERROR` before allocation when it exceeds `MaxMessageSize`); chunk indexes must be monotonic per group (TCP ordering guarantees it; violation is a protocol error); at most `MaxConcurrentLargeTransfers` concurrent groups, excess rejected with `ERROR` while the connection stays usable; partial groups discarded on connection loss.
* Routing completion: oversized messages to unlisted destinations chunk in place on their ordinary lane; oversized replies chunk in place on the connection that carried the request. When the peer revision is below 2, oversize fails fast with a descriptive error instead of a dead connection.
* Control-plane bulk exception, resolving the milestone 3 interim: `RelocateBatch`, `PersistPeerState`, and `GetState` payloads whose logical frame exceeds `ChunkSize` route over the large lane chunked; request/response correlation is lane-independent, so no ordering concern arises. Small control traffic stays on the control lane.
* Config: `ChunkSize`, `MaxMessageSize`, `MaxConcurrentLargeTransfers` live and validated; the legacy client's frame limit finally honors `remote.Config` instead of the hard-coded 16 MiB. The `MaxFrameSize` default and HELLO advertisement stay at 16 MiB: negotiation takes the pairwise minimum and a revision-1 peer cannot chunk, so advertising chunk-plus-headroom would kill every 256 KiB..16 MiB message in a mixed cluster (frame enforcement is read-side only). Between revision-2 peers the sender chunks everything above `ChunkSize`, so no legitimate frame exceeds the tight bound anyway; shrinking the enforced limit is deferred to the legacy-path removal in the next major.
* Frame pool: sender chunks are subslices of the once-serialized logical buffer (nothing allocated per chunk); receiver per-chunk read buffers may pool with release at the copy into the reassembly buffer; the reassembly buffer is one exact-size allocation at any size, deliberately unpooled, documented in code (ownership passes to dispatch with no deterministic release point until the milestone 6 buffer-lifetime pass).

**Tests.** Round-trip at 1 MiB, 20 MiB, and 100 MiB with content hashing; oversize rejected before buffering; concurrent group cap enforced in-band with the connection alive afterwards; in-place chunked message stays in order with surrounding small messages to the same actor; interleaved groups from different senders reassemble correctly; partial group discarded on connection drop without leaking the buffer; revision gating verified against a revision-1 peer; a relocation batch above `ChunkSize` rides the large lane while a concurrent watch notification meets its control-lane latency budget.

**Acceptance criteria.**

* [x] A 100 MiB message round-trips while concurrent small-message latency on ordinary lanes shows no measurable impact (benchmarked).
* [x] The reassembly regression tests from the issue pass here: a message larger than the previous 16 MiB ceiling completes within `MaxMessageSize`, cap violation is in-band and survivable, and in-place ordering holds. The larger-than-credit-window test belongs to milestone 7, where the window exists.
* [x] Allocation profile: steady-state large-message traffic allocates one reassembly buffer per message and nothing per chunk, verified with an allocation benchmark.
* [x] Docs present `LargeMessageDestinations` as a performance and isolation knob, not a correctness gate, with an example of both routed and in-place cases.

## Milestone 5: compression tables and receive-path caches

**Scope.** Actor paths and type names become per-connection varint IDs; the receive side gains the cached-PID fast path. Requires peer `revision >= 3`. Note on design mapping: the envelope layout and serializer IDs were already fixed in milestone 2 under the wire-stability rule, so this milestone delivers the table optimization that the design's phase 5 bundles together with envelope flattening; there is no envelope change here.

**Deliverables.**

* Sender tables: per connection per kind, IDs assigned monotonically from 1, `TABLE` frame emitted on the same connection strictly before the first referencing frame (single writer goroutine makes this ordering free); capacity 8192 per kind with inline-literal fallback beyond; tables die with the connection.
* Receiver tables: ID to string for type names, ID to cached receiver lookup for actor paths, and ID to cached sender `PID` handle replacing the per-message `newRemotePID` allocation in `actor/remote_server.go`. The existing sender-address parse cache becomes redundant on the new path and remains for legacy.
* The route decision from milestone 3 moves onto the sender's table entry so the steady-state send path performs a single map lookup for route, refs, and IDs together.
* Type resolution: table-hit receives skip the global registry cache entirely; inline literals keep using it.

**Tests.** Registration-before-use invariant under concurrent senders to one peer; overflow falls back to literals without error; tables reset on reconnect and re-register lazily; cached PID handles survive sender restarts correctly (address equality, not identity); a wire-bytes assertion test proves the steady-state envelope for a small payload shrinks below a stated byte budget (record the exact figure in the test).

**Acceptance criteria.**

* [x] Steady-state small-message envelope overhead drops from full strings to table refs, asserted numerically in tests (`TestPrepareRefEmitsTableBeforeData`); benchstat throughput gain for small messages to be posted on #1301 at commit handoff.
* [x] Zero per-message allocations for sender-PID materialization on the table-hit path, verified with an allocation benchmark (`TestDecodeDataEnvelopeTableHitAllocs`).
* [x] Revision gating verified: no `TABLE` frames are sent to a revision-2 peer and traffic still flows with inline literals (`TestPrepareRefNoopBelowRevisionThree`, `TestRevisionTwoRejectsInboundTable`).

## Milestone 6: zero-copy receive and allocation pass (benchstat-gated)

**Implementation plan:** [REMOTING_REFACTOR_MILESTONE_6.md](REMOTING_REFACTOR_MILESTONE_6.md)

**Scope.** Zero-copy duplex DATA/REPLY payload handoff through `FramePool`, pooled stock-protobuf encode (`MarshalAppend`) for handshake/control where a pool is available, and a final allocation audit of the hot path. **No new dependency:** vtprotobuf is deferred (unmaintained release cadence; see the implementation plan). After Milestone 2 envelope flattening, the user hot path does not need generated `internalpb` codecs for the copy-count goal.

**Deliverables.**

* Pool DATA/REPLY (and ERROR when release is deterministic) read bodies; return each buffer only after Deserialize / control decode completes. Custom serializer ID 255 copies out of the pooled buffer before Deserialize (or skips the pool). `DuplexSession.ReleasePayload` so remoteclient returns REPLY/ERROR bodies it owns. Explicit release on late-correlated and monitor drop paths.
* Control / handshake encode: `proto.MarshalOptions{UseCachedSize: true}.MarshalAppend` via an exported pooled helper only at caller-owned release sites (handshake after synchronous `WriteFrame`; `sendControlDuplex` after the envelope copies the bytes). ERROR/reject frames and `duplexErrorReply` stay on plain `proto.Marshal` (async queued payload lifetime); decode stays stock `proto.Unmarshal`. See [REMOTING_REFACTOR_MILESTONE_6.md](REMOTING_REFACTOR_MILESTONE_6.md).
* Allocation audit: benchmark-driven pass over the steady-state tell and ask paths with a target of zero allocations beyond the payload buffer, the concrete message, and the envelope refs, fixing stragglers found (waiter pooling, metadata maps, context values).
* The benchstat gate, run and recorded as a comment on #1301 against the Milestone 5 tip.

**Acceptance criteria.**

* [x] Zero-copy receive verified: DATA/REPLY bodies are pooled and released after Deserialize on tell, ask, and control paths (including remoteclient `ReleasePayload` and drop paths), with a focused test suite (`TestGetReadPayloadPoolsDataReplyError`, `TestDuplexTellReleasePayloadReusable`, `TestLateCorrelatedReplyDoesNotStallReader`, `TestDeserializeReplyEnvelopeCustomCopiesPayload`).
* [x] The steady-state tell allocation count is stated with a benchmark proving it (`TestDecodeDataEnvelopeTableHitAllocs`: 0 allocs on table-hit decode); count to be posted on #1301 at commit handoff.
* [ ] Benchstat tables posted on #1301 show no regression on the duplex tell/ask path versus the Milestone 5 tip.
* [x] `make lint` clean; `go.mod` unchanged (no vtprotobuf).

## Milestone 7: credit-based flow control and semantics completion

**Implementation plan:** [REMOTING_REFACTOR_MILESTONE_7.md](REMOTING_REFACTOR_MILESTONE_7.md)

**Status.** Implementation complete; acceptance criteria met (tests, docs, #1301 checklist and performance comment). Diff awaits maintainer approval before the milestone commit.

**Scope.** End-to-end backpressure: the byte-capped writer queue from milestone 2 gains a receiver-granted credit window. Requires peer `revision >= 4`; below that the window is unlimited (pre-credit behavior).

**Deliverables.**

* Sender window: signed atomic byte counter per connection, decremented at write time (not admission time); the writer parks when the window would go negative and resumes on `CREDIT`. Queue admission stays byte-capped as before, so total per-peer buffering remains bounded by queue plus window.
* Receiver accounting: grants issued at each charged frame's first terminal disposition, meaning mailbox enqueue for ordinary `DATA`, reassembly-buffer append for `CHUNK` (this rule is what lets a message larger than the window complete), worker-pool handoff for `expectsReply` frames, and the failure dispositions that keep the connection open (decode-error reply, dead-letter, reject-and-drop), so no path leaks the window. Grants batch at one `CREDIT` per quarter window consumed.
* `WithCreditWindow` becomes the negotiated window (HELLO `initial_credits`, pairwise minimum); config validated.
* Interaction rules, documented in code: credits order below the writer queue. Only `DATA` and `CHUNK` consume the window; `ERROR`, `REPLY`, `TABLE`, `CREDIT`, `PING`, `PONG` are exempt and also bypass a parked writer, so control signaling and liveness cannot deadlock behind windowed data. Windowed frames stay FIFO among themselves; exempt frames may overtake parked windowed frames (no ordering contract crosses the two classes).
* Final semantics statement in docs: enqueue success returns nil; full queue blocks to the caller's deadline, or the write timeout when the caller has none, then `errors.ErrRemoteSendBackpressure`; transport failures dead-letter with an event; slow receivers slow senders without loss.

**Tests.** Window exhaustion parks the writer and `CREDIT` resumes it; a message larger than the window completes (the deadlock regression test); a stalled receiver (no grants) causes sender-side backpressure errors at the deadline rather than memory growth, with per-peer bytes asserted bounded; exempt frame types flow while the window is exhausted; revision gating against a revision-3 peer runs windowless; fairness: two senders to one receiver both make progress under a constrained window.

**Acceptance criteria.**

* [x] The larger-than-window message completes; the stalled-receiver test shows bounded sender memory and timely `ErrRemoteSendBackpressure`.
* [x] Receiver memory under a flooding sender is bounded by window plus reassembly caps, asserted by test.
* [x] Full performance validation recorded on #1301: small-message throughput target (order of 1M msgs/sec aggregate per peer pair), slow-actor ask p99 isolation, and the 100 MiB concurrent-transfer run, benchstat against the Milestone 6 tip (no pre-Milestone-1 baseline comment existed on the issue; absolute M7 numbers and M6 tip benchstat posted).
* [x] Issue #1301 milestone checklist fully ticked; docs complete for every knob in the configuration table.

---

## Cross-milestone appendix

* **Baseline capture.** Before milestone 1 merges, record the pre-refactor benchmark baseline (small-message throughput, ask latency distribution, allocation counts, per-peer memory at 32-connection fan-out) and commit the numbers to the issue as a comment, so milestone 7's final comparison has a fixed reference.
* **File map of touched areas.** New protocol code lives in `internal/net` (frame, handshake, duplex, transport, envelope, chunk reassembly, tables, credits) and `internal/remoteclient` (peer manager, lanes, routing, correlation helper, protocol cache). `actor/remote_server.go` gains the new-protocol acceptor wiring; `remote/config.go` gains the knobs in the defaults table; `errors/errors.go` gains any new public sentinels (oversize, protocol mismatch) following existing patterns.
* **What is never in scope here:** QUIC transport (its own future issue), reliable-delivery changes, cluster membership transports, public serializer contract changes, legacy path deletion.
