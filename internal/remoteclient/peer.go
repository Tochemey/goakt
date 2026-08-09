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
// compression-table capacity.
const routeCacheLimit = 8192

// maxTellRedeliverAttempts bounds how many delivery attempts the lane pump
// makes for a single admitted tell whose write failed on a dying session. The
// value 2 is the original write plus one redial-and-resend, so a peer that
// keeps dropping the connection dead-letters the tell after one failed
// redelivery rather than retrying indefinitely. Retrying the message in place
// (never re-queuing it behind tells admitted later) preserves per-lane FIFO.
const maxTellRedeliverAttempts = 2

// pathEntry caches a sticky lane assignment and the receiver path's table ID
// on the session that assigned it.
type pathEntry struct {
	// lane is the sticky lane identity for the receiver.
	lane laneKey
	// pathID is the compression-table ID on session, or 0 for inline.
	pathID uint64
	// session is the duplex session that owns pathID; nil until registered.
	session inet.DuplexSession
}

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
	// routes caches sticky receiver lane assignments and path table IDs.
	routes map[string]pathEntry
	// pumps stages admitted fire-and-forget tells per lane until a live
	// session can carry them (see tell_pump.go). Guarded by mu.
	pumps map[laneKey]*tellPump
	// pumpStop tears down pump goroutines when the peer is discarded.
	pumpStop chan types.Unit
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
		routes:     make(map[string]pathEntry),
		pumps:      make(map[laneKey]*tellPump),
		pumpStop:   make(chan types.Unit),
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
	x.routes = make(map[string]pathEntry)
	x.backoff = make(map[laneKey]laneBackoff)
	x.generation++

	if !x.closed {
		// Wake pump goroutines so queued admitted tells fan out as transport
		// failures instead of vanishing with the discarded peer.
		close(x.pumpStop)
	}
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
			session.ReleasePayload(frame)
			x.retireLane(key, session)
			return
		}

		// PING/PONG never reach Recv; connection-scoped ERROR is handled
		// above. Remaining unsolicited frames are dropped.
		session.ReleasePayload(frame)
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
		Revision:                    inet.CapabilityRevisionCredits,
		LaneRole:                    role,
		LaneIndex:                   index,
		Compression:                 compressionCodec(x.client.compression),
		MaxFrameSize:                x.client.maxFrameSize,
		MaxMessageSize:              x.client.maxMessageSize,
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
		x.client.chunkSize,
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

// route returns the sticky lane assignment for a user receiver. cached is true
// when the entry lives in the peer route map (so the receiver path may use a
// session table ID). Receivers beyond [routeCacheLimit] still get a stable
// lane every time, but the receiver ref stays inline on the wire.
func (x *peer) route(receiver string) (entry pathEntry, cached bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.routeLocked(receiver)
}

// routeLocked is [peer.route] with x.mu already held, so the tell fast path can
// resolve the route and the live session under a single lock acquisition.
func (x *peer) routeLocked(receiver string) (entry pathEntry, cached bool) {
	if existing, ok := x.routes[receiver]; ok {
		return existing, true
	}

	role, index := routeUser(receiver, x.client.ordinaryLanes, x.client.largeMessageDestinations)
	entry = pathEntry{lane: laneKey{role: role, index: index}}

	if len(x.routes) < routeCacheLimit {
		x.routes[receiver] = entry
		return entry, true
	}

	return entry, false
}

// rememberPathRef stores the receiver path table ID for the session that
// assigned it. No-op when the receiver is not in the route cache.
func (x *peer) rememberPathRef(receiver string, pathID uint64, session inet.DuplexSession) {
	x.mu.Lock()
	defer x.mu.Unlock()

	entry, ok := x.routes[receiver]
	if !ok {
		return
	}

	entry.pathID = pathID
	entry.session = session
	x.routes[receiver] = entry
}

// admitTell accepts one fire-and-forget tell for delivery on key's lane
// without requiring a live session. The payload and metadata are copied
// because callers may recycle their buffers (payload pool) once this
// returns. Propagation headers and deadlines must already be snapshotted
// into params.metadata (via enrichContext / buildUserTellParams); the pump
// never sees the caller's context, so that wire blob is the sole carrier of
// context meta for async delivery. A full byte window blocks until ctx
// expires and then reports backpressure; a torn-down peer rejects admission.
func (x *peer) admitTell(ctx context.Context, key laneKey, params tellParams) error {
	pump, err := x.ensureTellPump(key)
	if err != nil {
		return err
	}

	owned := params
	owned.payload = cloneBytes(params.payload)
	owned.metadata = cloneBytes(params.metadata)
	return x.enqueueOnPump(ctx, pump, owned)
}

