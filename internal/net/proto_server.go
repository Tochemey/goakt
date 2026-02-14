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
	"io"
	"net"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// ProtoHandler processes a deserialized protobuf request and returns a
// response. Returning a nil [proto.Message] with a nil error signals a
// fire-and-forget message — no response frame is written back to the client.
//
// The handler receives the server's base [context.Context] (set via
// [WithProtoServerContext]) and the [Connection] that delivered the request,
// allowing access to peer addresses and connection metadata.
//
// Implementations must be safe for concurrent use; the same handler may be
// invoked from many connections simultaneously.
type ProtoHandler func(ctx context.Context, conn Connection, req proto.Message) (proto.Message, error)

// ProtoServer is a high-performance, low-GC protobuf-over-TCP server.
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
//	ps, err := tcp.NewProtoServer("0.0.0.0:9000",
//		tcp.WithProtoHandler("myapp.PingRequest", pingHandler),
//		tcp.WithProtoHandler("myapp.PutRequest", putHandler),
//		tcp.WithProtoIdleTimeout(30 * time.Second),
//	)
//	if err != nil { ... }
//
//	if err := ps.Listen(); err != nil { ... }
//
//	// Serve blocks until shutdown.
//	go func() { log.Fatal(ps.Serve()) }()
//
//	// Later …
//	ps.Shutdown(5 * time.Second)
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

// ProtoServer is a high-performance, low-GC protobuf-over-TCP server.
type ProtoServer struct {
	server       *TCPServer
	handlers     map[protoreflect.FullName]ProtoHandler
	fallback     ProtoHandler
	panicHandler PanicHandlerFunc
	serializer   *ProtoSerializer
	framePool    *framePool
	serverOpts   []ServerOption

	idleTimeout  time.Duration
	maxFrameSize uint32
}

// ProtoServerOption configures a [ProtoServer] before it is started.
type ProtoServerOption func(*ProtoServer)

// NewProtoServer creates a [ProtoServer] bound to the given address
// (host:port). The returned server is not yet listening; call
// [ProtoServer.Listen] followed by [ProtoServer.Serve] to start accepting
// connections.
//
// Defaults: no idle timeout (connections live until closed by the peer),
// no fallback handler (unregistered message types are silently skipped),
// max frame size of 16 MiB.
func NewProtoServer(listenAddr string, opts ...ProtoServerOption) (*ProtoServer, error) {
	ps := &ProtoServer{
		handlers:     make(map[protoreflect.FullName]ProtoHandler),
		serializer:   NewProtoSerializer(),
		framePool:    newFramePool(),
		maxFrameSize: defaultMaxFrameSize, // Default: 16 MiB
	}

	for _, opt := range opts {
		opt(ps)
	}

	// Always wire the proto read-loop as the underlying server's request handler.
	ps.serverOpts = append(ps.serverOpts, WithRequestHandler(ps.handleConn))

	srv, err := NewTCPServer(listenAddr, ps.serverOpts...)
	if err != nil {
		return nil, err
	}
	ps.server = srv
	return ps, nil
}

// WithProtoHandler registers a [ProtoHandler] for the given fully qualified
// protobuf message name (e.g. "myapp.PingRequest"). When a frame carrying
// this type name arrives, the handler is invoked with the deserialized
// message.
//
// Registering a handler for the same name twice silently overwrites the
// previous one. Handlers must be registered before [ProtoServer.Serve] is
// called.
func WithProtoHandler(fullName protoreflect.FullName, handler ProtoHandler) ProtoServerOption {
	return func(ps *ProtoServer) {
		ps.handlers[fullName] = handler
	}
}

// WithFallbackProtoHandler sets a catch-all [ProtoHandler] invoked when no
// handler is registered for the incoming message type. If no fallback is
// set, unregistered messages are silently skipped (no response is sent).
func WithFallbackProtoHandler(handler ProtoHandler) ProtoServerOption {
	return func(ps *ProtoServer) {
		ps.fallback = handler
	}
}

// WithProtoServerIdleTimeout sets the maximum duration a connection may remain
// idle (no complete frame received) before the server closes it. Zero
// (the default) means no idle timeout — the connection stays open until
// the peer disconnects or the server shuts down.
func WithProtoServerIdleTimeout(duration time.Duration) ProtoServerOption {
	return func(ps *ProtoServer) {
		ps.idleTimeout = duration
	}
}

