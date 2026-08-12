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
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/remote"
)

func startRemotingServer(t *testing.T, opts ...inet.RemotingServerOption) *inet.RemotingServer {
	t.Helper()

	ps, err := inet.NewRemotingServer("127.0.0.1:0", opts...)
	require.NoError(t, err)
	require.NoError(t, ps.Listen())

	done := make(chan error, 1)
	go func() { done <- ps.Serve() }()
	pause.For(100 * time.Millisecond)

	t.Cleanup(func() {
		require.NoError(t, ps.Shutdown(time.Second))
		<-done
	})

	return ps
}

func serverHostPort(t *testing.T, ps *inet.RemotingServer) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(ps.ListenAddr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	return host, port
}

func TestProtocolCacheAutoFallbackLegacy(t *testing.T) {
	ps := startRemotingServer(t,
		inet.WithRemotingServerAcceptProtocol(inet.AcceptProtocolLegacy),
		inet.WithProtoHandler("internalpb.RemoteLookupRequest", legacyLookupHandler(t)),
	)
	host, port := serverHostPort(t, ps)

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientProtocolPin(remote.ProtocolPinAuto),
	)
	defer r.Close()

	c := r.(*client)
	p := c.peerFor(host, port)
	assert.Equal(t, peerProtocolUnknown, p.cachedProtocol())

	addr, err := r.RemoteLookup(context.Background(), host, port, "actor")
	require.NoError(t, err)
	assert.Equal(t, "actor", addr.Name())

	assert.Equal(t, peerProtocolLegacy, p.cachedProtocol())
	assert.Nil(t, p.control)
}

func legacyLookupHandler(t *testing.T) inet.ProtoHandler {
	t.Helper()
	return func(_ context.Context, _ inet.Connection, msg proto.Message) (proto.Message, error) {
		req := msg.(*internalpb.RemoteLookupRequest)
		return internalpb.RemoteLookupResponse_builder{
			Address: address.New(req.GetName(), "test", req.GetHost(), int(req.GetPort())).String(),
		}.Build(), nil
	}
}

func TestProtocolPinLegacyOnDuplexCapableServer(t *testing.T) {
	ps := startRemotingServer(t,
		inet.WithProtoHandler("internalpb.RemoteLookupRequest", legacyLookupHandler(t)),
	)
	host, port := serverHostPort(t, ps)

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientProtocolPin(remote.ProtocolPinLegacy),
	)
	defer r.Close()

	c := r.(*client)
	p := c.peerFor(host, port)

	addr, err := r.RemoteLookup(context.Background(), host, port, "actor")
	require.NoError(t, err)
	assert.Equal(t, "actor", addr.Name())
	assert.Equal(t, peerProtocolUnknown, p.cachedProtocol())
	assert.Nil(t, p.control)
}

func TestProtocolPinDuplexControlRPC(t *testing.T) {
	ps := startRemotingServer(t,
		inet.WithProtoHandler("internalpb.RemoteLookupRequest", legacyLookupHandler(t)),
	)
	host, port := serverHostPort(t, ps)

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientProtocolPin(remote.ProtocolPinDuplex),
	)
	defer r.Close()

	c := r.(*client)
	p := c.peerFor(host, port)

	addr, err := r.RemoteLookup(context.Background(), host, port, "actor")
	require.NoError(t, err)
	assert.Equal(t, "actor", addr.Name())
	assert.Equal(t, peerProtocolDuplex, p.cachedProtocol())
	assert.NotNil(t, p.control)
}

func TestEnsureLaneDialsOrdinaryAndLarge(t *testing.T) {
	ps := startRemotingServer(t)
	host, port := serverHostPort(t, ps)

	c := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientProtocolPin(remote.ProtocolPinDuplex),
		WithClientOrdinaryLanes(4),
	).(*client)
	defer c.Close()

	p := c.peerFor(host, port)
	ordinary, err := p.ensureLane(context.Background(), internalpb.LaneRole_LANE_ROLE_ORDINARY, 3)
	require.NoError(t, err)
	assert.Equal(t, byte(4), ordinary.Lane())

	large, err := p.ensureLane(context.Background(), internalpb.LaneRole_LANE_ROLE_LARGE, 0)
	require.NoError(t, err)
	assert.NotEqual(t, ordinary.Lane(), large.Lane())
	assert.Same(t, ordinary, p.ordinary[3])
	assert.Same(t, large, p.large)
}

// TestLanesStayLazy is the regression gate for the goroutine budget plan:
// lanes dial on first use only. A peer that has served only a control RPC
// must hold no ordinary or large session, and ordinary traffic afterwards
// must not dial the large lane.
func TestLanesStayLazy(t *testing.T) {
	ps := startRemotingServer(t,
		inet.WithProtoHandler("internalpb.RemoteLookupRequest", legacyLookupHandler(t)),
	)
	host, port := serverHostPort(t, ps)

	c := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientProtocolPin(remote.ProtocolPinDuplex),
	).(*client)
	defer c.Close()

	p := c.peerFor(host, port)

	_, err := c.RemoteLookup(context.Background(), host, port, "actor")
	require.NoError(t, err)

	countLanes := func() (control, large bool, ordinary int) {
		p.mu.Lock()
		defer p.mu.Unlock()

		for _, session := range p.ordinary {
			if session != nil {
				ordinary++
			}
		}

		return p.control != nil, p.large != nil, ordinary
	}

	control, large, ordinary := countLanes()
	assert.True(t, control, "control RPC must dial the control lane")
	assert.False(t, large, "control RPC must not dial the large lane")
	assert.Zero(t, ordinary, "control RPC must not dial ordinary lanes")

	from := address.New("sender", "testSys", host, port)
	to := address.New("ghost", "remote", host, port)
	require.NoError(t, c.RemoteTell(context.Background(), from, to, durationpb.New(time.Second)))

	require.Eventually(t, func() bool {
		_, _, ordinary := countLanes()
		return ordinary == 1
	}, 3*time.Second, 20*time.Millisecond, "an ordinary tell must dial exactly one ordinary lane")

	_, large, _ = countLanes()
	assert.False(t, large, "ordinary traffic must not dial the large lane")
}

