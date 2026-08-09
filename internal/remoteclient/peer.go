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

package remoteclient

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"syscall"
	"time"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/remote"
)

// remotingTransport dials framed TCP connections and optionally upgrades them
// with TLS before the duplex HELLO exchange. Compression is applied after
// HELLO by [inet.OpenDuplex], not here.
type remotingTransport struct {
	*inet.TCPTransport
	// tlsConfig is cloned per dial when non-nil so session tickets and
	// ServerName are safe under concurrent dials.
	tlsConfig *tls.Config
}

// Dial connects to peer and returns a framed connection ready for HELLO.
// When TLS is configured the raw TCP connection is wrapped before return so
// the handshake bytes ride inside the TLS record layer.
func (x *remotingTransport) Dial(ctx context.Context, peer string, lane inet.LaneSpec) (inet.FramedConn, error) {
	framed, err := x.TCPTransport.Dial(ctx, peer, lane)
	if err != nil {
		return nil, err
	}

	if x.tlsConfig == nil {
		return framed, nil
	}

	// Clone per dial for safe concurrent mutation. The session cache pointer
	// is installed once on the client's stored config (see NewClient) and
	// shared by every clone, so TLS session resumption works across dials.
	cfg := x.tlsConfig.Clone()

	tlsConn := tls.Client(framed.NetConn(), cfg)
	if replacer, ok := framed.(interface{ ReplaceNetConn(net.Conn) }); ok {
		replacer.ReplaceNetConn(tlsConn)
	}

	return framed, nil
}

// peer owns one long-lived duplex session and cached protocol state for a
// single remote endpoint (host:port). All session and cache mutations are
// serialized by mu. legacyInflight tracks in-flight legacy SendProto calls so
// a switchover to duplex can drain them before the new path is used.
type peer struct {
	// addr is the JoinHostPort form used as the peers map key and dial target.
	addr string
	// host and port are the split endpoint components for NetClient lookups.
	host string
	port int
	// client is the owning remoting client (pin, timeouts, TLS, credits).
	client *client

	mu sync.Mutex
	// cache records unknown / duplex / legacy after the first successful
	// classification. Cleared when a duplex session is closed after failure.
	cache protocolCache
	// session is the live duplex connection; nil when unused or closed.
	session inet.DuplexSession
	// legacyInflight counts concurrent legacy unary sends for switchover
	// drain. It is guarded by mu together with legacyDrain, because a
	// sync.WaitGroup forbids Add racing an active Wait, and a legacy send
	// may legitimately begin while a switchover drain is waiting.
	legacyInflight int
	// legacyDrain is closed when legacyInflight reaches zero while a drain
	// waits; nil when nobody is waiting.
	legacyDrain chan struct{}
}

// peerFor returns the peer manager entry for host:port, creating it lazily.
// The hot path is lock-free via the peers map; creation uses peersMu.
func (x *client) peerFor(host string, port int) *peer {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	if p, ok := x.peers.Get(addr); ok {
		return p
	}

	x.peersMu.Lock()
	defer x.peersMu.Unlock()

	if p, ok := x.peers.Get(addr); ok {
		return p
	}

	p := &peer{
		addr:   addr,
		host:   host,
		port:   port,
		client: x,
	}
	x.peers.Set(addr, p)
	return p
}

// cachedProtocol returns the cached wire protocol for the peer.
func (x *peer) cachedProtocol() peerProtocol {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.cache.get()
}

// shouldUseCoalescer reports whether outbound tells may use the legacy
// coalescer. Coalescing is legacy-only: duplex tells skip it. Returns true
// when the pin forces legacy or the peer is already cached as legacy.
func (x *client) shouldUseCoalescer(host string, port int) bool {
	if x.protocolPin == remote.ProtocolPinLegacy {
		return true
	}

	return x.peerFor(host, port).cachedProtocol() == peerProtocolLegacy
}

// pinRequiresLegacy reports whether the configured pin forces the legacy
// unary SendProto path and skips duplex dial entirely.
func (x *client) pinRequiresLegacy() bool {
	return x.protocolPin == remote.ProtocolPinLegacy
}

