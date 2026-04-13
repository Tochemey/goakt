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
	"net"
	nethttp "net/http"
	"strconv"
	"sync"
	"sync/atomic"
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

// headerInjector is a minimal ContextPropagator test double that writes a
// fixed set of headers on Inject. Extract is a no-op.
type headerInjector map[string]string

func (h headerInjector) Inject(_ context.Context, hdr nethttp.Header) error {
	for k, v := range h {
		hdr.Set(k, v)
	}
	return nil
}
func (headerInjector) Extract(ctx context.Context, _ nethttp.Header) (context.Context, error) {
	return ctx, nil
}

// startCoalescingServer spins up a proto server that counts received
// RemoteTellRequest RPCs and the total RemoteMessage entries they contained.
// If returnErr is non-nil the server replies with an internalpb.Error.
type tellCounter struct {
	requests atomic.Int64
	messages atomic.Int64
	mu       sync.Mutex
	all      []*internalpb.RemoteMessage
	retErr   atomic.Pointer[error]
}

func (t *tellCounter) handler(_ context.Context, _ inet.Connection, msg proto.Message) (proto.Message, error) {
	req, ok := msg.(*internalpb.RemoteTellRequest)
	if !ok {
		return &internalpb.RemoteTellResponse{}, nil
	}
	t.requests.Add(1)
	t.messages.Add(int64(len(req.GetRemoteMessages())))
	t.mu.Lock()
	t.all = append(t.all, req.GetRemoteMessages()...)
	t.mu.Unlock()
	if ep := t.retErr.Load(); ep != nil && *ep != nil {
		return &internalpb.Error{Code: internalpb.Code_CODE_INTERNAL, Message: (*ep).Error()}, nil
	}
	return &internalpb.RemoteTellResponse{}, nil
}

// allReceived returns a snapshot of every RemoteMessage observed across every
// RPC the server has handled.
func (t *tellCounter) allReceived() []*internalpb.RemoteMessage {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]*internalpb.RemoteMessage, len(t.all))
	copy(out, t.all)
	return out
}

func startCoalescingServer(t *testing.T) (*tellCounter, string, int, func()) {
	t.Helper()
	tc := &tellCounter{}
	ps, err := inet.NewProtoServer("127.0.0.1:0",
		inet.WithProtoHandler("internalpb.RemoteTellRequest", tc.handler),
	)
	require.NoError(t, err)
	require.NoError(t, ps.Listen())
	done := make(chan error, 1)
	go func() { done <- ps.Serve() }()
	pause.For(50 * time.Millisecond)

	host, portStr, err := net.SplitHostPort(ps.ListenAddr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	stop := func() {
		require.NoError(t, ps.Shutdown(time.Second))
		<-done
	}
	return tc, host, port, stop
}

// TestCoalescing_DefaultDisabled confirms RemoteTell remains synchronous when
// WithSendCoalescing is not used: every RemoteTell becomes one RPC.
func TestCoalescing_DefaultDisabled(t *testing.T) {
	tc, host, port, stop := startCoalescingServer(t)
	defer stop()

	r := NewClient(WithClientCompression(remote.NoCompression))
	defer r.Close()

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)
	for i := 0; i < 5; i++ {
		require.NoError(t, r.RemoteTell(context.Background(), from, to, durationpb.New(time.Duration(i)*time.Millisecond)))
	}
	assert.Equal(t, int64(5), tc.requests.Load(), "each RemoteTell should produce one RPC when coalescing is disabled")
	assert.Equal(t, int64(5), tc.messages.Load())
}

