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

package stream

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/actor"
)

func TestPullSourceActor_NaturalCompletion(t *testing.T) {
	// Verifies that a finite Of source completes naturally and delivers all elements.
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	input := make([]int, 300)
	for i := range input {
		input[i] = i
	}
	col, sink := Collect[int]()
	handle, err := Of(input...).To(sink).Run(ctx, sys)
	require.NoError(t, err)
	<-handle.Done()
	assert.NotNil(t, col.Items())
}

func TestPullSourceActor_Cancel_ViaStop(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	// Use a large Unfold that runs indefinitely so we can cancel it.
	// Ignore sink never blocks, so the cancel propagates cleanly.
	handle, err := Unfold(0, func(s int) (int, int, bool) {
		return s + 1, s, true // infinite
	}).To(Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)

	time.Sleep(20 * time.Millisecond)
	require.NoError(t, handle.Stop(ctx))

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not stop after cancel")
	}
}

// wrongTypePullActor responds to PullRequest with a string instead of PullResponse[int].
type wrongTypePullActor struct{}

func (a *wrongTypePullActor) PreStart(_ *actor.Context) error { return nil }
func (a *wrongTypePullActor) PostStop(_ *actor.Context) error { return nil }
func (a *wrongTypePullActor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *PullRequest:
		ctx.Response("wrong type") // not *PullResponse[int]
	default:
		ctx.Unhandled()
	}
}

func TestActorSourceActor_FetchErr_WrongType(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	pid, err := sys.Spawn(ctx, "wrong-type-pull", &wrongTypePullActor{})
	require.NoError(t, err)

	handle, err := FromActor[int](pid).To(Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("stream did not terminate after fetchErr")
	}
}

// silentPullActor never responds to PullRequest, causing actor.Ask to time out.
type silentPullActor struct{}

func (a *silentPullActor) PreStart(_ *actor.Context) error { return nil }
func (a *silentPullActor) PostStop(_ *actor.Context) error { return nil }
func (a *silentPullActor) Receive(ctx *actor.ReceiveContext) {
	ctx.Unhandled() // intentionally ignore all messages
}

func TestActorSourceActor_FetchErr_Timeout(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	pid, err := sys.Spawn(ctx, "silent-pull", &silentPullActor{})
	require.NoError(t, err)

	// Build a source with very short pull timeout so actor.Ask times out.
	config := defaultStageConfig()
	config.PullTimeout = 50 * time.Millisecond
	desc := &stage{
		id:   newStageID(),
		kind: sourceKind,
		actorFn: func(cfg StageConfig) actor.Actor {
			return newActorSourceActor[int](pid, cfg)
		},
		config: config,
	}
	src := Source[int]{stages: []*stage{desc}}

	handle, err := src.To(Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not terminate after fetch timeout")
	}
}

// stalledPullActor responds to the first PullRequest with elements, then stalls
// so the source actor is waiting for a second pull when we kill the upstream.
type stalledPullActor struct {
	calls int
}

func (a *stalledPullActor) PreStart(_ *actor.Context) error { return nil }
func (a *stalledPullActor) PostStop(_ *actor.Context) error { return nil }
func (a *stalledPullActor) Receive(ctx *actor.ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *PullRequest:
		a.calls++
		if a.calls == 1 {
			// First pull: send some elements so the source actor sends them downstream
			// and then issues a second pull.
			elems := make([]int, msg.N)
			for i := range elems {
				elems[i] = i
			}
			ctx.Response(&PullResponse[int]{Elements: elems})
		}
		// Second pull: don't respond — the actor will be stopped externally.
	default:
		ctx.Unhandled()
	}
}

func TestActorSourceActor_Terminated(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	pid, err := sys.Spawn(ctx, "stalled-pull", &stalledPullActor{})
	require.NoError(t, err)

	handle, err := FromActor[int](pid).To(Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)

	// Let the first batch be consumed, then kill the upstream actor.
	// The actorSourceActor is watching it via rctx.Watch and should receive
	// actor.Terminated, which propagates a streamError downstream.
	require.Eventually(t, func() bool {
		return handle.Metrics().ElementsIn > 0
	}, 3*time.Second, 5*time.Millisecond)

	require.NoError(t, pid.Shutdown(ctx))

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not terminate after upstream actor was killed")
	}
}

