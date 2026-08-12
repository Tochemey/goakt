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

package net

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/tochemey/goakt/v4/internal/internalpb"
)

// acceptHandshakeTimeout bounds the acceptor-side handshake: the protocol
// sniff (first byte) and the duplex HELLO exchange. It stops a peer that
// connects and then never speaks from pinning the accept path, independently
// of the connection idle timeout (which may be unset, or set long for
// steady-state traffic while the handshake should still be prompt). It is
// cleared before the read and write loops start, so it never leaks into their
// own liveness deadlines. The sniff byte and HELLO frame are tiny, so the
// window is generous. A var (not a const) only so the slow-loris test can
// lower it; production never changes it.
var acceptHandshakeTimeout = 10 * time.Second

// ProtoHandler processes a deserialized protobuf request and returns a
// response. Returning a nil [proto.Message] with a nil error signals a
// fire-and-forget message — no response frame is written back to the client.
//
// The handler receives the server's base [context.Context] (set via
// [WithRemotingServerContext]) and the [Connection] that delivered the request,
// allowing access to peer addresses and connection metadata.
//
// Implementations must be safe for concurrent use; the same handler may be
// invoked from many connections simultaneously.
type ProtoHandler func(ctx context.Context, conn Connection, req proto.Message) (proto.Message, error)

// RemotingServer is a high-performance, low-GC remoting acceptor.
//
// It layers a self-describing protobuf wire protocol on top of [TCPServer],
// providing:
//
//   - Automatic length-prefixed framing via [ProtoSerializer].
//   - Dynamic message-type dispatch: each incoming frame carries the fully
//     qualified protobuf type name and is routed to a registered [ProtoHandler].
//   - Per-connection read loops that process multiple sequential messages.
//   - Pooled read buffers and frame byte slices to minimize heap allocations
//     and reduce GC pressure under sustained load.
//   - Optional per-message idle timeouts to reclaim stale connections.
//   - Full TLS, [ConnWrapper] (compression), and multi-loop accept support
//     inherited from the underlying [TCPServer].
//
// # Wire format
//
// Every message on the wire uses the [ProtoSerializer] frame layout:
//
//	┌──────────┬──────────┬────────────┬──────────────┐
//	│ totalLen │ nameLen  │ type name  │ proto bytes  │
//	│ 4 bytes  │ 4 bytes  │ N bytes    │ M bytes      │
//	│ uint32BE │ uint32BE │ UTF-8      │ marshaled    │
//	└──────────┴──────────┴────────────┴──────────────┘
//
// The totalLen field covers the entire frame (including itself). This
// format is self-describing: the receiver resolves the concrete [proto.Message]
// type via the global protobuf registry without compile-time knowledge of
// the sender's message set.
//
// # Usage
//
//	ps, err := tcp.NewRemotingServer("0.0.0.0:9000",
//		tcp.WithProtoHandler("myapp.PingRequest", pingHandler),
//		tcp.WithProtoHandler("myapp.PutRequest", putHandler),
//		tcp.WithRemotingServerIdleTimeout(30 * time.Second),
//	)
//	if err != nil { ... }
//
//	if err := x.Listen(); err != nil { ... }
//
//	// Serve blocks until shutdown.
//	go func() { log.Fatal(x.Serve()) }()
//
//	// Later …
//	x.Shutdown(5 * time.Second)
//
// # Low-GC design
//
// The following techniques keep allocation rates low:
//
//   - Frame header (4 bytes) is read into a stack-allocated [4]byte array,
//     producing zero heap allocations per message for the header read.
//   - Frame body buffers are drawn from a size-bucketed [sync.Pool]
//     ([framePool]) and returned after handler dispatch, avoiding
//     per-message allocations for the common case.
//   - The [ProtoSerializer] itself uses a pooled [bytes.Buffer] for
//     marshaling response frames.
//   - Connection structs are recycled via the underlying [TCPServer]'s
//     connStructPool ([sync.Pool]).
//   - The underlying [TCPServer] uses a sharded [WorkerPool] with per-shard
//     idle worker lists and a configurable GC ballast to further reduce
//     garbage-collection frequency.
//
// PanicHandlerFunc is called when a [ProtoHandler] panics during dispatch.
// It receives the fully qualified protobuf type name of the message being
// handled and the recovered value. The handler is invoked inside the
// recover block, so it must not re-panic.
type PanicHandlerFunc func(typeName protoreflect.FullName, recovered any)

// RemotingServer is a high-performance, low-GC remoting acceptor.
type RemotingServer struct {
	server       *TCPServer
	handlers     map[protoreflect.FullName]ProtoHandler
	fallback     ProtoHandler
	panicHandler PanicHandlerFunc
	serializer   *ProtoSerializer
	framePool    *FramePool
	serverOpts   []ServerOption

	idleTimeout                 time.Duration
	writeTimeout                time.Duration
	readIdleTimeout             time.Duration
	maxFrameSize                uint32
	maxMessageSize              uint64
	chunkSize                   uint32
	initialCredits              uint64
	maxConcurrentLargeTransfers uint32
	systemName                  string
	acceptProtocol              AcceptProtocol
	duplexTell                  DuplexTellHandler
	duplexAsk                   DuplexAskHandler
	senderResolver              func(path string) any
	askPool                     *WorkerPool[duplexAskTask]
}

