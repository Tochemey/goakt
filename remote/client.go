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
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	nethttp "net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v4/address"
	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/codec"
	"github.com/tochemey/goakt/v4/internal/id"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pointer"
	"github.com/tochemey/goakt/v4/internal/strconvx"
	"github.com/tochemey/goakt/v4/internal/xsync"
)

const DefaultMaxReadFrameSize = 16 * 1024 * 1024 // 16 MiB

// Remoting provides the client-side API surface to interact with actors running
// on remote nodes of a Go-Akt cluster. It encapsulates transport configuration
// (TCP/TLS), payload compression, and TCP client construction, and
// offers convenience methods for one-way messaging, request/response, batched
// delivery, lifecycle management, and discovery.
//
// General semantics:
//
//   - Transport: Remoting is implemented on top of a custom proto-based TCP protocol.
//     TLS can be enabled via WithRemotingTLS. The configured MaxReadFrameSize applies
//     to both read and send limits for all RPCs.
//   - Payloads: Messages are serialized as google.protobuf.Any. The caller is
//     responsible for using message types known to both peers.
//   - Addresses: All remote operations identify actors by address.Address.
//   - Concurrency: Implementations are safe for concurrent use by multiple goroutines.
//   - Context: All calls honor context cancellation and deadlines. If both a
//     per-call timeout (where applicable) and a context deadline are provided,
//     the effective deadline is the earliest of the two.
//   - Errors: Errors are returned as standard Go errors. When originating from
//     the remote system, they carry a code (internalpb.Code). Notable codes include
//     NotFound, DeadlineExceeded, Unavailable, and ResourceExhausted.
//   - Ordering & delivery: Fire-and-forget methods provide best-effort delivery
//     with at-most-once semantics at the RPC layer. Delivery to the target actor
//     and application-level ordering guarantees depend on the remote system.
//   - Batching: Batch variants submit multiple messages in a single RPC for
//     efficiency. Partial responses are possible for ask-style batching; callers
//     should attach their own correlation identifiers when needed.
//
// Use NewRemoting to construct an instance. Call Close to release underlying
// resources (e.g., idle TCP connections).
type Remoting interface {
	// RemoteTell sends a one-way (fire-and-forget) message to a remote actor.
	//
	// Parameters:
	//   - ctx: Governs cancellation and deadlines for the outbound RPC.
	//   - from: The sender's actor address. Used for correlation and routing on
	//     the remote system.
	//   - to: The target actor's address on the remote node.
	//   - message: A message that will be serialized into a byte slice.
	//
	// Behavior:
	//   - Returns when the RPC completes; no application-level acknowledgement
	//     from the target actor is awaited.
	//   - Message is serialized into a byte slice. If packing fails,
	//     gerrors.NewErrInvalidMessage is returned.
	//   - Honors compression and frame size limits configured on the client.
	//
	// Errors:
	//   - Transport or server errors with codes such as Unavailable or
	//     ResourceExhausted.
	//   - Context cancellation/deadline errors.
	RemoteTell(ctx context.Context, from, to *address.Address, message any) error

	// RemoteAsk sends a request message to a remote actor and waits for a reply,
	// subject to the provided timeout and context.
	//
	// Parameters:
	//   - ctx: Governs cancellation and deadlines for the outbound RPC.
	//   - from: The sender's actor address.
	//   - to: The target actor's address.
	//   - message: A message to send; serialized into a byte slice.
	//   - timeout: A server-side processing window for collecting responses. If
	//     zero, the server may apply a default policy; ctx still applies.
	//
	// Returns:
	//   - response: The first response returned by the remote actor, if any,
	//     serialized into a byte slice. Nil if no response was produced before deadlines.
	//
	// Behavior:
	//   - If multiple responses arrive, only the first one is returned.
	//   - Serialization failures yield gerrors.NewErrInvalidMessage.
	//
	// Errors:
	//   - NotFound if the receiver does not exist (server-dependent).
	//   - DeadlineExceeded when neither a reply nor completion occurs in time.
	//   - Transport, resource limits, or context errors.
	RemoteAsk(ctx context.Context, from, to *address.Address, message any, timeout time.Duration) (response any, err error)

	// RemoteLookup resolves the address of a named actor on a remote node.
	//
	// Parameters:
	//   - ctx: Governs cancellation and deadlines.
	//   - host, port: Location of the remote actor system.
	//   - name: The actor's registered name on the remote node.
	//
	// Returns:
	//   - addr: The resolved address. If the actor is not found, address.NoSender()
	//     is returned without error.
	//
	// Errors:
	//   - Transport and context errors.
	//   - Other server-side failures surfaced as proto errors.
	RemoteLookup(ctx context.Context, host string, port int, name string) (addr *address.Address, err error)

	// RemoteBatchTell sends multiple fire-and-forget messages to the same remote
	// actor in a single RPC for efficiency.
	//
	// Parameters:
	//   - ctx: Cancellation and deadlines.
	//   - from: Sender address.
	//   - to: Target actor address.
	//   - messages: Slice of messages; nil entries are ignored.
	//
	// Behavior:
	//   - Serializes each message into a byte slice; serialization failures abort and return
	//     gerrors.NewErrInvalidMessage.
	//   - Entire RPC succeeds or fails as a unit at the transport layer (no
	//     per-message delivery status is returned).
	//
	// Errors:
	//   - Transport, resource limits (e.g., frame size), and context errors.
	RemoteBatchTell(ctx context.Context, from, to *address.Address, messages []any) error

	// RemoteBatchAsk sends multiple request messages to a remote actor and
	// returns all responses collected before the provided timeout elapses.
	//
	// Parameters:
	//   - ctx: Cancellation and deadlines.
	//   - from: Sender address.
	//   - to: Target actor address.
	//   - messages: Slice of protobuf messages; nil entries are ignored.
	//   - timeout: Server-side collection window for responses. If zero, server
	//     policy applies; ctx still applies.
	//
	// Returns:
	//   - responses: Zero or more responses (Any). The number and order of
	//     responses may not match the requests. Use correlation IDs in your
	//     messages if you need to match responses to requests.
	//
	// Errors:
	//   - Serialization failures (gerrors.NewErrInvalidMessage).
	//   - Transport, resource limits, and context errors.
	RemoteBatchAsk(ctx context.Context, from, to *address.Address, messages []any, timeout time.Duration) (responses []any, err error)

	// RemoteSpawn requests creation of a new actor on the remote node.
	//
	// Parameters:
	//   - ctx: Cancellation and deadlines.
	//   - host, port: Location of the remote actor system.
	//   - spawnRequest: Desired actor name, type (kind), singleton/relocatable
	//     flags, passivation strategy, dependencies, and stash behavior.
	//
	// Behavior:
	//   - Validates and sanitizes spawnRequest before sending.
	//   - Dependencies and passivation strategy are encoded for transport.
	//
	// Errors:
	//   - ErrTypeNotRegistered when the remote system does not recognize the
	//     actor type (kind).
	//   - AlreadyExists or FailedPrecondition (server-dependent) if the name
	//     conflicts with an existing actor.
	//   - Transport and context errors.
	RemoteSpawn(ctx context.Context, host string, port int, spawnRequest *SpawnRequest) error

	// RemoteReSpawn requests a restart of an existing actor on the remote node.
	//
	// Parameters:
	//   - ctx: Cancellation and deadlines.
	//   - host, port: Location of the remote actor system.
	//   - name: Actor name.
	//
	// Behavior:
	//   - If the actor does not exist, this is treated as a no-op and returns nil.
	//
	// Errors:
	//   - Transport and context errors.
	RemoteReSpawn(ctx context.Context, host string, port int, name string) error

	// RemoteStop requests termination of an actor on the remote node.
	//
	// Parameters:
	//   - ctx: Cancellation and deadlines.
	//   - host, port: Location of the remote actor system.
	//   - name: Actor name.
	//
	// Behavior:
	//   - If the actor does not exist, this is treated as a no-op and returns nil.
	//
	// Errors:
	//   - Transport and context errors.
	RemoteStop(ctx context.Context, host string, port int, name string) error

	// RemoteReinstate requests resumption of a previously passivated actor on
	// the remote node.
	//
	// Parameters:
	//   - ctx: Cancellation and deadlines.
	//   - host, port: Location of the remote actor system.
	//   - name: Actor name.
	//
	// Behavior:
	//   - If the actor does not exist, this is treated as a no-op and returns nil.
	//
	// Errors:
	//   - Transport and context errors.
	RemoteReinstate(ctx context.Context, host string, port int, name string) error

	// RemoteActivateGrain requests activation of a grain on the given remote node.
	//
	// Parameters:
	//   - ctx: Governs cancellation and deadlines for the outbound RPC.
	//   - host, port: Location of the remote actor system where the grain should be activated.
	//   - grainRequest: Grain activation details (identity, kind, and any activation metadata).
	//
	// Behavior:
	//   - Ensures the target grain instance is activated on the remote system (idempotency is server-dependent).
	//
	// Errors:
	//   - Transport and context errors.
	//   - Server-side failures surfaced as proto errors (e.g., Unavailable, DeadlineExceeded).
	//
	// Note: The grain kind must be registered on the remote actor system using RegisterGrainKind.
	RemoteActivateGrain(ctx context.Context, host string, port int, grainRequest *GrainRequest) error

	// RemoteTellGrain sends a one-way (fire-and-forget) message to a grain.
	//
	// Parameters:
	//   - ctx: Governs cancellation and deadlines for the outbound RPC.
	//   - host, port: Location of the remote actor system where the grain is hosted.
	//   - grainRequest: Grain activation details (identity, kind, and any activation metadata).
	//   - message: A message that will be serialized into a byte slice.
	//
	// Behavior:
	//   - Returns when the RPC completes; no application-level acknowledgement is awaited.
	//   - Message serialization failures return gerrors.NewErrInvalidMessage.
	//
	// Errors:
	//   - Transport and context errors.
	//
	// Note: The grain must already be activated, or the grain kind must be registered on the remote actor system using RegisterGrainKind.
	RemoteTellGrain(ctx context.Context, host string, port int, grainRequest *GrainRequest, message any) error

	// RemoteAskGrain sends a request message to a grain and waits for a reply, subject to the provided timeout and context.
	//
	// Parameters:
	//   - ctx: Governs cancellation and deadlines for the outbound RPC.
	//   - host, port: Location of the remote actor system where the grain is hosted.
	//   - grainRequest: Grain activation details (identity, kind, and any activation metadata).
	//   - message: A protobuf message to send; packed into a google.protobuf.Any.
	//   - timeout: A server-side processing window for sending the request and collecting the response. If zero, the server may apply a default policy; ctx still applies.
	//
	// Returns:
	//   - response: The response from the remote grain, if any, wrapped in Any.
	//
	// Errors:
	//   - Packing failures return gerrors.NewErrInvalidMessage.
	//   - Transport and context errors.
	//
	// Note: The grain must already be activated, or the grain kind must be registered on the remote actor system using RegisterGrainKind.
	RemoteAskGrain(ctx context.Context, host string, port int, grainRequest *GrainRequest, message any, timeout time.Duration) (response any, err error)

	// NetClient returns a cached or newly created net client for the endpoint.
	// The returned client maintains its own connection pool and should NOT be closed by
	// callers — it lives for the lifetime of the Remoting instance.
	//
	// This method is thread-safe and uses double-checked locking for initialization.
	// Advanced use only: for custom net operations not covered by the standard interface.
	NetClient(host string, port int) *inet.Client

	// Close releases resources held by the client, such as idle TCP connections
	// and connection pools. Close does not cancel in-flight requests; use context
	// cancellation for that.
	Close()

	// Compression returns the payload compression strategy configured on this
	// client. This governs the content encoding used for both requests and responses.
	Compression() Compression

	// TLSConfig returns the TLS configuration used by this client, if any. A
	// nil return value indicates that the client is using an insecure transport.
	TLSConfig() *tls.Config

	// Serializer returns a [Serializer] for the given message.
	//
	// Send path — pass the outgoing message to obtain the most-specific
	// registered serializer for its dynamic type:
	//
	//	r.Serializer(message).Serialize(message)
	//
	// Receive path — pass nil to obtain a composite serializer that tries each
	// registered serializer in registration order; the first successful
	// [Serializer.Deserialize] result is returned:
	//
	//	r.Serializer(nil).Deserialize(rawBytes)
	Serializer(msg any) Serializer
}