func TestHandleLaneFrameConnectionErrorRetiresLane(t *testing.T) {
	c := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientProtocolPin(remote.ProtocolPinDuplex),
	).(*client)
	defer c.Close()

	p := c.peerFor("127.0.0.1", 65000)
	key := laneKey{role: internalpb.LaneRole_LANE_ROLE_CONTROL}
	session := &stubDuplexSession{id: 1}

	p.mu.Lock()
	p.setLaneLocked(key, session)
	p.cache.set(peerProtocolDuplex)
	p.mu.Unlock()

	// Correlated ERROR and ordinary frames must not retire the lane.
	p.handleLaneFrame(key, session, inet.Frame{Type: inet.FrameTypeError, Correlation: 7})
	p.handleLaneFrame(key, session, inet.Frame{Type: inet.FrameTypeData})

	p.mu.Lock()
	stillCached := p.laneLocked(key) == session
	p.mu.Unlock()
	assert.True(t, stillCached)
	assert.Equal(t, peerProtocolDuplex, p.cachedProtocol())

	// A connection-scoped ERROR (correlation zero) retires the cached lane
	// and completes the close on a transient goroutine.
	p.handleLaneFrame(key, session, inet.Frame{Type: inet.FrameTypeError})

	assert.Eventually(t, func() bool { return session.closeCount.Load() > 0 }, time.Second, 10*time.Millisecond)

	p.mu.Lock()
	cleared := p.laneLocked(key) == nil
	p.mu.Unlock()
	assert.True(t, cleared)
	assert.Equal(t, peerProtocolUnknown, p.cachedProtocol())
}

// TestRetireLaneReleasesMutexBeforeClose pins the lock-ordering fix:
// retireLane must not hold peer.mu while Close is in flight. Close waits for
// the session's read loop to exit, and that read loop's closed handler
// retires the lane through peer.mu; holding the mutex across Close deadlocks a
// send-path retire against the very read loop it is tearing down (the CI hang
// in TestTopicActor). The stub's Close blocks until a separate goroutine can
// take peer.mu, so a regression (close under the lock) makes that acquisition
// never complete and this test times out.
func TestRetireLaneReleasesMutexBeforeClose(t *testing.T) {
	c := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientProtocolPin(remote.ProtocolPinDuplex),
	).(*client)
	defer c.Close()

	p := c.peerFor("127.0.0.1", 65001)
	key := laneKey{role: internalpb.LaneRole_LANE_ROLE_CONTROL}

	locked := make(chan struct{})
	proceed := make(chan struct{})

	var once sync.Once
	unblock := func() { once.Do(func() { close(proceed) }) }

	session := &stubDuplexSession{id: 1}
	session.closeHook = func() {
		// Stands in for the read loop's closed handler, which retires
		// through peer.mu while Close waits for it: take the mutex and
		// inspect the cached lane exactly as retireLaneAsync does. It can
		// only run if retireLane released the mutex before calling Close.
		go func() {
			p.mu.Lock()
			_ = p.laneLocked(key)
			p.mu.Unlock()
			close(locked)
		}()

		<-proceed
	}

	p.mu.Lock()
	p.setLaneLocked(key, session)
	p.cache.set(peerProtocolDuplex)
	p.mu.Unlock()

	done := make(chan struct{})
	go func() {
		p.retireLane(key, session)
		close(done)
	}()

	select {
	case <-locked:
		unblock()
	case <-time.After(2 * time.Second):
		// Let the buggy retire finish so cleanup does not hang, then fail.
		unblock()
		t.Fatal("retireLane held peer.mu while Close was in flight: the read loop's closed handler could not retire through peer.mu")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("retireLane did not complete after Close returned")
	}

	p.mu.Lock()
	cleared := p.laneLocked(key) == nil
	p.mu.Unlock()
	assert.True(t, cleared, "retireLane must clear the cached lane")
	assert.EqualValues(t, 1, session.closeCount.Load(), "retireLane must close the session exactly once")
}