// RemotingServerOption configures a [RemotingServer] before it is started.
type RemotingServerOption func(*RemotingServer)

// NewRemotingServer creates a [RemotingServer] bound to the given address
// (host:port). The returned server is not yet listening; call
// [RemotingServer.Listen] followed by [RemotingServer.Serve] to start accepting
// connections.
//
// Defaults: no idle timeout (connections live until closed by the peer),
// no fallback handler (a frame carrying an unregistered message type closes
// its connection), max frame size of 16 MiB.
func NewRemotingServer(listenAddr string, opts ...RemotingServerOption) (*RemotingServer, error) {
	x := &RemotingServer{
		handlers:                    make(map[protoreflect.FullName]ProtoHandler),
		serializer:                  NewProtoSerializer(),
		framePool:                   NewFramePool(),
		maxFrameSize:                defaultMaxFrameSize, // Default: 16 MiB
		maxMessageSize:              DefaultMaxMessageSize,
		chunkSize:                   DefaultChunkSize,
		initialCredits:              defaultInitialCredits,
		maxConcurrentLargeTransfers: defaultMaxConcurrentLargeTransfers,
	}

	for _, opt := range opts {
		opt(x)
	}

	x.askPool = NewWorkerPool(handleDuplexAskTask)

	// Wire the legacy proto read-loop and the duplex acceptor used by the
	// dual-protocol sniff. Accept-protocol and idle-timeout options must land
	// after caller options so they reflect the final RemotingServer state.
	x.serverOpts = append(x.serverOpts,
		WithRequestHandler(x.handleConn),
		WithDuplexHandler(x.handleDuplexConn),
		WithAcceptProtocol(x.acceptProtocol),
		WithServerIdleTimeout(x.idleTimeout),
	)

	srv, err := NewTCPServer(listenAddr, x.serverOpts...)
	if err != nil {
		return nil, err
	}

	x.server = srv
	return x, nil
}

// WithProtoHandler registers a [ProtoHandler] for the given fully qualified
// protobuf message name (e.g. "myapp.PingRequest"). When a frame carrying
// this type name arrives, the handler is invoked with the deserialized
// message.
//
// Registering a handler for the same name twice silently overwrites the
// previous one. Handlers must be registered before [RemotingServer.Serve] is
// called.
func WithProtoHandler(fullName protoreflect.FullName, handler ProtoHandler) RemotingServerOption {
	return func(x *RemotingServer) {
		x.handlers[fullName] = handler
	}
}

// WithFallbackProtoHandler sets a catch-all [ProtoHandler] invoked when no
// handler is registered for the incoming message type. If no fallback is
// set, a frame carrying an unregistered message type closes its connection
// so a request/response peer fails fast instead of waiting for a reply that
// will never arrive.
func WithFallbackProtoHandler(handler ProtoHandler) RemotingServerOption {
	return func(x *RemotingServer) {
		x.fallback = handler
	}
}

// WithRemotingServerIdleTimeout sets the maximum duration a connection may remain
// idle (no complete frame received) before the server closes it. Zero
// (the default) means no idle timeout — the connection stays open until
// the peer disconnects or the server shuts down.
func WithRemotingServerIdleTimeout(duration time.Duration) RemotingServerOption {
	return func(x *RemotingServer) {
		x.idleTimeout = duration
	}
}

// WithRemotingServerContext sets a base [context.Context] on the underlying
// [TCPServer], propagated to every [ProtoHandler] invocation. Use this for
// cancellation signals or request-scoped values.
func WithRemotingServerContext(ctx context.Context) RemotingServerOption {
	return func(x *RemotingServer) {
		x.serverOpts = append(x.serverOpts, WithServerContext(ctx))
	}
}

// WithRemotingServerTLSConfig sets the TLS configuration used by the underlying
// [TCPServer].
//
// Providing a non-nil config enables TLS for this RemotingServer when you call
// [RemotingServer.ListenTLS]. If config is nil, TLS is not configured and
// [RemotingServer.ListenTLS] will fail with [ErrNoTLSConfig] (you can still use
// [RemotingServer.Listen] for plain TCP).
//
// The supplied config should be fully populated for server use (e.g. with
// Certificates / GetCertificate, and appropriate security settings such as
// MinVersion).
func WithRemotingServerTLSConfig(config *tls.Config) RemotingServerOption {
	return func(x *RemotingServer) {
		x.serverOpts = append(x.serverOpts, WithTLSConfig(config))
	}
}