// WithProtoServerContext sets a base [context.Context] on the underlying
// [TCPServer], propagated to every [ProtoHandler] invocation. Use this for
// cancellation signals or request-scoped values.
func WithProtoServerContext(ctx context.Context) ProtoServerOption {
	return func(ps *ProtoServer) {
		ps.serverOpts = append(ps.serverOpts, WithServerContext(ctx))
	}
}

// WithProtoServerTLSConfig sets the TLS configuration used by the underlying
// [TCPServer].
//
// Providing a non-nil config enables TLS for this ProtoServer when you call
// [ProtoServer.ListenTLS]. If config is nil, TLS is not configured and
// [ProtoServer.ListenTLS] will fail with [ErrNoTLSConfig] (you can still use
// [ProtoServer.Listen] for plain TCP).
//
// The supplied config should be fully populated for server use (e.g. with
// Certificates / GetCertificate, and appropriate security settings such as
// MinVersion).
func WithProtoServerTLSConfig(config *tls.Config) ProtoServerOption {
	return func(ps *ProtoServer) {
		ps.serverOpts = append(ps.serverOpts, WithTLSConfig(config))
	}
}

// WithProtoServerListenConfig overrides the [ListenConfig] used by the
// underlying [TCPServer] when creating its listener.
//
// This option affects calls to [ProtoServer.Listen] and [ProtoServer.ListenTLS]
// (i.e., the bind/listen step). It does not modify per-connection behavior
// after accept.
//
// If config is nil, the server's default listen configuration is used.
// If provided multiple times, the last call wins.
//
// The configuration must be set before calling Listen/ListenTLS.
func WithProtoServerListenConfig(config *ListenConfig) ProtoServerOption {
	return func(ps *ProtoServer) {
		ps.serverOpts = append(ps.serverOpts, WithListenConfig(config))
	}
}

// WithProtoServerLoops sets the number of concurrent accept loops used by the
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
//   - Consider pairing with [WithProtoServerAllowThreadLocking] only after
//     measuring, as pinning loops to OS threads can increase thread usage.
//
// This option must be provided before calling [ProtoServer.Listen] /
// [ProtoServer.ListenTLS].
func WithProtoServerLoops(loops int) ProtoServerOption {
	return func(ps *ProtoServer) {
		ps.serverOpts = append(ps.serverOpts, WithLoops(loops))
	}
}

// WithProtoServerAllowThreadLocking controls whether the underlying [TCPServer]
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
func WithProtoServerAllowThreadLocking(allow bool) ProtoServerOption {
	return func(ps *ProtoServer) {
		ps.serverOpts = append(ps.serverOpts, WithAllowThreadLocking(allow))
	}
}

// WithProtoServerMaxFrameSize sets the maximum allowed frame size (in bytes)
// for incoming messages. Frames exceeding this limit cause the connection to
// be closed. The default is 16 MiB ([defaultMaxFrameSize]).
func WithProtoServerMaxFrameSize(size uint32) ProtoServerOption {
	return func(ps *ProtoServer) {
		ps.maxFrameSize = size
	}
}

// WithProtoServerPanicHandler sets a [PanicHandlerFunc] that is called when a
// [ProtoHandler] panics during dispatch. Without a panic handler, panics in
// handlers will close the connection silently. With a handler set, panics are
// recovered, the callback is invoked, and the read loop continues serving
// subsequent messages on the same connection.
func WithProtoServerPanicHandler(f PanicHandlerFunc) ProtoServerOption {
	return func(ps *ProtoServer) {
		ps.panicHandler = f
	}
}

// WithProtoServerConnWrapper appends a [ConnWrapper] (e.g. compression) to the
// underlying [TCPServer]'s wrapping pipeline.
func WithProtoServerConnWrapper(w ConnWrapper) ProtoServerOption {
	return func(ps *ProtoServer) {
		ps.serverOpts = append(ps.serverOpts, WithConnWrapper(w))
	}
}

// WithProtoServerMaxAcceptConnections sets the maximum total connections on the
// underlying [TCPServer].
func WithProtoServerMaxAcceptConnections(limit int32) ProtoServerOption {
	return func(ps *ProtoServer) {
		ps.serverOpts = append(ps.serverOpts, WithMaxAcceptConnections(limit))
	}
}