// TestCombineSourceActor_TypeMismatch injects a wrong-typed value into the left
// buffer of a combineSourceActor so that the type assertion in tryEmit fails,
// exercises the defensive streamError + shutdown path.
func TestCombineSourceActor_TypeMismatch(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	downPID, err := sys.Spawn(ctx, "combine-tm-down", &dummyStageActor{})
	require.NoError(t, err)

	// combineSourceActor[int, string, string]: left expects int, right expects string.
	// Both inputs are idle channel sources, so the only values the actor sees
	// are the ones injected below.
	cfg := defaultStageConfig()
	cfg.System = sys
	a := newCombineSourceActor(
		FromChannel(make(chan int)).stages, FromChannel(make(chan string)).stages,
		func(_ int, s string) string { return s },
		cfg,
	)
	actorPID, err := sys.Spawn(ctx, "combine-tm-actor", a)
	require.NoError(t, err)

	require.NoError(t, actor.Tell(ctx, actorPID, &stageWire{
		subID: "unit", upstream: nil, downstream: downPID,
	}))
	time.Sleep(20 * time.Millisecond)

	// Signal demand first so tryEmit will attempt to emit.
	require.NoError(t, actor.Tell(ctx, actorPID, &streamRequest{subID: "unit", n: 5}))
	time.Sleep(5 * time.Millisecond)

	// Inject wrong-typed value into left slot (expects int, receives string).
	require.NoError(t, actor.Tell(ctx, actorPID, &mergeSubValue{slot: 0, value: "not-an-int"}))
	// Inject a valid right value.
	require.NoError(t, actor.Tell(ctx, actorPID, &mergeSubValue{slot: 1, value: "valid"}))

	// The actor detects the type mismatch in tryEmit, sends streamError downstream,
	// and shuts down.
	require.Eventually(t, func() bool {
		_, err := sys.ActorOf(ctx, actorPID.Name())
		return err != nil
	}, 2*time.Second, 5*time.Millisecond)
}

// infinitePullActor always returns elements so we can cancel mid-stream.
type infinitePullActor struct{}

func (a *infinitePullActor) PreStart(_ *actor.Context) error { return nil }
func (a *infinitePullActor) PostStop(_ *actor.Context) error { return nil }
func (a *infinitePullActor) Receive(ctx *actor.ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *PullRequest:
		elems := make([]int, msg.N)
		for i := range elems {
			elems[i] = i
		}
		ctx.Response(&PullResponse[int]{Elements: elems})
	default:
		ctx.Unhandled()
	}
}

func TestActorSourceActor_Cancel(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	pid, err := sys.Spawn(ctx, "infinite-pull", &infinitePullActor{})
	require.NoError(t, err)

	handle, err := FromActor[int](pid).To(Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)

	time.Sleep(20 * time.Millisecond)
	require.NoError(t, handle.Stop(ctx))

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("actor source did not stop after cancel")
	}
}

// streamActorNames returns the names of the stream actors (coordinators and
// stages) currently running in sys.
func streamActorNames(t *testing.T, sys actor.ActorSystem) []string {
	t.Helper()
	pids, err := sys.Actors(context.Background(), time.Second)
	require.NoError(t, err)

	var names []string
	for _, pid := range pids {
		if strings.HasPrefix(pid.Name(), "stream-") {
			names = append(names, pid.Name())
		}
	}

	return names
}

// TestFanIn_InputFailure_FailsTheStream verifies that a failed input of a
// fan-in source surfaces as the stream's terminal error instead of being
// reported as a normal completion of that input.
func TestFanIn_InputFailure_FailsTheStream(t *testing.T) {
	sentinel := errors.New("input failed")
	healthy := func() Source[int] { return Of(0, 1, 2) }
	failing := func() Source[int] {
		return Via(Of(3, 4, 5), TryMap(func(n int) (int, error) {
			if n == 4 {
				return 0, sentinel
			}
			return n, nil
		}))
	}

	testCases := []struct {
		name  string
		graph func() RunnableGraph
	}{
		{name: "Merge", graph: func() RunnableGraph { return Merge(healthy(), failing()).To(Ignore[int]()) }},
		{name: "Concat", graph: func() RunnableGraph { return Concat(healthy(), failing()).To(Ignore[int]()) }},
		{name: "Zip", graph: func() RunnableGraph { return Zip(healthy(), failing()).To(Ignore[[]int]()) }},
		{name: "Combine", graph: func() RunnableGraph {
			return Combine(healthy(), failing(), func(l, r int) int { return l + r }).To(Ignore[int]())
		}},
		{name: "MergeLatest", graph: func() RunnableGraph { return MergeLatest(healthy(), failing()).To(Ignore[[]int]()) }},
		{name: "MergePreferred", graph: func() RunnableGraph { return MergePreferred(0, healthy(), failing()).To(Ignore[int]()) }},
		{name: "MergeSequence", graph: func() RunnableGraph {
			return MergeSequence(func(n int) int64 { return int64(n) }, healthy(), failing()).To(Ignore[int]())
		}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sys := newInternalTestSystem(t)

			handle, err := tc.graph().Run(context.Background(), sys)
			require.NoError(t, err)

			waitDone(t, handle, 5*time.Second)
			require.ErrorIs(t, handle.Err(), sentinel)
		})
	}
}