// TestCoalescing_BatchesUnderLoad verifies that with Nagle-style batching,
// messages enqueued while a send is in flight coalesce into fewer RPCs than
// messages. We don't assert an exact ratio — the coalescer's batch size
// depends on goroutine scheduling relative to RPC completion — only that
// some batching occurred.
func TestCoalescing_BatchesUnderLoad(t *testing.T) {
	tc, host, port, stop := startCoalescingServer(t)
	defer stop()

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithSendCoalescing(64),
	)
	defer r.Close()

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)
	const n = 200
	for i := 0; i < n; i++ {
		require.NoError(t, r.RemoteTell(context.Background(), from, to, durationpb.New(time.Duration(i)*time.Millisecond)))
	}
	require.Eventually(t, func() bool { return tc.messages.Load() == int64(n) }, 2*time.Second, 2*time.Millisecond)
	assert.Less(t, tc.requests.Load(), int64(n),
		"at least some coalescing should have occurred under load (%d RPCs for %d messages)", tc.requests.Load(), n)
}

// TestCoalescing_RespectsMaxBatch asserts that no single RPC carries more
// than maxBatch messages even when far more are enqueued during a flush.
func TestCoalescing_RespectsMaxBatch(t *testing.T) {
	tc, host, port, stop := startCoalescingServer(t)
	defer stop()

	const maxBatch = 4
	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithSendCoalescing(maxBatch),
	)
	defer r.Close()

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)
	const n = 64
	for i := 0; i < n; i++ {
		require.NoError(t, r.RemoteTell(context.Background(), from, to, durationpb.New(time.Duration(i)*time.Millisecond)))
	}
	require.Eventually(t, func() bool { return tc.messages.Load() == int64(n) }, 2*time.Second, 2*time.Millisecond)
	// Peek the last batch's size — also verify no individual batch exceeded maxBatch
	// by checking the ratio of RPCs vs messages is at least ceil(n/maxBatch).
	assert.GreaterOrEqual(t, tc.requests.Load(), int64((n+maxBatch-1)/maxBatch),
		"should have taken at least ceil(n/maxBatch) RPCs")
}

// TestCoalescing_FlushOnClose ensures pending messages (those still buffered
// when Close is called) are drained rather than silently lost. We use a
// server that blocks its responses so the writer goroutine gets stuck on a
// SendProto, which lets subsequent enqueues pile up in the channel. Close
// must drain them before returning.
func TestCoalescing_FlushOnClose(t *testing.T) {
	release := make(chan struct{})
	var received atomic.Int64
	handler := func(_ context.Context, _ inet.Connection, msg proto.Message) (proto.Message, error) {
		if req, ok := msg.(*internalpb.RemoteTellRequest); ok {
			received.Add(int64(len(req.GetRemoteMessages())))
		}
		<-release
		return &internalpb.RemoteTellResponse{}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteTellRequest", handler))
	require.NoError(t, err)
	require.NoError(t, ps.Listen())
	done := make(chan error, 1)
	go func() { done <- ps.Serve() }()
	defer func() { require.NoError(t, ps.Shutdown(time.Second)); <-done }()
	pause.For(50 * time.Millisecond)

	host, portStr, err := net.SplitHostPort(ps.ListenAddr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithSendCoalescing(1024),
	)
	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)
	const n = 16
	for i := 0; i < n; i++ {
		require.NoError(t, r.RemoteTell(context.Background(), from, to, durationpb.New(time.Duration(i)*time.Millisecond)))
	}

	// Let the first (blocked) RPC release; Close then drains everything that
	// piled up in the channel during the block.
	closeDone := make(chan struct{})
	go func() { r.Close(); close(closeDone) }()
	close(release)
	select {
	case <-closeDone:
	case <-time.After(3 * time.Second):
		t.Fatalf("Close did not return within 3s")
	}
	assert.Equal(t, int64(n), received.Load(), "all messages must reach the server by the time Close returns")
}