// RemotingOption configures a remoting client instance during construction.
type RemotingOption func(*remoting)

// WithRemotingTLS configures the remoting system to use a secure connection
// for communication with the specified remote node. This requires a TLS
// client configuration to enable secure interactions with the remote actor system.
//
// Ensure that the remote actor system is configured with TLS enabled and
// capable of completing a successful handshake. It is recommended that both
// systems share the same root Certificate Authority (CA) for mutual trust and
// secure communication.
func WithRemotingTLS(tlsConfig *tls.Config) RemotingOption {
	return func(r *remoting) {
		r.tlsConfig = tlsConfig
	}
}

// WithRemotingMaxIdleConns sets the connection pool size per remote endpoint.
// Higher values improve throughput under high concurrency at the cost of more
// file descriptors. Default: 8 connections per endpoint.
func WithRemotingMaxIdleConns(n int) RemotingOption {
	return func(r *remoting) {
		if n > 0 {
			r.maxIdleConns = n
		}
	}
}

// WithRemotingIdleTimeout sets how long pooled connections remain idle before
// eviction. Longer timeouts improve connection reuse but consume more resources.
// Default: 30 seconds.
func WithRemotingIdleTimeout(d time.Duration) RemotingOption {
	return func(r *remoting) {
		if d > 0 {
			r.idleTimeout = d
		}
	}
}