// WithProtoServerBallast sets the GC ballast size (in MiB) on the underlying
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
func WithProtoServerBallast(sizeInMiB int) ProtoServerOption {
	return func(ps *ProtoServer) {
		ps.serverOpts = append(ps.serverOpts, WithBallast(sizeInMiB))
	}
}

// WithProtoServerConnectionCreator sets the factory used to create [Connection]
// values for incoming connections on the underlying [TCPServer].
func WithProtoServerConnectionCreator(f ConnectionCreatorFunc) ProtoServerOption {
	return func(ps *ProtoServer) {
		ps.serverOpts = append(ps.serverOpts, WithConnectionCreator(f))
	}
}

// Listen creates the TCP listener on the configured address. Call
// [ProtoServer.Serve] afterwards to start accepting connections.
func (ps *ProtoServer) Listen() error {
	return ps.server.Listen()
}

// ListenTLS enables TLS and creates the TCP listener. Returns
// [ErrNoTLSConfig] if no TLS configuration was provided.
func (ps *ProtoServer) ListenTLS() error {
	return ps.server.ListenTLS()
}

// Serve starts the accept loops and blocks until all loops and in-flight
// connections complete. Use [ProtoServer.Shutdown] or [ProtoServer.Halt]
// from another goroutine to stop the server.
func (ps *ProtoServer) Serve() error {
	return ps.server.Serve()
}

// Shutdown gracefully stops the server. See [TCPServer.Shutdown] for the
// semantics of the timeout parameter d:
//   - d > 0: wait up to d for in-flight connections to finish.
//   - d == 0: wait indefinitely.
//   - d < 0: return immediately without waiting.
func (ps *ProtoServer) Shutdown(d time.Duration) error {
	return ps.server.Shutdown(d)
}

// Halt immediately stops the server without waiting for in-flight
// connections. Equivalent to Shutdown(-1).
func (ps *ProtoServer) Halt() error {
	return ps.server.Halt()
}

// ListenAddr returns the actual [*net.TCPAddr] the server is listening on.
// Useful when the server was started on port 0. Returns nil if the server
// has not started listening.
func (ps *ProtoServer) ListenAddr() *net.TCPAddr {
	return ps.server.ListenAddr()
}

// ActiveConnections returns the number of connections currently being served.
func (ps *ProtoServer) ActiveConnections() int32 {
	return ps.server.ActiveConnections()
}