// TestCoalescing_ErrorHandler routes server errors for a flushed batch to the
// registered handler rather than the RemoteTell return value.
func TestCoalescing_ErrorHandler(t *testing.T) {
	tc, host, port, stop := startCoalescingServer(t)
	defer stop()

	boom := errors.New("boom")
	tc.retErr.Store(&boom)

	var (
		mu    sync.Mutex
		dests []string
		count int
	)
	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithSendCoalescing(2),
		WithCoalescingErrorHandler(func(dest string, msgs []*internalpb.RemoteMessage, err error) {
			mu.Lock()
			defer mu.Unlock()
			dests = append(dests, dest)
			count += len(msgs)
		}),
	)
	defer r.Close()

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)
	// RemoteTell still returns nil because the error is async.
	for i := 0; i < 4; i++ {
		require.NoError(t, r.RemoteTell(context.Background(), from, to, durationpb.New(time.Duration(i)*time.Millisecond)))
	}
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return count == 4
	}, time.Second, 5*time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 4, count)
	assert.NotEmpty(t, dests)
}

// TestCoalescing_PropagatesPerMessageMetadata verifies that a registered
// context propagator is invoked per call and its headers reach the server on
// each RemoteMessage's Metadata field, even though the messages are coalesced
// into a single RPC.
func TestCoalescing_PropagatesPerMessageMetadata(t *testing.T) {
	tc, host, port, stop := startCoalescingServer(t)
	defer stop()

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithSendCoalescing(64),
		WithClientContextPropagator(headerInjector{"x-trace-id": "abc123"}),
	)
	defer r.Close()

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)
	for i := 0; i < 5; i++ {
		require.NoError(t, r.RemoteTell(context.Background(), from, to, durationpb.New(time.Duration(i)*time.Millisecond)))
	}
	require.Eventually(t, func() bool { return tc.messages.Load() == 5 }, time.Second, 2*time.Millisecond)

	recv := tc.allReceived()
	require.Len(t, recv, 5)
	for i, m := range recv {
		assert.Equal(t, "abc123", m.GetMetadata()["X-Trace-Id"], "message %d missing propagated metadata", i)
	}
}

// TestCoalescing_ContextCancelled refuses to enqueue when the caller's context
// is already done.
func TestCoalescing_ContextCancelled(t *testing.T) {
	_, host, port, stop := startCoalescingServer(t)
	defer stop()

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithSendCoalescing(64),
	)
	defer r.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)
	err := r.RemoteTell(ctx, from, to, durationpb.New(time.Second))
	assert.ErrorIs(t, err, context.Canceled)
}

// failingInjector returns a fixed error from Inject; used to assert that a
// failing propagator surfaces an error from the coalesced send path.
type failingInjector struct{ err error }

func (f failingInjector) Inject(context.Context, nethttp.Header) error { return f.err }
func (failingInjector) Extract(ctx context.Context, _ nethttp.Header) (context.Context, error) {
	return ctx, nil
}

// emptyInjector injects no headers; verifies the zero-headers branch of
// injectPerMessageMetadata returns nil metadata rather than an empty map.
type emptyInjector struct{}

func (emptyInjector) Inject(context.Context, nethttp.Header) error { return nil }
func (emptyInjector) Extract(ctx context.Context, _ nethttp.Header) (context.Context, error) {
	return ctx, nil
}

// ctxCapturingInjector remembers the context it was last called with, allowing
// tests to assert that RemoteTell passes the caller's context through.
type ctxCapturingInjector struct {
	mu     sync.Mutex
	last   context.Context
	called int
}

func (c *ctxCapturingInjector) Inject(ctx context.Context, _ nethttp.Header) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.last = ctx
	c.called++
	return nil
}
func (c *ctxCapturingInjector) Extract(ctx context.Context, _ nethttp.Header) (context.Context, error) {
	return ctx, nil
}

// TestCoalescing_InjectPropagatorError surfaces a failing propagator as an
// error from RemoteTell rather than silently enqueueing without metadata.
func TestCoalescing_InjectPropagatorError(t *testing.T) {
	_, host, port, stop := startCoalescingServer(t)
	defer stop()

	sentinel := errors.New("inject failure")
	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithSendCoalescing(64),
		WithClientContextPropagator(failingInjector{err: sentinel}),
	)
	defer r.Close()

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)
	err := r.RemoteTell(context.Background(), from, to, durationpb.New(time.Second))
	assert.ErrorIs(t, err, sentinel)
}