// WithRemotingServerListenConfig overrides the [ListenConfig] used by the
// underlying [TCPServer] when creating its listener.
//
// This option affects calls to [RemotingServer.Listen] and [RemotingServer.ListenTLS]
// (i.e., the bind/listen step). It does not modify per-connection behavior
// after accept.
//
// If config is nil, the server's default listen configuration is used.
// If provided multiple times, the last call wins.
//
// The configuration must be set before calling Listen/ListenTLS.
func WithRemotingServerListenConfig(config *ListenConfig) RemotingServerOption {
	return func(x *RemotingServer) {
		x.serverOpts = append(x.serverOpts, WithListenConfig(config))
	}
}

// WithRemotingServerLoops sets the number of concurrent accept loops used by the
// underlying [TCPServer].
//
// An “accept loop” is a goroutine that repeatedly calls Accept on the listener
// and hands accepted connections off to the server’s connection handling path.
// Increasing the number of loops can improve connection-accept throughput on
// busy servers (e.g., high connection churn), at the cost of additional
// goroutines and (optionally) OS threads if thread locking is enabled.
//
// Notes:
//   - loops <= 0: uses the underlying server default.
//   - loops > 0: starts exactly that many accept loops.
//   - This only affects the accept phase; it does not change per-connection
//     read loops or [ProtoHandler] execution concurrency.
//   - Consider pairing with [WithRemotingServerAllowThreadLocking] only after
//     measuring, as pinning loops to OS threads can increase thread usage.
//
// This option must be provided before calling [RemotingServer.Listen] /
// [RemotingServer.ListenTLS].
func WithRemotingServerLoops(loops int) RemotingServerOption {
	return func(x *RemotingServer) {
		x.serverOpts = append(x.serverOpts, WithLoops(loops))
	}
}

// WithRemotingServerAllowThreadLocking controls whether the underlying [TCPServer]
// is permitted to pin (lock) its accept-loop goroutines to their current OS
// threads (i.e. the equivalent of calling runtime.LockOSThread).
//
// This is an advanced, opt-in performance/compatibility knob. Enabling thread
// locking can be useful in environments where the accept loop benefits from
// thread affinity (for example, when interacting with OS facilities or polling
// mechanisms that behave better with a stable OS thread). It may also reduce
// scheduler flexibility and increase the number of OS threads in use, which can
// hurt throughput or increase resource consumption under load.
//
// This option only affects the accept loops; it does not pin per-connection
// read loops or your [ProtoHandler] executions.
//
// Default: false.
func WithRemotingServerAllowThreadLocking(allow bool) RemotingServerOption {
	return func(x *RemotingServer) {
		x.serverOpts = append(x.serverOpts, WithAllowThreadLocking(allow))
	}
}

// WithRemotingServerMaxFrameSize sets the maximum allowed frame size (in bytes)
// for incoming messages. Frames exceeding this limit cause the connection to
// be closed. The default is 16 MiB ([defaultMaxFrameSize]).
func WithRemotingServerMaxFrameSize(size uint32) RemotingServerOption {
	return func(x *RemotingServer) {
		x.maxFrameSize = size
	}
}

// WithRemotingServerSystemName sets the system name advertised in duplex HELLO_ACK.
// Empty (the default) leaves the field blank on the wire.
func WithRemotingServerSystemName(name string) RemotingServerOption {
	return func(x *RemotingServer) {
		x.systemName = name
	}
}

// WithRemotingServerAcceptProtocol selects which remoting wire protocols the
// listener accepts. The default is [AcceptProtocolAuto].
func WithRemotingServerAcceptProtocol(protocol AcceptProtocol) RemotingServerOption {
	return func(x *RemotingServer) {
		x.acceptProtocol = protocol
	}
}

// WithRemotingServerInitialCredits sets the server's flow-control window
// advertised in HELLO (wire field initial_credits) and used as the duplex
// outbound admission cap. Zero keeps the default (16 MiB). Callers typically
// pass [remote.Config.CreditWindow].
func WithRemotingServerInitialCredits(credits uint64) RemotingServerOption {
	return func(x *RemotingServer) {
		if credits > 0 {
			x.initialCredits = credits
		}
	}
}

// WithRemotingServerWriteTimeout bounds duplex Submit waits when the caller
// context has no deadline.
func WithRemotingServerWriteTimeout(d time.Duration) RemotingServerOption {
	return func(x *RemotingServer) {
		x.writeTimeout = d
	}
}

// WithRemotingServerReadIdleTimeout sets the duplex PING/PONG liveness interval.
// Zero disables liveness probes.
func WithRemotingServerReadIdleTimeout(d time.Duration) RemotingServerOption {
	return func(x *RemotingServer) {
		x.readIdleTimeout = d
	}
}

// WithRemotingServerMaxConcurrentLargeTransfers sets the HELLO-advertised cap on
// concurrent large-message transfers. The value is negotiated and enforced by
// the chunk reassembler and the sender-side gate.
func WithRemotingServerMaxConcurrentLargeTransfers(n uint32) RemotingServerOption {
	return func(x *RemotingServer) {
		if n > 0 {
			x.maxConcurrentLargeTransfers = n
		}
	}
}

