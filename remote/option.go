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

import (
	"reflect"
	"sync"
	"time"

	"github.com/tochemey/goakt/v4/internal/types"
	gtls "github.com/tochemey/goakt/v4/tls"
)

// sharedCBOR is a lazy-initialized singleton for WithSerializables.
// CBORSerializer is stateless and safe for concurrent use; reusing it
// avoids per-option allocations.
var (
	sharedCBOR     *CBORSerializer
	sharedCBOROnce sync.Once
)

// sharedJSON is a lazy-initialized singleton for WithJSONSerializables.
// JSONSerializer is stateless and safe for concurrent use; reusing it
// avoids per-option allocations.
var (
	sharedJSON     *JSONSerializer
	sharedJSONOnce sync.Once
)

// DefaultCBORSerializer returns a shared [CBORSerializer] instance.
// CBORSerializer is stateless and safe for concurrent use; reusing it
// avoids per-option allocations when using [WithSerializables] or
// [WithClientSerializables].
func DefaultCBORSerializer() *CBORSerializer {
	sharedCBOROnce.Do(func() { sharedCBOR = NewCBORSerializer() })
	return sharedCBOR
}

// DefaultJSONSerializer returns a shared [JSONSerializer] instance.
// JSONSerializer is stateless and safe for concurrent use; reusing it
// avoids per-option allocations when using [WithJSONSerializables].
func DefaultJSONSerializer() *JSONSerializer {
	sharedJSONOnce.Do(func() { sharedJSON = NewJSONSerializer() })
	return sharedJSON
}

// Option is the interface that applies a configuration option.
type Option interface {
	// Apply sets the Option value of a config.
	Apply(*Config)
}

// enforce compilation error
var _ Option = OptionFunc(nil)

// OptionFunc implements the Option interface.
type OptionFunc func(config *Config)

func (f OptionFunc) Apply(c *Config) {
	f(c)
}

// WithWriteTimeout sets the write timeout
func WithWriteTimeout(timeout time.Duration) Option {
	return OptionFunc(func(config *Config) {
		config.writeTimeout = timeout
	})
}

// WithReadIdleTimeout sets the read timeout
// ReadIdleTimeout is the timeout after which a health check using a ping
// frame will be carried out if no frame is received on the connection.
// If zero, no health check is performed. When both this and IdleTimeout are
// nonzero, ReadIdleTimeout must be strictly less than IdleTimeout so liveness
// PINGs keep healthy idle lanes from being reclaimed.
func WithReadIdleTimeout(timeout time.Duration) Option {
	return OptionFunc(func(config *Config) {
		config.readIdleTimeout = timeout
	})
}

// WithOrdinaryLanes sets the number of ordinary duplex lanes dialed per peer
// for user tell/ask traffic.
//
// Each lane is its own duplex connection with its own writer queue and credit
// window, dialed on first use. Receivers are pinned to a lane by a stable
// hash of their canonical address, so one receiver always rides one lane and
// per-receiver FIFO holds at any count. What changes with the count is the
// breadth of ordering: at the default of 1 every receiver on a peer node
// shares one connection, so ordering holds across all of them; at higher
// counts different receivers may ride different lanes, and sends to them
// proceed in parallel with independent credit windows. Traffic matched by
// [WithLargeMessageDestinations] bypasses ordinary lanes entirely.
//
// The count is a local dialing-side choice, not negotiated: each lane
// declares its role and index in its HELLO, and the accepting peer serves
// whatever set arrives. Every dialed lane costs one connection with its own
// credit window and buffers, so raising the count trades memory for
// parallelism.
//
// Defaults to [DefaultOrdinaryLanes] (1). Valid values are 1 through 254,
// the largest count whose lane indexes fit the frame header's lane byte;
// [Config.Validate] rejects values outside that range.
func WithOrdinaryLanes(n uint32) Option {
	return OptionFunc(func(config *Config) {
		config.ordinaryLanes = n
	})
}

// WithLargeMessageDestinations sets hierarchical actor-path glob patterns that
// route matching user tell/ask traffic onto a dedicated large lane per peer.
//
// User traffic to a peer spreads over lanes, each its own duplex connection
// with its own writer queue and credit window: one or more ordinary lanes
// (see [WithOrdinaryLanes]) plus one large lane dialed on first use. Bulk
// transfers riding the large lane therefore cannot head-of-line block the
// small messages flowing beside them. That isolation is the whole effect:
// matching is not size enforcement, and oversized payloads to unlisted
// destinations still chunk in place on their ordinary lane, gated per
// connection by [WithMaxConcurrentLargeTransfers].
//
// Patterns match the hierarchical path after host:port (for example
// "orders/*"), never the full goakt:// URI; a leading slash is ignored on
// both pattern and path. Matching follows [path.Match] semantics, where "*"
// stops at "/": "orders/*" matches "orders/o1" but not "orders/o1/child". A
// receiver matching any pattern routes to the large lane; all matching
// receivers on a peer share it, and routing is stable per receiver, so
// per-receiver FIFO is preserved.
//
// The list is a local routing knob on the dialing side, not negotiated with
// peers. Defaults to empty, which matches nothing. [Config.Validate] rejects
// empty and malformed patterns.
func WithLargeMessageDestinations(patterns ...string) Option {
	return OptionFunc(func(config *Config) {
		if len(patterns) == 0 {
			config.largeMessageDestinations = nil
			return
		}
		out := make([]string, len(patterns))
		copy(out, patterns)
		config.largeMessageDestinations = out
	})
}