// TestFanIn_Abort_StopsInputPipelines verifies that aborting a stream whose
// source is a fan-in stage also stops the input pipelines that stage
// materialized, even when those inputs are idle.
func TestFanIn_Abort_StopsInputPipelines(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	left := make(chan int)
	right := make(chan int)
	handle, err := Merge(FromChannel(left), FromChannel(right)).To(Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)

	// outer pipeline: coordinator + 2 stages; each input: coordinator + 2 stages.
	require.Eventually(t, func() bool {
		return len(streamActorNames(t, sys)) == 9
	}, 5*time.Second, 10*time.Millisecond)

	handle.Abort()
	waitDone(t, handle, 5*time.Second)

	require.Eventually(t, func() bool {
		return len(streamActorNames(t, sys)) == 0
	}, 5*time.Second, 10*time.Millisecond, "input pipelines outlived the aborted stream")
}

// TestFanIn_InputMaterializationFailure_FailsTheStream verifies that an input
// that cannot be materialized fails the stream instead of leaving it waiting
// for an input that never started.
func TestFanIn_InputMaterializationFailure_FailsTheStream(t *testing.T) {
	sys := newInternalTestSystem(t)

	// An input without a source stage is rejected by the materializer.
	invalid := Source[int]{}
	handle, err := Merge(Of(1, 2), invalid).To(Ignore[int]()).Run(context.Background(), sys)
	require.NoError(t, err)

	waitDone(t, handle, 5*time.Second)
	require.ErrorIs(t, handle.Err(), ErrInvalidGraph)
}

// TestChanSourceActor_ReadsOnlyAgainstDemand verifies that the channel source
// takes values off its channel only as downstream demand allows, leaving the
// rest in the channel so the producer feels the backpressure.
func TestChanSourceActor_ReadsOnlyAgainstDemand(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	ch := make(chan int, 10)
	for i := range 10 {
		ch <- i
	}

	srcPID, _, down := spawnStageUnderTest(t, sys, "chan-demand", newChanSourceActor(ch, defaultStageConfig()))

	require.NoError(t, actor.Tell(ctx, srcPID, &streamRequest{subID: "unit", n: 3}))
	for want := range 3 {
		assert.Equal(t, want, expectProbeMessage[*streamElement](t, down).value)
	}

	// The reader took exactly the three demanded values before it handed them
	// over, so by now the remaining seven are still in the channel.
	require.Len(t, ch, 7)

	// The next demand yields the next value and takes exactly one more.
	require.NoError(t, actor.Tell(ctx, srcPID, &streamRequest{subID: "unit", n: 1}))
	assert.Equal(t, 3, expectProbeMessage[*streamElement](t, down).value)
	require.Len(t, ch, 6)
}

// TestChanSourceActor_StopsReadingWhenStreamEnds verifies that once the stream
// has ended the source no longer takes values off its channel.
func TestChanSourceActor_StopsReadingWhenStreamEnds(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	ch := make(chan int)
	handle, err := FromChannel(ch).To(Ignore[int]()).Run(ctx, sys)
	require.NoError(t, err)

	ch <- 1
	require.NoError(t, handle.Stop(ctx))
	waitDone(t, handle, 5*time.Second)

	sourceName := fmt.Sprintf("stream-%s-0", handle.ID())
	require.Eventually(t, func() bool {
		_, err := sys.ActorOf(ctx, sourceName)
		return err != nil
	}, 5*time.Second, 10*time.Millisecond)

	// The source actor is gone, and its PostStop ended the reader, so nothing
	// is receiving on the channel: a send cannot proceed.
	select {
	case ch <- 2:
		t.Fatal("a value was taken off the channel after the stream ended")
	default:
	}
}

