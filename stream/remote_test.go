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

package stream_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/stream"
)

// TestSourceRef_RoundTrip verifies that Source.SourceRef + ref.Source in the
// same actor system delivers every element from the underlying source to the
// downstream sink and signals completion.
func TestSourceRef_RoundTrip(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	ref, err := stream.Of(1, 2, 3, 4, 5).SourceRef(ctx, sys)
	require.NoError(t, err)

	col, sink := stream.Collect[int]()
	h, err := ref.Source(sys).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("source ref did not complete")
	}
	require.NoError(t, h.Err())
	assert.Equal(t, []int{1, 2, 3, 4, 5}, col.Items())
}

// TestSourceRef_AlreadySubscribed verifies that two concurrent ref.Source
// materializations against the same ref do not both succeed: exactly one
// receives the elements, the other surfaces a stream-level error.
//
// The two Run calls return without blocking, so the order in which the
// bridges' subscribe messages reach the endpoint is not deterministic.
// Either subscription may be the one that wins; the test therefore asserts
// "one succeeded, one failed" rather than fixing which is which.
func TestSourceRef_AlreadySubscribed(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	ref, err := stream.Of(1, 2, 3).SourceRef(ctx, sys)
	require.NoError(t, err)

	col1, sink1 := stream.Collect[int]()
	h1, err := ref.Source(sys).To(sink1).Run(ctx, sys)
	require.NoError(t, err)

	col2, sink2 := stream.Collect[int]()
	h2, err := ref.Source(sys).To(sink2).Run(ctx, sys)
	require.NoError(t, err)

	<-h1.Done()
	<-h2.Done()

	winners := 0
	losers := 0
	for _, h := range []struct {
		handle stream.StreamHandle
		col    *stream.Collector[int]
	}{{h1, col1}, {h2, col2}} {
		if h.handle.Err() == nil {
			winners++
			assert.Equal(t, []int{1, 2, 3}, h.col.Items(), "winning handle must receive every element")
		} else {
			losers++
			assert.Empty(t, h.col.Items(), "losing handle must not receive elements")
		}
	}
	assert.Equal(t, 1, winners, "exactly one subscription must succeed")
	assert.Equal(t, 1, losers, "exactly one subscription must fail (ref is single-use)")
}

// TestSinkRef_RoundTrip verifies that Sink.SinkRef + ref.Sink in the same
// actor system delivers every element from the producer's source through
// the remote sink and signals completion.
func TestSinkRef_RoundTrip(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	ref, err := sink.SinkRef(ctx, sys)
	require.NoError(t, err)

	h, err := stream.From(stream.Of(10, 20, 30, 40, 50)).
		To(ref.Sink(sys)).
		Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("sink ref did not complete")
	}
	require.NoError(t, h.Err())

	// The collector lives behind the sink endpoint; allow a brief moment
	// for the final element + completion to drain through both the wire
	// and the inner sub-pipeline before reading.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(col.Items()) < 5 {
		pause.For(10 * time.Millisecond)
	}
	assert.Equal(t, []int{10, 20, 30, 40, 50}, col.Items())
}

// TestSourceRef_StopCancels verifies that Stop on the consumer-side handle
// propagates a streamCancel through the bridge (sent to the endpoint as
// streamCancelWire) and the consumer pipeline completes cleanly. This
// exercises the streamCancel case in remoteSourceBridgeActor and the
// streamCancelWire case in sourceRefEndpointActor.
func TestSourceRef_StopCancels(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	// Use Tick as an effectively-infinite source so the stream cannot finish
	// on its own — only Stop should drive completion.
	ref, err := stream.Tick(50 * time.Millisecond).SourceRef(ctx, sys)
	require.NoError(t, err)

	_, sink := stream.Collect[time.Time]()
	h, err := ref.Source(sys).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	pause.For(150 * time.Millisecond)
	require.NoError(t, h.Stop(ctx))

	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("source ref did not complete after Stop")
	}
}