// AcceptedConnections returns the total number of connections accepted since
// the server started.
func (ps *ProtoServer) AcceptedConnections() int32 {
	return ps.server.AcceptedConnections()
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
func (ps *ProtoServer) handleConn(conn Connection) {
	ctx := ps.server.Context()

	for {
		// Apply idle timeout if configured.
		if ps.idleTimeout > 0 {
			deadline := time.Now().Add(ps.idleTimeout)
			if err := conn.SetReadDeadline(deadline); err != nil {
				return
			}
		}

		// Read the 4-byte length header into a stack-allocated array (zero alloc).
		var hdr [4]byte
		if _, err := io.ReadFull(conn, hdr[:]); err != nil {
			return // EOF, timeout, or connection reset — exit silently.
		}

		totalLen := binary.BigEndian.Uint32(hdr[:])
		if totalLen < 8 {
			return // Malformed frame — close connection.
		}
		if totalLen > ps.maxFrameSize {
			return // Frame too large — close connection.
		}

		// Acquire a pooled buffer for the frame body.
		frame := ps.framePool.Get(int(totalLen))
		copy(frame[:4], hdr[:])

		if _, err := io.ReadFull(conn, frame[4:]); err != nil {
			ps.framePool.Put(frame)
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
			msg, md, typeName, err = ps.serializer.UnmarshalBinaryWithMetadata(frame)
			if err == ErrInvalidMessageLength {
				// Might be a legacy frame where bytes 8:12 are part of the type name
				// or proto payload. Fall back to legacy unmarshaler.
				msg, typeName, err = ps.serializer.UnmarshalBinary(frame)
			}
		} else {
			// Frame too short for metadata format — must be legacy.
			msg, typeName, err = ps.serializer.UnmarshalBinary(frame)
		}

		ps.framePool.Put(frame)

		if err != nil {
			return // Corrupt frame — close connection.
		}

		// Enrich the context with metadata if present. This enables context
		// propagation (tracing, auth, deadlines) across the proto TCP transport.
		handlerCtx := ctx
		if md != nil {
			handlerCtx = md.ToContext(ctx)
		}

		// Dispatch to the registered handler.
		handler, ok := ps.handlers[typeName]
		if !ok {
			handler = ps.fallback
		}
		if handler == nil {
			// No handler and no fallback — skip the message.
			continue
		}

		resp, panicked, herr := ps.recover(handlerCtx, handler, conn, msg, typeName)
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
		respData, merr := ps.serializer.MarshalBinary(resp)
		if merr != nil {
			return // Marshal failure — close connection.
		}

		if ps.idleTimeout > 0 {
			deadline := time.Now().Add(ps.idleTimeout)
			if err := conn.SetWriteDeadline(deadline); err != nil {
				return
			}
		}

		if _, err := conn.Write(respData); err != nil {
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
func (ps *ProtoServer) recover(ctx context.Context, handler ProtoHandler, conn Connection, msg proto.Message, typeName protoreflect.FullName) (resp proto.Message, panicked bool, err error) {
	if ps.panicHandler == nil {
		// No recovery configured — let panics propagate.
		resp, err = handler(ctx, conn, msg)
		return resp, false, err
	}

	defer func() {
		if r := recover(); r != nil {
			ps.panicHandler(typeName, r)
			panicked = true
		}
	}()

	resp, err = handler(ctx, conn, msg)
	return resp, false, err
}

// framePool maintains a set of [sync.Pool] instances bucketed by power-of-two
// size. This avoids allocating a new []byte for every incoming frame and
// significantly reduces GC pressure under high message rates.
//
// Bucket boundaries (powers of two from 256 B to 4 MiB) cover the vast
// majority of protobuf messages while keeping internal fragmentation below 2x.
type framePool struct {
	pools [numBuckets]sync.Pool
}

const (
	minBucketShift = 8  // 256 B
	maxBucketShift = 22 // 4 MiB
	numBuckets     = maxBucketShift - minBucketShift + 1
)

func newFramePool() *framePool {
	fp := &framePool{}
	for i := range fp.pools {
		size := 1 << (minBucketShift + i)
		fp.pools[i] = sync.Pool{
			New: func() any {
				buf := make([]byte, size)
				return &buf
			},
		}
	}
	return fp
}

// Get returns a []byte of exactly n bytes, drawn from the smallest pool
// bucket that can satisfy the request. For sizes larger than the biggest
// bucket a fresh slice is allocated (and will be collected by the GC).
func (fp *framePool) Get(n int) []byte {
	idx := bucketIndex(n)
	if idx >= numBuckets {
		// Oversized frame — allocate directly.
		return make([]byte, n)
	}
	bp := fp.pools[idx].Get().(*[]byte)
	return (*bp)[:n]
}

// Put returns a buffer to the appropriate pool bucket. Buffers that do not
// match any bucket (oversized or misaligned capacity) are simply dropped
// for GC collection.
func (fp *framePool) Put(buf []byte) {
	c := cap(buf)
	idx := bucketIndexExact(c)
	if idx < 0 || idx >= numBuckets {
		return // oversized or misaligned — let GC collect it
	}
	buf = buf[:c]
	fp.pools[idx].Put(&buf)
}

// bucketIndex returns the pool index for a buffer of size n.
func bucketIndex(n int) int {
	if n <= 1<<minBucketShift {
		return 0
	}
	// Find the smallest power-of-two >= n by counting the bit-width of (n-1).
	shift := 0
	v := n - 1
	for v > 0 {
		v >>= 1
		shift++
	}
	idx := shift - minBucketShift
	if idx >= numBuckets {
		return numBuckets // signals "oversized"
	}
	return idx
}

// bucketIndexExact returns the pool index only if cap is an exact
// power-of-two matching a bucket boundary. Returns -1 otherwise.
func bucketIndexExact(c int) int {
	if c == 0 || c&(c-1) != 0 {
		return -1 // not a power of two
	}
	shift := 0
	v := c
	for v > 1 {
		v >>= 1
		shift++
	}
	idx := shift - minBucketShift
	if idx < 0 || idx >= numBuckets {
		return -1
	}
	return idx
}