// TestFanIn_BoundsItsBufferWithoutDownstreamDemand verifies that a fan-in
// source does not drain its inputs while its downstream asks for nothing:
// each input is pulled for one demand window and then waits. Once downstream
// asks, elements flow and the inputs are pulled again.
//
// MergeLatest is not covered: it keeps only the latest value of each input,
// so it acknowledges every element on arrival and its inputs are never idle;
// what it holds is one value per input, which no pull count can show.
func TestFanIn_BoundsItsBufferWithoutDownstreamDemand(t *testing.T) {
	// countingInput is an endless source of 0, 1, 2, … that counts in produced
	// every element it has been asked for.
	countingInput := func(produced *atomic.Int64) Source[int] {
		return Unfold(0, func(s int) (int, int, bool) {
			produced.Add(1)
			return s + 1, s, true
		})
	}

	testCases := []struct {
		name string
		// inputs is the number of inputs that are pulled at the same time.
		inputs int
		build  func(produced *atomic.Int64) *stage
	}{
		{name: "Merge", inputs: 2, build: func(p *atomic.Int64) *stage {
			return Merge(countingInput(p), countingInput(p)).stages[0]
		}},
		{name: "Concat", inputs: 1, build: func(p *atomic.Int64) *stage {
			return Concat(countingInput(p), countingInput(p)).stages[0]
		}},
		{name: "Zip", inputs: 2, build: func(p *atomic.Int64) *stage {
			return Zip(countingInput(p), countingInput(p)).stages[0]
		}},
		{name: "ZipWith", inputs: 2, build: func(p *atomic.Int64) *stage {
			return ZipWith(func(s []int) int { return s[0] + s[1] }, countingInput(p), countingInput(p)).stages[0]
		}},
		{name: "Combine", inputs: 2, build: func(p *atomic.Int64) *stage {
			return Combine(countingInput(p), countingInput(p), func(l, r int) int { return l + r }).stages[0]
		}},
		{name: "MergePreferred", inputs: 2, build: func(p *atomic.Int64) *stage {
			return MergePreferred(0, countingInput(p), countingInput(p)).stages[0]
		}},
		{name: "MergePrioritized", inputs: 2, build: func(p *atomic.Int64) *stage {
			return MergePrioritized([]int{1, 1}, countingInput(p), countingInput(p)).stages[0]
		}},
		{name: "MergeSequence", inputs: 1, build: func(p *atomic.Int64) *stage {
			return MergeSequence(func(n int) int64 { return int64(n) }, countingInput(p)).stages[0]
		}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sys := newInternalTestSystem(t)
			ctx := context.Background()

			var produced atomic.Int64
			desc := tc.build(&produced)
			config := desc.config
			config.System = sys
			fanInPID, _, down := spawnStageUnderTest(t, sys, "fan-in-bound", desc.actorFn(config))

			// Every active input is pulled for exactly one window.
			window := int64(tc.inputs) * defaultInitialDemand
			require.Eventually(t, func() bool {
				return produced.Load() >= window
			}, 5*time.Second, 5*time.Millisecond)

			// Nothing can signal that the inputs stay idle, so this is a
			// timed check: with no downstream demand the count must not move.
			require.Never(t, func() bool {
				return produced.Load() > window
			}, 300*time.Millisecond, 10*time.Millisecond, "inputs were pulled without downstream demand")

			// Downstream demand drains the buffer and pulls the inputs again.
			const demand = 1000
			require.NoError(t, actor.Tell(ctx, fanInPID, &streamRequest{subID: "unit", n: demand}))
			for range demand {
				expectProbeMessage[*streamElement](t, down)
			}

			require.Greater(t, produced.Load(), window)
			require.NoError(t, fanInPID.Shutdown(ctx))
		})
	}
}

