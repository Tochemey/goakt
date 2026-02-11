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

package tcp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// RequestHandlerFunc is the callback invoked for every accepted connection.
// The implementation owns the [Connection] for the duration of the call;
// once it returns the connection is closed automatically.
type RequestHandlerFunc func(conn Connection)

// ConnectionCreatorFunc returns a fresh [Connection] value used to service an
// incoming TCP connection. The default creates a plain [*TCPConn].
type ConnectionCreatorFunc func() Connection

// ServerOption configures a [Server] before it is started.
type ServerOption func(*Server)

var defaultListenConfig = &ListenConfig{
	SocketReusePort: true,
}

// Server is a multi-loop TCP server. It listens on a single address and
// dispatches accepted connections to a [WorkerPool]. Optional TLS and
// [ConnWrapper] layers (e.g. compression) are applied transparently.
//
// Create a Server with [NewServer], configure it with [ServerOption] values,
// then call [Server.Listen] (or [Server.ListenTLS]) followed by [Server.Serve].
type Server struct {
	listenAddr        *net.TCPAddr
	listener          *net.TCPListener
	requestHandler    RequestHandlerFunc
	connectionCreator ConnectionCreatorFunc
	ctx               context.Context
	tlsConfig         *tls.Config
	listenConfig      *ListenConfig
	connWrappers      []ConnWrapper
	connWaitGroup     sync.WaitGroup
	connStructPool    sync.Pool
	wp                *WorkerPool[net.Conn]
	ballast           []byte
	shutdownTimeout   time.Duration
	activeConnections atomic.Int32
	acceptedConns     atomic.Int32
	maxAcceptConns    atomic.Int32
	shutdown          atomic.Bool
	loops             int
	tlsEnabled        bool
	allowThreadLock   bool
}

// Connection is the interface presented to a [RequestHandlerFunc] for every
// accepted TCP connection. The default implementation is [*TCPConn].
type Connection interface {
	net.Conn

	// NetConn returns the underlying [net.Conn].
	NetConn() net.Conn
	// Server returns the [Server] that accepted this connection.
	Server() *Server
	// ClientAddr returns the remote peer address as a [*net.TCPAddr].
	ClientAddr() *net.TCPAddr
	// ServerAddr returns the local listen address as a [*net.TCPAddr].
	ServerAddr() *net.TCPAddr
	// StartTime returns when the connection was started via [TCPConn.Start].
	StartTime() time.Time
	// SetContext attaches a user-defined context to the connection.
	SetContext(ctx context.Context)
	// Context returns the context previously set with [SetContext],
	// or [context.Background] if none was set.
	Context() context.Context

	// Start records the connection start time. Called by the server before
	// the request handler is invoked.
	Start()
	// Reset reinitialises the connection with a new underlying [net.Conn].
	Reset(netConn net.Conn)
	// SetServer associates this connection with its parent [Server].
	SetServer(server *Server)
}

// TCPConn is the default [Connection] implementation backed by a [net.Conn].
type TCPConn struct {
	net.Conn
	server *Server
	ctx    context.Context
	ts     int64
	_      [16]byte // cache-line padding
}

// NewServer creates a [Server] bound to the given address (host:port).
//
// Defaults: 8 accept loops, 20 MiB ballast, SO_REUSEPORT enabled,
// plain [*TCPConn] connection struct, no TLS.
func NewServer(listenAddr string, opts ...ServerOption) (*Server, error) {
	tcpAddr, err := net.ResolveTCPAddr("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("resolving address %q: %w", listenAddr, err)
	}

	var s *Server
	s = &Server{
		listenAddr:   tcpAddr,
		listenConfig: defaultListenConfig,
		loops:        8,
		connStructPool: sync.Pool{
			New: func() any {
				conn := s.connectionCreator()
				conn.SetServer(s)
				return conn
			},
		},
	}

	s.connectionCreator = func() Connection {
		return &TCPConn{}
	}

	s.ballast = make([]byte, 20*1024*1024)

	for _, o := range opts {
		o(s)
	}

	return s, nil
}

// WithTLSConfig sets the TLS configuration used when TLS is enabled.
func WithTLSConfig(config *tls.Config) ServerOption {
	return func(s *Server) { s.tlsConfig = config }
}

