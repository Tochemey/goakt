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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/remote"
)

// tellFailureRecorder captures shared fan-out invocations for assertions.
type tellFailureRecorder struct {
	mu       sync.Mutex
	messages []*internalpb.RemoteMessage
	causes   []error
}

func (x *tellFailureRecorder) handle(_ string, messages []*internalpb.RemoteMessage, cause error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.messages = append(x.messages, messages...)
	x.causes = append(x.causes, cause)
}

func (x *tellFailureRecorder) count() int {
	x.mu.Lock()
	defer x.mu.Unlock()
	return len(x.messages)
}

func (x *tellFailureRecorder) message(i int) *internalpb.RemoteMessage {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.messages[i]
}

func TestRemoteTellUnreachablePeerAdmitsAndFansOut(t *testing.T) {
	recorder := &tellFailureRecorder{}
	c := NewClient(
		WithClientCompression(remote.NoCompression),
		WithTellFailureHandler(recorder.handle),
	).(*client)
	defer c.Close()

	host := "127.0.0.1"
	deadPort := inet.Get(1)[0]
	from := address.New("client", "testSys", host, deadPort)
	to := address.New("ghost", "remote", host, deadPort)

	const burst = 3
	for range burst {
		require.NoError(t, c.RemoteTell(context.Background(), from, to, durationpb.New(time.Second)))
	}

	// Every admitted tell to the dead peer must fan out, none may surface.
	require.Eventually(t, func() bool {
		return recorder.count() == burst
	}, 3*time.Second, 20*time.Millisecond)
	assert.Equal(t, to.String(), recorder.message(0).GetReceiver())
}

func TestAdmitTellPreservesContextMetadata(t *testing.T) {
	recorder := &tellFailureRecorder{}
	c := NewClient(
		WithClientCompression(remote.NoCompression),
		WithTellFailureHandler(recorder.handle),
		WithClientContextPropagator(&headerPropagator{key: "x-trace", value: "admit-meta"}),
	).(*client)
	defer c.Close()

	host := "127.0.0.1"
	deadPort := inet.Get(1)[0]
	from := address.New("client", "testSys", host, deadPort)
	to := address.New("ghost", "remote", host, deadPort)

	require.NoError(t, c.RemoteTell(context.Background(), from, to, durationpb.New(time.Second)))

	require.Eventually(t, func() bool {
		return recorder.count() == 1
	}, 3*time.Second, 20*time.Millisecond)

	md := recorder.message(0).GetMetadata()
	require.NotEmpty(t, md, "propagator headers must ride params.metadata through the pump")
	assert.Equal(t, "admit-meta", md["X-Trace"])
}

func TestAdmitTellCopiesCallerBuffers(t *testing.T) {
	recorder := &tellFailureRecorder{}
	c := NewClient(
		WithClientCompression(remote.NoCompression),
		WithTellFailureHandler(recorder.handle),
	).(*client)
	defer c.Close()

	deadPort := inet.Get(1)[0]
	p := c.peerFor("127.0.0.1", deadPort)
	key := laneKey{role: internalpb.LaneRole_LANE_ROLE_ORDINARY, index: 0}

	payload := []byte("admitted-payload")
	require.NoError(t, p.admitTell(context.Background(), key, tellParams{
		sender:   "goakt://testSys@127.0.0.1:1/sender",
		receiver: "goakt://remote@127.0.0.1:1/ghost",
		payload:  payload,
		serID:    1,
		typeName: "t",
	}))

	// The caller may recycle its buffer immediately after admission.
	payload[0] = 'X'

	require.Eventually(t, func() bool {
		return recorder.count() == 1
	}, 3*time.Second, 20*time.Millisecond)
	assert.Equal(t, []byte("admitted-payload"), recorder.message(0).GetMessage())
}