// WithRemotingDialTimeout sets the timeout for establishing new TCP connections.
// Default: 5 seconds.
func WithRemotingDialTimeout(d time.Duration) RemotingOption {
	return func(r *remoting) {
		if d > 0 {
			r.dialTimeout = d
		}
	}
}

// WithRemotingKeepAlive sets the TCP keep-alive interval for idle connections.
// This helps detect broken connections earlier. Default: 15 seconds.
func WithRemotingKeepAlive(d time.Duration) RemotingOption {
	return func(r *remoting) {
		if d > 0 {
			r.keepAlive = d
		}
	}
}

// WithRemotingCompression sets the compression algorithm applied to payloads
// exchanged with remote actor systems. The provided Compression must be one of
// the supported options for remoting traffic.
//
// Supported compression algorithms:
//   - ZstdCompression (default): Optimal balance of compression ratio and CPU usage.
//     Recommended for most use cases, especially high-frequency messaging.
//   - BrotliCompression: Excellent compression ratio, good for web contexts.
//   - GzipCompression: Widely supported, good compatibility but slower than Zstd.
//   - NoCompression: Disables compression. Use only if CPU is extremely constrained
//     or for debugging purposes.
//
// If not specified, ZstdCompression is used by default for optimal performance.
func WithRemotingCompression(c Compression) RemotingOption {
	return func(r *remoting) {
		r.compression = c
	}
}

// WithRemotingContextPropagator sets a propagator used to inject context values into outbound remoting calls.
// If nil, no propagation occurs.
func WithRemotingContextPropagator(propagator ContextPropagator) RemotingOption {
	return func(r *remoting) {
		if propagator != nil {
			r.contextPropagator = propagator
		}
	}
}

// WithRemotingSerializers registers a [Serializer] for a specific message type
// or for all messages that satisfy a given interface.
//
// # Concrete type registration
//
// Pass any value of the target type to bind a serializer to that exact type:
//
//	WithRemotingSerializers(new(MyMessage), mySerializer)
//
// # Interface registration
//
// Pass a typed nil pointer to an interface to bind a serializer to every
// message that implements that interface:
//
//	WithRemotingSerializers((*proto.Message)(nil), remote.NewProtoSerializer())
//
// # Dispatch order
//
// When [Remoting] serializes a message it checks, in order:
//  1. Exact concrete type — the entry registered with the message's dynamic type.
//  2. Interface match — the first registered interface the message implements.
//  3. Default — the [Serializer] configured via [WithRemotingSerializer].
//
// If serializer is nil the option is silently ignored.
func WithRemotingSerializers(msg any, serializer Serializer) RemotingOption {
	return func(r *remoting) {
		if serializer == nil {
			return
		}

		typ := reflect.TypeOf(msg)
		// A typed nil pointer whose element is an interface (e.g. (*proto.Message)(nil))
		// registers the serializer for all values that implement that interface.
		if typ != nil && typ.Kind() == reflect.Ptr && typ.Elem().Kind() == reflect.Interface {
			r.serializers = append(r.serializers, ifaceEntry{
				iface:      typ.Elem(),
				serializer: serializer,
			})
			return
		}

		r.serializers = append(r.serializers, ifaceEntry{
			iface:      reflect.TypeOf(msg),
			serializer: serializer,
		})
	}
}

// ifaceEntry pairs a reflect.Type that represents an interface with the
// [Serializer] to use for any message that implements that interface.
// Entries are appended via [WithRemotingSerializers] and evaluated in
// registration order inside [remoting.resolveSerializer].
type ifaceEntry struct {
	iface      reflect.Type
	serializer Serializer
}

