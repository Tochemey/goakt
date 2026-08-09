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
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/remote"
)

// maxLaneReconnectBackoff caps the per-lane exponential redial backoff after
// consecutive dial failures.
const maxLaneReconnectBackoff = 30 * time.Second

// routeCacheLimit bounds the per-peer sticky route cache so unbounded receiver
// churn cannot grow peer memory without limit. It matches the per-kind
// compression-table capacity the cache migrates onto in Milestone 5.
const routeCacheLimit = 8192

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

// peer owns role-separated long-lived duplex sessions and cached protocol state
// for a single remote endpoint (host:port). All session and cache mutations are
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
	// control, ordinary, and large are the active role-separated duplex lanes.
	control  inet.DuplexSession
	ordinary []inet.DuplexSession
	large    inet.DuplexSession
	// dialing serializes one concurrent dial per lane identity.
	dialing map[laneKey]chan types.Unit
	// backoff limits failed re-dials per lane.
	backoff map[laneKey]laneBackoff
	// routes caches sticky receiver lane assignments.
	routes map[string]laneKey
	// generation invalidates a dial that was in flight during ClosePeer.
	generation uint64
	// closed is set by closeAllLanes. Close and ClosePeer discard the peer
	// afterward, so ensureLane must not redial on a torn-down instance.
	closed bool
	// dialCtx is cancelled by closeAllLanes so in-flight HELLO dials abort
	// promptly instead of waiting on the caller's context alone.
	dialCtx    context.Context
	dialCancel context.CancelFunc
	// legacyInflight counts concurrent legacy unary sends for switchover
	// drain. It is guarded by mu together with legacyDrain, because a
	// sync.WaitGroup forbids Add racing an active Wait, and a legacy send
	// may legitimately begin while a switchover drain is waiting.
	legacyInflight int
	// legacyDrain is closed when legacyInflight reaches zero while a drain
	// waits; nil when nobody is waiting.
	legacyDrain chan types.Unit
}

// peerFor returns the peer manager entry for host:port, creating it lazily.
// The hot path is lock-free via the peers map; creation uses peersMu.
func (x *client) peerFor(host string, port int) *peer {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	if peer, ok := x.peers.Get(addr); ok {
		return peer
	}

	x.peersMu.Lock()
	defer x.peersMu.Unlock()

	if peer, ok := x.peers.Get(addr); ok {
		return peer
	}

	dialCtx, dialCancel := context.WithCancel(context.Background())
	peer := &peer{
		addr:       addr,
		host:       host,
		port:       port,
		client:     x,
		ordinary:   make([]inet.DuplexSession, x.ordinaryLanes),
		dialing:    make(map[laneKey]chan types.Unit),
		backoff:    make(map[laneKey]laneBackoff),
		routes:     make(map[string]laneKey),
		dialCtx:    dialCtx,
		dialCancel: dialCancel,
	}
	x.peers.Set(addr, peer)
	return peer
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
		x.legacyDrain = make(chan types.Unit)
	}
	ch := x.legacyDrain
	x.mu.Unlock()
	<-ch
}

// closeAllLanes closes every lane and clears protocol, route, and reconnect
// state. It is safe to call when no lane has been opened. Session Close runs
// outside mu and in parallel so a blocked writer cannot serialize teardown
// across every ordinary lane.
func (x *peer) closeAllLanes() {
	x.mu.Lock()

	sessions := make([]inet.DuplexSession, 0, 2+len(x.ordinary))
	if x.control != nil {
		sessions = append(sessions, x.control)
	}

	if x.large != nil {
		sessions = append(sessions, x.large)
	}

	for _, session := range x.ordinary {
		if session != nil {
			sessions = append(sessions, session)
		}
	}

	x.control = nil
	x.large = nil
	x.ordinary = make([]inet.DuplexSession, x.client.ordinaryLanes)
	x.cache.clear()
	x.routes = make(map[string]laneKey)
	x.backoff = make(map[laneKey]laneBackoff)
	x.generation++
	x.closed = true

	for _, wait := range x.dialing {
		close(wait)
	}

	x.dialing = make(map[laneKey]chan types.Unit)

	if x.dialCancel != nil {
		x.dialCancel()
	}
	// Leave dialCtx cancelled. This peer is discarded by Close/ClosePeer;
	// a fresh peerFor entry gets a new dial context.
	x.mu.Unlock()

	var wg sync.WaitGroup
	for _, session := range sessions {
		wg.Add(1)
		go func(session inet.DuplexSession) {
			defer wg.Done()
			_ = session.Close()
		}(session)
	}
	wg.Wait()
}