// TestEnsureLaneReleasesMutexBeforeClosingStaleSession pins the dial-path twin
// of the retireLane lock-ordering rule: ensureLane must not hold peer.mu while
// closing a cached session it found already closed. Close waits for the
// session's read loop to exit, and that read loop's closed handler retires the
// lane through peer.mu (see retireLaneAsync); closing under the lock deadlocks
// the dial path against the read loop it is waiting on, and every later send
// to the peer wedges behind peer.mu. The stub's Close blocks until a separate
// goroutine can take peer.mu, so a regression (close under the lock) makes
// that acquisition never complete and this test times out.
func TestEnsureLaneReleasesMutexBeforeClosingStaleSession(t *testing.T) {
	c := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientProtocolPin(remote.ProtocolPinDuplex),
	).(*client)
	defer c.Close()

	p := c.peerFor("127.0.0.1", 65001)
	key := laneKey{role: internalpb.LaneRole_LANE_ROLE_CONTROL}

	locked := make(chan struct{})
	proceed := make(chan struct{})

	var once sync.Once
	unblock := func() { once.Do(func() { close(proceed) }) }

	session := &stubDuplexSession{id: 1, stale: true}
	session.closeHook = func() {
		// Stands in for the read loop's closed handler, which retires
		// through peer.mu while Close waits for it: take the mutex and
		// inspect the cached lane exactly as retireLaneAsync does. It can
		// only run if ensureLane released the mutex before calling Close.
		go func() {
			p.mu.Lock()
			_ = p.laneLocked(key)
			p.mu.Unlock()
			close(locked)
		}()

		<-proceed
	}

	p.mu.Lock()
	p.setLaneLocked(key, session)
	p.cache.set(peerProtocolDuplex)
	p.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		// The redial after the stale session is cleared fails fast: nothing
		// listens on the port. Only the lock ordering matters here.
		_, _ = p.ensureLane(ctx, key.role, key.index)
		close(done)
	}()

	select {
	case <-locked:
		unblock()
	case <-time.After(2 * time.Second):
		// Let the buggy dial finish so cleanup does not hang, then fail.
		unblock()
		t.Fatal("ensureLane held peer.mu while Close was in flight: the read loop's closed handler could not retire through peer.mu")
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ensureLane did not return after the stale session was closed")
	}

	assert.EqualValues(t, 1, session.closeCount.Load(), "ensureLane must close the stale session exactly once")

	p.mu.Lock()
	replaced := p.laneLocked(key) != session
	p.mu.Unlock()
	assert.True(t, replaced, "ensureLane must clear the stale cached lane")
}

func TestLaneDeathRetiresCachedSession(t *testing.T) {
	ps := startRemotingServer(t)
	host, port := serverHostPort(t, ps)

	c := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientProtocolPin(remote.ProtocolPinDuplex),
	).(*client)
	defer c.Close()

	p := c.peerFor(host, port)
	key := laneKey{role: internalpb.LaneRole_LANE_ROLE_CONTROL}
	session, err := p.ensureLane(context.Background(), key.role, key.index)
	require.NoError(t, err)

	p.mu.Lock()
	cached := p.laneLocked(key) == session
	p.mu.Unlock()
	require.True(t, cached)

	// Killing the session fires the read-loop closed hook, which must evict
	// the cached lane without any caller touching the peer.
	require.NoError(t, session.Close())

	assert.Eventually(t, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.laneLocked(key) == nil
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, peerProtocolUnknown, p.cachedProtocol())
}

func TestClosePeerClosesEveryLane(t *testing.T) {
	ps := startRemotingServer(t)
	host, port := serverHostPort(t, ps)

	c := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientProtocolPin(remote.ProtocolPinDuplex),
		WithClientOrdinaryLanes(2),
	).(*client)
	defer c.Close()

	p := c.peerFor(host, port)
	control, err := p.ensureLane(context.Background(), internalpb.LaneRole_LANE_ROLE_CONTROL, 0)
	require.NoError(t, err)
	ordinary, err := p.ensureLane(context.Background(), internalpb.LaneRole_LANE_ROLE_ORDINARY, 0)
	require.NoError(t, err)
	large, err := p.ensureLane(context.Background(), internalpb.LaneRole_LANE_ROLE_LARGE, 0)
	require.NoError(t, err)

	c.ClosePeer(host, port)
	assert.True(t, control.IsClosed())
	assert.True(t, ordinary.IsClosed())
	assert.True(t, large.IsClosed())
	_, ok := c.peers.Get(net.JoinHostPort(host, strconv.Itoa(port)))
	assert.False(t, ok)
}

func TestClosePeerCancelsInFlightDial(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		accepted <- conn
	}()

	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	c := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientProtocolPin(remote.ProtocolPinDuplex),
		WithClientDialTimeout(5*time.Second),
	).(*client)
	defer c.Close()

	p := c.peerFor(host, port)
	errCh := make(chan error, 1)
	go func() {
		_, dialErr := p.ensureLane(context.Background(), internalpb.LaneRole_LANE_ROLE_CONTROL, 0)
		errCh <- dialErr
	}()

	select {
	case conn := <-accepted:
		t.Cleanup(func() { _ = conn.Close() })
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for dial accept")
	}

	c.ClosePeer(host, port)

	select {
	case dialErr := <-errCh:
		require.Error(t, dialErr)
		assert.True(t,
			errors.Is(dialErr, context.Canceled) ||
				errors.Is(dialErr, errLaneClosedDuringDial) ||
				errors.Is(dialErr, io.EOF) ||
				errors.Is(dialErr, net.ErrClosed) ||
				strings.Contains(dialErr.Error(), "use of closed network connection"),
			"unexpected dial error: %v", dialErr,
		)
	case <-time.After(2 * time.Second):
		t.Fatal("ClosePeer did not unblock in-flight dial")
	}
}