// remoting is the default Remoting implementation backed by proto TCP
// clients. It encapsulates transport setup, compression, connection pooling,
// and TLS configuration required to reach remote actor systems.
type remoting struct {
	// Client configuration
	tlsConfig         *tls.Config
	compression       Compression
	contextPropagator ContextPropagator

	// Connection pool settings
	maxIdleConns int           // Pool size per endpoint (default: 8)
	idleTimeout  time.Duration // Connection idle timeout (default: 30s)
	dialTimeout  time.Duration // Dial timeout (default: 5s)
	keepAlive    time.Duration // TCP keep-alive (default: 15s)

	// Client factory and cache
	clientFactory func(host string, port int) *inet.Client
	clientMu      sync.RWMutex // Protects clientCache during initialization

	// clientCache provides thread-safe caching of inet.Client instances
	// keyed by "host:port". Each client maintains its own connection pool
	// for maximum performance. This cache is populated lazily on first access
	// and persists for the lifetime of the remoting instance.
	//
	// Uses xsync.Map for type safety and lock-free reads in the common path.
	clientCache *xsync.Map[string, *inet.Client]

	// serializers holds all per-type and per-interface serializer entries
	// evaluated in registration order by [resolveSerializer].
	// Concrete-type entries are stored with iface == reflect.TypeOf(msg);
	// interface entries are stored with iface == reflect.TypeOf((*I)(nil)).Elem().
	//
	// The slice is populated exclusively during NewRemoting (option application
	// is single-threaded) and is never mutated afterwards. resolveSerializer
	// therefore reads it without any lock, eliminating mutex overhead on the
	// hot message-send path.
	serializers []ifaceEntry

	// dispatcher is the pre-built composite serializer returned by
	// Serializer(nil) on the receive path. It is constructed once after all
	// options are applied and reused for every inbound message, avoiding a
	// per-call heap allocation.
	dispatcher Serializer
}

var _ Remoting = (*remoting)(nil)

// NewRemoting constructs a Remoting client configured with the supplied
// options. By default, the returned client uses an insecure TCP transport with
// connection pooling (8 connections per endpoint) and Zstd compression.
// Custom options may enable TLS, adjust pool sizes, or change compression.
//
// Default settings are optimized for low-latency, high-throughput actor messaging:
//   - 8 pooled connections per remote endpoint
//   - 30s idle timeout before connection eviction
//   - 5s dial timeout for new connections
//   - 15s TCP keep-alive for detecting broken connections
//   - Zstd compression (matches server default in remote.Config)
//
// Call Close when the client is no longer needed to avoid leaking TCP connections.
func NewRemoting(opts ...RemotingOption) Remoting {
	r := &remoting{
		// Performance defaults based on benchmarks
		maxIdleConns: 8,                // 8 pooled connections per endpoint
		idleTimeout:  30 * time.Second, // 30s before eviction
		dialTimeout:  5 * time.Second,  // 5s dial timeout
		keepAlive:    15 * time.Second, // 15s TCP keep-alive
		compression:  ZstdCompression,  // Zstd compression (matches server default in remote.Config)
		clientCache:  xsync.NewMap[string, *inet.Client](),
		// Pre-allocate with capacity for the default entry plus a few custom ones.
		serializers: make([]ifaceEntry, 0, 4),
	}

	// Register the default proto serializer for all proto.Message implementations.
	r.serializers = append(r.serializers, ifaceEntry{
		iface:      reflect.TypeOf((*proto.Message)(nil)).Elem(),
		serializer: NewProtoSerializer(),
	})

	// Apply options
	for _, opt := range opts {
		opt(r)
	}

	// Build the composite dispatcher once; it references the now-frozen
	// serializers slice and is returned by Serializer(nil) on every
	// inbound message without allocation.
	r.dispatcher = &serializerDispatch{entries: r.serializers}

	// Set default factory if not overridden (for testing)
	if r.clientFactory == nil {
		r.clientFactory = r.newNetClient
	}

	return r
}

// NetClient returns a cached or newly created net client for the endpoint.
// The returned client maintains its own connection pool and should NOT be closed by
// callers — it lives for the lifetime of the Remoting instance.
//
// This method is thread-safe and uses double-checked locking for initialization.
func (r *remoting) NetClient(host string, port int) *inet.Client {
	cacheKey := net.JoinHostPort(host, strconv.Itoa(port))

	// Fast path: return cached client (lock-free read)
	if cached, ok := r.clientCache.Get(cacheKey); ok {
		return cached
	}

	// Slow path: create new client (rare, happens once per endpoint)
	r.clientMu.Lock()
	defer r.clientMu.Unlock()

	// Double-check after acquiring lock
	if cached, ok := r.clientCache.Get(cacheKey); ok {
		return cached
	}

	client := r.clientFactory(host, port)
	r.clientCache.Set(cacheKey, client)
	return client
}

// RemoteActivateGrain requests activation of a grain on the given remote node.
//
// Parameters:
//   - ctx: Governs cancellation and deadlines for the outbound RPC.
//   - host, port: Location of the remote actor system where the grain should be activated.
//   - grainRequest: Grain activation details (identity, kind, and any activation metadata).
//
// Behavior:
//   - Ensures the target grain instance is activated on the remote system (idempotency is server-dependent).
//
// Errors:
//   - Transport and context errors.
//   - Server-side failures surfaced as proto errors (e.g., Unavailable, DeadlineExceeded).
//
// Note: The grain kind must be registered on the remote actor system using RegisterGrainKind.
func (r *remoting) RemoteActivateGrain(ctx context.Context, host string, port int, grainRequest *GrainRequest) error {
	grain, err := getGrainFromRequest(host, port, grainRequest)
	if err != nil {
		return err
	}

	// Enrich context with metadata
	ctx, err = r.enrichContext(ctx)
	if err != nil {
		return err
	}

	// Get pooled client
	client := r.NetClient(host, port)
	request := &internalpb.RemoteActivateGrainRequest{
		Grain: grain,
	}

	// Send request
	resp, err := client.SendProto(ctx, request)
	if err != nil {
		return err
	}

	return checkProtoError(resp)
}