// WithRemotingServerMaxMessageSize sets the HELLO-advertised cap on a
// reassembled logical frame. Zero keeps the default (16 MiB).
func WithRemotingServerMaxMessageSize(size uint64) RemotingServerOption {
	return func(x *RemotingServer) {
		if size > 0 {
			x.maxMessageSize = size
		}
	}
}

// WithRemotingServerChunkSize sets the local CHUNK send threshold for
// oversized REPLY frames. Zero keeps the default (256 KiB).
func WithRemotingServerChunkSize(size uint32) RemotingServerOption {
	return func(x *RemotingServer) {
		if size > 0 {
			x.chunkSize = size
		}
	}
}

// WithRemotingServerDuplexTellHandler registers the fire-and-forget DATA handler
// for the duplex acceptor.
func WithRemotingServerDuplexTellHandler(h DuplexTellHandler) RemotingServerOption {
	return func(x *RemotingServer) {
		x.duplexTell = h
	}
}

// WithRemotingServerDuplexAskHandler registers the expectsReply user-message
// handler for the duplex acceptor. Control RPCs continue to use registered
// ProtoHandlers.
func WithRemotingServerDuplexAskHandler(h DuplexAskHandler) RemotingServerOption {
	return func(x *RemotingServer) {
		x.duplexAsk = h
	}
}

// WithRemotingServerSenderResolver installs the actor-layer hook used to
// materialize opaque sender handles on path table hits. A nil resolver leaves
// DataEnvelope.SenderHandle empty.
func WithRemotingServerSenderResolver(resolve func(path string) any) RemotingServerOption {
	return func(x *RemotingServer) {
		x.senderResolver = resolve
	}
}

// WithRemotingServerPanicHandler sets a [PanicHandlerFunc] that is called when a
// [ProtoHandler] panics during dispatch. Without a panic handler, panics in
// handlers will close the connection silently. With a handler set, panics are
// recovered, the callback is invoked, and the read loop continues serving
// subsequent messages on the same connection.
func WithRemotingServerPanicHandler(f PanicHandlerFunc) RemotingServerOption {
	return func(x *RemotingServer) {
		x.panicHandler = f
	}
}

// WithRemotingServerConnWrapper appends a [ConnWrapper] (e.g. compression) to the
// underlying [TCPServer]'s wrapping pipeline.
func WithRemotingServerConnWrapper(w ConnWrapper) RemotingServerOption {
	return func(x *RemotingServer) {
		x.serverOpts = append(x.serverOpts, WithConnWrapper(w))
	}
}

// WithRemotingServerMaxAcceptConnections sets the maximum total connections on the
// underlying [TCPServer].
func WithRemotingServerMaxAcceptConnections(limit int32) RemotingServerOption {
	return func(x *RemotingServer) {
		x.serverOpts = append(x.serverOpts, WithMaxAcceptConnections(limit))
	}
}

// WithRemotingServerBallast sets the GC ballast size (in MiB) on the underlying
// [TCPServer].
//
// A “GC ballast” is a deliberately retained heap allocation used to bias the
// garbage collector toward running less frequently. This can improve tail
// latency and throughput for allocation-heavy workloads by reducing GC cycle
// rate, at the cost of increased baseline memory usage.
//
// Guidance:
//   - sizeInMiB <= 0 disables the ballast (no additional retained memory).
//   - Start small (e.g. 32–256 MiB) and benchmark/observe p99 latency, GC CPU,
//     and RSS before increasing.
//   - Prefer tuning allocation hot spots first; ballast is a last-mile knob.
//
// Note: this option only affects the underlying server’s runtime/worker-pool
// behavior; it does not change the wire protocol or per-connection semantics.
func WithRemotingServerBallast(sizeInMiB int) RemotingServerOption {
	return func(x *RemotingServer) {
		x.serverOpts = append(x.serverOpts, WithBallast(sizeInMiB))
	}
}

// WithRemotingServerConnectionCreator sets the factory used to create [Connection]
// values for incoming connections on the underlying [TCPServer].
func WithRemotingServerConnectionCreator(f ConnectionCreatorFunc) RemotingServerOption {
	return func(x *RemotingServer) {
		x.serverOpts = append(x.serverOpts, WithConnectionCreator(f))
	}
}

// Listen creates the TCP listener on the configured address. Call
// [RemotingServer.Serve] afterwards to start accepting connections.
func (x *RemotingServer) Listen() error {
	return x.server.Listen()
}

// ListenTLS enables TLS and creates the TCP listener. Returns
// [ErrNoTLSConfig] if no TLS configuration was provided.
func (x *RemotingServer) ListenTLS() error {
	return x.server.ListenTLS()
}