// TestTellPumpRunnerExitsWhenIdle confirms the transient runner contract: the
// pump goroutine exists only while tells are queued or in flight, hands its
// role back once the queue runs dry, and a later admission re-spawns it.
func TestTellPumpRunnerExitsWhenIdle(t *testing.T) {
	recorder := &tellFailureRecorder{}
	c := NewClient(
		WithClientCompression(remote.NoCompression),
		WithTellFailureHandler(recorder.handle),
	).(*client)
	defer c.Close()

	deadPort := inet.Get(1)[0]
	p := c.peerFor("127.0.0.1", deadPort)
	key := laneKey{role: internalpb.LaneRole_LANE_ROLE_ORDINARY, index: 0}
	params := tellParams{
		sender:   "goakt://testSys@127.0.0.1:1/sender",
		receiver: "goakt://remote@127.0.0.1:1/ghost",
		payload:  []byte("transient"),
		serID:    1,
		typeName: "t",
	}

	require.NoError(t, p.admitTell(context.Background(), key, params))
	require.Eventually(t, func() bool { return recorder.count() == 1 }, 3*time.Second, 20*time.Millisecond)

	p.mu.Lock()
	pump, ok := p.pumps[key]
	p.mu.Unlock()
	require.True(t, ok)
	require.Eventually(t, func() bool {
		pump.mu.Lock()
		defer pump.mu.Unlock()
		return !pump.running
	}, 3*time.Second, 20*time.Millisecond, "runner must exit once the queue runs dry")

	// A later admission must re-spawn the runner and deliver (here: fan out).
	require.NoError(t, p.admitTell(context.Background(), key, params))
	require.Eventually(t, func() bool { return recorder.count() == 2 }, 3*time.Second, 20*time.Millisecond)
}

func TestAdmitTellBackpressureOnFullByteWindow(t *testing.T) {
	const window = 64
	c := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientInitialCredits(window),
	).(*client)
	defer c.Close()

	p := c.peerFor("127.0.0.1", 65531)
	key := laneKey{role: internalpb.LaneRole_LANE_ROLE_ORDINARY, index: 0}

	// Pre-install a pump with no goroutine and a full byte window so
	// admission must block and then time out with backpressure.
	pump := newTellPump()
	filler := tellParams{payload: make([]byte, window)}
	pump.queue = []admittedTell{{params: filler, cost: admitCost(filler)}}
	pump.queuedBytes = admitCost(filler)

	p.mu.Lock()
	p.pumps[key] = pump
	p.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := p.admitTell(ctx, key, tellParams{payload: []byte("x")})
	require.ErrorIs(t, err, gerrors.ErrRemoteSendBackpressure)
}

func TestAdmitTellNoDeadlineBoundedByWriteTimeout(t *testing.T) {
	const window = 64
	c := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientInitialCredits(window),
		WithClientWriteTimeout(50*time.Millisecond),
	).(*client)
	defer c.Close()

	p := c.peerFor("127.0.0.1", 65530)
	key := laneKey{role: internalpb.LaneRole_LANE_ROLE_ORDINARY, index: 0}

	// Pre-install a full pump with no goroutine: a caller context without a
	// deadline must still surface backpressure once the write timeout elapses
	// instead of blocking forever.
	pump := newTellPump()
	filler := tellParams{payload: make([]byte, window)}
	pump.queue = []admittedTell{{params: filler, cost: admitCost(filler)}}
	pump.queuedBytes = admitCost(filler)

	p.mu.Lock()
	p.pumps[key] = pump
	p.mu.Unlock()

	start := time.Now()
	err := p.admitTell(context.Background(), key, tellParams{payload: []byte("x")})
	require.ErrorIs(t, err, gerrors.ErrRemoteSendBackpressure)
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestAdmitTellAllowsOversizedWhenQueueEmpty(t *testing.T) {
	recorder := &tellFailureRecorder{}
	const window = 8
	c := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientInitialCredits(window),
		WithTellFailureHandler(recorder.handle),
	).(*client)
	defer c.Close()

	deadPort := inet.Get(1)[0]
	p := c.peerFor("127.0.0.1", deadPort)
	key := laneKey{role: internalpb.LaneRole_LANE_ROLE_ORDINARY, index: 0}

	require.NoError(t, p.admitTell(context.Background(), key, tellParams{
		receiver: "goakt://remote@127.0.0.1:1/ghost",
		payload:  make([]byte, window*4),
	}))

	require.Eventually(t, func() bool {
		return recorder.count() == 1
	}, 3*time.Second, 20*time.Millisecond)
}