// RemoteAskGrain sends a request message to a grain and waits for a reply, subject to the provided timeout and context.
//
// Parameters:
//   - ctx: Governs cancellation and deadlines for the outbound RPC.
//   - host, port: Location of the remote actor system where the grain is hosted.
//   - grainRequest: Grain activation details (identity, kind, and any activation metadata).
//   - message: A protobuf message to send; packed into a google.protobuf.Any.
//   - timeout: A server-side processing window for sending the request and collecting the response. If zero, the server may apply a default policy; ctx still applies.
//
// Returns:
//   - response: The response from the remote grain, if any, wrapped in Any.
//
// Errors:
//   - Packing failures return gerrors.NewErrInvalidMessage.
//   - Transport and context errors.
//
// Note: The grain must already be activated, or the grain kind must be registered on the remote actor system using RegisterGrainKind.
func (r *remoting) RemoteAskGrain(ctx context.Context, host string, port int, grainRequest *GrainRequest, message any, timeout time.Duration) (response any, err error) {
	grain, err := getGrainFromRequest(host, port, grainRequest)
	if err != nil {
		return nil, err
	}

	serializer := r.resolveSerializer(message)
	if serializer == nil {
		return nil, gerrors.NewErrInvalidMessage(errors.New("no serializer found for message type"))
	}

	marshaled, err := serializer.Serialize(message)
	if err != nil {
		return nil, gerrors.NewErrInvalidMessage(err)
	}

	// Enrich context with metadata
	ctx, err = r.enrichContext(ctx)
	if err != nil {
		return nil, err
	}

	// Get pooled client
	client := r.NetClient(host, port)
	request := &internalpb.RemoteAskGrainRequest{
		Grain:          grain,
		Message:        marshaled,
		RequestTimeout: durationpb.New(timeout),
	}

	// Send request
	resp, err := client.SendProto(ctx, request)
	if err != nil {
		return nil, err
	}

	if err := checkProtoError(resp); err != nil {
		return nil, err
	}

	askResp, ok := resp.(*internalpb.RemoteAskGrainResponse)
	if !ok {
		return nil, errors.New("invalid response type")
	}

	deserialized, err := serializer.Deserialize(askResp.GetMessage())
	if err != nil {
		return nil, gerrors.NewErrInvalidMessage(err)
	}
	return deserialized, nil
}

// RemoteTellGrain sends a one-way (fire-and-forget) message to a grain.
//
// Parameters:
//   - ctx: Governs cancellation and deadlines for the outbound RPC.
//   - host, port: Location of the remote actor system where the grain is hosted.
//   - grainRequest: Grain activation details (identity, kind, and any activation metadata).
//   - message: A protobuf message that will be packed into a google.protobuf.Any.
//
// Behavior:
//   - Returns when the RPC completes; no application-level acknowledgement is awaited.
//   - Message packing failures return gerrors.NewErrInvalidMessage.
//
// Errors:
//   - Transport and context errors.
//
// Note: The grain must already be activated, or the grain kind must be registered on the remote actor system using RegisterGrainKind.
func (r *remoting) RemoteTellGrain(ctx context.Context, host string, port int, grainRequest *GrainRequest, message any) error {
	grain, err := getGrainFromRequest(host, port, grainRequest)
	if err != nil {
		return err
	}

	serializer := r.resolveSerializer(message)
	if serializer == nil {
		return gerrors.NewErrInvalidMessage(fmt.Errorf("no serializer found for message type %T", message))
	}

	marshaled, err := serializer.Serialize(message)
	if err != nil {
		return gerrors.NewErrInvalidMessage(err)
	}

	// Enrich context with metadata
	ctx, err = r.enrichContext(ctx)
	if err != nil {
		return err
	}

	// Get pooled client
	client := r.NetClient(host, port)
	request := &internalpb.RemoteTellGrainRequest{
		Grain:   grain,
		Message: marshaled,
	}

	// Send request
	resp, err := client.SendProto(ctx, request)
	if err != nil {
		return err
	}

	return checkProtoError(resp)
}

// RemoteTell sends a message to a remote actor without awaiting a reply. The
// message is serialized into google.protobuf.Any and transmitted as a single RPC.
//
// Semantics and caveats:
//   - Best-effort, at-most-once delivery at the transport layer.
//   - No application-level acknowledgement is awaited; success indicates the
//     RPC completed, not that the actor processed the message.
//   - Context cancellation and client/server frame limits are enforced.
//   - Packing failures return gerrors.NewErrInvalidMessage.
func (r *remoting) RemoteTell(ctx context.Context, from, to *address.Address, message any) error {
	serializer := r.resolveSerializer(message)
	if serializer == nil {
		return gerrors.NewErrInvalidMessage(fmt.Errorf("no serializer found for message type %T", message))
	}

	marshaled, err := serializer.Serialize(message)
	if err != nil {
		return gerrors.NewErrInvalidMessage(err)
	}

	// Enrich context with metadata
	ctx, err = r.enrichContext(ctx)
	if err != nil {
		return err
	}

	// Get pooled client
	client := r.NetClient(to.Host(), to.Port())
	request := &internalpb.RemoteTellRequest{
		RemoteMessages: []*internalpb.RemoteMessage{
			{
				Sender:   from.String(),
				Receiver: to.String(),
				Message:  marshaled,
			},
		},
	}

	// Send request
	resp, err := client.SendProto(ctx, request)
	if err != nil {
		return err
	}

	return checkProtoError(resp)
}

// RemoteAsk delivers a request message to a remote actor and waits for the first
// reply within the specified timeout. The response is returned as an Any proto.
//
// Notes:
//   - If multiple responses are produced, the first one is returned.
//   - A zero timeout defers to server policy; ctx still governs cancellation.
//   - Errors may include NotFound, DeadlineExceeded, Unavailable, etc.
func (r *remoting) RemoteAsk(ctx context.Context, from, to *address.Address, message any, timeout time.Duration) (response any, err error) {
	serializer := r.resolveSerializer(message)
	if serializer == nil {
		return nil, gerrors.NewErrInvalidMessage(fmt.Errorf("no serializer found for message type %T", message))
	}

	marshaled, err := serializer.Serialize(message)
	if err != nil {
		return nil, gerrors.NewErrInvalidMessage(err)
	}

	// Enrich context with metadata
	ctx, err = r.enrichContext(ctx)
	if err != nil {
		return nil, err
	}

	// Get pooled client
	client := r.NetClient(to.Host(), to.Port())
	request := &internalpb.RemoteAskRequest{
		RemoteMessages: []*internalpb.RemoteMessage{
			{
				Sender:   from.String(),
				Receiver: to.String(),
				Message:  marshaled,
			},
		},
		Timeout: durationpb.New(timeout),
	}

	// Send request
	resp, err := client.SendProto(ctx, request)
	if err != nil {
		return nil, err
	}

	if err := checkProtoError(resp); err != nil {
		return nil, err
	}

	askResp, ok := resp.(*internalpb.RemoteAskResponse)
	if !ok {
		return nil, errors.New("invalid response type")
	}

	if len(askResp.Messages) == 0 {
		return nil, nil
	}

	deserialized, err := serializer.Deserialize(askResp.Messages[0])
	if err != nil {
		return nil, gerrors.NewErrInvalidMessage(err)
	}
	return deserialized, nil
}