func TestClosePeerDoesNotRedialForWaiters(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		accepted <- conn
	}()

	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	c := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientProtocolPin(remote.ProtocolPinDuplex),
		WithClientDialTimeout(5*time.Second),
	).(*client)
	defer c.Close()

	p := c.peerFor(host, port)
	dialerErr := make(chan error, 1)
	waiterErr := make(chan error, 1)

	go func() {
		_, err := p.ensureLane(context.Background(), internalpb.LaneRole_LANE_ROLE_CONTROL, 0)
		dialerErr <- err
	}()

	select {
	case conn := <-accepted:
		t.Cleanup(func() { _ = conn.Close() })
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for dial accept")
	}

	// Wait until the dialer has published the single-flight marker.
	require.Eventually(t, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		_, ok := p.dialing[laneKey{role: internalpb.LaneRole_LANE_ROLE_CONTROL}]
		return ok
	}, time.Second, 5*time.Millisecond)

	go func() {
		_, err := p.ensureLane(context.Background(), internalpb.LaneRole_LANE_ROLE_CONTROL, 0)
		waiterErr <- err
	}()

	c.ClosePeer(host, port)

	select {
	case err := <-dialerErr:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("dialer was not unblocked")
	}

	select {
	case err := <-waiterErr:
		require.ErrorIs(t, err, errLaneClosedDuringDial)
	case <-time.After(2 * time.Second):
		t.Fatal("waiter redialed or hung after ClosePeer")
	}

	assert.Nil(t, p.control)
	assert.Empty(t, p.dialing)
}

func TestSwitchoverDrainOrder(t *testing.T) {
	ps := startRemotingServer(t,
		inet.WithProtoHandler("internalpb.RemoteLookupRequest", legacyLookupHandler(t)),
	)
	host, port := serverHostPort(t, ps)

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientProtocolPin(remote.ProtocolPinAuto),
	)
	defer r.Close()

	c := r.(*client)
	p := c.peerFor(host, port)
	p.cache.set(peerProtocolLegacy)
	// Age the legacy mark so ensureLane re-probes and drains first.
	p.cache.markedAt = time.Now().Add(-peerLegacyReprobeInterval - time.Second)

	order := make(chan string, 2)
	p.beginLegacySend()
	go func() {
		time.Sleep(80 * time.Millisecond)
		order <- "legacy-done"
		p.endLegacySend()
	}()

	started := make(chan struct{})
	go func() {
		close(started)
		_, err := p.ensureLane(context.Background(), internalpb.LaneRole_LANE_ROLE_CONTROL, 0)
		require.NoError(t, err)
		order <- "duplex-ready"
	}()
	<-started

	require.Equal(t, "legacy-done", <-order)
	require.Equal(t, "duplex-ready", <-order)
	assert.Equal(t, peerProtocolDuplex, p.cachedProtocol())
}

func TestBatchAskOrderDuplex(t *testing.T) {
	protoSer := remote.NewProtoSerializer()
	duplexAsk := func(_ context.Context, env inet.DataEnvelope) (inet.ReplyEnvelope, error) {
		msg, err := protoSer.Deserialize(env.Payload)
		require.NoError(t, err)
		req := msg.(*durationpb.Duration)
		payload, err := proto.Marshal(req)
		require.NoError(t, err)
		return inet.ReplyEnvelope{
			TypeName:     string(proto.MessageName(req)),
			SerializerID: inet.SerializerIDInternalProto,
			Payload:      payload,
		}, nil
	}

	ps := startRemotingServer(t, inet.WithRemotingServerDuplexAskHandler(duplexAsk))
	host, port := serverHostPort(t, ps)

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientProtocolPin(remote.ProtocolPinDuplex),
	)
	defer r.Close()

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)

	responses, err := r.RemoteBatchAsk(
		context.Background(),
		from,
		to,
		[]any{durationpb.New(time.Second), durationpb.New(2 * time.Second), durationpb.New(3 * time.Second)},
		time.Second,
	)
	require.NoError(t, err)
	require.Len(t, responses, 3)

	for i, resp := range responses {
		d, ok := resp.(*durationpb.Duration)
		require.True(t, ok)
		assert.Equal(t, int64(i+1), d.GetSeconds())
	}
}

func TestDuplexTellAskRoundTrip(t *testing.T) {
	duplexAsk := func(_ context.Context, env inet.DataEnvelope) (inet.ReplyEnvelope, error) {
		reply := durationpb.New(5 * time.Second)
		payload, err := proto.Marshal(reply)
		require.NoError(t, err)
		return inet.ReplyEnvelope{
			TypeName:     string(proto.MessageName(reply)),
			SerializerID: inet.SerializerIDInternalProto,
			Payload:      payload,
		}, nil
	}

	tellCount := atomic.Int32{}
	duplexTell := func(_ context.Context, env inet.DataEnvelope) {
		tellCount.Add(1)
	}

	ps := startRemotingServer(t,
		inet.WithRemotingServerDuplexAskHandler(duplexAsk),
		inet.WithRemotingServerDuplexTellHandler(duplexTell),
	)
	host, port := serverHostPort(t, ps)

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientProtocolPin(remote.ProtocolPinDuplex),
	)
	defer r.Close()

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)

	require.NoError(t, r.RemoteTell(context.Background(), from, to, durationpb.New(time.Second)))
	pause.For(50 * time.Millisecond)
	assert.Equal(t, int32(1), tellCount.Load())

	resp, err := r.RemoteAsk(context.Background(), from, to, durationpb.New(time.Second), time.Second)
	require.NoError(t, err)
	d, ok := resp.(*durationpb.Duration)
	require.True(t, ok)
	assert.Equal(t, int64(5), d.GetSeconds())
}