// Serve starts the accept loops and blocks until all loops and in-flight
// connections complete. Use [RemotingServer.Shutdown] or [RemotingServer.Halt]
// from another goroutine to stop the server.
func (x *RemotingServer) Serve() error {
	x.askPool.Start()
	defer x.askPool.Stop()
	return x.server.Serve()
}

// Shutdown gracefully stops the server. See [TCPServer.Shutdown] for the
// semantics of the timeout parameter d:
//   - d > 0: wait up to d for in-flight connections to finish.
//   - d == 0: wait indefinitely.
//   - d < 0: return immediately without waiting.
func (x *RemotingServer) Shutdown(d time.Duration) error {
	return x.server.Shutdown(d)
}

// Halt immediately stops the server without waiting for in-flight
// connections. Equivalent to Shutdown(-1).
func (x *RemotingServer) Halt() error {
	return x.server.Halt()
}

// ListenAddr returns the actual [*net.TCPAddr] the server is listening on.
// Useful when the server was started on port 0. Returns nil if the server
// has not started listening.
func (x *RemotingServer) ListenAddr() *net.TCPAddr {
	return x.server.ListenAddr()
}

// ActiveConnections returns the number of connections currently being served.
func (x *RemotingServer) ActiveConnections() int32 {
	return x.server.ActiveConnections()
}

// AcceptedConnections returns the total number of connections accepted since
// the server started.
func (x *RemotingServer) AcceptedConnections() int32 {
	return x.server.AcceptedConnections()
}

// handleDuplexConn accepts a sniffed duplex connection: it completes HELLO,
// applies negotiated compression, wires DATA/PING service into the duplex
// read loop, and returns; the accept worker goes back to its pool while the
// connection's own transport goroutines serve it (detached mode, see
// [DuplexHandlerFunc]).
//
// Fire-and-forget DATA is handled inline on the read loop; expectsReply DATA
// is offloaded to the ask worker pool so a slow actor cannot stall the socket.
// Unknown frame types and table-ref capability violations receive a
// connection-scoped ERROR then close; teardown drains admitted outbound
// frames so that ERROR reaches the peer. release retires the connection's
// server accounting when the read loop exits, so shutdown still waits for
// live duplex connections.
func (x *RemotingServer) handleDuplexConn(raw net.Conn, release func()) {
	framed := newTCPFramedConn(raw, x.maxFrameSize)

	hello := &internalpb.Hello{}
	hello.SetRevision(CapabilityRevisionCredits)
	hello.SetSystemName(x.systemName)
	hello.SetLaneRole(internalpb.LaneRole_LANE_ROLE_CONTROL)
	hello.SetCompression(internalpb.CompressionCodec_COMPRESSION_CODEC_NONE)
	hello.SetMaxFrameSize(x.maxFrameSize)
	hello.SetMaxMessageSize(x.maxMessageSize)
	hello.SetInitialCredits(x.initialCredits)
	hello.SetMaxConcurrentLargeTransfers(x.maxConcurrentLargeTransfers)

	// Bound the HELLO exchange so a silent or stalled peer cannot pin this
	// accept worker. Cleared immediately after: the handshake I/O is done,
	// and the read and write loops arm their own deadlines, so a stale
	// handshake deadline must not leak into them.
	_ = raw.SetDeadline(time.Now().Add(acceptHandshakeTimeout))
	result, err := acceptHello(framed, hello)
	_ = raw.SetDeadline(time.Time{})
	if err != nil {
		_ = framed.Close()
		release()
		return
	}

	wrapped, err := wrapCompression(framed.NetConn(), result.Effective.GetCompression())
	if err != nil {
		_ = framed.Close()
		release()
		return
	}

	framed.ReplaceNetConn(wrapped)
	// Negotiation is complete and the compression layer is final: batch all
	// further frame reads through the fixed connection buffer.
	framed.EnableReadBuffering()

	credits := int64(result.Effective.GetInitialCredits())
	if credits <= 0 {
		credits = int64(x.initialCredits)
	}

	lane, err := laneByte(result.Effective.GetLaneRole(), result.Effective.GetLaneIndex())
	if err != nil {
		_ = framed.Close()
		release()
		return
	}

	baseCtx := x.server.Context()
	if baseCtx == nil {
		baseCtx = context.Background()
	}

	// The base-context watch is registered only after newDuplexConn returns
	// (it needs the conn), while the closed handler can fire as soon as the
	// loops start. The buffered channel hands the watch's stop function to
	// the handler race-free: the handler blocks until registration completes,
	// then stops the watch before retiring the connection's accounting.
	watchReady := make(chan func() bool, 1)

	conn := newDuplexConn(
		framed,
		credits,
		withDuplexWriteTimeout(x.writeTimeout),
		withDuplexReadIdleTimeout(x.readIdleTimeout),
		withDuplexConnIdleTimeout(x.idleTimeout),
		withDuplexLane(lane),
		withDuplexChunkSize(x.chunkSize),
		withDuplexNegotiated(result.Effective),
		withDuplexSenderResolver(x.senderResolver),
		withDuplexInboundHandler(func(session DuplexSession, frame Frame) {
			x.serveDuplexFrame(baseCtx, session.(*duplexConn), frame)
		}),
		// Pipelined: the acceptor reads the socket concurrently with frame
		// dispatch (a transient drainer replaces the pre-fold serve
		// goroutine), so a decode plus mailbox enqueue never stalls reads
		// under sustained load.
		withDuplexPipelinedInbound(),
		withDuplexClosedHandler(func(DuplexSession) {
			stop := <-watchReady
			stop()
			release()
		}),
	)

	// The pre-fold serve loop exited when the server base context was
	// cancelled; a detached read loop blocks on the socket instead, so watch
	// the context and complete the close on the watcher's own goroutine.
	watchReady <- context.AfterFunc(baseCtx, func() { _ = conn.Close() })
}

