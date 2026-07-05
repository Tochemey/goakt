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

package testkit

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// fireEvent records one delivery observed by a fireRecorder actor during a
// cluster-single-fire test.
type fireEvent struct {
	node string
	at   time.Time
}

// fireSink collects fireEvents across every node's fireRecorder instance. All nodes in a
// MultiNodes test run in the same process, so a single sink shared by pointer across the
// per-node actor instances gives the test a cluster-wide view of deliveries.
type fireSink struct {
	mu     sync.Mutex
	events []fireEvent
}

func (s *fireSink) record(node string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, fireEvent{node: node, at: time.Now()})
}

func (s *fireSink) snapshot() []fireEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]fireEvent, len(s.events))
	copy(out, s.events)
	return out
}

// fireRecorder is a test actor that records every testpb.TestSend it receives into a
// shared fireSink, tagged with the node it was spawned on.
type fireRecorder struct {
	node string
	sink *fireSink
}

var _ actor.Actor = (*fireRecorder)(nil)

func (r *fireRecorder) PreStart(*actor.Context) error { return nil }
func (r *fireRecorder) PostStop(*actor.Context) error { return nil }
func (r *fireRecorder) Receive(ctx *actor.ReceiveContext) {
	if _, ok := ctx.Message().(*testpb.TestSend); ok {
		r.sink.record(r.node)
	}
}

// bucketByTick groups fire events into non-overlapping windows of the given size, keyed by
// the window start. Every event in a window is presumed to originate from the same
// underlying trigger tick.
func bucketByTick(events []fireEvent, window time.Duration) map[int64][]fireEvent {
	buckets := make(map[int64][]fireEvent)
	for _, e := range events {
		key := e.at.UnixNano() / int64(window)
		buckets[key] = append(buckets[key], e)
	}
	return buckets
}

// TestClusterSingleFireSchedule asserts that a cron schedule registered independently on
// every node of a cluster, with the same reference, is delivered exactly once per tick
// cluster-wide - single fire is intrinsic to cron schedules in cluster mode, no option
// required - and that this invariant survives one of the racing nodes leaving mid-run.
func TestClusterSingleFireSchedule(t *testing.T) {
	ctx := context.Background()

	multi := NewMultiNodes(t, log.DiscardLogger, []actor.Actor{&fireRecorder{}}, nil)
	multi.Start()
	t.Cleanup(multi.Stop)

	const (
		reference  = "cluster-single-fire-schedule-test"
		cronExpr   = "*/2 * * ? * *"
		tickWindow = 2 * time.Second
		actorName  = "fire-recorder"
	)

	sink := &fireSink{}
	nodeNames := []string{"node-1", "node-2", "node-3"}

	for _, name := range nodeNames {
		node := multi.StartNode(ctx, name)
		system := node.ActorSystem()

		// Actor names must be unique cluster-wide, so each node spawns its own
		// receiver under a node-scoped name. The schedules still race against each
		// other because they all share the same reference below.
		pid, err := system.Spawn(ctx, actorName+"-"+name, &fireRecorder{node: name, sink: sink})
		require.NoError(t, err)
		require.NotNil(t, pid)

		err = system.ScheduleWithCron(
			ctx, new(testpb.TestSend), pid, cronExpr,
			actor.WithReference(reference),
		)
		require.NoError(t, err)
	}

	// let several ticks elapse while all three nodes race for each one.
	require.Eventually(t, func() bool {
		return len(sink.snapshot()) >= 2
	}, 15*time.Second, 200*time.Millisecond)

	preStopEvents := sink.snapshot()
	assertAtMostOneDeliveryPerTick(t, preStopEvents, tickWindow)

	// takeover: stop one of the racing nodes mid-run and assert the single-fire
	// invariant still holds using only the survivors.
	multi.StopNode(ctx, "node-2")

	countBefore := len(preStopEvents)
	require.Eventually(t, func() bool {
		return len(sink.snapshot()) >= countBefore+2
	}, 15*time.Second, 200*time.Millisecond)

	postStopEvents := sink.snapshot()[countBefore:]
	assertAtMostOneDeliveryPerTick(t, postStopEvents, tickWindow)
	for _, e := range postStopEvents {
		require.NotEqual(t, "node-2", e.node, "stopped node must not deliver after leaving")
	}
}

// assertAtMostOneDeliveryPerTick fails the test if any tick window observed more than one
// delivery, which would mean cluster-wide single fire failed to arbitrate a duplicate.
func assertAtMostOneDeliveryPerTick(t *testing.T, events []fireEvent, window time.Duration) {
	t.Helper()

	buckets := bucketByTick(events, window)
	for tick, bucketEvents := range buckets {
		require.Lenf(t, bucketEvents, 1,
			"tick %d: expected exactly one delivery, got %d: %+v", tick, len(bucketEvents), bucketEvents)
	}
}

// TestNodeLocalIntervalSchedule asserts the kept node-local behavior for non-cron schedules:
// since interval RunTimes are anchored to each node's own registration clock and never align
// cluster-wide, an interval schedule never claims, so the same reference registered on every
// node must still deliver independently on every node instead of arbitrating to one winner.
func TestNodeLocalIntervalSchedule(t *testing.T) {
	ctx := context.Background()

	multi := NewMultiNodes(t, log.DiscardLogger, []actor.Actor{&fireRecorder{}}, nil)
	multi.Start()
	t.Cleanup(multi.Stop)

	const (
		reference = "node-local-interval-schedule-test"
		interval  = 500 * time.Millisecond
		actorName = "fire-recorder"
	)

	sink := &fireSink{}
	nodeNames := []string{"node-1", "node-2", "node-3"}

	for _, name := range nodeNames {
		node := multi.StartNode(ctx, name)
		system := node.ActorSystem()

		pid, err := system.Spawn(ctx, actorName+"-"+name, &fireRecorder{node: name, sink: sink})
		require.NoError(t, err)
		require.NotNil(t, pid)

		err = system.Schedule(
			ctx, new(testpb.TestSend), pid, interval,
			actor.WithReference(reference),
		)
		require.NoError(t, err)
	}

	// every node must accumulate its own deliveries independently; none arbitrates against
	// the others since interval schedules never claim.
	require.Eventually(t, func() bool {
		perNode := make(map[string]int)
		for _, e := range sink.snapshot() {
			perNode[e.node]++
		}
		for _, name := range nodeNames {
			if perNode[name] < 2 {
				return false
			}
		}
		return true
	}, 15*time.Second, 200*time.Millisecond)
}