func TestMapDuplexBackpressure(t *testing.T) {
	err := mapDuplexErr(inet.ErrDuplexBackpressure)
	require.ErrorIs(t, err, gerrors.ErrRemoteSendBackpressure)
}

func TestMapDuplexMessageTooLarge(t *testing.T) {
	err := mapDuplexErr(inet.ErrMessageTooLarge)
	require.ErrorIs(t, err, gerrors.ErrRemoteMessageTooLarge)
}

func TestShouldRetireDuplexSession(t *testing.T) {
	assert.False(t, shouldRetireDuplexSession(nil, inet.Frame{}))
	assert.False(t, shouldRetireDuplexSession(context.Canceled, inet.Frame{}))
	assert.False(t, shouldRetireDuplexSession(context.DeadlineExceeded, inet.Frame{}))
	assert.False(t, shouldRetireDuplexSession(inet.ErrDuplexBackpressure, inet.Frame{}))
	assert.False(t, shouldRetireDuplexSession(inet.ErrMessageTooLarge, inet.Frame{}))
	assert.False(t, shouldRetireDuplexSession(errors.New("handler"), inet.Frame{
		Type:        inet.FrameTypeError,
		Correlation: 7,
	}))
	assert.True(t, shouldRetireDuplexSession(inet.ErrDuplexClosed, inet.Frame{}))
}

func TestIsLegacyHandshakeFailure(t *testing.T) {
	assert.True(t, isLegacyHandshakeFailure(io.EOF))
	assert.True(t, isLegacyHandshakeFailure(io.ErrUnexpectedEOF))
	assert.False(t, isLegacyHandshakeFailure(nil))
	assert.False(t, isLegacyHandshakeFailure(context.DeadlineExceeded))

	// Connect-phase failures prove neither protocol and stay non-legacy:
	// duplex admission owns fire-and-forget delivery to unreachable peers.
	assert.False(t, isLegacyHandshakeFailure(&net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}))
	assert.False(t, isLegacyHandshakeFailure(&net.OpError{Op: "dial", Err: syscall.EINVAL}))
	assert.True(t, isLegacyHandshakeFailure(&net.OpError{Op: "read", Err: syscall.ECONNRESET}))
}

func TestCompressionCodec(t *testing.T) {
	assert.Equal(t, internalpb.CompressionCodec_COMPRESSION_CODEC_NONE, compressionCodec(remote.NoCompression))
	assert.Equal(t, internalpb.CompressionCodec_COMPRESSION_CODEC_GZIP, compressionCodec(remote.GzipCompression))
	assert.Equal(t, internalpb.CompressionCodec_COMPRESSION_CODEC_ZSTD, compressionCodec(remote.ZstdCompression))
	assert.Equal(t, internalpb.CompressionCodec_COMPRESSION_CODEC_BROTLI, compressionCodec(remote.BrotliCompression))
}

func TestPeerRouteCacheBounded(t *testing.T) {
	c := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientOrdinaryLanes(4),
	).(*client)
	defer c.Close()

	p := c.peerFor("127.0.0.1", 65500)
	for i := 0; i < routeCacheLimit+5; i++ {
		_, _ = p.route("goakt://system@127.0.0.1:65500/actor-" + strconv.Itoa(i))
	}

	p.mu.Lock()
	cached := len(p.routes)
	p.mu.Unlock()
	assert.Equal(t, routeCacheLimit, cached)

	// Receivers beyond the cap are computed per send and stay deterministic.
	overflow := "goakt://system@127.0.0.1:65500/actor-overflow"
	first, firstCached := p.route(overflow)
	second, secondCached := p.route(overflow)
	assert.Equal(t, first.lane, second.lane)
	assert.False(t, firstCached)
	assert.False(t, secondCached)
}

func TestPeerRememberPathRefSessionIdentity(t *testing.T) {
	c := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientOrdinaryLanes(1),
	).(*client)
	defer c.Close()

	p := c.peerFor("127.0.0.1", 65501)
	receiver := "goakt://system@127.0.0.1:65501/actor"
	entry, cached := p.route(receiver)
	require.True(t, cached)
	assert.Equal(t, uint64(0), entry.pathID)
	assert.Nil(t, entry.session)

	sessA := &stubDuplexSession{id: 1}
	p.rememberPathRef(receiver, 7, sessA)

	entry, cached = p.route(receiver)
	require.True(t, cached)
	assert.Equal(t, uint64(7), entry.pathID)
	assert.Same(t, sessA, entry.session)

	sessB := &stubDuplexSession{id: 2}
	p.rememberPathRef(receiver, 3, sessB)

	entry, _ = p.route(receiver)
	assert.Equal(t, uint64(3), entry.pathID)
	assert.Same(t, sessB, entry.session)

	p.closeAllLanes()
	entry, cached = p.route(receiver)
	require.True(t, cached)
	assert.Equal(t, uint64(0), entry.pathID)
	assert.Nil(t, entry.session)
}