// WithMaxConcurrentLargeTransfers sets the cap on concurrent chunked message
// transfers per duplex connection.
//
// A message larger than [Config.ChunkSize] crosses the wire as a group of
// CHUNK frames that the receiver holds in a reassembly buffer until complete,
// so every open group can pin up to [Config.MaxMessageSize] bytes on the
// receiving side. This cap bounds that exposure per connection. It is
// enforced on both ends: a sender opening a group beyond the cap waits for a
// slot (surfacing backpressure if its deadline expires first), and a receiver
// soft-rejects an excess group with a request-scoped error rather than
// tearing the connection down.
//
// Peers advertise the value in HELLO and the pairwise minimum takes effect on
// each connection; a peer that omits it is treated as advertising
// [DefaultMaxConcurrentLargeTransfers]. The cap applies per lane connection,
// so a peer dialed with several lanes admits that many groups on each.
//
// Defaults to [DefaultMaxConcurrentLargeTransfers] (4). Must be at least 1;
// [Config.Validate] rejects zero.
func WithMaxConcurrentLargeTransfers(n uint32) Option {
	return OptionFunc(func(config *Config) {
		config.maxConcurrentLargeTransfers = n
	})
}

// WithChunkSize sets the logical-frame size above which duplex senders split
// a message into CHUNK frames.
//
// A message at or below the threshold travels as a single wire frame; a
// larger one is split into CHUNK frames of at most this size and reassembled
// by the peer. Each chunk is admitted and credited individually, so control
// frames and other messages interleave between the chunks of a bulk transfer
// instead of waiting behind it. Smaller chunks give finer interleaving and
// smaller per-frame buffers; larger chunks spend less on per-frame overhead.
// Messages above the threshold count against
// [WithMaxConcurrentLargeTransfers]; when the peer predates chunking support,
// a message must instead fit [Config.MaxFrameSize] or the send fails.
//
// The value is local and not negotiated: it governs what this side emits,
// and peers with different chunk sizes still interoperate because the
// receiver reassembles whatever sizes arrive.
//
// Defaults to [DefaultChunkSize] (256 KiB). Valid values are 16 KiB through
// 4 MiB; [Config.Validate] also requires [Config.MaxFrameSize] and
// [Config.CreditWindow] to be at least this size.
func WithChunkSize(size uint32) Option {
	return OptionFunc(func(config *Config) {
		config.chunkSize = size
	})
}

// WithMaxMessageSize sets the HELLO-advertised cap on a reassembled logical
// frame. It must be at least [Config.MaxFrameSize] and may exceed 16 MiB.
func WithMaxMessageSize(size uint64) Option {
	return OptionFunc(func(config *Config) {
		config.maxMessageSize = size
	})
}

// WithCreditWindow sets how much unreclaimed send traffic a peer may have in
// flight on one duplex connection, in bytes.
//
// Credit is end-to-end permission to send. The receiver opens a byte budget at
// handshake; every DATA/CHUNK write spends from that budget; the receiver
// returns credit only after it has taken ownership of those bytes (mailbox
// enqueue for tells, ask worker-pool handoff, or CHUNK reassembly append).
// That keeps a slow or stuck actor from forcing the sender to grow memory
// without bound: when credit runs out the writer parks, and further sends
// surface as admission backpressure instead of silent buffering. Fire-and-forget
// traffic is not dropped for flow control; it is slowed.
//
// The same value also sizes the local outbound admission queue, so
// per-connection buffering stays on the order of one window queued plus one
// window in flight. Peers advertise it in HELLO and take the pairwise minimum
// when both support capability revision 4; older peers keep an unlimited send
// window. Defaults to [DefaultCreditWindow] (16 MiB). Must be greater than zero
// and at least [Config.ChunkSize].
func WithCreditWindow(bytes uint64) Option {
	return OptionFunc(func(config *Config) {
		config.creditWindow = bytes
	})
}

