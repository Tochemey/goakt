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

// Reproduction for https://github.com/Tochemey/goakt/issues/1434
//
// MergeSubstreams must honor the demand of its downstream: a slow downstream
// holds the source back, whatever the overflow strategy of the substreams, and
// the stream holds a bounded number of elements per open substream. Nothing is
// dropped for it. This sample runs a source of
// 1,000,000 elements through GroupBy (four keys) and MergeSubstreams into a
// sink that blocks on its first element, and counts:
//
//   - pulled: the elements the source emitted;
//   - delivered: the elements the sink received;
//   - dropped: the elements the stream reported as dropped;
//   - held: pulled - delivered - dropped, the elements sitting inside the
//     stream while the sink is blocked.
//
// It runs two scenarios:
//
//   - default: the default substream buffer (256, BackpressureSource);
//   - DropTail: WithSubstreamBuffer(256, DropTail).
//
// It fails when:
//
//   - the stream holds more than heldLimit elements, or drops an element,
//     while the sink is blocked (both scenarios);
//   - once the sink is released, the default scenario does not deliver every
//     element, or the DropTail scenario neither delivers nor counts as dropped
//     an element. Running fast, DropTail may drop a few elements: the source
//     can feed one substream faster than the substream hands them on.
//
// Run it with:
//
//	go run ./playground/issue-1434
//
// Exit status 0 means every check held, 1 means one did not, and 2 means the
// setup failed.
package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/stream"
)

const (
	// total is the number of elements the source emits.
	total = 1_000_000
	// keys is the number of substreams GroupBy opens.
	keys = 4
	// substreamBuffer is the per-substream in-flight cap of the DropTail
	// scenario, the default cap.
	substreamBuffer = 256
	// heldLimit is the most elements the stream may hold while the sink is
	// blocked. A bounded stream holds a few demand windows (224 elements) per
	// substream, about 2,600 here; the limit leaves room above that.
	heldLimit = 8192
	// drainWait is how long the sample waits for the source to run dry while
	// the sink is blocked. A stream that honors downstream demand never does.
	drainWait = 3 * time.Second
	// settleWait lets the elements already pulled reach the place where they
	// are held or dropped.
	settleWait = 500 * time.Millisecond
	// finishWait bounds the wait for the stream to end once the sink is
	// released.
	finishWait = time.Minute
)

// scenario is one run of the stream.
type scenario struct {
	// name is printed in front of the scenario's lines.
	name string
	// configure applies the scenario's substream buffer settings.
	configure func(stream.SubFlow[int64, int64]) stream.SubFlow[int64, int64]
	// lossless tells that the scenario must deliver every element once the
	// sink is released.
	lossless bool
}

func main() {
	ctx := context.Background()

	system, err := actor.NewActorSystem("issue-1434", actor.WithLogger(log.DiscardLogger))
	if err != nil {
		fail("creating the actor system: %v", err)
	}

	if err := system.Start(ctx); err != nil {
		fail("starting the actor system: %v", err)
	}

	scenarios := []scenario{
		{
			name:      "default (BackpressureSource)",
			configure: func(sf stream.SubFlow[int64, int64]) stream.SubFlow[int64, int64] { return sf },
			lossless:  true,
		},
		{
			name: "DropTail",
			configure: func(sf stream.SubFlow[int64, int64]) stream.SubFlow[int64, int64] {
				return sf.WithSubstreamBuffer(substreamBuffer, stream.DropTail)
			},
		},
	}

	failed := false
	for _, s := range scenarios {
		failed = run(ctx, system, s) || failed
	}

	_ = system.Stop(ctx)

	if failed {
		fmt.Println("\nFAIL: MergeSubstreams did not honor the demand of its downstream")
		os.Exit(1)
	}

	fmt.Println("\nPASS: under a blocked downstream MergeSubstreams held the source back, held a bounded number of elements and dropped none")
}

// run executes one scenario and reports whether a check failed.
func run(ctx context.Context, system actor.ActorSystem, s scenario) bool {
	var pulled, delivered atomic.Int64
	// drained is closed when the source has emitted its last element.
	drained := make(chan types.Unit)
	// release unblocks the sink.
	release := make(chan types.Unit)

	source := stream.Via(stream.Range(0, total), stream.Map(func(n int64) int64 {
		if pulled.Add(1) == total {
			close(drained)
		}

		return n
	}))

	substreams := s.configure(stream.GroupBy(source, 0, func(n int64) int64 { return n % keys }))
	heapBefore := heapInUse()

	handle, err := stream.From(stream.MergeSubstreams(substreams)).
		To(stream.ForEach(func(int64) {
			delivered.Add(1)
			<-release
		})).
		Run(ctx, system)
	if err != nil {
		fail("running the stream: %v", err)
	}

	fmt.Printf("\n%s: %d elements, %d substreams, the sink blocks on its first element\n", s.name, total, keys)

	select {
	case <-drained:
		fmt.Println("  the source ran dry while the sink was blocked")
	case <-time.After(drainWait):
		fmt.Printf("  the source was held back: it had not run dry after %s\n", drainWait)
	}

	pause.For(settleWait)

	dropped := int64(handle.Metrics().DroppedElements)
	held := pulled.Load() - delivered.Load() - dropped
	heapGrowth := heapInUse() - heapBefore
	fmt.Printf("  pulled %d, delivered %d, dropped %d, held %d (heap grew by %.1f MB)\n", pulled.Load(), delivered.Load(), dropped, held, heapGrowth)

	failed := false
	if held > heldLimit {
		fmt.Printf("  BUG: the stream holds %d elements, more than the limit of %d\n", held, heldLimit)
		failed = true
	} else {
		fmt.Printf("  OK: the stream holds %d elements, within the limit of %d\n", held, heldLimit)
	}

	if dropped > 0 {
		fmt.Printf("  BUG: the stream dropped %d elements while the sink was blocked\n", dropped)
		failed = true
	}

	close(release)

	select {
	case <-handle.Done():
	case <-time.After(finishWait):
		fail("the stream did not end within %s of releasing the sink", finishWait)
	}

	if err := handle.Err(); err != nil {
		fmt.Printf("  BUG: the stream ended with an error: %v\n", err)
		failed = true
	}

	dropped = int64(handle.Metrics().DroppedElements)
	fmt.Printf("  sink released: delivered %d, dropped %d\n", delivered.Load(), dropped)

	if s.lossless && (dropped > 0 || delivered.Load() != total) {
		fmt.Printf("  BUG: the stream lost %d of %d elements\n", total-delivered.Load(), total)
		return true
	}

	if delivered.Load()+dropped != total {
		fmt.Printf("  BUG: %d of %d elements were neither delivered nor counted as dropped\n", total-delivered.Load()-dropped, total)
		return true
	}

	if s.lossless {
		fmt.Printf("  OK: the stream delivered all %d elements\n", total)
		return failed
	}

	fmt.Printf("  OK: every element was delivered or counted as dropped\n")
	return failed
}

// heapInUse returns the size of the live heap in MB after a collection.
func heapInUse() float64 {
	runtime.GC()

	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return float64(stats.HeapAlloc) / (1 << 20)
}

// fail reports a setup failure and exits with status 2.
func fail(format string, args ...any) {
	fmt.Printf("setup failed: "+format+"\n", args...)
	os.Exit(2)
}