func TestEncodeUserDataEnvelopeSkipsRememberBelowRevisionThree(t *testing.T) {
	c := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientOrdinaryLanes(1),
	).(*client)
	defer c.Close()

	p := c.peerFor("127.0.0.1", 65502)
	receiver := "goakt://system@127.0.0.1:65502/actor"
	entry, cached := p.route(receiver)
	require.True(t, cached)

	session := &stubDuplexSession{id: 1, revision: inet.CapabilityRevisionChunking}
	_, err := encodeUserDataEnvelope(p, session, entry, cached, inet.DataEnvelope{
		Sender:       "goakt://system@127.0.0.1:65502/sender",
		Receiver:     receiver,
		TypeName:     "t",
		SerializerID: inet.SerializerIDInternalProto,
		Payload:      []byte("x"),
	})
	require.NoError(t, err)

	// Revision < 3 must not rewrite the route cache on every send.
	entry, _ = p.route(receiver)
	assert.Equal(t, uint64(0), entry.pathID)
	assert.Nil(t, entry.session)

	_, err = encodeUserDataEnvelope(p, session, entry, true, inet.DataEnvelope{
		Sender:       "goakt://system@127.0.0.1:65502/sender",
		Receiver:     receiver,
		TypeName:     "t",
		SerializerID: inet.SerializerIDInternalProto,
		Payload:      []byte("y"),
	})
	require.NoError(t, err)

	entry, _ = p.route(receiver)
	assert.Equal(t, uint64(0), entry.pathID)
	assert.Nil(t, entry.session)
}

func TestEncodeUserDataEnvelopeRemembersPathIDAtRevisionThree(t *testing.T) {
	c := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientOrdinaryLanes(1),
	).(*client)
	defer c.Close()

	p := c.peerFor("127.0.0.1", 65503)
	receiver := "goakt://system@127.0.0.1:65503/actor"
	entry, cached := p.route(receiver)
	require.True(t, cached)

	session := &stubDuplexSession{
		id:       1,
		revision: inet.CapabilityRevisionTables,
		refs:     map[string]uint64{receiver: 7},
	}
	_, err := encodeUserDataEnvelope(p, session, entry, cached, inet.DataEnvelope{
		Sender:       "goakt://system@127.0.0.1:65503/sender",
		Receiver:     receiver,
		TypeName:     "t",
		SerializerID: inet.SerializerIDInternalProto,
		Payload:      []byte("x"),
	})
	require.NoError(t, err)

	entry, _ = p.route(receiver)
	assert.Equal(t, uint64(7), entry.pathID)
	assert.Same(t, session, entry.session)
}

// stubDuplexSession is an identity-only DuplexSession for route-cache tests.
type stubDuplexSession struct {
	id            int
	revision      uint32
	refs          map[string]uint64
	prepareRefErr error
	tellErr       error
	// tellHook, when set, runs at the start of every Tell. Tests use it to
	// hold a write open so later admissions queue behind an in-flight tell.
	tellHook func()
	// closeCount records Close invocations so retire paths can be asserted.
	closeCount atomic.Int32
	// closeHook, when set, runs inside Close before it returns. Tests use it
	// to observe the caller's lock state while a close is in flight.
	closeHook func()
	// stale, when set, reports the session as already closed so lane-cache
	// paths that probe IsClosed before reuse can be driven.
	stale bool
}

func (x *stubDuplexSession) Tell(context.Context, inet.Frame) error {
	if x.tellHook != nil {
		x.tellHook()
	}

	return x.tellErr
}

func (x *stubDuplexSession) Ask(context.Context, inet.Frame) (inet.Frame, error) {
	return inet.Frame{}, errors.New("stub")
}

func (x *stubDuplexSession) Recv(context.Context) (inet.Frame, error) {
	return inet.Frame{}, errors.New("stub")
}

func (x *stubDuplexSession) IsClosed() bool { return x.stale }

func (x *stubDuplexSession) Lane() byte { return 0 }

func (x *stubDuplexSession) Revision() uint32 {
	if x.revision == 0 {
		return inet.CapabilityRevisionTables
	}

	return x.revision
}

func (x *stubDuplexSession) MaxFrameSize() uint32 { return 0 }

func (x *stubDuplexSession) MaxMessageSize() uint64 { return 0 }

func (x *stubDuplexSession) MaxConcurrentLargeTransfers() uint32 { return 0 }

func (x *stubDuplexSession) ChunkSize() uint32 { return 0 }

func (x *stubDuplexSession) PrepareRef(_ byte, literal string) (uint64, error) {
	if x.prepareRefErr != nil {
		return 0, x.prepareRefErr
	}

	if x.refs == nil {
		return 0, nil
	}

	return x.refs[literal], nil
}

func (x *stubDuplexSession) DecodeReplyEnvelope([]byte, bool) (inet.ReplyEnvelope, error) {
	return inet.ReplyEnvelope{}, errors.New("stub")
}

func (x *stubDuplexSession) ReleasePayload(inet.Frame) {}

func (x *stubDuplexSession) Close() error {
	x.closeCount.Add(1)

	if x.closeHook != nil {
		x.closeHook()
	}

	return nil
}