// RemoteLookup resolves the address of an actor hosted on a remote node. A
// NoSender address is returned when the target actor cannot be found.
//
// Errors are returned for transport problems or other server-side failures.
func (r *remoting) RemoteLookup(ctx context.Context, host string, port int, name string) (addr *address.Address, err error) {
	port32, err := strconvx.Int2Int32(port)
	if err != nil {
		return nil, err
	}

	// Enrich context with metadata
	ctx, err = r.enrichContext(ctx)
	if err != nil {
		return nil, err
	}

	// Get pooled client
	client := r.NetClient(host, port)
	request := &internalpb.RemoteLookupRequest{
		Host: host,
		Port: port32,
		Name: name,
	}

	// Send request
	resp, err := client.SendProto(ctx, request)
	if err != nil {
		return nil, err
	}

	// Handle NOT_FOUND specially - return NoSender without error
	if errResp, ok := resp.(*internalpb.Error); ok {
		if errResp.GetCode() == internalpb.Code_CODE_NOT_FOUND {
			return address.NoSender(), nil
		}
		return nil, checkProtoError(errResp)
	}

	lookupResp, ok := resp.(*internalpb.RemoteLookupResponse)
	if !ok {
		return nil, errors.New("invalid response type")
	}

	return address.Parse(lookupResp.GetAddress())
}

// RemoteBatchTell sends multiple asynchronous messages to the same remote actor
// in a single RPC. Nil entries in messages are ignored.
//
// The call succeeds or fails as a whole at the transport layer; no per-message
// acknowledgement is returned.
func (r *remoting) RemoteBatchTell(ctx context.Context, from, to *address.Address, messages []any) error {
	remoteMessages := make([]*internalpb.RemoteMessage, 0, len(messages))
	// Pre-compute address strings once; they are constant across the whole batch.
	fromStr := from.String()
	toStr := to.String()
	for _, message := range messages {
		if message != nil {
			serializer := r.resolveSerializer(message)
			if serializer == nil {
				return gerrors.NewErrInvalidMessage(fmt.Errorf("no serializer found for message type %T", message))
			}

			packed, err := serializer.Serialize(message)
			if err != nil {
				return gerrors.NewErrInvalidMessage(err)
			}

			remoteMessages = append(remoteMessages, &internalpb.RemoteMessage{
				Sender:   fromStr,
				Receiver: toStr,
				Message:  packed,
			})
		}
	}

	// Enrich context with metadata
	ctx, err := r.enrichContext(ctx)
	if err != nil {
		return err
	}

	// Get pooled client
	client := r.NetClient(to.Host(), to.Port())
	request := &internalpb.RemoteTellRequest{
		RemoteMessages: remoteMessages,
	}

	// Send request
	resp, err := client.SendProto(ctx, request)
	if err != nil {
		return err
	}

	return checkProtoError(resp)
}

// RemoteBatchAsk delivers multiple request messages to a remote actor and
// aggregates all responses received before the provided timeout elapses.
//
// The number and order of responses may not match the requests. If correlation
// is required, include a correlation ID within your message payloads.
func (r *remoting) RemoteBatchAsk(ctx context.Context, from, to *address.Address, messages []any, timeout time.Duration) (responses []any, err error) {
	remoteMessages := make([]*internalpb.RemoteMessage, 0, len(messages))
	serializers := make([]Serializer, 0, len(messages))
	// Pre-compute address strings once; they are constant across the whole batch.
	fromStr := from.String()
	toStr := to.String()

	for _, message := range messages {
		if message != nil {
			serializer := r.resolveSerializer(message)
			if serializer == nil {
				return nil, gerrors.NewErrInvalidMessage(fmt.Errorf("no serializer found for message type %T", message))
			}

			packed, err := serializer.Serialize(message)
			if err != nil {
				return nil, gerrors.NewErrInvalidMessage(err)
			}

			serializers = append(serializers, serializer)
			remoteMessages = append(remoteMessages, &internalpb.RemoteMessage{
				Sender:   fromStr,
				Receiver: toStr,
				Message:  packed,
			})
		}
	}

	// Enrich context with metadata
	ctx, err = r.enrichContext(ctx)
	if err != nil {
		return nil, err
	}

	// Get pooled client
	client := r.NetClient(to.Host(), to.Port())
	request := &internalpb.RemoteAskRequest{
		RemoteMessages: remoteMessages,
		Timeout:        durationpb.New(timeout),
	}

	// Send request
	resp, err := client.SendProto(ctx, request)
	if err != nil {
		return nil, err
	}

	if err := checkProtoError(resp); err != nil {
		return nil, err
	}

	askResp, ok := resp.(*internalpb.RemoteAskResponse)
	if !ok {
		return nil, errors.New("invalid response type")
	}

	responses = make([]any, 0, len(askResp.GetMessages()))
	for index, message := range askResp.GetMessages() {
		deserialized, err := serializers[index].Deserialize(message)
		if err != nil {
			return nil, gerrors.NewErrInvalidMessage(err)
		}
		responses = append(responses, deserialized)
	}

	return responses, nil
}