// TestSinkRef_LateSubscribeRejected verifies that a second producer attempting
// to subscribe to the same SinkRef receives a stream-level error. This covers
// the "already subscribed" branch in sinkRefEndpointActor and the
// streamErrorWire path in remoteSinkBridgeActor.
func TestSinkRef_LateSubscribeRejected(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	_, sink := stream.Collect[int]()
	ref, err := sink.SinkRef(ctx, sys)
	require.NoError(t, err)

	// First producer subscribes and runs to completion; ref is now consumed.
	h1, err := stream.From(stream.Of(1, 2, 3)).To(ref.Sink(sys)).Run(ctx, sys)
	require.NoError(t, err)
	<-h1.Done()
	require.NoError(t, h1.Err())

	// Second producer must surface a stream-level error.
	h2, err := stream.From(stream.Of(4, 5, 6)).To(ref.Sink(sys)).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h2.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("late sink ref subscribe did not terminate")
	}
	require.Error(t, h2.Err(), "second subscribe must fail; ref is single-use")
}

// TestSinkRef_UpstreamErrorPropagates verifies that an upstream error on the
// producer side surfaces on the producer's StreamHandle and translates into a
// streamErrorWire that tears down the endpoint sub-pipeline. This covers the
// streamError case in remoteSinkBridgeActor.
func TestSinkRef_UpstreamErrorPropagates(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	_, sink := stream.Collect[int]()
	ref, err := sink.SinkRef(ctx, sys)
	require.NoError(t, err)

	boom := errors.New("upstream boom")
	h, err := stream.Via(
		stream.Of(1, 2, 3, 4, 5),
		stream.TryMap(func(v int) (int, error) {
			if v == 3 {
				return 0, boom
			}
			return v, nil
		}),
	).To(ref.Sink(sys)).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("sink ref did not complete after upstream error")
	}
	require.Error(t, h.Err())
	assert.Contains(t, h.Err().Error(), "upstream boom")
}

// TestSourceRef_NonexistentEndpointFails verifies that resolving a SourceRef
// whose endpoint actor does not exist surfaces a descriptive error on
// StreamHandle.Err(). This covers the resolveEndpoint failure branch in
// remoteSourceBridgeActor.
func TestSourceRef_NonexistentEndpointFails(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	bogus := stream.SourceRef[int]{
		Name: "src-ref-missing",
		Host: sys.Host(),
		Port: sys.Port(),
	}

	_, sink := stream.Collect[int]()
	h, err := bogus.Source(sys).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(15 * time.Second):
		t.Fatal("source ref did not surface resolve error")
	}
	require.Error(t, h.Err())
	assert.Contains(t, h.Err().Error(), "resolve source ref")
}

// TestSourceRef_AlreadyConsumedRejected verifies that a second subscriber
// arriving AFTER the first has fully drained the SourceRef receives an
// "already consumed" rejection during the post-completion grace window.
// This covers the `terminated` reason branch in sourceRefEndpointActor.
func TestSourceRef_AlreadyConsumedRejected(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	ref, err := stream.Of(1, 2, 3).SourceRef(ctx, sys)
	require.NoError(t, err)

	col1, sink1 := stream.Collect[int]()
	h1, err := ref.Source(sys).To(sink1).Run(ctx, sys)
	require.NoError(t, err)
	<-h1.Done()
	require.NoError(t, h1.Err())
	assert.Equal(t, []int{1, 2, 3}, col1.Items())

	// Endpoint is now in terminated state (still alive for the grace window).
	col2, sink2 := stream.Collect[int]()
	h2, err := ref.Source(sys).To(sink2).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h2.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("post-completion subscribe did not terminate")
	}
	require.Error(t, h2.Err())
	assert.Contains(t, h2.Err().Error(), "already consumed")
	assert.Empty(t, col2.Items())
}