// serveDuplexFrame dispatches one inbound frame on the connection's read
// loop, replacing the dedicated per-connection serve goroutine. Lane identity
// is enforced in duplexConn.readLoop for every frame, including PING/PONG
// that never reach this handler. Connection-scoped failures admit an ERROR
// and tear the transport down from the read loop (the reader never blocks on
// its own exit).
func (x *RemotingServer) serveDuplexFrame(ctx context.Context, conn *duplexConn, frame Frame) {
	switch frame.Type {
	case FrameTypeData:
		if err := x.handleDuplexData(ctx, conn, frame); err != nil {
			conn.drainAndCloseFramed()
		}
	default:
		conn.ReleasePayload(frame)
		_ = submitErrorFrame(ctx, conn, 0, internalpb.Code_CODE_FAILED_PRECONDITION, "unsupported frame type")
		conn.drainAndCloseFramed()
	}
}

// invokeDuplexTell runs the registered duplex tell handler with optional panic
// recovery so a misbehaving handler cannot crash the accept worker. It reports
// whether the handler panicked, so the caller can repay credit the handler
// claimed from its lease but never released. When no duplex tell handler is
// set, a registered RemoteTellRequest [ProtoHandler] is used as a
// compatibility bridge for fixture servers.
func (x *RemotingServer) invokeDuplexTell(ctx context.Context, env DataEnvelope) (panicked bool) {
	if env.SerializerID == SerializerIDInternalProto {
		return x.dispatchDuplexInternalTell(ctx, env)
	}

	if x.duplexTell != nil {
		if x.panicHandler == nil {
			x.duplexTell(ctx, env)
			return false
		}

		defer func() {
			if r := recover(); r != nil {
				panicked = true
				x.panicHandler(protoreflect.FullName(env.TypeName), r)
			}
		}()

		x.duplexTell(ctx, env)
		return false
	}

	handler, ok := x.handlers[remoteTellRequestName]
	if !ok {
		return false
	}

	rm := &internalpb.RemoteMessage{}
	rm.SetSender(env.Sender)
	rm.SetReceiver(env.Receiver)
	rm.SetMessage(env.Payload)
	rm.SetMetadata(metadataMapFromEnvelope(env.Metadata))
	req := &internalpb.RemoteTellRequest{}
	req.SetRemoteMessages([]*internalpb.RemoteMessage{rm})

	_, panicked, _ = x.recover(ctx, handler, nil, req, remoteTellRequestName)
	return panicked
}

// dispatchDuplexInternalTell routes an internal-proto DATA frame without
// expectsReply through the registered [ProtoHandler] for its wire type name,
// mirroring the ask-side dispatch in dispatchDuplexControl: internal-proto
// frames are control-plane messages and the handler registry is their single
// dispatch surface. Fire-and-forget semantics apply: an unregistered type or
// an unmarshalable payload is dropped (a broken frame has no reply channel),
// and handler errors are resolved by the handler itself (unknown receivers
// dead-letter server-side), so the error return is intentionally discarded.
// It reports whether the handler panicked, so the caller can repay credit
// the handler claimed from its lease but never released.
func (x *RemotingServer) dispatchDuplexInternalTell(ctx context.Context, env DataEnvelope) bool {
	name := protoreflect.FullName(env.TypeName)
	handler, ok := x.handlers[name]
	if !ok {
		return false
	}

	msgType, err := FindMessageType(name)
	if err != nil {
		return false
	}

	msg := msgType.New().Interface()
	if err := proto.Unmarshal(env.Payload, msg); err != nil {
		return false
	}

	_, panicked, _ := x.recover(ctx, handler, nil, msg, name)
	return panicked
}