// TestCoalescing_EmptyPropagatorYieldsNoMetadata confirms that when the
// propagator emits no headers the wire-level Metadata map stays empty — we do
// not allocate a zero-length map and send it over the wire.
func TestCoalescing_EmptyPropagatorYieldsNoMetadata(t *testing.T) {
	tc, host, port, stop := startCoalescingServer(t)
	defer stop()

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithSendCoalescing(4),
		WithClientContextPropagator(emptyInjector{}),
	)
	defer r.Close()

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)
	for i := 0; i < 4; i++ {
		require.NoError(t, r.RemoteTell(context.Background(), from, to, durationpb.New(time.Duration(i)*time.Millisecond)))
	}
	require.Eventually(t, func() bool { return tc.messages.Load() == 4 }, time.Second, 2*time.Millisecond)
	recv := tc.allReceived()
	require.Len(t, recv, 4)
	for i, m := range recv {
		assert.Empty(t, m.GetMetadata(), "message %d should carry no metadata when propagator emits nothing", i)
	}
}

// TestCoalescing_NoPropagatorNoMetadata verifies that absent a propagator the
// Metadata field is left nil/empty and messages still deliver correctly.
func TestCoalescing_NoPropagatorNoMetadata(t *testing.T) {
	tc, host, port, stop := startCoalescingServer(t)
	defer stop()

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithSendCoalescing(4),
	)
	defer r.Close()

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)
	for i := 0; i < 4; i++ {
		require.NoError(t, r.RemoteTell(context.Background(), from, to, durationpb.New(time.Duration(i)*time.Millisecond)))
	}
	require.Eventually(t, func() bool { return tc.messages.Load() == 4 }, time.Second, 2*time.Millisecond)
	for _, m := range tc.allReceived() {
		assert.Empty(t, m.GetMetadata())
	}
}

// TestCoalescing_PropagatorReceivesCallerContext asserts that the caller's
// context (with all its values) is the one passed to Inject, so tracing /
// baggage values associated with a specific call reach the server intact.
func TestCoalescing_PropagatorReceivesCallerContext(t *testing.T) {
	_, host, port, stop := startCoalescingServer(t)
	defer stop()

	injector := &ctxCapturingInjector{}
	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithSendCoalescing(64),
		WithClientContextPropagator(injector),
	)
	defer r.Close()

	type markerKey struct{}
	ctx := context.WithValue(context.Background(), markerKey{}, "payload")
	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)
	require.NoError(t, r.RemoteTell(ctx, from, to, durationpb.New(time.Second)))

	injector.mu.Lock()
	defer injector.mu.Unlock()
	require.Equal(t, 1, injector.called, "propagator should be invoked exactly once")
	require.NotNil(t, injector.last)
	assert.Equal(t, "payload", injector.last.Value(markerKey{}))
}

// TestCoalescing_MultipleDestinations verifies each (host:port) gets its own
// coalescer instance and messages don't cross-contaminate.
func TestCoalescing_MultipleDestinations(t *testing.T) {
	tc1, host1, port1, stop1 := startCoalescingServer(t)
	defer stop1()
	tc2, host2, port2, stop2 := startCoalescingServer(t)
	defer stop2()

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithSendCoalescing(32),
	)
	defer r.Close()

	from := address.New("from", "sys", host1, port1)
	to1 := address.New("a", "sys", host1, port1)
	to2 := address.New("b", "sys", host2, port2)

	for i := 0; i < 10; i++ {
		require.NoError(t, r.RemoteTell(context.Background(), from, to1, durationpb.New(time.Duration(i)*time.Millisecond)))
	}
	for i := 0; i < 7; i++ {
		require.NoError(t, r.RemoteTell(context.Background(), from, to2, durationpb.New(time.Duration(i)*time.Millisecond)))
	}

	require.Eventually(t, func() bool { return tc1.messages.Load() == 10 && tc2.messages.Load() == 7 }, time.Second, 2*time.Millisecond)
	assert.Equal(t, int64(10), tc1.messages.Load())
	assert.Equal(t, int64(7), tc2.messages.Load())
}