// TestPeerCloseRacingRemoteTells stresses the teardown path that produced the
// CI deadlock: many concurrent duplex RemoteTells to a peer while that peer is
// repeatedly closed out from under them. It exercises peer close racing
// in-flight sends, lane death racing redial, and the duplexConn.Close versus
// read-loop closed-handler retire that the retireLane lock-ordering fix
// addressed. Every send must complete (deliver or return a clean error) and
// every close must return: nothing may hang, and -race must stay quiet.
func TestPeerCloseRacingRemoteTells(t *testing.T) {
	_, host, port, stop := startCoalescingServer(t)
	defer stop()

	// No coalescing: RemoteTell drives the duplex lane synchronously, so a send
	// is in flight on the lane exactly when ClosePeer tears it down.
	r := NewClient(
		WithClientProtocolPin(remote.ProtocolPinDuplex),
		WithClientCompression(remote.NoCompression),
	).(*client)
	defer r.Close()

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)

	const senders = 8
	deadline := time.Now().Add(2 * time.Second)

	var wg sync.WaitGroup
	wg.Add(senders + 1)

	for range senders {
		go func() {
			defer wg.Done()

			for time.Now().Before(deadline) {
				callCtx, cancel := context.WithTimeout(context.Background(), time.Second)
				// A send that races a close may fail; that is fine. A send that
				// hangs is not, and the outer guard catches it.
				_ = r.RemoteTell(callCtx, from, to, durationpb.New(time.Millisecond))
				cancel()
			}
		}()
	}

	go func() {
		defer wg.Done()

		for time.Now().Before(deadline) {
			r.ClosePeer(host, port)
			pause.For(2 * time.Millisecond)
		}
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("peer close racing remote tells hung: a teardown path failed to make progress")
	}
}

// The teardown fault-injection campaign. Each test drives the duplex teardown
// surface while a specific transport fault is active, racing many concurrent
// sends against repeated ClosePeer. The invariant is uniform: a send that
// races a fault or a teardown may fail, but no send and no close may hang, and
// -race must stay quiet. These complement TestPeerCloseRacingRemoteTells, which
// races close against a healthy server; here the peer is faulty.

// runTeardownRace hammers send from several goroutines while teardown races it
// for a fixed window, then fails if the whole race does not drain within a
// generous outer guard. send receives a per-call context bounded by
// callTimeout; a send that loses the race may return an error, but it must
// never block past its context. teardown runs repeatedly in its own goroutine
// (typically ClosePeer) so a close is always in flight against some send.
func runTeardownRace(t *testing.T, callTimeout time.Duration, send func(ctx context.Context), teardown func()) {
	t.Helper()

	const (
		senders = 8
		window  = 2 * time.Second
		guard   = 30 * time.Second
	)

	deadline := time.Now().Add(window)

	var wg sync.WaitGroup
	wg.Add(senders + 1)

	for range senders {
		go func() {
			defer wg.Done()

			for time.Now().Before(deadline) {
				ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
				send(ctx)
				cancel()
			}
		}()
	}

	go func() {
		defer wg.Done()

		for time.Now().Before(deadline) {
			teardown()
			pause.For(2 * time.Millisecond)
		}
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(guard):
		t.Fatal("teardown race hung: a send or close path failed to make progress")
	}
}

// faultMode selects how startFaultListener mistreats each accepted connection
// so a dialing duplex client exercises a specific dial/handshake failure path.
type faultMode int

const (
	// faultSilent accepts the connection and never speaks HELLO, so the dial
	// stalls in the handshake until the caller's deadline elapses or ClosePeer
	// cancels the dial. It exercises the OpenDuplex cancel-close path (the
	// connMu-guarded region) with the handshake frozen mid-exchange.
	faultSilent faultMode = iota

	// faultAcceptClose accepts then immediately closes, so the handshake read
	// fails with EOF. It exercises the dial-failure classification and backoff
	// path racing ClosePeer.
	faultAcceptClose
)

// startFaultListener starts a bare TCP listener that mistreats every accepted
// connection per mode and returns its host, port, and a stop func. It never
// speaks the framed protocol; it exists only to drive the dial and handshake
// failure paths a well-behaved RemotingServer would never produce on demand.
func startFaultListener(t *testing.T, mode faultMode) (string, int, func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	var mu sync.Mutex
	var held []net.Conn

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}

			switch mode {
			case faultAcceptClose:
				_ = conn.Close()

			case faultSilent:
				// Retain the connection so the peer's handshake read blocks
				// rather than seeing EOF; stop() closes them to unblock any
				// dial still parked at teardown.
				mu.Lock()
				held = append(held, conn)
				mu.Unlock()
			}
		}
	}()

	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)

	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	stop := func() {
		_ = ln.Close()

		mu.Lock()
		for _, c := range held {
			_ = c.Close()
		}

		held = nil
		mu.Unlock()
	}

	return host, port, stop
}

// newDuplexStressClient builds a duplex-pinned, uncompressed client with
// coalescing off so every RemoteTell drives the lane synchronously: a send is
// in flight on the wire exactly when ClosePeer tears the lane down.
func newDuplexStressClient(t *testing.T, opts ...ClientOption) *client {
	t.Helper()

	base := []ClientOption{
		WithClientProtocolPin(remote.ProtocolPinDuplex),
		WithClientCompression(remote.NoCompression),
	}

	return NewClient(append(base, opts...)...).(*client)
}

// TestDialFailureRacingClose races sends against ClosePeer when every dial is
// refused (the listener is bound then closed, so connects return ECONNREFUSED).
// It exercises recordDialFailure, per-lane backoff, and the single-flight dial
// marker teardown while ClosePeer repeatedly resets the peer.
func TestDialFailureRacingClose(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)

	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	// Close the listener so every subsequent connect is refused.
	require.NoError(t, ln.Close())

	r := newDuplexStressClient(t)
	defer r.Close()

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)

	runTeardownRace(t, time.Second, func(ctx context.Context) {
		_ = r.RemoteTell(ctx, from, to, durationpb.New(time.Millisecond))
	}, func() {
		r.ClosePeer(host, port)
	})
}