// beginLegacySend increments the in-flight legacy counter used by switchover
// drain. Every beginLegacySend must be paired with [peer.endLegacySend].
func (x *peer) beginLegacySend() {
	x.mu.Lock()
	x.legacyInflight++
	x.mu.Unlock()
}

// endLegacySend decrements the in-flight legacy counter after a unary send
// completes (success or failure) and releases a waiting switchover drain
// when the count reaches zero.
func (x *peer) endLegacySend() {
	x.mu.Lock()
	x.legacyInflight--

	if x.legacyInflight <= 0 && x.legacyDrain != nil {
		close(x.legacyDrain)
		x.legacyDrain = nil
	}

	x.mu.Unlock()
}

// waitLegacyDrain blocks until no legacy unary send is in flight. A legacy
// send that begins during the wait is legal and simply extends the drain.
func (x *peer) waitLegacyDrain() {
	x.mu.Lock()
	if x.legacyInflight == 0 {
		x.mu.Unlock()
		return
	}

	if x.legacyDrain == nil {
		x.legacyDrain = make(chan struct{})
	}
	ch := x.legacyDrain
	x.mu.Unlock()
	<-ch
}

// closeSession closes an active duplex session and clears cached protocol
// state so the next call re-probes. Safe when no session is open.
func (x *peer) closeSession() {
	x.mu.Lock()
	defer x.mu.Unlock()

	if x.session != nil {
		_ = x.session.Close()
		x.session = nil
	}

	x.cache.clear()
}

// ensureDuplex returns a live duplex session, dialing when needed.
//
// When the pin is legacy, or the peer is cached as legacy within the re-probe
// window, it returns [errPreferLegacy] so callers fall back to SendProto. In
// auto mode, an EOF/reset before HELLO_ACK marks the peer legacy and returns
// [errPreferLegacy]. When a prior legacy classification expires (or is
// cleared), in-flight legacy sends are drained before the new duplex session
// is installed (order-preserving switchover).
func (x *peer) ensureDuplex(ctx context.Context) (inet.DuplexSession, error) {
	x.mu.Lock()
	if x.session != nil {
		if !x.session.IsClosed() {
			s := x.session
			x.mu.Unlock()
			return s, nil
		}

		// The session died while idle (peer restart, connection reclaim).
		// Discard it here so this caller transparently re-dials instead of
		// failing its first send on a dead session. Nothing has been sent
		// yet, so the retry is safe under at-most-once semantics.
		_ = x.session.Close()
		x.session = nil
		x.cache.clear()
	}

	if x.client.pinRequiresLegacy() {
		x.mu.Unlock()
		return nil, errPreferLegacy
	}

	switchingFromLegacy := false
	if x.cache.isLegacy() {
		if !x.cache.legacyExpired(time.Now()) {
			x.mu.Unlock()
			return nil, errPreferLegacy
		}
		// Legacy classification aged out: re-probe duplex after draining
		// any in-flight unary sends so request order is preserved.
		x.cache.clear()
		switchingFromLegacy = true
	}
	x.mu.Unlock()

	if switchingFromLegacy {
		x.waitLegacyDrain()
	}

	session, err := x.dialDuplex(ctx)
	if err != nil {
		if x.client.protocolPin == remote.ProtocolPinAuto && isLegacyHandshakeFailure(err) {
			x.mu.Lock()
			x.cache.set(peerProtocolLegacy)
			x.mu.Unlock()
			return nil, errPreferLegacy
		}

		return nil, err
	}

	// Drain again after dial so any legacy send that started during the
	// handshake finishes before duplex traffic begins. Never Wait while
	// holding mu: endLegacySend does not take mu, but other ensureDuplex
	// callers do.
	if switchingFromLegacy {
		x.waitLegacyDrain()
	}

	x.mu.Lock()
	defer x.mu.Unlock()

	if x.session != nil {
		_ = session.Close()
		return x.session, nil
	}

	x.session = session
	x.cache.set(peerProtocolDuplex)
	go x.monitorSession(session)
	return session, nil
}