// TestSourceRef_EndpointDeathSurfacedAsError verifies that an unexpected
// endpoint actor death after a successful subscribe surfaces a stream-level
// error on the consumer. This covers the actor.Terminated unexpected-death
// branch in remoteSourceBridgeActor.
func TestSourceRef_EndpointDeathSurfacedAsError(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	// Tick is effectively-infinite so the bridge cannot finish on its own
	// before we kill the endpoint.
	ref, err := stream.Tick(50 * time.Millisecond).SourceRef(ctx, sys)
	require.NoError(t, err)

	_, sink := stream.Collect[time.Time]()
	h, err := ref.Source(sys).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	// Wait for the bridge to subscribe and start watching the endpoint.
	pause.For(150 * time.Millisecond)

	endpoint, err := sys.ActorOf(ctx, ref.Name)
	require.NoError(t, err)
	require.NoError(t, endpoint.Shutdown(ctx))

	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("source ref did not surface endpoint death")
	}
	require.Error(t, h.Err())
	assert.Contains(t, h.Err().Error(), "endpoint terminated before completion")
}

// TestSourceRef_LargePayloadDrainsBuffered exercises the endpoint's pending
// buffer / drainPending path. With enough elements in flight, the sub-pipeline
// will deliver mergeSubValue before each streamRequestWire refill arrives, so
// values pile up briefly and are drained when credit lands.
func TestSourceRef_LargePayloadDrainsBuffered(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	const n = 1000
	values := make([]int, n)
	for i := range values {
		values[i] = i
	}

	ref, err := stream.Of(values...).SourceRef(ctx, sys)
	require.NoError(t, err)

	col, sink := stream.Collect[int]()
	h, err := ref.Source(sys).To(sink).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(15 * time.Second):
		t.Fatal("source ref did not complete")
	}
	require.NoError(t, h.Err())

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(col.Items()) < n {
		pause.For(20 * time.Millisecond)
	}
	require.Len(t, col.Items(), n)
	for i, v := range col.Items() {
		require.Equal(t, i, v)
	}
}

// TestSinkRef_NonexistentEndpointFails verifies that resolving a SinkRef
// whose endpoint actor does not exist surfaces a descriptive error. This
// covers the resolveEndpoint failure branch in remoteSinkBridgeActor.
func TestSinkRef_NonexistentEndpointFails(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	bogus := stream.SinkRef[int]{
		Name: "sink-ref-missing",
		Host: sys.Host(),
		Port: sys.Port(),
	}

	h, err := stream.From(stream.Of(1, 2, 3)).To(bogus.Sink(sys)).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(15 * time.Second):
		t.Fatal("sink ref did not surface resolve error")
	}
	require.Error(t, h.Err())
	assert.Contains(t, h.Err().Error(), "resolve sink ref")
}

// TestSinkRef_LateSubscribeWhileActiveRejected verifies that a second
// producer attempting to subscribe to a SinkRef whose first subscriber is
// still active receives an "already subscribed" stream-level error. This
// covers the subscriber-already-set branch in sinkRefEndpointActor and the
// streamErrorWire reception path in remoteSinkBridgeActor.
func TestSinkRef_LateSubscribeWhileActiveRejected(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	_, sink := stream.Collect[int]()
	ref, err := sink.SinkRef(ctx, sys)
	require.NoError(t, err)

	// First producer's source never completes — keeps the endpoint subscriber
	// active so the second subscribe lands while the first is still owning it.
	blocking := make(chan int)
	h1, err := stream.From(stream.FromChannel(blocking)).To(ref.Sink(sys)).Run(ctx, sys)
	require.NoError(t, err)
	t.Cleanup(func() {
		close(blocking)
		_ = h1.Stop(context.Background())
	})

	// Wait for the first subscribe to land.
	pause.For(150 * time.Millisecond)

	h2, err := stream.From(stream.Of(4, 5, 6)).To(ref.Sink(sys)).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h2.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("late sink ref subscribe did not terminate")
	}
	require.Error(t, h2.Err())
	assert.Contains(t, h2.Err().Error(), "already subscribed")
}