// RemoteSpawn creates an actor on a remote node using the provided spawn
// request. The target actor type must already be registered on the remote
// system.
//
// Returns ErrTypeNotRegistered if the remote node does not recognize the actor
// type (kind). Other failures may include name conflicts or transport errors.
func (r *remoting) RemoteSpawn(ctx context.Context, host string, port int, spawnRequest *SpawnRequest) error {
	if err := spawnRequest.Validate(); err != nil {
		return fmt.Errorf("invalid spawn option: %w", err)
	}

	spawnRequest.Sanitize()

	var (
		dependencies []*internalpb.Dependency
		err          error
	)

	if len(spawnRequest.Dependencies) > 0 {
		dependencies, err = codec.EncodeDependencies(spawnRequest.Dependencies...)
		if err != nil {
			return err
		}
	}

	port32, err := strconvx.Int2Int32(port)
	if err != nil {
		return err
	}

	var singletonSpec *internalpb.SingletonSpec
	if spawnRequest.Singleton != nil {
		singletonSpec = &internalpb.SingletonSpec{
			SpawnTimeout: durationpb.New(spawnRequest.Singleton.SpawnTimeout),
			WaitInterval: durationpb.New(spawnRequest.Singleton.WaitInterval),
			MaxRetries:   spawnRequest.Singleton.MaxRetries,
		}
	}

	var reentrancy *internalpb.ReentrancyConfig
	if spawnRequest.Reentrancy != nil {
		reentrancy = codec.EncodeReentrancy(spawnRequest.Reentrancy)
	}

	// Enrich context with metadata
	ctx, err = r.enrichContext(ctx)
	if err != nil {
		return err
	}

	// Get pooled client
	client := r.NetClient(host, port)
	request := &internalpb.RemoteSpawnRequest{
		Host:                host,
		Port:                port32,
		ActorName:           spawnRequest.Name,
		ActorType:           spawnRequest.Kind,
		Singleton:           singletonSpec,
		Relocatable:         spawnRequest.Relocatable,
		PassivationStrategy: codec.EncodePassivationStrategy(spawnRequest.PassivationStrategy),
		Dependencies:        dependencies,
		EnableStash:         spawnRequest.EnableStashing,
		Role:                spawnRequest.Role,
		Supervisor:          codec.EncodeSupervisor(spawnRequest.Supervisor),
		Reentrancy:          reentrancy,
	}

	// Send request
	resp, err := client.SendProto(ctx, request)
	if err != nil {
		return err
	}

	return checkProtoError(resp)
}

// RemoteReSpawn requests a restart of an existing actor on the remote node.
func (r *remoting) RemoteReSpawn(ctx context.Context, host string, port int, name string) error {
	port32, err := strconvx.Int2Int32(port)
	if err != nil {
		return err
	}

	// Enrich context with metadata
	ctx, err = r.enrichContext(ctx)
	if err != nil {
		return err
	}

	// Get pooled client
	client := r.NetClient(host, port)
	request := &internalpb.RemoteReSpawnRequest{
		Host: host,
		Port: port32,
		Name: name,
	}

	// Send request
	resp, err := client.SendProto(ctx, request)
	if err != nil {
		return err
	}

	// Ignore NOT_FOUND errors (actor doesn't exist)
	if errResp, ok := resp.(*internalpb.Error); ok {
		if errResp.GetCode() == internalpb.Code_CODE_NOT_FOUND {
			return nil
		}
	}

	return checkProtoError(resp)
}

// RemoteStop terminates the specified actor on the remote node. Missing actors
// are ignored and return nil.
func (r *remoting) RemoteStop(ctx context.Context, host string, port int, name string) error {
	port32, err := strconvx.Int2Int32(port)
	if err != nil {
		return err
	}

	// Enrich context with metadata
	ctx, err = r.enrichContext(ctx)
	if err != nil {
		return err
	}

	// Get pooled client
	client := r.NetClient(host, port)
	request := &internalpb.RemoteStopRequest{
		Host: host,
		Port: port32,
		Name: name,
	}

	// Send request
	resp, err := client.SendProto(ctx, request)
	if err != nil {
		return err
	}

	// Ignore NOT_FOUND errors (actor doesn't exist)
	if errResp, ok := resp.(*internalpb.Error); ok {
		if errResp.GetCode() == internalpb.Code_CODE_NOT_FOUND {
			return nil
		}
	}

	return checkProtoError(resp)
}

// RemoteReinstate reactivates a previously passivated actor on the remote node.
// Missing actors are treated as a no-op and return nil.
func (r *remoting) RemoteReinstate(ctx context.Context, host string, port int, name string) error {
	port32, err := strconvx.Int2Int32(port)
	if err != nil {
		return err
	}

	// Enrich context with metadata
	ctx, err = r.enrichContext(ctx)
	if err != nil {
		return err
	}

	// Get pooled client
	client := r.NetClient(host, port)
	request := &internalpb.RemoteReinstateRequest{
		Host: host,
		Port: port32,
		Name: name,
	}

	// Send request
	resp, err := client.SendProto(ctx, request)
	if err != nil {
		return err
	}

	// Ignore NOT_FOUND errors (actor doesn't exist)
	if errResp, ok := resp.(*internalpb.Error); ok {
		if errResp.GetCode() == internalpb.Code_CODE_NOT_FOUND {
			return nil
		}
	}

	return checkProtoError(resp)
}

// Close releases resources held by the remoting client, such as idle TCP
// connections in the connection pools. It does not cancel in-flight requests;
// use context cancellation to stop ongoing operations.
//
// After calling Close, the client should not be used for new requests.
func (r *remoting) Close() {
	// Close all pooled clients to release TCP connections and file descriptors
	r.clientCache.Range(func(_ string, client *inet.Client) {
		client.Close()
	})

	// Clear the cache to release references
	r.clientCache.Reset()
}

// Compression returns the compression algorithm configured on the remoting
// client. It determines the encodings offered for responses and, where
// supported, the encoding used for outbound requests.
func (r *remoting) Compression() Compression {
	return r.compression
}

// TLSConfig returns the TLS configuration used by this client, if any. A
// nil return value indicates that the client is using an insecure transport.
func (r *remoting) TLSConfig() *tls.Config {
	return r.tlsConfig
}

// Serializer implements [Remoting]. Pass nil to get the composite
// dispatching serializer for the receive path, or pass the outgoing message
// to get the most-specific serializer registered for its dynamic type.
func (r *remoting) Serializer(msg any) Serializer {
	return r.resolveSerializer(msg)
}

// resolveSerializer returns the [Serializer] to use for the given message.
//
// When message is nil the receive path is assumed: a composite
// [dispatchingSerializer] is returned that tries every registered serializer
// in order during [Serializer.Deserialize] and returns the first success.
//
// When message is non-nil the send path is assumed: the first entry whose
// type matches the message's dynamic type wins (exact concrete type, then
// interface match). If no entry matches, nil is returned.
//
// The slice r.serializers is immutable after [NewRemoting] returns, so no
// lock is needed.
func (r *remoting) resolveSerializer(message any) Serializer {
	msgType := reflect.TypeOf(message)
	if msgType == nil {
		return r.dispatcher
	}
	for i := range r.serializers {
		entry := &r.serializers[i]
		if entry.iface.Kind() == reflect.Interface {
			if msgType.Implements(entry.iface) {
				return entry.serializer
			}
		} else if msgType == entry.iface {
			return entry.serializer
		}
	}
	return nil
}

