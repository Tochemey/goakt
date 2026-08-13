# Unreleased

## ✨ Features

### 🌐 Multiplexed remoting

- 🚦 Added a duplex remoting protocol with dedicated control, ordinary, and large-message lanes ([#1301](https://github.com/Tochemey/goakt/issues/1301)).
- 🔄 Added protocol negotiation with legacy fallback, configurable lane counts, deadlines, frame limits, chunked large messages, and credit-based flow control.
- 🔒 Existing actor APIs (`Tell`, `Ask`, and batches) remain unchanged.

### 📬 Reliable delivery

- ✅ Added confirmed, ordered, flow-controlled point-to-point delivery for local and remote actors ([#1296](https://github.com/Tochemey/goakt/issues/1296), [#1300](https://github.com/Tochemey/goakt/issues/1300)).
- 🧰 Added work-pulling delivery for distributing jobs across dynamic worker groups.
- 🛡️ Added delivery confirmations, retries, deduplication, chunking, durable queues, and cluster/remoting deployment options.

## 🔧 Changes

### 📤 Remote fire-and-forget

- ✅ Enqueue success is now reported consistently on the duplex path.
- 📨 Transport failures are dead-lettered, while outbound congestion returns `errors.ErrRemoteSendBackpressure` after the configured deadline.
- 🔢 Message ordering is preserved per sender–target pair.

## ⚙️ Improvements & Refactors

- 📈 Local `Tell` and `Ask` now scale across independent actor pairs instead of contending on process-wide bottlenecks.
- 🧵 Receive-context recycling now uses sharded pools, reducing contention and steady-state memory use.
- ⚡ Dispatcher wake-ups now use direct worker handoff, removing a shared condition-variable bottleneck.
- 💬 `Ask` uses per-request reply channels, removing global channel-pool contention and timeout reuse hazards.

## 🧪 Tests & Benchmarks

- 📊 Added benchmarks for pairwise throughput and tail latency.

## 📚 Documentation

- 📘 Updated the remoting documentation with duplex protocol, lane, chunking, credit-window, and send-semantics configuration.
- 🏗️ Added contributor architecture documentation for reliable delivery.