// handleDuplexData decodes a DATA frame and either runs a tell inline on the
// read loop or enqueues an ask/control RPC on the ask worker pool.
//
// Table-ref decode failures are connection-scoped (ERROR then close). Other
// decode failures are request-scoped ERROR and the connection stays up.
// Each DATA frame that keeps the connection open is credit-granted exactly
// once: asks and request-scoped failures grant at their first terminal
// disposition, while tells hand the grant to a [CreditLease] repaid as the
// actor layer consumes the frame's messages (with a full grant at dispatch
// return when no handler claims the lease).
func (x *RemotingServer) handleDuplexData(ctx context.Context, conn *duplexConn, frame Frame) error {
	env, err := conn.DecodeDataEnvelope(frame.Payload, frame.HasMetadata())
	if err != nil {
		if errors.Is(err, ErrTableRefUnsupported) || errors.Is(err, ErrUnknownTableRef) {
			_ = submitErrorFrame(ctx, conn, 0, internalpb.Code_CODE_FAILED_PRECONDITION, err.Error())
			conn.ReleasePayload(frame)
			return err
		}

		_ = submitErrorFrame(ctx, conn, frame.Correlation, internalpb.Code_CODE_INVALID_ARGUMENT, err.Error())
		conn.ReleasePayload(frame)
		conn.noteOwnedFrame(frame)
		return nil
	}

	handlerCtx := ctx
	var cancel context.CancelFunc
	if len(env.Metadata) > 0 {
		md := NewMetadata()
		if err := md.UnmarshalBinary(env.Metadata); err != nil {
			_ = submitErrorFrame(ctx, conn, frame.Correlation, internalpb.Code_CODE_INVALID_ARGUMENT, err.Error())
			conn.ReleasePayload(frame)
			conn.noteOwnedFrame(frame)
			return nil
		}

		handlerCtx = md.ToContext(ctx)
		// Ask/control paths must enforce the rebased wire deadline; tell
		// stays unbounded beyond the connection idle timeout.
		if frame.ExpectsReply() {
			handlerCtx, cancel = md.DeadlineContext(handlerCtx)
		}
	}

	if !frame.ExpectsReply() {
		// The tell's credit grant rides a lease instead of being settled
		// here: the actor-side handler claims it and repays the sender as
		// the frame's messages are consumed, extending flow control across
		// mailbox residency. A handler that does not claim the lease falls
		// back to the full grant below, so the window can never leak.
		lease := conn.newTellLease(frame)
		panicked := x.invokeDuplexTell(WithCreditLease(handlerCtx, lease), env)
		conn.ReleasePayload(frame)

		if panicked {
			// A recovered handler panic can unwind between Split and the
			// per-share releases; repay everything still outstanding so the
			// sender's window survives the panic.
			lease.releaseOutstanding()
			return nil
		}

		lease.releaseUnclaimed()
		return nil
	}

	task := duplexAskTask{
		server: x,
		conn:   conn,
		frame:  frame,
		env:    env,
		ctx:    handlerCtx,
		cancel: cancel,
	}
	if err := x.askPool.AddTask(task); err != nil {
		if cancel != nil {
			cancel()
		}

		_ = submitErrorFrame(ctx, conn, frame.Correlation, internalpb.Code_CODE_UNAVAILABLE, err.Error())
		conn.ReleasePayload(frame)
		conn.noteOwnedFrame(frame)
		return nil
	}

	// Ownership left the read loop at worker-pool handoff; grant here, never
	// at the worker's deferred ReleasePayload (that runs after user code).
	conn.noteOwnedFrame(frame)
	return nil
}