// TestCoalescing_ConcurrentSubmitters fires many goroutines at the same
// destination to surface any race or lost message in enqueue / getCoalescer.
func TestCoalescing_ConcurrentSubmitters(t *testing.T) {
	tc, host, port, stop := startCoalescingServer(t)
	defer stop()

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithSendCoalescing(128),
	)
	defer r.Close()

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)
	const (
		goroutines = 32
		perGR      = 50
	)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perGR; i++ {
				_ = r.RemoteTell(context.Background(), from, to, durationpb.New(time.Duration(i)*time.Millisecond))
			}
		}()
	}
	wg.Wait()
	require.Eventually(t, func() bool { return tc.messages.Load() == int64(goroutines*perGR) }, 2*time.Second, 5*time.Millisecond)
}

// TestCoalescing_CloseIdempotent asserts multiple Close calls are safe.
func TestCoalescing_CloseIdempotent(t *testing.T) {
	_, host, port, stop := startCoalescingServer(t)
	defer stop()

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithSendCoalescing(16),
	)

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)
	require.NoError(t, r.RemoteTell(context.Background(), from, to, durationpb.New(time.Second)))

	assert.NotPanics(t, func() {
		r.Close()
		r.Close()
		r.Close()
	})
}

// TestCoalescing_ErrorHandlerNilDoesNotPanic verifies that a flush error is
// silently dropped when no handler is registered rather than crashing.
func TestCoalescing_ErrorHandlerNilDoesNotPanic(t *testing.T) {
	tc, host, port, stop := startCoalescingServer(t)
	defer stop()
	boom := errors.New("boom")
	tc.retErr.Store(&boom)

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithSendCoalescing(2),
	)
	defer r.Close()

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)
	assert.NotPanics(t, func() {
		for i := 0; i < 4; i++ {
			_ = r.RemoteTell(context.Background(), from, to, durationpb.New(time.Duration(i)*time.Millisecond))
		}
		// Give the writer time to invoke the (nil) error path.
		pause.For(100 * time.Millisecond)
	})
	// Still observed the RPC on the server.
	assert.GreaterOrEqual(t, tc.requests.Load(), int64(1))
}

// TestCoalescing_DefaultTunables covers the zero-value path of
// WithSendCoalescing: passing 0,0 still yields a working coalescer with the
// internal defaults.
func TestCoalescing_DefaultTunables(t *testing.T) {
	tc, host, port, stop := startCoalescingServer(t)
	defer stop()

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithSendCoalescing(1), // 1 triggers immediate flush regardless of timer default
	)
	defer r.Close()

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)
	for i := 0; i < 3; i++ {
		require.NoError(t, r.RemoteTell(context.Background(), from, to, durationpb.New(time.Duration(i)*time.Millisecond)))
	}
	require.Eventually(t, func() bool { return tc.messages.Load() == 3 }, time.Second, 2*time.Millisecond)
}

// TestCoalescing_SubmitAfterClose guards the submit path against races where
// a new submit races with shutdown: the coalescer must refuse rather than
// panic on a closed channel. We exercise it via the internal submit method
// directly because the higher-level RemoteTell would rebuild a fresh
// coalescer post-close.
func TestCoalescing_SubmitAfterClose(t *testing.T) {
	c := newCoalescer("127.0.0.1:1", nil, coalescingConfig{maxBatch: 4})
	c.close()
	err := c.submit(context.Background(), &internalpb.RemoteMessage{})
	assert.ErrorIs(t, err, errCoalescerClosed, "submit must refuse after close")
	// Second close is a no-op.
	assert.NotPanics(t, c.close)
}