func TestTellPumpPopCompactsAbandonedPrefix(t *testing.T) {
	pump := newTellPump()
	for range queueCompactHead + 4 {
		item := admittedTell{params: tellParams{payload: []byte("x")}, cost: 1}
		pump.queue = append(pump.queue, item)
		pump.queuedBytes += item.cost
	}

	for range queueCompactHead {
		_, ok := pump.popAdmit()
		require.True(t, ok)
	}

	pump.mu.Lock()
	assert.Equal(t, 0, pump.head, "abandoned prefix should be compacted")
	assert.Equal(t, 4, len(pump.queue))
	assert.Equal(t, int64(4), pump.queuedBytes)
	pump.mu.Unlock()
}

func TestAdmitTellRejectedAfterClose(t *testing.T) {
	c := NewClient(WithClientCompression(remote.NoCompression)).(*client)
	defer c.Close()

	p := c.peerFor("127.0.0.1", 65532)
	p.closeAllLanes()

	key := laneKey{role: internalpb.LaneRole_LANE_ROLE_ORDINARY, index: 0}
	err := p.admitTell(context.Background(), key, tellParams{receiver: "r"})
	require.ErrorIs(t, err, errLaneClosedDuringDial)
}

func TestSendTellDuplexEncodeFailureFansOut(t *testing.T) {
	recorder := &tellFailureRecorder{}
	c := NewClient(
		WithClientCompression(remote.NoCompression),
		WithTellFailureHandler(recorder.handle),
	).(*client)
	defer c.Close()

	p := c.peerFor("127.0.0.1", 65534)
	receiver := "goakt://remote@127.0.0.1:1/ghost"
	entry, _ := p.route(receiver)
	session := &stubDuplexSession{id: 1, prepareRefErr: errors.New("encode boom")}

	p.mu.Lock()
	p.setLaneLocked(entry.lane, session)
	p.mu.Unlock()

	err := c.sendTellDuplex(context.Background(), p, tellParams{
		sender:   "goakt://testSys@127.0.0.1:1/sender",
		receiver: receiver,
		payload:  []byte("p"),
		serID:    1,
		typeName: "t",
	})
	require.NoError(t, err, "fire-and-forget encode failure must not surface")
	require.Equal(t, 1, recorder.count())
	assert.Equal(t, []byte("p"), recorder.message(0).GetMessage())
}

func TestSendTellDuplexOversizeFansOut(t *testing.T) {
	recorder := &tellFailureRecorder{}
	c := NewClient(
		WithClientCompression(remote.NoCompression),
		WithTellFailureHandler(recorder.handle),
	).(*client)
	defer c.Close()

	p := c.peerFor("127.0.0.1", 65535)
	receiver := "goakt://remote@127.0.0.1:1/ghost"
	entry, _ := p.route(receiver)
	session := &stubDuplexSession{id: 1, tellErr: inet.ErrMessageTooLarge}

	p.mu.Lock()
	p.setLaneLocked(entry.lane, session)
	p.mu.Unlock()

	err := c.sendTellDuplex(context.Background(), p, tellParams{
		sender:   "goakt://testSys@127.0.0.1:1/sender",
		receiver: receiver,
		payload:  []byte("p"),
		serID:    1,
		typeName: "t",
	})
	require.NoError(t, err, "permanent tell reject must fan out, not surface")
	require.Equal(t, 1, recorder.count())
}