// admitTellOrFanOut admits a fire-and-forget tell to the lane pump. A full
// byte window surfaces backpressure to the caller so senders keep their own
// deadline policy; a torn-down peer cannot deliver the tell, so it fans out as
// a dead letter and admission reports success. This keeps the internal
// teardown sentinel from leaking to fire-and-forget callers.
func (x *peer) admitTellOrFanOut(ctx context.Context, key laneKey, params tellParams) error {
	err := x.admitTell(ctx, key, params)
	if err == nil {
		return nil
	}

	if errors.Is(err, errLaneClosedDuringDial) {
		x.fanOutTellFailure(params, err)
		return nil
	}

	return err
}

// ensureTellPump returns the lane pump for key, creating it if needed.
// Rejects admission when the peer is torn down. pending is incremented under
// mu so directTellSession cannot race a live-session fast path ahead of the
// tell being enqueued (FIFO fence).
func (x *peer) ensureTellPump(key laneKey) (*tellPump, error) {
	x.mu.Lock()
	defer x.mu.Unlock()

	if x.closed {
		return nil, errLaneClosedDuringDial
	}

	pump, ok := x.pumps[key]
	if !ok {
		pump = newTellPump()
		x.pumps[key] = pump
		go x.runTellPump(key, pump)
	}

	pump.pending.Add(1)
	return pump, nil
}

// enqueueOnPump blocks until params (already owned) is queued on pump, the
// caller's ctx expires (backpressure), or the peer is torn down. Caller must
// have already incremented pump.pending via ensureTellPump.
func (x *peer) enqueueOnPump(ctx context.Context, pump *tellPump, params tellParams) error {
	item := admittedTell{params: params, cost: admitCost(params)}

	for {
		if x.tryEnqueueAdmit(pump, item) {
			return nil
		}

		select {
		case <-ctx.Done():
			pump.pending.Add(-1)
			return errors.Join(gerrors.ErrRemoteSendBackpressure, ctx.Err())
		case <-x.pumpStop:
			pump.pending.Add(-1)
			return errLaneClosedDuringDial
		case <-pump.space:
		}
	}
}

// tryEnqueueAdmit appends item when the byte window allows it (or the queue
// is empty so an oversized single tell can still make progress). Returns
// true when the item was enqueued.
func (x *peer) tryEnqueueAdmit(pump *tellPump, item admittedTell) bool {
	maxBytes := x.admitMaxBytes()

	pump.mu.Lock()
	defer pump.mu.Unlock()

	if pump.liveLen() > 0 && pump.queuedBytes+item.cost > maxBytes {
		return false
	}

	pump.queue = append(pump.queue, item)
	pump.queuedBytes += item.cost
	select {
	case pump.notify <- types.Unit{}:
	default:
	}
	return true
}

// admitMaxBytes is the per-lane admission window, matched to the duplex writer
// credit window (initialCredits). A zero credit window floors to the default,
// mirroring the duplex connection (see OpenDuplex), so admission is never
// throttled to a single byte.
func (x *peer) admitMaxBytes() int64 {
	credits := x.client.initialCredits
	if credits == 0 {
		credits = remote.DefaultCreditWindow
	}
	return int64(credits)
}

// runTellPump drains admitted tells for key until the peer is torn down.
// Teardown fans out anything still queued as transport failures so admitted
// messages are never lost silently.
func (x *peer) runTellPump(key laneKey, pump *tellPump) {
	for {
		select {
		case <-x.pumpStop:
			x.drainPumpOnStop(pump)
			return
		case <-pump.notify:
			x.drainPumpQueue(key, pump)
		}
	}
}

// drainPumpQueue pops and delivers every tell currently queued.
func (x *peer) drainPumpQueue(key laneKey, pump *tellPump) {
	for {
		item, ok := pump.popAdmit()
		if !ok {
			return
		}
		x.deliverAdmittedTell(key, pump, item.params)
	}
}