// WithListenConfig overrides the default [ListenConfig] used to create
// the listening socket.
func WithListenConfig(config *ListenConfig) ServerOption {
	return func(s *Server) { s.listenConfig = config }
}

// WithConnectionCreator sets the factory used to create [Connection] values
// for incoming connections. Use this to substitute a custom implementation
// that embeds [TCPConn].
func WithConnectionCreator(f ConnectionCreatorFunc) ServerOption {
	return func(s *Server) { s.connectionCreator = f }
}

// WithRequestHandler sets the callback invoked for every accepted connection.
func WithRequestHandler(f RequestHandlerFunc) ServerOption {
	return func(s *Server) { s.requestHandler = f }
}

// WithServerContext sets a base [context.Context] that is returned by
// [Server.GetContext]. It can be used to propagate cancellation or
// request-scoped values.
func WithServerContext(ctx context.Context) ServerOption {
	return func(s *Server) { s.ctx = ctx }
}

// WithLoops sets the number of concurrent accept loops. The default is 8.
// Values less than 1 are clamped to 1.
func WithLoops(loops int) ServerOption {
	return func(s *Server) {
		if loops < 1 {
			loops = 1
		}
		s.loops = loops
	}
}

// WithAllowThreadLocking enables pinning half of the accept loops to OS
// threads via [runtime.LockOSThread]. This can improve latency on systems
// with many cores by reducing scheduler migration.
func WithAllowThreadLocking(allow bool) ServerOption {
	return func(s *Server) { s.allowThreadLock = allow }
}

// WithConnWrapper appends a [ConnWrapper] (e.g. compression) to the
// server's wrapping pipeline, applied after TLS. Multiple wrappers
// are applied in the order they were added.
func WithConnWrapper(w ConnWrapper) ServerOption {
	return func(s *Server) { s.connWrappers = append(s.connWrappers, w) }
}

// WithMaxAcceptConnections sets the maximum number of connections the
// server will accept in total. Zero (the default) means unlimited.
// Once the limit is reached the server initiates a graceful shutdown.
func WithMaxAcceptConnections(limit int32) ServerOption {
	return func(s *Server) { s.maxAcceptConns.Store(limit) }
}

// WithBallast pre-allocates and retains a byte slice of the given size (in MiB)
// on the [Server] to increase the baseline heap size.
//
// Why this exists:
//   - A larger baseline heap can reduce GC frequency and GC-induced latency
//     spikes under allocation-heavy workloads (accept loops, buffering, TLS, etc.).
//
// Trade-offs:
//   - The ballast is intentionally kept reachable for the lifetime of the server,
//     so it increases memory usage (RSS/heap) immediately.
//   - It does not “speed up” your program by itself; it primarily shifts the
//     GC/latency vs. memory trade-off.
//
// Notes:
//   - sizeInMiB must be >= 0. Use 0 to disable the ballast.
//   - The default is 20 MiB.
func WithBallast(sizeInMiB int) ServerOption {
	return func(s *Server) { s.ballast = make([]byte, sizeInMiB*1024*1024) }
}

// TLSConfig returns the TLS configuration, or nil if none was set.
func (s *Server) TLSConfig() *tls.Config {
	return s.tlsConfig
}

// ListenConfig returns the [ListenConfig] used to create the
// listening socket.
func (s *Server) ListenConfig() *ListenConfig {
	return s.listenConfig
}

// Context returns the server's base context. If none was set via
// [WithServerContext], [context.Background] is returned.
func (s *Server) Context() context.Context {
	if s.ctx == nil {
		s.ctx = context.Background()
	}
	return s.ctx
}

// Loops returns the number of accept loops configured for this server.
func (s *Server) Loops() int {
	return s.loops
}

// ActiveConnections returns the number of connections currently being
// served.
func (s *Server) ActiveConnections() int32 {
	return s.activeConnections.Load()
}

// AcceptedConnections returns the total number of connections accepted
// since the server started.
func (s *Server) AcceptedConnections() int32 {
	return s.acceptedConns.Load()
}

// ListenAddr returns the actual [*net.TCPAddr] the server is listening
// on, which is useful when the server was started on port 0. Returns nil
// if the server has not started listening.
func (s *Server) ListenAddr() *net.TCPAddr {
	if s.listener == nil {
		return nil
	}
	addr, _ := s.listener.Addr().(*net.TCPAddr)
	return addr
}