// TestFanIn_AbortRightAfterRun_StopsInputPipelines verifies that a fan-in
// stream aborted while its source stage is still materializing its inputs
// leaves no input pipeline behind.
func TestFanIn_AbortRightAfterRun_StopsInputPipelines(t *testing.T) {
	// idle returns n channel sources that never produce.
	idle := func(n int) []Source[int] {
		sources := make([]Source[int], n)
		for i := range sources {
			sources[i] = FromChannel(make(chan int))
		}

		return sources
	}

	const inputs = 8
	testCases := map[string]func() RunnableGraph{
		"Merge":          func() RunnableGraph { return Merge(idle(inputs)...).To(Ignore[int]()) },
		"Concat":         func() RunnableGraph { return Concat(idle(inputs)...).To(Ignore[int]()) },
		"Zip":            func() RunnableGraph { return Zip(idle(inputs)...).To(Ignore[[]int]()) },
		"MergeLatest":    func() RunnableGraph { return MergeLatest(idle(inputs)...).To(Ignore[[]int]()) },
		"MergePreferred": func() RunnableGraph { return MergePreferred(0, idle(inputs)...).To(Ignore[int]()) },
		"MergePrioritized": func() RunnableGraph {
			return MergePrioritized(make([]int, inputs), idle(inputs)...).To(Ignore[int]())
		},
		"MergeSequence": func() RunnableGraph {
			return MergeSequence(func(n int) int64 { return int64(n) }, idle(inputs)...).To(Ignore[int]())
		},
		"Combine": func() RunnableGraph {
			sources := idle(2)
			return Combine(sources[0], sources[1], func(l, r int) int { return l + r }).To(Ignore[int]())
		},
	}

	for name, build := range testCases {
		t.Run(name, func(t *testing.T) {
			sys := newInternalTestSystem(t)

			for range 30 {
				handle, err := build().Run(context.Background(), sys)
				require.NoError(t, err)
				handle.Abort()
			}

			requireNoStreamActors(t, sys)
		})
	}
}

// TestFanIn_InputMaterializationFailure_AllStages verifies, for every fan-in
// source, that an input that cannot be materialized fails the stream.
func TestFanIn_InputMaterializationFailure_AllStages(t *testing.T) {
	// An input without a source stage is rejected by the materializer.
	invalid := Source[int]{}
	valid := func() Source[int] { return FromChannel(make(chan int)) }

	testCases := map[string]func() RunnableGraph{
		"Concat":         func() RunnableGraph { return Concat(invalid, valid()).To(Ignore[int]()) },
		"Zip":            func() RunnableGraph { return Zip(valid(), invalid).To(Ignore[[]int]()) },
		"MergeLatest":    func() RunnableGraph { return MergeLatest(valid(), invalid).To(Ignore[[]int]()) },
		"MergePreferred": func() RunnableGraph { return MergePreferred(0, valid(), invalid).To(Ignore[int]()) },
		"MergeSequence": func() RunnableGraph {
			return MergeSequence(func(n int) int64 { return int64(n) }, valid(), invalid).To(Ignore[int]())
		},
		"Combine": func() RunnableGraph {
			return Combine(valid(), invalid, func(l, r int) int { return l + r }).To(Ignore[int]())
		},
	}

	for name, build := range testCases {
		t.Run(name, func(t *testing.T) {
			sys := newInternalTestSystem(t)

			handle, err := build().Run(context.Background(), sys)
			require.NoError(t, err)

			waitDone(t, handle, 5*time.Second)
			require.ErrorIs(t, handle.Err(), ErrInvalidGraph)
			requireNoStreamActors(t, sys)
		})
	}
}

// TestFanIn_ManyFastInputs_KeepsFlowing verifies that a Merge fed by more
// fast inputs than the dispatcher has workers keeps delivering and can be
// stopped: the inputs' sinks must never block a worker on the fan-in's
// mailbox.
func TestFanIn_ManyFastInputs_KeepsFlowing(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	inputs := max(16, 2*runtime.GOMAXPROCS(0))
	sources := make([]Source[int], inputs)
	for i := range sources {
		sources[i] = Unfold(0, func(s int) (int, int, bool) { return s + 1, s, true })
	}

	var received atomic.Int64
	handle, err := Merge(sources...).To(ForEach(func(int) { received.Add(1) })).Run(ctx, sys)
	require.NoError(t, err)

	// Well past what the inputs' first windows alone could deliver, sampled twice.
	first := int64(inputs) * defaultInitialDemand * 2
	require.Eventually(t, func() bool { return received.Load() > first }, 10*time.Second, time.Millisecond)
	second := received.Load() + first
	require.Eventually(t, func() bool { return received.Load() > second }, 10*time.Second, time.Millisecond)

	stopped := make(chan error, 1)
	go func() {
		stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		stopped <- handle.Stop(stopCtx)
	}()

	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return")
	}

	requireNoStreamActors(t, sys)
}