// ensureLane returns a live requested duplex lane, dialing it once when needed.
//
// When the pin is legacy, or the peer is cached as legacy within the re-probe
// window, it returns [errPreferLegacy] so callers fall back to SendProto. In
// auto mode, an EOF/reset before HELLO_ACK marks the peer legacy and returns
// [errPreferLegacy]. When a prior legacy classification expires (or is
// cleared), in-flight legacy sends are drained before the new duplex session
// is installed (order-preserving switchover).
func (x *peer) ensureLane(ctx context.Context, role internalpb.LaneRole, index uint32) (inet.DuplexSession, error) {
	key := laneKey{role: role, index: index}
	for {
		x.mu.Lock()
		if x.closed {
			x.mu.Unlock()
			return nil, errLaneClosedDuringDial
		}
		generation := x.generation

		if session := x.laneLocked(key); session != nil {
			if !session.IsClosed() {
				x.mu.Unlock()
				return session, nil
			}
			_ = session.Close()
			x.setLaneLocked(key, nil)
			x.clearCacheIfNoLanesLocked()
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

		if retry, ok := x.backoff[key]; ok && time.Now().Before(retry.until) {
			err := retry.err
			x.mu.Unlock()
			return nil, err
		}

		if wait, ok := x.dialing[key]; ok {
			x.mu.Unlock()
			select {
			case <-wait:
				// closeAllLanes sets closed and wakes waiters. Do not loop
				// into a fresh dial on a torn-down peer.
				x.mu.Lock()
				reset := x.closed || x.generation != generation
				x.mu.Unlock()
				if reset {
					return nil, errLaneClosedDuringDial
				}
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		wait := make(chan types.Unit)
		x.dialing[key] = wait
		x.mu.Unlock()

		if switchingFromLegacy {
			x.waitLegacyDrain()
		}

		session, err := x.dialLane(ctx, role, index)
		if err != nil {
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				x.recordDialFailure(key, wait, generation, err)
			} else {
				x.releaseDialing(key, wait)
			}

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
		// holding mu: endLegacySend does not take mu, but other ensureLane
		// callers do.
		if switchingFromLegacy {
			x.waitLegacyDrain()
		}

		x.mu.Lock()
		x.releaseDialingLocked(key, wait)
		if x.generation != generation {
			x.mu.Unlock()
			_ = session.Close()
			return nil, errLaneClosedDuringDial
		}

		if current := x.laneLocked(key); current != nil && !current.IsClosed() {
			x.mu.Unlock()
			_ = session.Close()
			return current, nil
		}

		x.setLaneLocked(key, session)
		x.cache.set(peerProtocolDuplex)
		delete(x.backoff, key)
		x.mu.Unlock()
		go x.monitorSession(key, session)
		return session, nil
	}
}

// releaseDialing wakes waiters for key when the dialer still owns wait.
func (x *peer) releaseDialing(key laneKey, wait chan types.Unit) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.releaseDialingLocked(key, wait)
}

// releaseDialingLocked closes wait only when it is still the in-flight dial
// marker. closeAllLanes may have already closed it.
func (x *peer) releaseDialingLocked(key laneKey, wait chan types.Unit) {
	if current, ok := x.dialing[key]; ok && current == wait {
		delete(x.dialing, key)
		close(wait)
	}
}

// monitorSession drains non-correlated inbound frames for the life of a lane
// and retires the peer's cached session when the connection dies or the peer
// reports a connection-scoped ERROR. Without this drain, unsolicited frames
// would fill the session's inbound buffer and stall its reader, and an idle
// session death would only be discovered by the next caller.
func (x *peer) monitorSession(key laneKey, session inet.DuplexSession) {
	for {
		frame, err := session.Recv(context.Background())
		if err != nil {
			x.retireLane(key, session)
			return
		}

		if frame.Type == inet.FrameTypeError && frame.Correlation == 0 {
			x.retireLane(key, session)
			return
		}
		// PING/PONG never reach Recv; connection-scoped ERROR is handled
		// above. Remaining unsolicited frames are dropped.
	}
}

// retireLane closes session and clears the peer's cached state only when
// session is still the current one, so a newer session installed after the
// failure is never clobbered.
func (x *peer) retireLane(key laneKey, session inet.DuplexSession) {
	x.mu.Lock()
	defer x.mu.Unlock()

	_ = session.Close()

	if x.laneLocked(key) != session {
		return
	}
	x.setLaneLocked(key, nil)
	// Keep the peer classified as duplex while any sibling lane is still
	// live. Clearing here would force an unnecessary re-probe on the next
	// send even though other lanes already proved the peer speaks duplex.
	x.clearCacheIfNoLanesLocked()
}

// clearCacheIfNoLanesLocked clears the protocol cache only when every lane
// slot is empty. Caller must hold x.mu.
func (x *peer) clearCacheIfNoLanesLocked() {
	if x.control != nil || x.large != nil {
		return
	}
	for _, ordinary := range x.ordinary {
		if ordinary != nil {
			return
		}
	}
	x.cache.clear()
}

// dialLane opens a requested duplex lane using the client's transport settings.
func (x *peer) dialLane(ctx context.Context, role internalpb.LaneRole, index uint32) (inet.DuplexSession, error) {
	x.mu.Lock()
	dialCtx := x.dialCtx
	x.mu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(dialCtx, cancel)
	defer stop()

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
		LaneRole:                    role,
		LaneIndex:                   index,
		Compression:                 compressionCodec(x.client.compression),
		MaxFrameSize:                x.client.maxFrameSize,
		MaxMessageSize:              uint64(x.client.maxFrameSize),
		InitialCredits:              x.client.initialCredits,
		MaxConcurrentLargeTransfers: x.client.maxConcurrentLargeTransfers,
	}

	session, _, err := inet.OpenDuplex(
		ctx,
		transport,
		x.addr,
		localHello,
		inet.LaneSpec{Role: role, Index: index},
		x.client.writeTimeout,
		x.client.readIdleTimeout,
	)
	return session, err
}

type laneKey struct {
	role  internalpb.LaneRole
	index uint32
}

type laneBackoff struct {
	err   error
	until time.Time
	delay time.Duration
}

// laneLocked returns the lane referenced by key while x.mu is held.
func (x *peer) laneLocked(key laneKey) inet.DuplexSession {
	switch key.role {
	case internalpb.LaneRole_LANE_ROLE_CONTROL:
		return x.control
	case internalpb.LaneRole_LANE_ROLE_LARGE:
		return x.large
	case internalpb.LaneRole_LANE_ROLE_ORDINARY:
		if key.index < uint32(len(x.ordinary)) {
			return x.ordinary[key.index]
		}
	}
	return nil
}

// setLaneLocked replaces the lane referenced by key while x.mu is held.
func (x *peer) setLaneLocked(key laneKey, session inet.DuplexSession) {
	switch key.role {
	case internalpb.LaneRole_LANE_ROLE_CONTROL:
		x.control = session
	case internalpb.LaneRole_LANE_ROLE_LARGE:
		x.large = session
	case internalpb.LaneRole_LANE_ROLE_ORDINARY:
		if key.index < uint32(len(x.ordinary)) {
			x.ordinary[key.index] = session
		}
	}
}

// recordDialFailure records per-lane exponential reconnect backoff and wakes
// concurrent callers waiting on the failed dial. wait and generation identify
// the dial attempt: only the still-registered marker is released, and backoff
// is not recorded when closeAllLanes reset the peer mid-dial, so a stale
// failure can neither close a fresh dialer's marker nor gate a fresh lane set.
func (x *peer) recordDialFailure(key laneKey, wait chan types.Unit, generation uint64, err error) {
	x.mu.Lock()
	defer x.mu.Unlock()

	x.releaseDialingLocked(key, wait)
	if x.generation != generation {
		return
	}

	if previous, ok := x.backoff[key]; ok {
		previous.delay *= 2
		if previous.delay > maxLaneReconnectBackoff {
			previous.delay = maxLaneReconnectBackoff
		}
		previous.err = err
		previous.until = time.Now().Add(previous.delay)
		x.backoff[key] = previous
		return
	}

	x.backoff[key] = laneBackoff{err: err, delay: time.Second, until: time.Now().Add(time.Second)}
}

// route returns the cached sticky lane assignment for a user receiver.
// Assignment is a pure function of the receiver, the lane count, and the
// configured patterns, so receivers beyond [routeCacheLimit] are computed per
// send instead of cached; they still land on the same lane every time.
func (x *peer) route(receiver string) laneKey {
	x.mu.Lock()
	defer x.mu.Unlock()

	if route, ok := x.routes[receiver]; ok {
		return route
	}

	role, index := routeUser(receiver, x.client.ordinaryLanes, x.client.largeMessageDestinations)
	route := laneKey{role: role, index: index}

	if len(x.routes) < routeCacheLimit {
		x.routes[receiver] = route
	}

	return route
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