// EnableTLS marks the server to wrap every accepted connection with TLS
// using the previously configured TLS config. Returns [ErrNoTLSConfig] if
// no TLS configuration has been set.
func (s *Server) EnableTLS() error {
	if s.tlsConfig == nil {
		return ErrNoTLSConfig
	}
	s.tlsEnabled = true
	return nil
}

// Listen creates the TCP listener. Call [Server.Serve] afterwards to start
// accepting connections. The listener uses the address and [ListenConfig]
// provided at construction time.
func (s *Server) Listen() error {
	network := "tcp4"
	if IsIPv6Addr(s.listenAddr) {
		network = "tcp6"
	}

	s.listenConfig.underlying.Control = applyListenSocketOptions(s.listenConfig)

	listener, err := s.listenConfig.underlying.Listen(s.Context(), network, s.listenAddr.String())
	if err != nil {
		return err
	}

	tcpListener, ok := listener.(*net.TCPListener)
	if !ok {
		return errors.Join(listener.Close(), ErrInvalidListener)
	}
	s.listener = tcpListener
	return nil
}

// ListenTLS is a convenience that calls [Server.EnableTLS] followed by
// [Server.Listen]. Returns [ErrNoTLSConfig] if no TLS configuration has
// been set.
func (s *Server) ListenTLS() error {
	if err := s.EnableTLS(); err != nil {
		return err
	}
	return s.Listen()
}

// Serve starts the accept loops and blocks until all loops and in-flight
// connections complete. Call [Server.Listen] (or [Server.ListenTLS]) before
// calling Serve. Use [Server.Shutdown] or [Server.Halt] from another
// goroutine to stop the server.
func (s *Server) Serve() error {
	if s.listener == nil {
		return ErrNoListener
	}

	maxProcs := runtime.GOMAXPROCS(0)
	loops := s.Loops()

	s.wp = NewWorkerPool(s.serveConn)
	s.wp.SetNumShards(maxProcs * 2)
	s.wp.SetIdleWorkerLifetime(5 * time.Second)
	s.wp.Start()
	defer s.wp.Stop()

	errChan := make(chan error, loops)
	for i := range loops {
		go func(id int) {
			if s.allowThreadLock && maxProcs >= 2 && id < loops/2 {
				runtime.LockOSThread()
				defer runtime.UnlockOSThread()
			}
			errChan <- s.acceptLoop()
		}(i)
	}

	for range loops {
		if err := <-errChan; err != nil {
			return err
		}
	}

	if s.activeConnections.Load() == 0 {
		return nil
	}
	return s.awaitConnections()
}

// Shutdown gracefully stops the server. The behaviour depends on d:
//   - d > 0: wait up to d for in-flight connections to finish.
//   - d == 0: wait indefinitely.
//   - d < 0: return immediately without waiting.
//
// Shutdown is idempotent — subsequent calls are no-ops.
func (s *Server) Shutdown(d time.Duration) error {
	if !s.shutdown.CompareAndSwap(false, true) {
		return nil
	}
	s.shutdownTimeout = d
	return s.listener.Close()
}

// Halt immediately stops the server without waiting for in-flight
// connections. It is equivalent to Shutdown(-1).
func (s *Server) Halt() error {
	return s.Shutdown(-1 * time.Second)
}

func (s *Server) acceptLoop() error {
	for {
		if s.shutdown.Load() {
			return nil
		}

		tcpConn, err := s.listener.AcceptTCP()
		if err != nil {
			// Shutdown may have closed the listener.
			if s.shutdown.Load() {
				return nil
			}

			// Retry on deadline/timeout errors; surface everything else.
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}

			return err
		}

		newCount := s.acceptedConns.Add(1)
		limit := s.maxAcceptConns.Load()

		// Check limit after incrementing to avoid race.
		if limit > 0 && newCount > limit {
			s.acceptedConns.Add(-1)
			// Close error intentionally ignored — connection is being rejected.
			_ = tcpConn.Close()
			// Signal shutdown only on the first breach.
			if newCount == limit+1 {
				_ = s.Shutdown(0)
			}
			continue
		}

		if err := s.wp.AddTask(tcpConn); err != nil {
			s.acceptedConns.Add(-1)
			// Close error intentionally ignored — connection already failed to enqueue.
			_ = tcpConn.Close()
		}
	}
}