func TestDeliverAdmittedTellRetriesInPlaceThenFansOut(t *testing.T) {
	recorder := &tellFailureRecorder{}
	deadPort := inet.Get(1)[0]
	c := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientDialTimeout(500*time.Millisecond),
		WithTellFailureHandler(recorder.handle),
	).(*client)
	defer c.Close()

	p := c.peerFor("127.0.0.1", deadPort)
	receiver := "goakt://remote@127.0.0.1:1/ghost"
	entry, _ := p.route(receiver)
	key := entry.lane

	// A live session that dies under the first write drives the pump's in-place
	// retry: it retires the lane, redials (the port is dead), and dead-letters
	// the message exactly once instead of re-queuing it behind later tells.
	session := &stubDuplexSession{id: 1, tellErr: errors.New("connection reset")}
	p.mu.Lock()
	p.setLaneLocked(key, session)
	p.mu.Unlock()

	require.NoError(t, p.admitTell(context.Background(), key, tellParams{
		sender:   "goakt://testSys@127.0.0.1:1/sender",
		receiver: receiver,
		payload:  []byte("p"),
		serID:    1,
		typeName: "t",
	}))

	require.Eventually(t, func() bool {
		return recorder.count() == 1
	}, 3*time.Second, 20*time.Millisecond)

	// A re-queue regression would produce a second fan-out during this settle
	// window; the in-place retry must dead-letter exactly once.
	time.Sleep(150 * time.Millisecond)
	assert.Equal(t, 1, recorder.count())
	assert.Equal(t, []byte("p"), recorder.message(0).GetMessage())
}

func TestDeliverAdmittedTellRetryPreservesFIFO(t *testing.T) {
	recorder := &tellFailureRecorder{}
	deadPort := inet.Get(1)[0]
	c := NewClient(
		WithClientCompression(remote.NoCompression),
		WithClientDialTimeout(300*time.Millisecond),
		WithTellFailureHandler(recorder.handle),
	).(*client)
	defer c.Close()

	p := c.peerFor("127.0.0.1", deadPort)
	receiver := "goakt://remote@127.0.0.1:1/ghost"
	entry, _ := p.route(receiver)
	key := entry.lane

	// Hold the first write open so B and C are admitted (and queued behind A)
	// before A's write fails. The in-place retry must resolve A before B and C,
	// so the dead letters arrive in admission order A, B, C. The old tail
	// re-admit would have re-queued A behind them and produced B, C, A.
	inTell := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	session := &stubDuplexSession{
		id:      1,
		tellErr: errors.New("connection reset"),
		tellHook: func() {
			once.Do(func() {
				close(inTell)
				<-release
			})
		},
	}

	p.mu.Lock()
	p.setLaneLocked(key, session)
	p.mu.Unlock()

	admit := func(tag string) {
		require.NoError(t, p.admitTell(context.Background(), key, tellParams{
			sender:   "goakt://testSys@127.0.0.1:1/sender",
			receiver: receiver,
			payload:  []byte(tag),
			serID:    1,
			typeName: "t",
		}))
	}

	admit("A")
	<-inTell // A is now blocked mid-write; B and C queue strictly behind it.
	admit("B")
	admit("C")
	close(release)

	require.Eventually(t, func() bool {
		return recorder.count() == 3
	}, 3*time.Second, 20*time.Millisecond)

	assert.Equal(t, []byte("A"), recorder.message(0).GetMessage())
	assert.Equal(t, []byte("B"), recorder.message(1).GetMessage())
	assert.Equal(t, []byte("C"), recorder.message(2).GetMessage())
}

func TestTellSendPlanFIFOFence(t *testing.T) {
	c := NewClient(WithClientCompression(remote.NoCompression)).(*client)
	defer c.Close()

	p := c.peerFor("127.0.0.1", 65533)
	receiver := "goakt://remote@127.0.0.1:1/ghost"
	entry, _ := p.route(receiver)
	key := entry.lane

	session := &stubDuplexSession{id: 1}
	p.mu.Lock()
	p.setLaneLocked(key, session)
	p.mu.Unlock()

	// A live session with an idle pump allows the synchronous fast path.
	_, _, got, preferLegacy := p.tellSendPlan(receiver)
	require.False(t, preferLegacy)
	require.Same(t, session, got)

	// Pending admitted tells fence the fast path so FIFO order survives.
	pump := newTellPump()
	pump.pending.Add(1)

	p.mu.Lock()
	p.pumps[key] = pump
	p.mu.Unlock()

	_, _, got, _ = p.tellSendPlan(receiver)
	assert.Nil(t, got)

	pump.pending.Add(-1)
	_, _, got, _ = p.tellSendPlan(receiver)
	require.Same(t, session, got)
}