// monitorSession drains non-correlated inbound frames for the life of session
// and retires the peer's cached session when the connection dies or the peer
// reports a connection-scoped ERROR. Without this drain, unsolicited frames
// would fill the session's inbound buffer and stall its reader, and an idle
// session death would only be discovered by the next caller.
func (x *peer) monitorSession(session inet.DuplexSession) {
	for {
		frame, err := session.Recv(context.Background())
		if err != nil {
			x.retireSession(session)
			return
		}

		if frame.Type == inet.FrameTypeError && frame.Correlation == 0 {
			x.retireSession(session)
			return
		}
		// Stray frames (PONG and future unsolicited traffic) are dropped in
		// Milestone 2.
	}
}

// retireSession closes session and clears the peer's cached state only when
// session is still the current one, so a newer session installed after the
// failure is never clobbered.
func (x *peer) retireSession(session inet.DuplexSession) {
	x.mu.Lock()
	defer x.mu.Unlock()

	_ = session.Close()

	if x.session != session {
		return
	}

	x.session = nil
	x.cache.clear()
}

// dialDuplex opens a new duplex session to the peer using the client's TLS,
// compression, frame-size, and credit settings. The HELLO LaneRole is CONTROL
// until Milestone 3 introduces lane isolation.
func (x *peer) dialDuplex(ctx context.Context) (inet.DuplexSession, error) {
	transport := &remotingTransport{
		TCPTransport: inet.NewTCPTransport(
			inet.WithTCPTransportDialTimeout(x.client.dialTimeout),
			inet.WithTCPTransportKeepAlive(x.client.keepAlive),
			inet.WithTCPTransportMaxFrameSize(x.client.maxFrameSize),
		),
		tlsConfig: x.client.tlsConfig,
	}

	localHello := &internalpb.Hello{
		Revision:                    inet.CapabilityRevisionBaseline,
		LaneRole:                    internalpb.LaneRole_LANE_ROLE_CONTROL,
		Compression:                 compressionCodec(x.client.compression),
		MaxFrameSize:                x.client.maxFrameSize,
		MaxMessageSize:              uint64(x.client.maxFrameSize),
		InitialCredits:              x.client.initialCredits,
		MaxConcurrentLargeTransfers: remote.DefaultMaxConcurrentLargeTransfers,
	}

	session, _, err := inet.OpenDuplex(ctx, transport, x.addr, localHello, x.client.writeTimeout)
	return session, err
}

// isLegacyHandshakeFailure reports whether err indicates the peer closed or
// reset before HELLO_ACK, which auto mode treats as a legacy-only listener.
// Timeouts are not treated as legacy: they surface to the caller so ask
// deadlines remain enforceable against a silent peer.
func isLegacyHandshakeFailure(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return false
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if errors.Is(opErr.Err, syscall.ECONNRESET) || errors.Is(opErr.Err, syscall.EPIPE) {
			return true
		}
	}

	return false
}

// compressionCodec maps remote compression settings to HELLO codec values
// advertised during duplex negotiation.
func compressionCodec(c remote.Compression) internalpb.CompressionCodec {
	switch c {
	case remote.GzipCompression:
		return internalpb.CompressionCodec_COMPRESSION_CODEC_GZIP
	case remote.ZstdCompression:
		return internalpb.CompressionCodec_COMPRESSION_CODEC_ZSTD
	case remote.BrotliCompression:
		return internalpb.CompressionCodec_COMPRESSION_CODEC_BROTLI
	default:
		return internalpb.CompressionCodec_COMPRESSION_CODEC_NONE
	}
}

// mapDuplexErr translates duplex transport errors to remoteclient semantics.
// [inet.ErrDuplexBackpressure] becomes [gerrors.ErrRemoteSendBackpressure]
// while preserving the original cause via [errors.Join].
func mapDuplexErr(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, inet.ErrDuplexBackpressure) {
		return errors.Join(gerrors.ErrRemoteSendBackpressure, err)
	}

	return err
}

// shouldRetireDuplexSession reports whether err is a terminal transport
// failure that should close the shared peer session. Caller cancellation,
// ask timeouts, backpressure, and request-scoped ERROR frames must not
// tear down multiplexing for unrelated concurrent callers.
func shouldRetireDuplexSession(err error, reply inet.Frame) bool {
	if err == nil {
		return false
	}

	if reply.Type == inet.FrameTypeError && reply.Correlation != 0 {
		return false
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	if errors.Is(err, inet.ErrDuplexBackpressure) {
		return false
	}

	return true
}