func (s *Server) serveConn(netConn net.Conn) {
	s.connWaitGroup.Add(1)
	s.activeConnections.Add(1)

	conn := s.connStructPool.Get().(Connection)

	if s.tlsEnabled {
		netConn = tls.Server(netConn, s.tlsConfig)
	}

	for _, w := range s.connWrappers {
		wrapped, err := w.Wrap(netConn)
		if err != nil {
			// Close error intentionally ignored — wrapper setup already failed.
			_ = netConn.Close()
			s.activeConnections.Add(-1)
			s.connWaitGroup.Done()
			return
		}
		netConn = wrapped
	}

	conn.Reset(netConn)
	conn.Start()
	s.requestHandler(conn)
	// Close error intentionally ignored — request handling is complete.
	_ = conn.Close()

	s.connStructPool.Put(conn)
	s.activeConnections.Add(-1)
	s.connWaitGroup.Done()
}

func (s *Server) awaitConnections() error {
	if s.shutdownTimeout < 0 {
		return nil
	}

	done := make(chan struct{})
	go func() {
		s.connWaitGroup.Wait()
		close(done)
	}()

	if s.shutdownTimeout == 0 {
		<-done
		return nil
	}

	timer := time.NewTimer(s.shutdownTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
	return nil
}

// ClientAddr returns the remote peer address as a [*net.TCPAddr],
// or nil if the underlying connection is not TCP.
func (conn *TCPConn) ClientAddr() *net.TCPAddr {
	addr, _ := conn.RemoteAddr().(*net.TCPAddr)
	return addr
}

// ServerAddr returns the local listen address as a [*net.TCPAddr],
// or nil if the underlying connection is not TCP.
func (conn *TCPConn) ServerAddr() *net.TCPAddr {
	addr, _ := conn.LocalAddr().(*net.TCPAddr)
	return addr
}

// StartTime returns the time recorded by [TCPConn.Start].
func (conn *TCPConn) StartTime() time.Time {
	return time.Unix(conn.ts/1e9, conn.ts%1e9)
}

// SetContext attaches a user-defined context to the connection.
func (conn *TCPConn) SetContext(ctx context.Context) {
	conn.ctx = ctx
}

// Context returns the context previously set with [TCPConn.SetContext],
// or [context.Background] if none was set.
func (conn *TCPConn) Context() context.Context {
	if conn.ctx == nil {
		conn.ctx = context.Background()
	}
	return conn.ctx
}

// NetConn returns the underlying [net.Conn].
func (conn *TCPConn) NetConn() net.Conn {
	return conn.Conn
}

// GetNetTCPConn returns the underlying [*net.TCPConn], or nil if the
// connection has been wrapped by TLS or a [ConnWrapper].
func (conn *TCPConn) GetNetTCPConn() *net.TCPConn {
	c, _ := conn.Conn.(*net.TCPConn)
	return c
}

// SetServer associates this connection with its parent [Server].
func (conn *TCPConn) SetServer(s *Server) {
	conn.server = s
}

// Server returns the [Server] that accepted this connection.
func (conn *TCPConn) Server() *Server {
	return conn.server
}

// Reset reinitialises the connection with a new underlying [net.Conn] and
// clears the context. Called by the server when recycling a pooled
// [Connection] struct.
func (conn *TCPConn) Reset(netConn net.Conn) {
	conn.Conn = netConn
	conn.ctx = nil
}

// Start records the connection start time as the current wall-clock time.
func (conn *TCPConn) Start() {
	conn.ts = time.Now().UnixNano()
}

// StartTLS upgrades the connection to TLS. If config is nil the server's
// TLS configuration is used. Returns [ErrInvalidTLSConfig] if no
// configuration is available.
func (conn *TCPConn) StartTLS(config *tls.Config) error {
	if config == nil {
		config = conn.Server().TLSConfig()
	}
	if config == nil {
		return ErrInvalidTLSConfig
	}
	conn.Conn = tls.Server(conn.Conn, config)
	return nil
}

// IsIPv6Addr reports whether addr is an IPv6 address.
func IsIPv6Addr(addr *net.TCPAddr) bool {
	return addr.IP.To4() == nil && len(addr.IP) == net.IPv6len
}
