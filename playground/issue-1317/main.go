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

// Package main is a living sample for github.com/Tochemey/goakt/pull/1317:
// passivationHeap.Pop shrank the heap slice without clearing the popped slot,
// so the backing array kept a strong reference to every popped entry. Each
// entry pins its target PID, and the PID pins the actor instance, so
// passivated actors stayed reachable and were never reclaimed by the GC.
//
// The sample spawns a population of idle actors that each carry a ballast
// buffer and a runtime.AddCleanup hook, waits until every actor has been
// passivated, then forces GC and guards two invariants: nearly all actor
// instances must be reclaimed, and the heap retained over the post-start
// baseline must stay far below the ballast size. Before the fix zero
// instances were reclaimed and the full ballast stayed retained, so either
// guard failing means the retention has regressed.
package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/passivation"
)

// population is the number of idle actors spawned and passivated.
const population = 20_000

// ballastBytes is the payload each actor carries so retention is visible in
// heap terms: 20k actors pin at least 78 MB of ballast when leaked.
const ballastBytes = 4096

// passivationTimeout is the idle time after which every actor passivates.
const passivationTimeout = 2 * time.Second

// passivationDeadline bounds how long the sample waits for the whole
// population to passivate before giving up.
const passivationDeadline = 3 * time.Minute

// maxGCRounds bounds the GC settle loop that lets cleanup callbacks run.
const maxGCRounds = 20

// minReclaimed sits between the 0 instances reclaimed before the fix and the
// full population reclaimed after it; the slack tolerates cleanup callbacks
// that have not run yet when the guard is checked.
const minReclaimed = 19_000

// maxRetainedBytes sits between the roughly 113 MB retained before the fix
// and the roughly 5 MB retained after it.
const maxRetainedBytes = 32 << 20

// reclaimed counts actor instances whose cleanup callback has run, meaning
// the GC proved them unreachable.
var reclaimed atomic.Int64

// sleeper is an idle actor carrying ballast so retained instances are visible
// in heap measurements.
type sleeper struct {
	ballast []byte
}

// PreStart implements actor.Actor.
func (s *sleeper) PreStart(*actor.Context) error { return nil }

// Receive implements actor.Actor.
func (s *sleeper) Receive(*actor.ReceiveContext) {}

// PostStop implements actor.Actor.
func (s *sleeper) PostStop(*actor.Context) error { return nil }

// liveHeap runs a GC and returns the current live-heap bytes.
func liveHeap() uint64 {
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapInuse
}

// settle runs GC rounds until the reclaimed count stops growing, giving the
// runtime time to execute the cleanup callbacks of collected actors.
func settle() int64 {
	last := int64(-1)

	for range maxGCRounds {
		runtime.GC()
		time.Sleep(50 * time.Millisecond)

		current := reclaimed.Load()
		if current == population || current == last {
			break
		}
		last = current
	}

	return reclaimed.Load()
}

func main() {
	ctx := context.Background()

	system, err := actor.NewActorSystem("issue1317", actor.WithLogger(log.DiscardLogger))
	if err != nil {
		panic(err)
	}

	if err := system.Start(ctx); err != nil {
		panic(err)
	}

	baseline := liveHeap()
	fmt.Printf("baseline heap after start: %.2f MB\n", float64(baseline)/(1<<20))

	strategy := passivation.NewTimeBasedStrategy(passivationTimeout)

	for i := range population {
		s := &sleeper{ballast: make([]byte, ballastBytes)}
		runtime.AddCleanup(s, func(struct{}) { reclaimed.Add(1) }, struct{}{})

		if _, err := system.Spawn(ctx, fmt.Sprintf("sleeper-%d", i), s, actor.WithPassivationStrategy(strategy)); err != nil {
			panic(err)
		}
	}

	fmt.Printf("spawned %d actors, heap: %.2f MB\n", population, float64(liveHeap())/(1<<20))

	deadline := time.Now().Add(passivationDeadline)

	for system.NumActors() > 0 {
		if time.Now().After(deadline) {
			fmt.Printf("FAIL: %d actors still active after %s\n", system.NumActors(), passivationDeadline)
			os.Exit(1)
		}
		time.Sleep(100 * time.Millisecond)
	}

	collected := settle()
	retained := int64(liveHeap()) - int64(baseline)
	fmt.Printf("after passivation: reclaimed %d/%d actor instances, retained over baseline: %.2f MB\n", collected, population, float64(retained)/(1<<20))

	failed := false

	if collected < minReclaimed {
		fmt.Printf("FAIL: reclaimed %d actor instances, want at least %d; popped passivation entries are pinning passivated actors again\n", collected, minReclaimed)
		failed = true
	}

	if retained > maxRetainedBytes {
		fmt.Printf("FAIL: retained %d bytes over baseline, want at most %d\n", retained, int64(maxRetainedBytes))
		failed = true
	}

	if err := system.Stop(ctx); err != nil {
		panic(err)
	}

	if failed {
		os.Exit(1)
	}

	fmt.Println("PASS: passivated actors are reclaimable, the passivation heap releases popped entries")
}