// newNetClient creates a new net client with connection pooling.
// This is the default factory used by NetClient.
func (r *remoting) newNetClient(host string, port int) *inet.Client {
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	opts := []inet.ClientOption{
		inet.WithMaxIdleConns(r.maxIdleConns),
		inet.WithIdleTimeout(r.idleTimeout),
		inet.WithDialTimeout(r.dialTimeout),
		inet.WithKeepAlive(r.keepAlive),
	}

	// Add TLS with session caching for connection reuse
	if r.tlsConfig != nil {
		tlsConfigClone := r.tlsConfig.Clone()
		// Enable TLS session cache for faster reconnects
		if tlsConfigClone.ClientSessionCache == nil {
			tlsConfigClone.ClientSessionCache = tls.NewLRUClientSessionCache(32)
		}
		opts = append(opts, inet.WithTLS(tlsConfigClone))
	}

	// Add compression wrapper
	switch r.compression {
	case BrotliCompression:
		opts = append(opts, inet.WithClientConnWrapper(inet.NewBrotliConnWrapper()))
	case ZstdCompression:
		if wrapper, err := inet.NewZstdConnWrapper(); err == nil {
			opts = append(opts, inet.WithClientConnWrapper(wrapper))
		}
	case GzipCompression:
		if wrapper, err := inet.NewGzipConnWrapper(); err == nil {
			opts = append(opts, inet.WithClientConnWrapper(wrapper))
		}
	}

	return inet.NewClient(addr, opts...)
}

// enrichContext adds metadata to context if propagator is configured.
// This is called once per request and reuses the same context object.
func (r *remoting) enrichContext(ctx context.Context) (context.Context, error) {
	// Create proto metadata
	md := inet.NewMetadata()

	// Add context propagator headers if configured
	if r.contextPropagator != nil {
		headers := make(nethttp.Header, 4) // Pre-size for common case (tracing)
		if err := r.contextPropagator.Inject(ctx, headers); err != nil {
			return nil, err
		}

		// Convert headers to metadata
		for key, values := range headers {
			if len(values) > 0 {
				md.Set(key, values[0])
			}
		}
	}

	// Apply deadline from context if present
	if deadline, ok := ctx.Deadline(); ok {
		md.SetDeadline(deadline)
	}

	// Always attach metadata (even if empty, it's a no-op on the wire)
	return inet.ContextWithMetadata(ctx, md), nil
}

// checkProtoError examines response and converts proto errors to Go errors.
// Returns nil if response is a success type.
func checkProtoError(resp proto.Message) error {
	errResp, isError := resp.(*internalpb.Error)
	if !isError {
		return nil
	}

	msg := errResp.GetMessage()

	// Fast path: common errors
	switch errResp.GetCode() {
	case internalpb.Code_CODE_NOT_FOUND:
		return gerrors.ErrAddressNotFound
	case internalpb.Code_CODE_DEADLINE_EXCEEDED:
		return gerrors.ErrRequestTimeout
	case internalpb.Code_CODE_UNAVAILABLE:
		return gerrors.ErrRemoteSendFailure
	case internalpb.Code_CODE_FAILED_PRECONDITION:
		return parseFailedPrecondition(msg)
	case internalpb.Code_CODE_ALREADY_EXISTS:
		return parseAlreadyExists(msg)
	case internalpb.Code_CODE_INVALID_ARGUMENT:
		return fmt.Errorf("invalid argument: %s", msg)
	case internalpb.Code_CODE_INTERNAL_ERROR:
		return errors.New(msg)
	default:
		return errors.New(msg)
	}
}

// parseFailedPrecondition extracts specific errors from message string
func parseFailedPrecondition(msg string) error {
	// Use simple string checks to avoid allocations
	if strings.Contains(msg, gerrors.ErrTypeNotRegistered.Error()) {
		return gerrors.ErrTypeNotRegistered
	}
	if strings.Contains(msg, gerrors.ErrRemotingDisabled.Error()) {
		return gerrors.ErrRemotingDisabled
	}
	if strings.Contains(msg, gerrors.ErrClusterDisabled.Error()) {
		return gerrors.ErrClusterDisabled
	}
	return errors.New(msg)
}

// parseAlreadyExists determines the specific "already exists" error type
func parseAlreadyExists(msg string) error {
	if strings.Contains(msg, "singleton") {
		return gerrors.ErrSingletonAlreadyExists
	}
	return gerrors.ErrActorAlreadyExists
}

func getGrainFromRequest(host string, port int, grainRequest *GrainRequest) (*internalpb.Grain, error) {
	if err := grainRequest.Validate(); err != nil {
		return nil, fmt.Errorf("invalid grain request: %w", err)
	}

	grainRequest.Sanitize()

	port32, err := strconvx.Int2Int32(port)
	if err != nil {
		return nil, err
	}

	var dependencies []*internalpb.Dependency

	if len(grainRequest.Dependencies) > 0 {
		dependencies, err = codec.EncodeDependencies(grainRequest.Dependencies...)
		if err != nil {
			return nil, err
		}
	}

	grain := &internalpb.Grain{
		Host: host,
		Port: port32,
		GrainId: &internalpb.GrainId{
			Kind:  grainRequest.Kind,
			Name:  grainRequest.Name,
			Value: fmt.Sprintf("%s%s%s", grainRequest.Kind, id.GrainIdentitySeparator, grainRequest.Name),
		},
		Dependencies:      dependencies,
		ActivationRetries: int32(grainRequest.ActivationRetries),
		ActivationTimeout: durationpb.New(grainRequest.ActivationTimeout),
		MailboxCapacity:   pointer.To(grainRequest.MailboxCapacity),
	}

	return grain, nil
}