// WithMaxFrameSize sets the largest single wire frame this node is willing
// to read, in bytes.
//
// The framed reader rejects a frame announcing a larger length before
// reading its body and closes the connection, so the cap bounds what one
// inbound frame can make this node buffer. Peers advertise the value in
// HELLO and each duplex connection settles on the pairwise minimum, floored
// to the protocol minimum when a peer advertises less.
//
// The cap bounds one frame, not one message. On duplex connections a message
// larger than the frame cap is split into CHUNK frames of at most
// [Config.ChunkSize] each and reassembled by the peer, so message size is
// governed by [WithMaxMessageSize] alone: a 100 MiB payload crosses a
// connection with the default 16 MiB frame cap untouched once
// [WithMaxMessageSize] allows it. Only the legacy protocol, which cannot
// chunk, treats this value as the whole-message cap.
//
// Defaults to 16 MiB. Valid values are 16 KiB through 16 MiB inclusive;
// [Config.Validate] also requires the value to be at least
// [Config.ChunkSize] and at most [Config.MaxMessageSize].
func WithMaxFrameSize(size uint32) Option {
	return OptionFunc(func(config *Config) {
		config.maxFrameSize = size
	})
}

// WithCompression sets the compression algorithm to use
// when sending or receiving data.
func WithCompression(c Compression) Option {
	return OptionFunc(func(config *Config) {
		config.compression = c
	})
}

// WithProtocolPin selects which remoting wire protocol this node dials and
// accepts. The pin applies to both sides of the actor system: the remoting
// listener's accept mode and the outbound client's dial mode.
//
// Remoting currently speaks two wire protocols:
//   - duplex: multiplexed, correlation-driven frames over persistent lane
//     connections (the default path for current peers);
//   - legacy: unary protobuf-over-TCP with pooled sockets and the send
//     coalescer (kept so mixed-version clusters can roll node by node).
//
// [ProtocolPinAuto] (the default) keeps both paths live. On accept, the
// listener peeks the first byte after TLS and routes duplex traffic when that
// byte is the duplex version discriminator (0x02); anything else is replayed
// into the legacy path. On dial, the client prefers duplex and falls back to
// legacy when the peer closes or resets before HELLO_ACK, caching that peer as
// legacy and re-probing duplex after 30 seconds so an upgraded peer can
// switch over without a dialer restart.
//
// [ProtocolPinLegacy] and [ProtocolPinDuplex] force a single protocol on both
// dial and accept. Use them for homogeneous clusters, or whenever first-byte
// discrimination is unsafe: legacy brotli compression has no magic byte and
// can collide with the duplex 0x02 discriminator under [ProtocolPinAuto].
// Pinning also makes rollout behavior deterministic when operators do not want
// automatic fallback.
//
// Must be a valid [ProtocolPin]; [Config.Validate] rejects unknown values.
func WithProtocolPin(pin ProtocolPin) Option {
	return OptionFunc(func(config *Config) {
		config.protocolPin = pin
	})
}

// WithTLS sets the TLS settings used to secure the remoting transport.
//
// The tls.Info carries a standard crypto/tls configuration for each side of
// a connection. An actor system requires both ServerConfig and ClientConfig
// because every node accepts connections and dials other nodes; the
// standalone client package only uses ClientConfig. Ensure that both
// configurations chain to the same root Certificate Authority (CA) to enable
// successful handshakes and mutual authentication.
//
// When the actor system runs in cluster mode, these settings also secure the
// cluster engine and membership gossip transports, and all nodes must share
// the same root CA.
func WithTLS(info *gtls.Info) Option {
	return OptionFunc(func(config *Config) {
		config.tlsInfo = info
	})
}

// WithContextPropagator sets the ContextPropagator used to inject and extract
// cross-cutting metadata (e.g., custom headers, correlation IDs, auth tokens)
// for remote calls.
//
// Passing a non-nil propagator enables propagation across process boundaries,
// ensuring values from a context are serialized into headers on outgoing calls
// and restored into the context on incoming calls.
// If propagator is nil, this option is ignored and the default/no-op propagator
// remains in effect.
//
// Typical use:
//   - Integrate distributed tracing (e.g., OpenTelemetry) by providing a propagator
//     implementation that injects/extracts trace context.
//   - Forward request-scoped metadata like user/session IDs or feature flags.
//
// Note: Only non-nil propagators are applied.
// Multiple calls will overwrite the previous propagator with the last non-nil value.
func WithContextPropagator(propagator ContextPropagator) Option {
	return OptionFunc(func(config *Config) {
		if propagator != nil {
			config.contextPropagator = propagator
		}
	})
}