// handleConn is the [RequestHandlerFunc] wired into the underlying [TCPServer].
// It runs a per-connection read loop: read frame -> deserialize (with metadata) ->
// enrich context -> dispatch -> serialize response -> write frame. The loop exits
// on EOF, read error, or idle timeout.
//
// Context Propagation:
//   - Request frames are deserialized with [UnmarshalBinaryWithMetadata] to
//     extract any metadata (headers, deadline) sent by the client.
//   - If metadata is present, it is used to enrich the context passed to the
//     handler via [Metadata.ToContext], enabling distributed tracing, auth
//     tokens, and other context-aware features.
//
// Low-GC strategy:
//   - The 4-byte frame header is read into a stack-allocated array.
//   - Frame body buffers come from [framePool] and are returned immediately
//     after deserialization.
//   - Response serialization reuses the [ProtoSerializer]'s internal buffer
//     pool.
func (x *RemotingServer) handleConn(conn Connection) {
	ctx := x.server.Context()

	// Buffer the read side for the connection's lifetime: the frame header
	// and body coalesce into one kernel read, and back-to-back frames of a
	// pipelined batch drain from the buffer instead of separate reads.
	// Deadlines still apply; the reader draws from conn.
	reader := getPooledReader(conn)
	defer putPooledReader(reader)

	for {
		// Apply idle timeout if configured.
		if x.idleTimeout > 0 {
			deadline := time.Now().Add(x.idleTimeout)
			if err := conn.SetReadDeadline(deadline); err != nil {
				return
			}
		}

		// Read the 4-byte length header into a stack-allocated array (zero alloc).
		var hdr [4]byte
		if _, err := io.ReadFull(reader, hdr[:]); err != nil {
			return // EOF, timeout, or connection reset — exit silently.
		}

		totalLen := binary.BigEndian.Uint32(hdr[:])
		if totalLen < 8 {
			return // Malformed frame — close connection.
		}
		if totalLen > x.maxFrameSize {
			return // Frame too large — close connection.
		}

		// Acquire a pooled buffer for the frame body.
		frame := x.framePool.Get(int(totalLen))
		copy(frame[:4], hdr[:])

		if _, err := io.ReadFull(reader, frame[4:]); err != nil {
			x.framePool.Put(frame)
			return
		}

		// Deserialize the frame with metadata support. The frame may be in one
		// of two formats:
		//   - Legacy format: [totalLen|nameLen|typeName|protoBytes] (8+ bytes header)
		//   - Metadata format: [totalLen|nameLen|metaLen|typeName|metadata|protoBytes] (12+ bytes header)
		//
		// We detect the format and use the appropriate unmarshaler for backward
		// compatibility while supporting context propagation when metadata is present.
		var msg proto.Message
		var md *Metadata
		var typeName protoreflect.FullName
		var err error

		// Metadata frames have at least 12 bytes header; legacy frames have 8 bytes.
		// Try metadata format first if frame is long enough.
		if len(frame) >= 12 {
			msg, md, typeName, err = x.serializer.UnmarshalBinaryWithMetadata(frame)
			if err == ErrInvalidMessageLength {
				// Might be a legacy frame where bytes 8:12 are part of the type name
				// or proto payload. Fall back to legacy unmarshaler.
				msg, typeName, err = x.serializer.UnmarshalBinary(frame)
			}
		} else {
			// Frame too short for metadata format — must be legacy.
			msg, typeName, err = x.serializer.UnmarshalBinary(frame)
		}

		if err != nil {
			x.framePool.Put(frame)
			return // Corrupt frame — close connection.
		}

		// Resolve the handler while the frame is still alive: typeName is a
		// zero-copy view into the pooled frame buffer and reading it after
		// the buffer is returned to the pool races with the next Get that
		// reuses and overwrites the buffer, yielding a garbage name and a
		// spurious lookup miss.
		handler, ok := x.handlers[typeName]
		if !ok {
			handler = x.fallback
		}

		x.framePool.Put(frame)

		// Enrich the context with metadata if present. This enables context
		// propagation (tracing, auth, deadlines) across the proto TCP transport.
		handlerCtx := ctx
		if md != nil {
			handlerCtx = md.ToContext(ctx)
		}

		if handler == nil {
			// No handler and no fallback. Close the connection instead of
			// silently consuming the frame: on request/response connections a
			// silently dropped request leaves the peer blocked forever
			// waiting for a reply that will never come, whereas a close
			// surfaces an immediate EOF. An unroutable frame also indicates a
			// protocol error or frame corruption, so the connection cannot be
			// trusted anyway.
			return
		}

		// proto.MessageName reads the name from the decoded message's
		// descriptor, which is heap-stable, unlike typeName, which must not
		// outlive the frame buffer released above.
		resp, panicked, herr := x.recover(handlerCtx, handler, conn, msg, proto.MessageName(msg))
		if panicked {
			// Handler panicked — close the connection so the client receives an
			// immediate EOF instead of blocking forever waiting for a response
			// that will never arrive.
			return
		}

		if herr != nil {
			return // Handler signaled a fatal error — close connection.
		}

		// Fire-and-forget: handler returned nil response.
		if resp == nil {
			continue
		}

		// Serialize and write the response frame.
		respData, merr := x.serializer.MarshalBinaryTo(x.framePool, resp)
		if merr != nil {
			return // Marshal failure — close connection.
		}

		if x.idleTimeout > 0 {
			deadline := time.Now().Add(x.idleTimeout)
			if err := conn.SetWriteDeadline(deadline); err != nil {
				x.framePool.Put(respData)
				return
			}
		}

		_, writeErr := conn.Write(respData)
		x.framePool.Put(respData)
		if writeErr != nil {
			return // Write failure — close connection.
		}
	}
}

// recover invokes the handler inside a deferred recover. If the handler
// panics and a [PanicHandlerFunc] is configured, the callback is invoked and
// panicked is set to true so the caller can skip the current message without
// tearing down the connection. When no panic handler is configured, the panic
// propagates normally (closing the connection via the deferred close in
// [TCPServer.serveConn]).
func (x *RemotingServer) recover(ctx context.Context, handler ProtoHandler, conn Connection, msg proto.Message, typeName protoreflect.FullName) (resp proto.Message, panicked bool, err error) {
	if x.panicHandler == nil {
		// No recovery configured — let panics propagate.
		resp, err = handler(ctx, conn, msg)
		return resp, false, err
	}

	defer func() {
		if r := recover(); r != nil {
			x.panicHandler(typeName, r)
			panicked = true
		}
	}()

	resp, err = handler(ctx, conn, msg)
	return resp, false, err
}
