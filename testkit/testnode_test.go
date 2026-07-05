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
	"sort"
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

func TestTestNode(t *testing.T) {
	ctx := context.Background()
	multi := NewMultiNodes(t, log.DiscardLogger, []actor.Actor{&pinger{}}, nil)
	multi.Start()
	t.Cleanup(multi.Stop)

	node := multi.StartNode(ctx, "node-1")

	t.Run("NodeName", func(t *testing.T) {
		require.Equal(t, "node-1", node.NodeName())
	})

	t.Run("Spawn", func(t *testing.T) {
		name := "spawn-actor"
		node.Spawn(ctx, name, &pinger{})

		require.Eventually(t, func() bool {
			pid, err := node.ActorSystem().ActorOf(ctx, name)
			return err == nil && pid != nil
		}, time.Second, 10*time.Millisecond)
	})

	t.Run("SpawnSingleton", func(t *testing.T) {
		name := "singleton-actor"
		node.SpawnSingleton(ctx, name, &pinger{})

		require.Eventually(t, func() bool {
			pid, err := node.ActorSystem().ActorOf(ctx, name)
			return err == nil && pid != nil
		}, 2*time.Second, 10*time.Millisecond)
	})

	t.Run("SpawnProbe", func(t *testing.T) {
		target := "probe-target"
		node.Spawn(ctx, target, &pinger{})

		probe := node.SpawnProbe(ctx)
		probe.Send(target, new(testpb.TestPing))
		probe.ExpectMessage(new(testpb.TestPong))
		probe.ExpectNoMessage()
		probe.Stop()
	})

	t.Run("SpawnGrainProbe", func(t *testing.T) {
		target := node.GrainIdentity(ctx, "grain-probe-target", func(ctx context.Context) (actor.Grain, error) {
			return &grain{}, nil
		})

		probe := node.SpawnGrainProbe(ctx)
		// send a message to the grain to be tested
		probe.Send(target, new(testpb.TestPing))

		// here we expect no response from the grain
		probe.ExpectNoResponse()
	})

	t.Run("Subscribe", func(t *testing.T) {
		subscriber := node.Subscribe()
		require.NotNil(t, subscriber)
	})

	t.Run("Kill", func(t *testing.T) {
		name := "kill-actor"
		node.Spawn(ctx, name, &pinger{})
		node.Kill(ctx, name)

		require.Eventually(t, func() bool {
			_, err := node.ActorSystem().ActorOf(ctx, name)
			return err != nil
		}, 2*time.Second, 10*time.Millisecond)
	})

	t.Run("ActorSystem", func(t *testing.T) {
		sys := node.ActorSystem()
		require.NotNil(t, sys)
		require.True(t, sys.Running())
	})
}

func TestMultiNodes(t *testing.T) {
	ctx := context.Background()

	t.Run("NodeCount", func(t *testing.T) {
		multi := NewMultiNodes(t, log.DiscardLogger, []actor.Actor{&pinger{}}, nil)
		multi.Start()
		t.Cleanup(multi.Stop)

		require.Equal(t, 0, multi.NodeCount())

		multi.StartNode(ctx, "node-a")
		require.Equal(t, 1, multi.NodeCount())

		multi.StartNode(ctx, "node-b")
		require.Equal(t, 2, multi.NodeCount())
	})

	t.Run("GetNode", func(t *testing.T) {
		multi := NewMultiNodes(t, log.DiscardLogger, []actor.Actor{&pinger{}}, nil)
		multi.Start()
		t.Cleanup(multi.Stop)

		started := multi.StartNode(ctx, "get-node")
		retrieved := multi.GetNode("get-node")
		require.Equal(t, started.NodeName(), retrieved.NodeName())
	})

	t.Run("StopNode", func(t *testing.T) {
		multi := NewMultiNodes(t, log.DiscardLogger, []actor.Actor{&pinger{}}, nil)
		multi.Start()
		t.Cleanup(multi.Stop)

		multi.StartNode(ctx, "stop-node")
		require.Equal(t, 1, multi.NodeCount())

		multi.StopNode(ctx, "stop-node")
		require.Equal(t, 0, multi.NodeCount())
	})
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
	assertSingleDeliveryPerTick(t, preStopEvents, tickWindow)

	// takeover: stop one of the racing nodes mid-run and assert the single-fire
	// invariant still holds using only the survivors.
	multi.StopNode(ctx, "node-2")

	countBefore := len(preStopEvents)
	require.Eventually(t, func() bool {
		return len(sink.snapshot()) >= countBefore+2
	}, 15*time.Second, 200*time.Millisecond)

	postStopEvents := sink.snapshot()[countBefore:]
	assertSingleDeliveryPerTick(t, postStopEvents, tickWindow)
	for _, e := range postStopEvents {
		require.NotEqual(t, "node-2", e.node, "stopped node must not deliver after leaving")
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

// assertSingleDeliveryPerTick fails the test if two deliveries land closer together than half
// the cron period. Duplicates of the same tick arrive nearly simultaneously while consecutive
// ticks are a full period apart, so spacing distinguishes them without assuming which fixed
// window a delivery falls into (duplicates straddling a window boundary would evade a
// bucket-based check).
func assertSingleDeliveryPerTick(t *testing.T, events []fireEvent, period time.Duration) {
	t.Helper()

	sorted := make([]fireEvent, len(events))
	copy(sorted, events)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].at.Before(sorted[j].at) })

	for i := 1; i < len(sorted); i++ {
		gap := sorted[i].at.Sub(sorted[i-1].at)
		require.GreaterOrEqualf(t, gap, period/2,
			"deliveries %+v and %+v are only %s apart: duplicate delivery for a single tick", sorted[i-1], sorted[i], gap)
	}
}