// TestHandshakeStallRacingClose races sends against ClosePeer when the peer
// accepts the connection but never completes HELLO, so each dial parks in the
// handshake. It exercises the OpenDuplex cancel-close path (dialCancel firing
// the AfterFunc that closes the framed conn) against the compression-wrapper
// swap, the connMu-guarded region that once carried a data race, with the
// handshake frozen rather than racing the swap directly.
func TestHandshakeStallRacingClose(t *testing.T) {
	host, port, stop := startFaultListener(t, faultSilent)
	defer stop()

	r := newDuplexStressClient(t)
	defer r.Close()

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)

	// A short per-call timeout keeps the parked handshakes churning so many
	// dials are in flight when ClosePeer cancels them.
	runTeardownRace(t, 200*time.Millisecond, func(ctx context.Context) {
		_ = r.RemoteTell(ctx, from, to, durationpb.New(time.Millisecond))
	}, func() {
		r.ClosePeer(host, port)
	})
}

// TestAcceptThenCloseRacingClose races sends against ClosePeer when the peer
// accepts then immediately drops the connection, so the handshake read fails
// with EOF. It exercises the handshake-failure classification, dial-marker
// release, and backoff recording while ClosePeer resets the peer.
func TestAcceptThenCloseRacingClose(t *testing.T) {
	host, port, stop := startFaultListener(t, faultAcceptClose)
	defer stop()

	r := newDuplexStressClient(t)
	defer r.Close()

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)

	runTeardownRace(t, time.Second, func(ctx context.Context) {
		_ = r.RemoteTell(ctx, from, to, durationpb.New(time.Millisecond))
	}, func() {
		r.ClosePeer(host, port)
	})
}

// TestServerDeathMidStreamRacingSends drives healthy sends against a live
// server, then kills the server out from under the in-flight lane while sends
// continue and ClosePeer races. It exercises the read-loop closed-handler
// retire (handleLaneClosed -> retireLaneAsync) racing send-path retire and
// ClosePeer, the transition from a healthy lane to a dead one under load.
func TestServerDeathMidStreamRacingSends(t *testing.T) {
	_, host, port, stopServer := startCoalescingServer(t)

	var stopOnce sync.Once
	killServer := func() { stopOnce.Do(stopServer) }
	defer killServer()

	r := newDuplexStressClient(t)
	defer r.Close()

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)

	const (
		senders = 8
		window  = 2 * time.Second
		guard   = 30 * time.Second
	)

	deadline := time.Now().Add(window)

	var wg sync.WaitGroup
	wg.Add(senders + 1)

	for range senders {
		go func() {
			defer wg.Done()

			for time.Now().Before(deadline) {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				_ = r.RemoteTell(ctx, from, to, durationpb.New(time.Millisecond))
				cancel()
			}
		}()
	}

	go func() {
		defer wg.Done()

		for time.Now().Before(deadline) {
			r.ClosePeer(host, port)
			pause.For(2 * time.Millisecond)
		}
	}()

	// Let the lane warm up on a healthy server, then kill it mid-stream so the
	// read loop discovers the death while sends and closes are racing.
	pause.For(400 * time.Millisecond)
	killServer()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(guard):
		t.Fatal("server death racing sends hung: a teardown path failed to make progress")
	}
}

// TestUnresponsiveReceiverSendsReleasedByClose races sends against ClosePeer
// when the server accepts traffic but never processes it: its tell handler
// blocks, so it stops returning credit. With a small credit window the sender
// parks on the window, and ClosePeer (or the per-call context) must release
// the parked send. It exercises close and cancellation interrupting a send
// blocked on flow control rather than on a dead socket.
func TestUnresponsiveReceiverSendsReleasedByClose(t *testing.T) {
	release := make(chan struct{})

	blocking := func(_ context.Context, _ inet.Connection, msg proto.Message) (proto.Message, error) {
		if _, ok := msg.(*internalpb.RemoteTellRequest); ok {
			<-release
		}

		return &internalpb.RemoteTellResponse{}, nil
	}

	ps, err := inet.NewRemotingServer("127.0.0.1:0",
		inet.WithProtoHandler("internalpb.RemoteTellRequest", blocking),
	)
	require.NoError(t, err)
	require.NoError(t, ps.Listen())

	serveDone := make(chan error, 1)
	go func() { serveDone <- ps.Serve() }()
	pause.For(50 * time.Millisecond)

	host, portStr, err := net.SplitHostPort(ps.ListenAddr().String())
	require.NoError(t, err)

	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	// Shut the server down last; unblock the handlers first so Shutdown drains.
	defer func() {
		require.NoError(t, ps.Shutdown(time.Second))
		<-serveDone
	}()
	defer close(release)

	// A small window and a short write timeout make the sender park on flow
	// control quickly and give a bounded floor when no caller deadline applies.
	r := newDuplexStressClient(t,
		WithClientInitialCredits(4096),
		WithClientWriteTimeout(500*time.Millisecond),
	)
	defer r.Close()

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)

	runTeardownRace(t, 500*time.Millisecond, func(ctx context.Context) {
		_ = r.RemoteTell(ctx, from, to, durationpb.New(time.Millisecond))
	}, func() {
		r.ClosePeer(host, port)
	})
}
