# Unreleased

## ✨ Features

### 🌐 Multiplexed remoting

- 🚦 Added a duplex remoting protocol with dedicated control, ordinary, and large-message lanes ([#1301](https://github.com/Tochemey/goakt/issues/1301)).
- 🔄 Added protocol negotiation with legacy fallback and `remote.WithProtocolPin` (`auto`, `legacy`, or `duplex`).
- 📦 Added configurable lanes, deadlines, frame limits, chunked messages (default `256 KiB`), and credit-based flow control (default `16 MiB`).
- 🔒 Existing actor APIs (`Tell`, `Ask`, and batches) remain unchanged.

### 📬 Reliable delivery

- ✅ Added confirmed, ordered, flow-controlled point-to-point delivery for local and remote actors ([#1296](https://github.com/Tochemey/goakt/issues/1296), [#1300](https://github.com/Tochemey/goakt/issues/1300)).
- 🧰 Added work-pulling delivery for distributing jobs across dynamic worker groups.
- 🛡️ Added delivery confirmations, retries, deduplication, chunking, durable queues, and cluster/remoting deployment options. Point-to-point delivery is effectively-once on the fault-free path; work-pulling is at-least-once.
- 🎛️ Added consumer-driven flow control with a default window of `50` messages (maximum `10,000`) and configurable retry/resend intervals.

## 🔧 Changes

### 📤 Remote fire-and-forget

- ✅ Enqueue success is now reported consistently on the duplex path.
- 📨 Transport failures are dead-lettered, while outbound congestion returns `errors.ErrRemoteSendBackpressure` after the configured deadline.
- 🔢 Message ordering is preserved per sender–target pair.

## ⚙️ Improvements & Refactors

- 📈 Local `Tell` and `Ask` now scale across independent actor pairs instead of contending on process-wide bottlenecks. On an 8-core Apple M1, pairwise `Tell` increased from `9.3M` to `25M` msg/s and `Ask` from `1.0M` to `3.1M` req/s.
- 🚀 Single-pair `Tell` increased from `7.8M` to approximately `12M` msg/s; a 32-vCPU GCP instance reached `54–62M` msg/s across pairwise workloads.
- 🧵 Receive-context recycling now uses sharded pools, reducing contention and steady-state memory use.
- ⚡ Dispatcher wake-ups now use direct worker handoff, removing a shared condition-variable bottleneck.
- 💬 `Ask` uses per-request reply channels, removing global channel-pool contention and timeout reuse hazards.

## 🧪 Tests & Benchmarks

- 📊 Added benchmarks for pairwise throughput and tail latency.

## 📚 Documentation

- 📘 Updated the remoting documentation with duplex protocol, lane, chunking, credit-window, and send-semantics configuration.
- 🏗️ Added contributor architecture documentation for reliable delivery.