// drainPumpOnStop empties the pump queue after peer teardown, reporting each
// stranded tell through the failure fan-out. The dead-letter drain on the
// actor side drops these gracefully when the system is already stopping.
func (x *peer) drainPumpOnStop(pump *tellPump) {
	for {
		item, ok := pump.popAdmit()
		if !ok {
			return
		}
		x.fanOutTellFailureShared(item.params, errLaneClosedDuringDial)
		pump.pending.Add(-1)
	}
}

// deliverAdmittedTell carries one admitted tell to the wire: ensure a live
// session (dialing if needed), encode with table refs, and submit to the
// session writer queue. A peer that turns out to be legacy is served through
// the legacy delivery path; every transport failure fans out instead of
// surfacing, because the caller already received nil at admission.
//
// A write that fails because the session died is retried in place against a
// freshly dialed lane, up to [maxTellRedeliverAttempts]. Retrying the same
// message here (rather than re-queuing it) keeps it ahead of tells admitted
// behind it on this lane (per-lane FIFO) and never re-enters the admission
// byte window, so the pump goroutine cannot stall itself.
//
// The caller's context is intentionally not used here. Admission already
// returned nil, so caller cancel must not abort delivery. Wire context meta
// (propagator headers, absolute deadline) lives in params.metadata and is
// encoded into the DATA frame. Dial is bounded by dialTimeout; Submit relies
// on the duplex writeTimeout when the tell context has no deadline, so the
// steady-state path (live session) allocates no per-message timer.
func (x *peer) deliverAdmittedTell(key laneKey, pump *tellPump, params tellParams) {
	defer pump.pending.Add(-1)

	var lastErr error
	for attempt := 0; attempt < maxTellRedeliverAttempts; attempt++ {
		session := x.liveLane(key)
		if session == nil {
			ctx, cancel := x.dialBoundContext()
			var err error
			session, err = x.ensureLane(ctx, key.role, key.index)
			cancel()
			if err != nil {
				if errors.Is(err, errPreferLegacy) {
					x.deliverAdmittedTellLegacy(params)
					return
				}

				x.fanOutTellFailureShared(params, err)
				return
			}
		}

		entry, cached := x.route(params.receiver)
		encoded, flags, err := encodeTellFrame(x, session, entry, cached, params)
		if err != nil {
			x.fanOutTellFailureShared(params, err)
			return
		}

		// Background: duplex Submit applies writeTimeout via bindWriteDeadline.
		err = session.Tell(context.Background(), inet.Frame{
			Type:    inet.FrameTypeData,
			Flags:   flags,
			Lane:    session.Lane(),
			Payload: encoded,
		})
		if err == nil {
			return
		}

		if !shouldRetireDuplexSession(err, inet.Frame{}) {
			x.fanOutTellFailureShared(params, mapDuplexErr(err))
			return
		}

		// The session died under the write: the frame never entered the writer
		// queue. Retire the lane and retry this same message on the next
		// iteration, which dials a fresh lane.
		x.retireLane(key, session)
		lastErr = mapDuplexErr(err)
	}

	x.fanOutTellFailureShared(params, lastErr)
}

// deliverAdmittedTellLegacy hands an admitted tell to the legacy path after
// the peer proved legacy mid-pump: the coalescer when enabled (it owns
// legacy-tell failure fan-out), otherwise one unary send whose failure fans
// out here. The pump owns params, so the message can be handed off directly.
func (x *peer) deliverAdmittedTellLegacy(params tellParams) {
	if x.client.coalescing.enabled() {
		if c := x.client.getCoalescer(x.host, x.port); c != nil {
			// Coalescer submit only needs a cancelable ctx for queue wait;
			// flush uses its own transport deadline.
			if err := c.submit(context.Background(), tellRemoteMessage(params)); err != nil {
				x.fanOutTellFailureShared(params, err)
			}
			return
		}
	}

	ctx, cancel := x.dialBoundContext()
	err := x.client.sendTellLegacy(ctx, x.host, x.port, params)
	cancel()
	if err != nil {
		x.fanOutTellFailureShared(params, err)
	}
}

// liveLane returns the current session for key when it is open, without
// consulting the admit pump. Used by the pump delivery path so a live
// session does not pay for a dial-timeout timer.
func (x *peer) liveLane(key laneKey) inet.DuplexSession {
	x.mu.Lock()
	defer x.mu.Unlock()

	session := x.laneLocked(key)
	if session != nil && !session.IsClosed() {
		return session
	}
	return nil
}