// WithSerializers registers a [Serializer] for a specific message type or for
// all messages that satisfy a given interface.
//
// # Concrete type registration
//
// Pass any value of the target type to bind a serializer to that exact type:
//
//	WithSerializers(new(MyMessage), mySerializer)
//
// When the serializer is [CBORSerializer] and the type is not a [proto.Message],
// the type is automatically registered in the global type registry used for
// CBOR serialization. No separate registration step is required.
//
// # Interface registration
//
// Pass a typed nil pointer to an interface to bind a serializer to every
// message that implements that interface:
//
//	WithSerializers((*proto.Message)(nil), remote.NewProtoSerializer())
//
// # Dispatch order
//
// When [Config.Serializer] resolves a serializer for a message it checks, in order:
//  1. Exact concrete type — the entry registered with the message's dynamic type.
//  2. Interface match — the first registered interface the message implements.
//
// Registration order within each category determines priority.
// If serializer is nil the option is silently ignored.
//
// The default configuration registers [ProtoSerializer] for all [proto.Message]
// implementations. Calling this option with a typed nil pointer to
// [proto.Message] overrides that default for proto messages.
func WithSerializers(msg any, serializer Serializer) Option {
	return OptionFunc(func(config *Config) {
		if serializer == nil {
			return
		}

		typ := reflect.TypeOf(msg)
		// A typed nil pointer whose element is an interface (e.g. (*proto.Message)(nil))
		// registers the serializer for all values that implement that interface.
		if typ != nil && typ.Kind() == reflect.Pointer && typ.Elem().Kind() == reflect.Interface {
			config.serializers[typ.Elem()] = serializer
			return
		}

		types.RegisterSerializerType(msg, serializer)
		config.serializers[reflect.TypeOf(msg)] = serializer
	})
}

// WithSerializables registers the CBOR serializer for each of the given concrete
// or interface types. It is a convenience for registering multiple types with
// [CBORSerializer] without repeating the serializer instance.
//
// See [WithJSONSerializables] for the JSON counterpart.
//
// # Concrete type registration
//
// Pass any value of the target type to bind the CBOR serializer to that exact type:
//
//	WithSerializables(new(MyMessage), new(OtherMessage))
//
// Each concrete type is automatically registered in the global type registry used
// for CBOR serialization. No separate registration step is required.
//
// # Interface registration
//
// Pass a typed nil pointer to an interface to bind the CBOR serializer to every
// message that implements that interface:
//
//	WithSerializables((*MyInterface)(nil))
//
// # Dispatch order
//
// When [Config.Serializer] resolves a serializer for a message it checks, in order:
//  1. Exact concrete type — the entry registered with the message's dynamic type.
//  2. Interface match — the first registered interface the message implements.
//
// Nil entries in the types slice are silently ignored.
func WithSerializables(msgs ...any) Option {
	cbor := DefaultCBORSerializer()
	return OptionFunc(func(config *Config) {
		for _, msg := range msgs {
			if msg == nil {
				continue
			}

			typ := reflect.TypeOf(msg)
			if typ == nil {
				continue
			}
			// A typed nil pointer whose element is an interface (e.g. (*MyInterface)(nil))
			// registers the serializer for all values that implement that interface.
			if typ.Kind() == reflect.Pointer && typ.Elem().Kind() == reflect.Interface {
				config.serializers[typ.Elem()] = cbor
				continue
			}
			// Concrete type — register in global registry and config
			types.RegisterSerializerType(msg, cbor)
			config.serializers[typ] = cbor
		}
	})
}

// WithJSONSerializables registers the JSON serializer for each of the given
// concrete or interface types. It is the JSON counterpart to [WithSerializables]
// and behaves identically aside from the encoding format.
//
// # Concrete type registration
//
// Pass any value of the target type to bind the JSON serializer to that exact type:
//
//	WithJSONSerializables(new(MyMessage), new(OtherMessage))
//
// Each concrete type is automatically registered in the global type registry used
// for JSON serialization. No separate registration step is required.
//
// # Interface registration
//
// Pass a typed nil pointer to an interface to bind the JSON serializer to every
// message that implements that interface:
//
//	WithJSONSerializables((*MyInterface)(nil))
//
// # Dispatch order
//
// When [Config.Serializer] resolves a serializer for a message it checks, in order:
//  1. Exact concrete type — the entry registered with the message's dynamic type.
//  2. Interface match — the first registered interface the message implements.
//
// Nil entries in the types slice are silently ignored.
func WithJSONSerializables(msgs ...any) Option {
	json := DefaultJSONSerializer()
	return OptionFunc(func(config *Config) {
		for _, msg := range msgs {
			if msg == nil {
				continue
			}

			typ := reflect.TypeOf(msg)
			if typ == nil {
				continue
			}
			// A typed nil pointer whose element is an interface (e.g. (*MyInterface)(nil))
			// registers the serializer for all values that implement that interface.
			if typ.Kind() == reflect.Pointer && typ.Elem().Kind() == reflect.Interface {
				config.serializers[typ.Elem()] = json
				continue
			}
			// Concrete type — register in global registry and config
			types.RegisterSerializerType(msg, json)
			config.serializers[typ] = json
		}
	})
}