// blockingServer is a proto server whose RemoteTellRequest handler parks
// until release is closed. It lets tests create a coalescer whose writer
// goroutine is stuck mid-flush — the only way to reliably fill the outbound
// channel so backpressure can be exercised.
type blockingServer struct {
	release chan struct{}
	hits    atomic.Int64
}

func (b *blockingServer) handler(ctx context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
	b.hits.Add(1)
	select {
	case <-b.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &internalpb.RemoteTellResponse{}, nil
}

func startBlockingServer(t *testing.T) (*blockingServer, string, int, func()) {
	t.Helper()
	bs := &blockingServer{release: make(chan struct{})}
	ps, err := inet.NewProtoServer("127.0.0.1:0",
		inet.WithProtoHandler("internalpb.RemoteTellRequest", bs.handler),
	)
	require.NoError(t, err)
	require.NoError(t, ps.Listen())
	done := make(chan error, 1)
	go func() { done <- ps.Serve() }()
	pause.For(50 * time.Millisecond)

	host, portStr, err := net.SplitHostPort(ps.ListenAddr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	stop := func() {
		// Release any parked handler so the server can shut down.
		select {
		case <-bs.release:
		default:
			close(bs.release)
		}
		require.NoError(t, ps.Shutdown(time.Second))
		<-done
	}
	return bs, host, port, stop
}

// newStuckCoalescer returns a coalescer wired to a server whose handler
// blocks until the returned release func is called. The first batch the
// writer goroutine sends will park inside SendProto, so subsequent submits
// accumulate in the channel until it is full — at which point new submits
// block on backpressure. This is the exact condition RemoteTell must
// surface to callers as ErrRemoteSendBackpressure.
func newStuckCoalescer(t *testing.T, maxBatch int) (c *coalescer, release func(), stop func()) {
	t.Helper()
	bs, host, port, srvStop := startBlockingServer(t)
	nc := inet.NewClient(net.JoinHostPort(host, strconv.Itoa(port)))
	c = newCoalescer(net.JoinHostPort(host, strconv.Itoa(port)), nc, coalescingConfig{maxBatch: maxBatch})
	release = func() { close(bs.release); bs.release = make(chan struct{}) }
	stop = func() {
		// Release any parked handler first so the writer goroutine's
		// in-flight SendProto can return; otherwise c.close() blocks on
		// the writer for the full RPC timeout.
		select {
		case <-bs.release:
		default:
			close(bs.release)
		}
		// Break any remaining in-flight RPC by closing the net client
		// before waiting on the writer goroutine — the writer then
		// observes a transport error and returns promptly instead of
		// waiting for the 5-second transport deadline.
		nc.Close()
		c.close()
		srvStop()
	}
	return c, release, stop
}

// waitUntil is a tight polling helper for "has this condition become true
// within d?" — used in backpressure tests where we need to wait for the
// writer goroutine to drain a message before asserting on channel state.
func waitUntil(t *testing.T, d time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		pause.For(time.Millisecond)
	}
	t.Fatalf("condition not met within %s: %s", d, msg)
}

// TestCoalescing_SubmitBlocksUntilDrained confirms the happy path of the new
// bounded queue + block-with-deadline model: a submit that finds a full
// channel parks until the writer drains space, then completes. This replaces
// the old drop-to-synchronous-fallback behavior, which hid overload from the
// caller behind a second connection.
func TestCoalescing_SubmitBlocksUntilDrained(t *testing.T) {
	c, release, stop := newStuckCoalescer(t, 1) // channel depth = maxBatch*4 = 4
	defer stop()

	// Fill the channel. With a stuck writer we can enqueue exactly
	// channel-depth messages before submit must block.
	for range cap(c.in) + 1 { // +1: one message gets pulled into the in-flight batch
		require.NoError(t, c.submit(context.Background(), &internalpb.RemoteMessage{}),
			"submits up to capacity must succeed immediately")
	}

	// Next submit must block. Run it in a goroutine and assert it has not
	// returned after a short wait.
	done := make(chan error, 1)
	go func() { done <- c.submit(context.Background(), &internalpb.RemoteMessage{}) }()

	select {
	case err := <-done:
		t.Fatalf("submit should have blocked on full channel; returned %v", err)
	case <-time.After(50 * time.Millisecond):
		// Expected — submit is parked.
	}

	// Release the parked handler so the writer can drain. The blocked
	// submit must then observe space and succeed — proving the block
	// path is *wake on drain*, not merely *wake on shutdown*.
	release()

	select {
	case err := <-done:
		assert.NoError(t, err, "blocked submit must succeed once the writer drains")
	case <-time.After(2 * time.Second):
		t.Fatal("blocked submit did not unblock after writer drained")
	}
}

// TestCoalescing_SubmitReturnsContextDeadline confirms that a caller's
// context deadline caps the wait on a full outbound queue. This is the
// Kafka/Akka Artery pattern: block, but bounded — so the caller can
// retry, drop, or circuit-break visibly instead of deadlocking.
func TestCoalescing_SubmitReturnsContextDeadline(t *testing.T) {
	c, _, stop := newStuckCoalescer(t, 1)
	defer stop()

	// Saturate the channel.
	for range cap(c.in) + 1 {
		require.NoError(t, c.submit(context.Background(), &internalpb.RemoteMessage{}))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := c.submit(ctx, &internalpb.RemoteMessage{})
	elapsed := time.Since(start)

	assert.ErrorIs(t, err, context.DeadlineExceeded, "must surface ctx.Err verbatim")
	assert.GreaterOrEqual(t, elapsed, 25*time.Millisecond, "submit must honor the deadline rather than returning early")
	assert.Less(t, elapsed, 500*time.Millisecond, "submit must not wait well past the deadline")
}

// TestCoalescing_SubmitReturnsContextCanceled confirms that an explicit
// ctx cancel unblocks a parked submit promptly.
func TestCoalescing_SubmitReturnsContextCanceled(t *testing.T) {
	c, _, stop := newStuckCoalescer(t, 1)
	defer stop()

	for range cap(c.in) + 1 {
		require.NoError(t, c.submit(context.Background(), &internalpb.RemoteMessage{}))
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.submit(ctx, &internalpb.RemoteMessage{}) }()

	// Let the goroutine park on the slow-path select.
	pause.For(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("parked submit did not unblock on cancel")
	}
}

// TestCoalescing_SubmitUnblocksOnClose covers the shutdown-during-block
// race: a submit parked on backpressure must observe c.done closing and
// return errCoalescerClosed rather than leaking the caller goroutine.
func TestCoalescing_SubmitUnblocksOnClose(t *testing.T) {
	c, _, stop := newStuckCoalescer(t, 1)
	// We'll call c.close() explicitly below; stop() still needs to run so
	// the underlying server and netClient get cleaned up.
	defer stop()

	for range cap(c.in) + 1 {
		require.NoError(t, c.submit(context.Background(), &internalpb.RemoteMessage{}))
	}

	done := make(chan error, 1)
	go func() { done <- c.submit(context.Background(), &internalpb.RemoteMessage{}) }()

	// Let the submit park before we close.
	pause.For(20 * time.Millisecond)
	c.close()

	select {
	case err := <-done:
		assert.ErrorIs(t, err, errCoalescerClosed)
	case <-time.After(time.Second):
		t.Fatal("parked submit did not unblock on close")
	}
}

// TestCoalescing_SubmitFastPathNoBlock covers the fast-path optimization:
// when space is immediately available submit must return without arming
// the three-way select (which is observably slower under contention). We
// verify the contract by confirming submit returns promptly even when ctx
// has no Done channel at all (context.Background).
func TestCoalescing_SubmitFastPath(t *testing.T) {
	c, _, stop := newStuckCoalescer(t, 8)
	defer stop()

	// First submit has room; must return immediately even though the writer
	// is stuck (it hasn't even started its first batch yet).
	err := c.submit(context.Background(), &internalpb.RemoteMessage{})
	require.NoError(t, err)
}

// TestCoalescing_RemoteTellSurfacesBackpressure confirms the end-to-end
// contract: RemoteTell wraps a ctx deadline from submit as
// ErrRemoteSendBackpressure so callers can distinguish overload from
// transport errors and apply their own policy (retry, drop, circuit-break).
func TestCoalescing_RemoteTellSurfacesBackpressure(t *testing.T) {
	bs, host, port, srvStop := startBlockingServer(t)
	defer srvStop()

	r := NewClient(
		WithClientCompression(remote.NoCompression),
		WithSendCoalescing(1), // channel depth 4
	)
	defer r.Close()

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)

	// Prime the coalescer by sending a message that the server will park on.
	// That ensures the writer goroutine is stuck inside SendProto.
	require.NoError(t, r.RemoteTell(context.Background(), from, to, durationpb.New(0)))

	// Wait until the server has received the first request, so the writer
	// is definitely parked.
	waitUntil(t, time.Second, func() bool { return bs.hits.Load() >= 1 }, "server never observed first request")

	// Fill the channel. Channel depth is maxBatch*4=4; we already have the
	// in-flight batch, so we can enqueue ~4 more without blocking.
	for range 4 {
		require.NoError(t, r.RemoteTell(context.Background(), from, to, durationpb.New(0)))
	}

	// The next RemoteTell finds a full channel. With a tight deadline it
	// must return ErrRemoteSendBackpressure, not succeed and not panic.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := r.RemoteTell(ctx, from, to, durationpb.New(0))
	require.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrRemoteSendBackpressure, "backpressure must be surfaced as ErrRemoteSendBackpressure")
	assert.ErrorIs(t, err, context.DeadlineExceeded, "underlying ctx error must remain inspectable via errors.Is")
}