// dialBoundContext bounds a pump-side ensureLane / legacy dial. TCP dial
// timeout also applies inside OpenDuplex; this caps waits on concurrent
// dialers and black-hole peers.
func (x *peer) dialBoundContext() (context.Context, context.CancelFunc) {
	if d := x.client.dialTimeout; d > 0 {
		return context.WithTimeout(context.Background(), d)
	}
	return context.WithCancel(context.Background())
}

// fanOutTellFailure reports one undeliverable fire-and-forget tell through
// the shared [TellFailureHandler]. The payload is copied because the
// synchronous tell path may return the caller's buffer to the payload pool
// immediately after this returns, while the actor-system handler queues the
// message asynchronously.
func (x *peer) fanOutTellFailure(params tellParams, cause error) {
	x.dispatchTellFailure(tellRemoteMessageOwned(params), cause)
}

// fanOutTellFailureShared is the pump-side fan-out: params buffers are already
// owned by the admit path and are not recycled, so the RemoteMessage can
// share the payload slice (the async handler retains the message).
func (x *peer) fanOutTellFailureShared(params tellParams, cause error) {
	x.dispatchTellFailure(tellRemoteMessage(params), cause)
}

// dispatchTellFailure invokes the configured tell-failure handler, if any.
func (x *peer) dispatchTellFailure(msg *internalpb.RemoteMessage, cause error) {
	handler := x.client.tellFailureHandler
	if handler == nil {
		return
	}

	handler(x.addr, []*internalpb.RemoteMessage{msg}, cause)
}

// tellSendPlan resolves, under a single peer.mu acquisition, everything the
// duplex tell fast path needs: whether the peer currently demands the legacy
// path, the sticky route for receiver, and a live session to write on when the
// FIFO fence permits the synchronous fast path. A nil session with preferLegacy
// false means the caller must admit the tell to the lane pump.
func (x *peer) tellSendPlan(receiver string) (entry pathEntry, cached bool, session inet.DuplexSession, preferLegacy bool) {
	if x.client.pinRequiresLegacy() {
		return pathEntry{}, false, nil, true
	}

	x.mu.Lock()
	defer x.mu.Unlock()

	if x.cache.isLegacy() && !x.cache.legacyExpired(time.Now()) {
		return pathEntry{}, false, nil, true
	}

	entry, cached = x.routeLocked(receiver)
	session = x.directTellSessionLocked(entry.lane)
	return entry, cached, session, false
}

// directTellSessionLocked returns a live session for key only when no admitted
// tell is queued or in flight on the lane pump, so the synchronous fast path
// can never overtake earlier admitted tells (per sender-target pair FIFO). Nil
// means the caller must admit instead. Caller holds x.mu.
func (x *peer) directTellSessionLocked(key laneKey) inet.DuplexSession {
	if x.closed {
		return nil
	}

	if pump, ok := x.pumps[key]; ok && pump.pending.Load() > 0 {
		return nil
	}

	if session := x.laneLocked(key); session != nil && !session.IsClosed() {
		return session
	}

	return nil
}

// isLegacyHandshakeFailure reports whether err indicates the peer closed or
// reset before HELLO_ACK, which auto mode treats as a legacy-only listener.
// Timeouts are not treated as legacy: they surface to the caller so ask
// deadlines remain enforceable against a silent peer. Connect-phase failures
// (nobody listening) prove neither protocol and are not legacy: fire-and-forget
// tells absorb them through duplex admission and the transport-failure
// fan-out, never through the coalescer.
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
// [inet.ErrDuplexBackpressure] becomes [gerrors.ErrRemoteSendBackpressure] and
// [inet.ErrMessageTooLarge] becomes [gerrors.ErrRemoteMessageTooLarge], while
// preserving the original cause via [errors.Join].
func mapDuplexErr(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, inet.ErrDuplexBackpressure) {
		return errors.Join(gerrors.ErrRemoteSendBackpressure, err)
	}

	if errors.Is(err, inet.ErrMessageTooLarge) {
		return errors.Join(gerrors.ErrRemoteMessageTooLarge, err)
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

	if errors.Is(err, inet.ErrMessageTooLarge) {
		return false
	}

	return true
}