// TestSinkRef_RefillCreditAfterAck verifies that the endpoint refills wire
// credit on the producer once the local sink-side sub-pipeline acknowledges
// a quarter-window worth of consumed elements. This covers the subFeedAck
// case in sinkRefEndpointActor.
func TestSinkRef_RefillCreditAfterAck(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	col, sink := stream.Collect[int]()
	ref, err := sink.SinkRef(ctx, sys)
	require.NoError(t, err)

	// Send more than sinkRefAckThreshold (64) elements so at least one refill
	// cycle is observed.
	const n = 200
	values := make([]int, n)
	for i := range values {
		values[i] = i
	}

	h, err := stream.From(stream.Of(values...)).To(ref.Sink(sys)).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("sink ref did not complete")
	}
	require.NoError(t, h.Err())

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(col.Items()) < n {
		pause.For(20 * time.Millisecond)
	}
	require.Len(t, col.Items(), n)
}

// TestSourceRef_SerializableViaGoAktSerializer verifies that a SourceRef
// round-trips through the GoAkt CBOR serializer (the path used when refs
// are shipped between nodes inside any remote message).
func TestSourceRef_SerializableViaGoAktSerializer(t *testing.T) {
	sys := newTestSystem(t)
	ctx := context.Background()

	// Defensively register the ref types — tests run in any order and the
	// init() above only registers the wire envelopes.
	_ = remote.NewConfig("127.0.0.1", 0,
		remote.WithSerializables(new(stream.SourceRef[int]), new(stream.SinkRef[int])),
	)

	original, err := stream.Of(7, 8, 9).SourceRef(ctx, sys)
	require.NoError(t, err)

	ser := remote.DefaultCBORSerializer()
	encoded, err := ser.Serialize(&original)
	require.NoError(t, err)

	decoded, err := ser.Deserialize(encoded)
	require.NoError(t, err)
	assert.Equal(t, original.Name, decoded.(*stream.SourceRef[int]).Name)

	// Resolve through the decoded ref to confirm it points at the live endpoint.
	col, sink := stream.Collect[int]()
	h, err := decoded.(*stream.SourceRef[int]).Source(sys).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-h.Done()
	require.NoError(t, h.Err())
	assert.Equal(t, []int{7, 8, 9}, col.Items())
}

// TestRefs_CrossNode validates SourceRef and SinkRef across two real
// cluster-enabled actor systems. Each subtest builds its own cluster pair
// so they cannot interfere with each other.
func TestRefs_CrossNode(t *testing.T) {
	t.Run("SourceRef", func(t *testing.T) {
		sysA, sysB := newClusterPair(t,
			remote.WithSerializables(new(int)),
		)
		ctx := context.Background()

		ref, err := stream.Of(1, 2, 3, 4, 5).SourceRef(ctx, sysA)
		require.NoError(t, err)

		col, sink := stream.Collect[int]()
		h, err := ref.Source(sysB).To(sink).Run(ctx, sysB)
		require.NoError(t, err)

		select {
		case <-h.Done():
		case <-time.After(30 * time.Second):
			t.Fatal("cross-node source ref did not complete")
		}
		require.NoError(t, h.Err())
		assert.Equal(t, []int{1, 2, 3, 4, 5}, col.Items())
	})

	t.Run("SinkRef", func(t *testing.T) {
		sysA, sysB := newClusterPair(t,
			remote.WithSerializables(new(int)),
		)
		ctx := context.Background()

		col, sink := stream.Collect[int]()
		ref, err := sink.SinkRef(ctx, sysA)
		require.NoError(t, err)

		h, err := stream.From(stream.Of(10, 20, 30, 40, 50)).
			To(ref.Sink(sysB)).
			Run(ctx, sysB)
		require.NoError(t, err)

		select {
		case <-h.Done():
		case <-time.After(30 * time.Second):
			t.Fatal("cross-node sink ref did not complete")
		}
		require.NoError(t, h.Err())

		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) && len(col.Items()) < 5 {
			pause.For(20 * time.Millisecond)
		}
		assert.Equal(t, []int{10, 20, 30, 40, 50}, col.Items())
	})
}
