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

// Package main is a living sample for github.com/Tochemey/goakt/issues/1315:
// every spawned actor retained eager bookkeeping allocations it never used,
// and the spawn path allocated objects that were immediately discarded, which
// multiplied into a measurable heap floor and recurring GC cost at large
// resident populations.
//
// The sample reruns the issue's reproduction and guards the fix with
// deterministic object-count thresholds: the resident footprint of idle
// actors (flat spawns and SpawnChild populations) and the allocation and GC
// cycle cost of spawn/stop churn. Each guard sits between the measured value
// before the fix and the measured value after it, so a regression on any of
// the removed allocations fails the run.
package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
)

// flatPopulation is the number of idle top-level actors measured.
const flatPopulation = 50_000

// childParents and childrenPerParent shape the SpawnChild population.
const childParents = 50

// childrenPerParent is the number of children each parent spawns.
const childrenPerParent = 1_000

// churnCycles is the number of spawn/stop cycles measured.
const churnCycles = 20_000

// maxFlatObjectsPerActor sits between the 17 objects a flat idle actor
// retains after the fix and the 19 it retained before.
const maxFlatObjectsPerActor = 18.0

// maxChildObjectsPerActor sits between the 19 objects an idle child retains
// after the fix and the 25 it retained before.
const maxChildObjectsPerActor = 21.0

// maxChurnObjectsPerCycle sits between the roughly 287 objects a spawn/stop
// cycle allocates after the fix and the roughly 312 it allocated before.
const maxChurnObjectsPerCycle = 300.0

// noop is the smallest possible actor: no state, no message handling.
type noop struct{}

// PreStart implements actor.Actor.
func (n *noop) PreStart(*actor.Context) error { return nil }

// Receive implements actor.Actor.
func (n *noop) Receive(*actor.ReceiveContext) {}

// PostStop implements actor.Actor.
func (n *noop) PostStop(*actor.Context) error { return nil }

// heap runs a GC and returns the current live-heap bytes and object count.
func heap() (uint64, uint64) {
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapInuse, m.HeapObjects
}

// startSystem creates and starts a quiet actor system.
func startSystem(ctx context.Context, name string) actor.ActorSystem {
	system, err := actor.NewActorSystem(name, actor.WithLogger(log.DiscardLogger))
	if err != nil {
		panic(err)
	}

	if err := system.Start(ctx); err != nil {
		panic(err)
	}

	return system
}

// measureFlat reports the retained bytes and objects per idle top-level actor.
func measureFlat(ctx context.Context) (uint64, float64) {
	system := startSystem(ctx, "flat")
	defer func() { _ = system.Stop(ctx) }()

	baseBytes, baseObjects := heap()

	for i := range flatPopulation {
		if _, err := system.Spawn(ctx, fmt.Sprintf("a-%d", i), &noop{}, actor.WithLongLived()); err != nil {
			panic(err)
		}
	}

	liveBytes, liveObjects := heap()
	return (liveBytes - baseBytes) / flatPopulation, float64(liveObjects-baseObjects) / flatPopulation
}

// measureChildren reports the retained bytes and objects per idle child
// spawned through SpawnChild, the path that allocated a supervisor and a
// passivation strategy per child before the fix.
func measureChildren(ctx context.Context) (uint64, float64) {
	system := startSystem(ctx, "children")
	defer func() { _ = system.Stop(ctx) }()

	parents := make([]*actor.PID, childParents)

	for i := range childParents {
		pid, err := system.Spawn(ctx, fmt.Sprintf("p-%d", i), &noop{}, actor.WithLongLived())
		if err != nil {
			panic(err)
		}

		parents[i] = pid
	}

	baseBytes, baseObjects := heap()

	for i, parent := range parents {
		for j := range childrenPerParent {
			if _, err := parent.SpawnChild(ctx, fmt.Sprintf("c-%d-%d", i, j), &noop{}, actor.WithLongLived()); err != nil {
				panic(err)
			}
		}
	}

	liveBytes, liveObjects := heap()
	population := uint64(childParents * childrenPerParent)
	return (liveBytes - baseBytes) / population, float64(liveObjects-baseObjects) / float64(population)
}

// measureChurn reports the bytes and objects allocated per spawn/stop cycle
// and the number of GC cycles the churn triggered.
func measureChurn(ctx context.Context) (uint64, float64, uint32) {
	system := startSystem(ctx, "churn")
	defer func() { _ = system.Stop(ctx) }()

	runtime.GC()

	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	for i := range churnCycles {
		pid, err := system.Spawn(ctx, fmt.Sprintf("churn-%d", i), &noop{}, actor.WithLongLived())
		if err != nil {
			panic(err)
		}

		if err := pid.Shutdown(ctx); err != nil {
			panic(err)
		}
	}

	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	bytesPerCycle := (after.TotalAlloc - before.TotalAlloc) / churnCycles
	objectsPerCycle := float64(after.Mallocs-before.Mallocs) / churnCycles
	return bytesPerCycle, objectsPerCycle, after.NumGC - before.NumGC
}

func main() {
	ctx := context.Background()
	failed := false

	// check reports one guarded measurement and records a failure when the
	// measured value exceeds its threshold.
	check := func(name string, value, limit float64) {
		verdict := "ok"

		if value > limit {
			verdict = "REGRESSION"
			failed = true
		}

		fmt.Printf("  %-28s %8.1f (limit %.1f) %s\n", name, value, limit, verdict)
	}

	fmt.Println("struct sizes:")
	fmt.Printf("  PID:              %d bytes\n", unsafe.Sizeof(actor.PID{}))
	fmt.Printf("  ReceiveContext:   %d bytes\n", unsafe.Sizeof(actor.ReceiveContext{}))
	fmt.Printf("  UnboundedMailbox: %d bytes\n", unsafe.Sizeof(actor.UnboundedMailbox{}))

	// churn runs first so its GC cycle count is not dampened by the heap the
	// resident populations leave behind.
	churnBytes, churnObjects, gcCycles := measureChurn(ctx)
	fmt.Printf("\nspawn/stop churn (%d cycles): %d bytes per cycle, %d GC cycles\n", churnCycles, churnBytes, gcCycles)
	check("objects per cycle", churnObjects, maxChurnObjectsPerCycle)

	flatBytes, flatObjects := measureFlat(ctx)
	fmt.Printf("\nflat population (%d idle actors): %d bytes per actor\n", flatPopulation, flatBytes)
	check("objects per actor", flatObjects, maxFlatObjectsPerActor)

	childBytes, childObjects := measureChildren(ctx)
	fmt.Printf("\nchild population (%d idle children): %d bytes per child\n", childParents*childrenPerParent, childBytes)
	check("objects per child", childObjects, maxChildObjectsPerActor)

	if failed {
		fmt.Println("\nFAIL: at least one footprint guard regressed")
		os.Exit(1)
	}

	fmt.Println("\nPASS: resident footprint and churn allocation guards hold")
}
