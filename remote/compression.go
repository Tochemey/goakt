// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package remote

// Compression represents the compression algorithm applied to data sent and
// received over TCP connections between remote actor systems. Both the client
// (Remoting) and the server (remote.Config) must agree on the algorithm;
// a mismatch will produce unreadable frames.
//
// The default for both NewRemoting and NewConfig / DefaultConfig is
// NoCompression. This matches the convention adopted by gRPC, Akka, Erlang
// distribution, Kafka, Orleans, and most service meshes: transport-level
// compression is left off because typical actor payloads are small structured
// protobuf for which compression yields little bandwidth and meaningful CPU
// and allocation overhead, especially on intra-cluster LAN links.
//
// Enable ZstdCompression (or GzipCompression / BrotliCompression) explicitly
// via WithCompression for deployments where bandwidth is the binding constraint
// — most commonly cross-region / WAN links, or when sending large or highly
// repetitive payloads.
type Compression int

const (
	// NoCompression disables compression entirely. Data is transmitted as raw
	// protobuf-encoded frames with no additional processing. This is the
	// default for both NewRemoting and NewConfig / DefaultConfig.
	//
	// Pros:
	//   - Zero CPU overhead for compression/decompression.
	//   - Lowest possible per-message latency.
	//   - No per-connection encoder/decoder state — lighter on the allocator
	//     under high fan-out topologies.
	//   - Simplifies debugging because payloads are not transformed on the wire.
	//
	// Cons:
	//   - No bandwidth savings; large or repetitive messages consume more
	//     network I/O than they would with a compressed transport.
	//   - For bandwidth-constrained links (cross-region, WAN, metered
	//     networks) prefer ZstdCompression.
	NoCompression Compression = iota

	// GzipCompression uses the gzip (RFC 1952 / DEFLATE) algorithm.
	//
	// Pros:
	//   - Universally supported; well-understood and battle-tested.
	//   - Good compression ratio for most payloads.
	//
	// Cons:
	//   - Higher CPU cost than Zstd at comparable compression levels.
	//   - Slower compression and decompression speeds, which can become a
	//     bottleneck under high message throughput.
	//   - No built-in dictionary support for small-message optimization.
	GzipCompression

	// ZstdCompression uses the Zstandard (RFC 8878) algorithm. Recommended
	// when bandwidth is the binding constraint (cross-region / WAN links,
	// large or highly repetitive payloads). Must be set explicitly via
	// WithCompression; it is not enabled by default.
	//
	// Pros:
	//   - Excellent compression ratio (typically 50-70% bandwidth reduction
	//     on protobuf payloads) with very low CPU overhead.
	//   - Significantly faster compression and decompression than gzip.
	//   - Supports trained dictionaries for small messages (used internally).
	//   - Scales well under high concurrency and message rates.
	//
	// Cons:
	//   - Slightly larger compressed output than Brotli at maximum settings.
	//   - Requires the zstd C library or a pure-Go port, adding a build
	//     dependency.
	ZstdCompression

	// BrotliCompression uses the Brotli (RFC 7932) algorithm.
	//
	// Pros:
	//   - Best compression ratio among the supported algorithms, especially
	//     for text-heavy or repetitive payloads.
	//   - Built-in static dictionary improves ratio on small messages.
	//
	// Cons:
	//   - Compression is notably slower than Zstd, particularly at higher
	//     quality levels; may add measurable latency per message.
	//   - Decompression speed is comparable to gzip but slower than Zstd.
	//   - Higher memory usage during compression.
	//   - Best suited for scenarios where bandwidth is scarce and latency
	//     requirements are relaxed.
	BrotliCompression
)
