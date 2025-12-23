/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package remote

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	nethttp "net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"go.akshayshah.org/connectproto"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v3/address"
	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/internal/codec"
	"github.com/tochemey/goakt/v3/internal/compression/brotli"
	"github.com/tochemey/goakt/v3/internal/compression/zstd"
	"github.com/tochemey/goakt/v3/internal/http"
	"github.com/tochemey/goakt/v3/internal/id"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/internalpb/internalpbconnect"
	"github.com/tochemey/goakt/v3/internal/size"
	"github.com/tochemey/goakt/v3/internal/strconvx"
)

const DefaultMaxReadFrameSize = 16 * size.MB

// Remoting provides the client-side API surface to interact with actors running
// on remote nodes of a Go-Akt cluster. It encapsulates transport configuration
// (HTTP/TLS), payload compression, and Connect RPC client construction, and
// offers convenience methods for one-way messaging, request/response, batched
// delivery, lifecycle management, and discovery.
//
// General semantics:
//
//   - Transport: Remoting is implemented on top of Connect RPC over HTTP. TLS can
//     be enabled via WithRemotingTLS. The configured MaxReadFrameSize applies to
//     both read and send limits for all RPCs.
//   - Payloads: Messages are serialized as google.protobuf.Any. The caller is
//     responsible for using message types known to both peers.
//   - Addresses: All remote operations identify actors by address.Address.
//   - Concurrency: Implementations are safe for concurrent use by multiple goroutines.
//   - Context: All calls honor context cancellation and deadlines. If both a
//     per-call timeout (where applicable) and a context deadline are provided,
//     the effective deadline is the earliest of the two.
//   - Errors: Errors are returned as standard Go errors. When originating from
//     Connect RPC, they carry a code (connect.Code*). Notable codes include
//     NotFound, DeadlineExceeded, Unavailable, and ResourceExhausted.
//   - Ordering & delivery: Fire-and-forget methods provide best-effort delivery
//     with at-most-once semantics at the RPC layer. Delivery to the target actor
//     and application-level ordering guarantees depend on the remote system.
//   - Batching: Batch variants submit multiple messages in a single RPC for
//     efficiency. Partial responses are possible for ask-style batching; callers
//     should attach their own correlation identifiers when needed.
//
// Use NewRemoting to construct an instance. Call Close to release underlying
// resources (e.g., idle HTTP connections).
type Remoting interface {
	// RemoteTell sends a one-way (fire-and-forget) message to a remote actor.
	//
	// Parameters:
	//   - ctx: Governs cancellation and deadlines for the outbound RPC.
	//   - from: The sender's actor address. Used for correlation and routing on
	//     the remote system.
	//   - to: The target actor's address on the remote node.
	//   - message: A protobuf message that will be packed into an Any.
	//
	// Behavior:
	//   - Returns when the RPC completes; no application-level acknowledgement
	//     from the target actor is awaited.
	//   - Message is serialized into google.protobuf.Any. If packing fails,
	//     gerrors.NewErrInvalidMessage is returned.
	//   - Honors compression and frame size limits configured on the client.
	//
	// Errors:
	//   - Transport or server errors (connect.Error) with codes such as
	//     Unavailable or ResourceExhausted.
	//   - Context cancellation/deadline errors.
	RemoteTell(ctx context.Context, from, to *address.Address, message proto.Message) error

	// RemoteAsk sends a request message to a remote actor and waits for a reply,
	// subject to the provided timeout and context.
	//
	// Parameters:
	//   - ctx: Governs cancellation and deadlines for the outbound RPC.
	//   - from: The sender's actor address.
	//   - to: The target actor's address.
	//   - message: A protobuf message to send; packed into an Any.
	//   - timeout: A server-side processing window for collecting responses. If
	//     zero, the server may apply a default policy; ctx still applies.
	//
	// Returns:
	//   - response: The first response returned by the remote actor, if any,
	//     wrapped in Any. Nil if no response was produced before deadlines.
	//
	// Behavior:
	//   - If multiple responses arrive, only the first one is returned.
	//   - Packing failures yield gerrors.NewErrInvalidMessage.
	//
	// Errors:
	//   - NotFound if the receiver does not exist (server-dependent).
	//   - DeadlineExceeded when neither a reply nor completion occurs in time.
	//   - Transport, resource limits, or context errors.
	RemoteAsk(ctx context.Context, from, to *address.Address, message proto.Message, timeout time.Duration) (response *anypb.Any, err error)

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
	//   - Other server-side failures surfaced as connect errors.
	RemoteLookup(ctx context.Context, host string, port int, name string) (addr *address.Address, err error)

	// RemoteBatchTell sends multiple fire-and-forget messages to the same remote
	// actor in a single RPC for efficiency.
	//
	// Parameters:
	//   - ctx: Cancellation and deadlines.
	//   - from: Sender address.
	//   - to: Target actor address.
	//   - messages: Slice of protobuf messages; nil entries are ignored.
	//
	// Behavior:
	//   - Serializes each message into Any; packing failures abort and return
	//     gerrors.NewErrInvalidMessage.
	//   - Entire RPC succeeds or fails as a unit at the transport layer (no
	//     per-message delivery status is returned).
	//
	// Errors:
	//   - Transport, resource limits (e.g., frame size), and context errors.
	RemoteBatchTell(ctx context.Context, from, to *address.Address, messages []proto.Message) error

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
	//   - Packing failures (gerrors.NewErrInvalidMessage).
	//   - Transport, resource limits, and context errors.
	RemoteBatchAsk(ctx context.Context, from, to *address.Address, messages []proto.Message, timeout time.Duration) (responses []*anypb.Any, err error)

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
	//   - Server-side failures surfaced as connect errors (e.g., Unavailable, DeadlineExceeded).
	//
	// Note: The grain kind must be registered on the remote actor system using RegisterGrainKind.
	RemoteActivateGrain(ctx context.Context, host string, port int, grainRequest *GrainRequest) error

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
	RemoteTellGrain(ctx context.Context, host string, port int, grainRequest *GrainRequest, message proto.Message) error

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
	RemoteAskGrain(ctx context.Context, host string, port int, grainRequest *GrainRequest, message proto.Message, timeout time.Duration) (response *anypb.Any, err error)

	// HTTPClient returns the underlying net/http.Client used by this instance.
	//
	// The returned client reflects the TLS, compression, and frame size
	// configuration applied to the Remoting instance. Modifying the client while
	// requests are in flight may have undefined effects; prefer configuring via
	// RemotingOptions at construction time.
	HTTPClient() *nethttp.Client

	// MaxReadFrameSize reports the maximum frame size (in bytes) enforced by the
	// client for both reading responses and sending requests. Values are applied
	// to the Connect client via WithReadMaxBytes and WithSendMaxBytes.
	MaxReadFrameSize() int

	// Close releases resources held by the client, such as idle HTTP
	// connections. Close does not cancel in-flight RPCs; use context cancellation
	// for that.
	Close()

	// RemotingServiceClient constructs a typed Connect client for the target
	// host and port, honoring the instance's TLS, compression, and framing
	// configuration. This is primarily intended for advanced/extension scenarios
	// where direct access to RPC methods is needed.
	RemotingServiceClient(host string, port int) internalpbconnect.RemotingServiceClient

	// Compression returns the payload compression strategy configured on this
	// client. This governs both the content encoding offered for responses and,
	// where supported, the encoding used for outbound requests.
	Compression() Compression

	// TLSConfig returns the TLS configuration used by this client, if any. A
	// nil return value indicates that the client is using an insecure transport.
	TLSConfig() *tls.Config
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

// WithRemotingMaxReadFameSize sets the maximum frame size, in bytes, used when
// sending or receiving data over the remoting transport. The configured value is
// applied symmetrically to both read and write limits and is passed through to
// the underlying Connect client.
func WithRemotingMaxReadFameSize(size int) RemotingOption {
	return func(r *remoting) {
		r.maxReadFrameSize = size
	}
}

// WithRemotingCompression sets the compression algorithm applied to payloads
// exchanged with remote actor systems. The provided Compression must be one of
// the supported options for remoting traffic.
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

// remoting is the default Remoting implementation backed by Connect RPC
// clients. It encapsulates transport setup, compression, and TLS configuration
// required to reach remote actor systems.
type remoting struct {
	client            *nethttp.Client
	tlsConfig         *tls.Config
	maxReadFrameSize  int
	compression       Compression
	contextPropagator ContextPropagator
	clientFactory     func(host string, port int) internalpbconnect.RemotingServiceClient
}

var _ Remoting = (*remoting)(nil)

// NewRemoting constructs a Remoting client configured with the supplied
// options. By default, the returned client uses an insecure HTTP transport with
// no compression and enforces the DefaultMaxReadFrameSize limit. Custom options
// may enable TLS, change the frame size, or select a compression algorithm.
//
// Call Close when the client is no longer needed to avoid leaking idle socket
// connections.
func NewRemoting(opts ...RemotingOption) Remoting {
	r := &remoting{
		maxReadFrameSize: DefaultMaxReadFrameSize,
		compression:      NoCompression,
	}

	// apply the options
	for _, opt := range opts {
		opt(r)
	}

	if r.tlsConfig != nil {
		r.client = http.NewHTTPSClient(r.tlsConfig, uint32(r.maxReadFrameSize)) // nolint
	} else {
		r.client = http.NewHTTPClient(uint32(r.maxReadFrameSize))
	}

	r.clientFactory = func(host string, port int) internalpbconnect.RemotingServiceClient {
		return r.newRemotingServiceClient(host, port)
	}
	return r
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
//   - Server-side failures surfaced as connect errors (e.g., Unavailable, DeadlineExceeded).
//
// Note: The grain kind must be registered on the remote actor system using RegisterGrainKind.
func (r *remoting) RemoteActivateGrain(ctx context.Context, host string, port int, grainRequest *GrainRequest) error {
	grain, err := getGrainFromRequest(host, port, grainRequest)
	if err != nil {
		return err
	}

	remoteClient := r.RemotingServiceClient(host, port)
	request := connect.NewRequest(&internalpb.RemoteActivateGrainRequest{
		Grain: grain,
	})

	if propagator := r.contextPropagator; propagator != nil {
		if err := propagator.Inject(ctx, request.Header()); err != nil {
			return err
		}
	}

	_, err = remoteClient.RemoteActivateGrain(ctx, request)
	return err
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
func (r *remoting) RemoteAskGrain(ctx context.Context, host string, port int, grainRequest *GrainRequest, message proto.Message, timeout time.Duration) (response *anypb.Any, err error) {
	grain, err := getGrainFromRequest(host, port, grainRequest)
	if err != nil {
		return nil, err
	}

	marshaled, err := anypb.New(message)
	if err != nil {
		return nil, gerrors.NewErrInvalidMessage(err)
	}

	remoteClient := r.RemotingServiceClient(host, port)
	request := connect.NewRequest(&internalpb.RemoteAskGrainRequest{
		Grain:          grain,
		Message:        marshaled,
		RequestTimeout: durationpb.New(timeout),
	})

	if propagator := r.contextPropagator; propagator != nil {
		if err := propagator.Inject(ctx, request.Header()); err != nil {
			return nil, err
		}
	}

	resp, err := remoteClient.RemoteAskGrain(ctx, request)
	if err != nil {
		return nil, err
	}

	if resp != nil {
		response = resp.Msg.GetMessage()
	}

	return
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
func (r *remoting) RemoteTellGrain(ctx context.Context, host string, port int, grainRequest *GrainRequest, message proto.Message) error {
	grain, err := getGrainFromRequest(host, port, grainRequest)
	if err != nil {
		return err
	}

	marshaled, err := anypb.New(message)
	if err != nil {
		return gerrors.NewErrInvalidMessage(err)
	}

	remoteClient := r.RemotingServiceClient(host, port)
	request := connect.NewRequest(&internalpb.RemoteTellGrainRequest{
		Grain:   grain,
		Message: marshaled,
	})

	if propagator := r.contextPropagator; propagator != nil {
		if err := propagator.Inject(ctx, request.Header()); err != nil {
			return err
		}
	}

	_, err = remoteClient.RemoteTellGrain(ctx, request)
	return err
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
func (r *remoting) RemoteTell(ctx context.Context, from, to *address.Address, message proto.Message) error {
	marshaled, err := anypb.New(message)
	if err != nil {
		return gerrors.NewErrInvalidMessage(err)
	}

	remoteClient := r.RemotingServiceClient(to.Host(), to.Port())
	request := connect.NewRequest(&internalpb.RemoteTellRequest{
		RemoteMessages: []*internalpb.RemoteMessage{
			{
				Sender:   from.String(),
				Receiver: to.String(),
				Message:  marshaled,
			},
		},
	})

	if propagator := r.contextPropagator; propagator != nil {
		if err := propagator.Inject(ctx, request.Header()); err != nil {
			return err
		}
	}

	_, err = remoteClient.RemoteTell(ctx, request)
	return err
}

// RemoteAsk delivers a request message to a remote actor and waits for the first
// reply within the specified timeout. The response is returned as an Any proto.
//
// Notes:
//   - If multiple responses are produced, the first one is returned.
//   - A zero timeout defers to server policy; ctx still governs cancellation.
//   - Errors may include NotFound, DeadlineExceeded, Unavailable, etc.
func (r *remoting) RemoteAsk(ctx context.Context, from, to *address.Address, message proto.Message, timeout time.Duration) (response *anypb.Any, err error) {
	marshaled, err := anypb.New(message)
	if err != nil {
		return nil, gerrors.NewErrInvalidMessage(err)
	}

	remoteClient := r.RemotingServiceClient(to.Host(), to.Port())
	request := connect.NewRequest(&internalpb.RemoteAskRequest{
		RemoteMessages: []*internalpb.RemoteMessage{
			{
				Sender:   from.String(),
				Receiver: to.String(),
				Message:  marshaled,
			},
		},
		Timeout: durationpb.New(timeout),
	})

	if propagator := r.contextPropagator; propagator != nil {
		if err := propagator.Inject(ctx, request.Header()); err != nil {
			return nil, err
		}
	}

	resp, err := remoteClient.RemoteAsk(ctx, request)
	if err != nil {
		return nil, err
	}

	if resp != nil {
		for _, msg := range resp.Msg.GetMessages() {
			response = msg
			break
		}
	}

	return
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
	remoteClient := r.RemotingServiceClient(host, port)
	request := connect.NewRequest(
		&internalpb.RemoteLookupRequest{
			Host: host,
			Port: port32,
			Name: name,
		},
	)

	if propagator := r.contextPropagator; propagator != nil {
		if err := propagator.Inject(ctx, request.Header()); err != nil {
			return nil, err
		}
	}

	response, err := remoteClient.RemoteLookup(ctx, request)
	if err != nil {
		code := connect.CodeOf(err)
		if code == connect.CodeNotFound {
			return address.NoSender(), nil
		}
		return nil, err
	}

	return address.Parse(response.Msg.GetAddress())
}

// RemoteBatchTell sends multiple asynchronous messages to the same remote actor
// in a single RPC. Nil entries in messages are ignored.
//
// The call succeeds or fails as a whole at the transport layer; no per-message
// acknowledgement is returned.
func (r *remoting) RemoteBatchTell(ctx context.Context, from, to *address.Address, messages []proto.Message) error {
	remoteClient := r.RemotingServiceClient(to.Host(), to.Port())
	remoteMessages := make([]*internalpb.RemoteMessage, 0, len(messages))
	for _, message := range messages {
		if message != nil {
			packed, err := anypb.New(message)
			if err != nil {
				return gerrors.NewErrInvalidMessage(err)
			}
			remoteMessages = append(remoteMessages, &internalpb.RemoteMessage{
				Sender:   from.String(),
				Receiver: to.String(),
				Message:  packed,
			})
		}
	}

	req := connect.NewRequest(&internalpb.RemoteTellRequest{
		RemoteMessages: remoteMessages,
	})

	if propagator := r.contextPropagator; propagator != nil {
		if err := propagator.Inject(ctx, req.Header()); err != nil {
			return err
		}
	}

	_, err := remoteClient.RemoteTell(ctx, req)
	return err
}

// RemoteBatchAsk delivers multiple request messages to a remote actor and
// aggregates all responses received before the provided timeout elapses.
//
// The number and order of responses may not match the requests. If correlation
// is required, include a correlation ID within your message payloads.
func (r *remoting) RemoteBatchAsk(ctx context.Context, from, to *address.Address, messages []proto.Message, timeout time.Duration) (responses []*anypb.Any, err error) {
	remoteClient := r.RemotingServiceClient(to.Host(), to.Port())

	remoteMessages := make([]*internalpb.RemoteMessage, 0, len(messages))
	for _, message := range messages {
		if message != nil {
			packed, err := anypb.New(message)
			if err != nil {
				return nil, gerrors.NewErrInvalidMessage(err)
			}
			remoteMessages = append(remoteMessages, &internalpb.RemoteMessage{
				Sender:   from.String(),
				Receiver: to.String(),
				Message:  packed,
			})
		}
	}

	req := connect.NewRequest(&internalpb.RemoteAskRequest{
		RemoteMessages: remoteMessages,
		Timeout:        durationpb.New(timeout),
	})

	if propagator := r.contextPropagator; propagator != nil {
		if err := propagator.Inject(ctx, req.Header()); err != nil {
			return nil, err
		}
	}

	resp, err := remoteClient.RemoteAsk(ctx, req)

	if err != nil {
		return nil, err
	}

	if resp != nil {
		responses = append(responses, resp.Msg.GetMessages()...)
	}

	return
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

	remoteClient := r.RemotingServiceClient(host, port)
	request := connect.NewRequest(
		&internalpb.RemoteSpawnRequest{
			Host:                host,
			Port:                port32,
			ActorName:           spawnRequest.Name,
			ActorType:           spawnRequest.Kind,
			IsSingleton:         spawnRequest.Singleton,
			Relocatable:         spawnRequest.Relocatable,
			PassivationStrategy: codec.EncodePassivationStrategy(spawnRequest.PassivationStrategy),
			Dependencies:        dependencies,
			EnableStash:         spawnRequest.EnableStashing,
			Role:                spawnRequest.Role,
			Supervisor:          codec.EncodeSupervisor(spawnRequest.Supervisor),
		},
	)

	if propagator := r.contextPropagator; propagator != nil {
		if err := propagator.Inject(ctx, request.Header()); err != nil {
			return err
		}
	}

	if _, err := remoteClient.RemoteSpawn(ctx, request); err != nil {
		code := connect.CodeOf(err)
		if code == connect.CodeFailedPrecondition {
			var connectErr *connect.Error
			errors.As(err, &connectErr)
			e := connectErr.Unwrap()
			if strings.Contains(e.Error(), gerrors.ErrTypeNotRegistered.Error()) {
				return gerrors.ErrTypeNotRegistered
			}
			if strings.Contains(e.Error(), gerrors.ErrRemotingDisabled.Error()) {
				return gerrors.ErrRemotingDisabled
			}
		}
		return err
	}
	return nil
}

// RemoteReSpawn requests a restart of an existing actor on the remote node.
func (r *remoting) RemoteReSpawn(ctx context.Context, host string, port int, name string) error {
	port32, err := strconvx.Int2Int32(port)
	if err != nil {
		return err
	}
	remoteClient := r.RemotingServiceClient(host, port)
	request := connect.NewRequest(
		&internalpb.RemoteReSpawnRequest{
			Host: host,
			Port: port32,
			Name: name,
		},
	)

	if propagator := r.contextPropagator; propagator != nil {
		if err := propagator.Inject(ctx, request.Header()); err != nil {
			return err
		}
	}

	if _, err := remoteClient.RemoteReSpawn(ctx, request); err != nil {
		code := connect.CodeOf(err)
		if code == connect.CodeNotFound {
			return nil
		}
		return err
	}

	return nil
}

// RemoteStop terminates the specified actor on the remote node. Missing actors
// are ignored and return nil.
func (r *remoting) RemoteStop(ctx context.Context, host string, port int, name string) error {
	port32, err := strconvx.Int2Int32(port)
	if err != nil {
		return err
	}
	remoteClient := r.RemotingServiceClient(host, port)
	request := connect.NewRequest(
		&internalpb.RemoteStopRequest{
			Host: host,
			Port: port32,
			Name: name,
		},
	)

	if propagator := r.contextPropagator; propagator != nil {
		if err := propagator.Inject(ctx, request.Header()); err != nil {
			return err
		}
	}

	if _, err := remoteClient.RemoteStop(ctx, request); err != nil {
		code := connect.CodeOf(err)
		if code == connect.CodeNotFound {
			return nil
		}
		return err
	}

	return nil
}

// RemoteReinstate reactivates a previously passivated actor on the remote node.
// Missing actors are treated as a no-op and return nil.
func (r *remoting) RemoteReinstate(ctx context.Context, host string, port int, name string) error {
	port32, err := strconvx.Int2Int32(port)
	if err != nil {
		return err
	}
	remoteClient := r.RemotingServiceClient(host, port)
	request := connect.NewRequest(
		&internalpb.RemoteReinstateRequest{
			Host: host,
			Port: port32,
			Name: name,
		},
	)

	if propagator := r.contextPropagator; propagator != nil {
		if err := propagator.Inject(ctx, request.Header()); err != nil {
			return err
		}
	}

	if _, err := remoteClient.RemoteReinstate(ctx, request); err != nil {
		code := connect.CodeOf(err)
		if code == connect.CodeNotFound {
			return nil
		}
		return err
	}

	return nil
}

// HTTPClient exposes the underlying HTTP client used for remoting requests. The
// returned client reflects the TLS, compression, and frame size options applied
// to the remoting instance. Avoid mutating the client concurrently with active
// requests; prefer configuring via options at creation time.
func (r *remoting) HTTPClient() *nethttp.Client {
	return r.client
}

// MaxReadFrameSize reports the maximum frame size enforced by the remoting
// client for both reads and writes. This value is propagated to Connect via
// WithReadMaxBytes and WithSendMaxBytes.
func (r *remoting) MaxReadFrameSize() int {
	return r.maxReadFrameSize
}

// Close releases resources held by the remoting client, such as idle HTTP
// connections. It does not cancel in-flight RPCs; use context cancellation to
// stop ongoing operations.
func (r *remoting) Close() {
	r.client.CloseIdleConnections()
}

// RemotingServiceClient creates a typed RemotingService client for the target
// endpoint, applying the remoting client's transport and compression settings.
// This is primarily for advanced scenarios where direct RPC access is required.
func (r *remoting) RemotingServiceClient(host string, port int) internalpbconnect.RemotingServiceClient {
	if r.clientFactory != nil {
		return r.clientFactory(host, port)
	}

	return r.newRemotingServiceClient(host, port)
}

func (r *remoting) setClientFactory(factory func(string, int) internalpbconnect.RemotingServiceClient) {
	if factory == nil {
		return
	}
	r.clientFactory = factory
}

func (r *remoting) newRemotingServiceClient(host string, port int) internalpbconnect.RemotingServiceClient {
	endpoint := http.URL(host, port)
	if r.tlsConfig != nil {
		endpoint = http.URLs(host, port)
	}

	opts := []connect.ClientOption{
		connectproto.WithBinary(
			proto.MarshalOptions{},
			proto.UnmarshalOptions{DiscardUnknown: true},
		),
	}

	if r.maxReadFrameSize > 0 {
		opts = append(opts, connect.WithReadMaxBytes(r.maxReadFrameSize))
		opts = append(opts, connect.WithSendMaxBytes(r.maxReadFrameSize))
	}

	switch r.compression {
	case GzipCompression:
		// Connect clients send uncompressed requests and ask for gzipped responses by default.
		// Specifying gzip here enables gzipped requests as well.
		opts = append(opts, connect.WithSendGzip())
	case ZstdCompression:
		opts = append(opts, zstd.WithCompression())
		opts = append(opts, connect.WithSendCompression(zstd.Name))
	case BrotliCompression:
		opts = append(opts, brotli.WithCompression())
		opts = append(opts, connect.WithSendCompression(brotli.Name))
	default:
		// No compression
	}

	return internalpbconnect.NewRemotingServiceClient(
		r.client,
		endpoint,
		opts...,
	)
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
	}

	return grain, nil
}