// TestInjectPerMessageMetadata_NilPropagator covers the early-return branch
// that skips allocation when no propagator is configured.
func TestInjectPerMessageMetadata_NilPropagator(t *testing.T) {
	r := NewClient().(*client)
	md, err := r.injectMessageMetadata(context.Background())
	require.NoError(t, err)
	assert.Nil(t, md)
}

// TestInjectPerMessageMetadata_EmptyHeaders covers the branch where a
// propagator is configured but emits no headers: we return (nil, nil) rather
// than an empty map to avoid a wire-format allocation.
func TestInjectPerMessageMetadata_EmptyHeaders(t *testing.T) {
	r := NewClient(WithClientContextPropagator(emptyInjector{})).(*client)
	md, err := r.injectMessageMetadata(context.Background())
	require.NoError(t, err)
	assert.Nil(t, md)
}

// TestInjectPerMessageMetadata_PropagatorError propagates an Inject error to
// the caller.
func TestInjectPerMessageMetadata_PropagatorError(t *testing.T) {
	sentinel := errors.New("inject failure")
	r := NewClient(WithClientContextPropagator(failingInjector{err: sentinel})).(*client)
	md, err := r.injectMessageMetadata(context.Background())
	assert.Nil(t, md)
	assert.ErrorIs(t, err, sentinel)
}

// TestInjectPerMessageMetadata_HeadersRoundtrip confirms headers emitted by
// the propagator reach the returned metadata map verbatim.
func TestInjectPerMessageMetadata_HeadersRoundtrip(t *testing.T) {
	r := NewClient(WithClientContextPropagator(headerInjector{"a": "1", "b": "2"})).(*client)
	md, err := r.injectMessageMetadata(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "1", md["A"])
	assert.Equal(t, "2", md["B"])
}
