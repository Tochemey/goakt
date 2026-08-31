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

// Package main is a living sample for github.com/Tochemey/goakt/issues/1322.
//
// It reproduces the issue's two defects at the OpenTelemetry layer without an
// SDK, using an in-process recording meter that captures how goakt registers
// each instrument and the values it exports during a scrape. This mirrors the
// issue's reproduction: create a metrics-enabled system, collect, and inspect
// the exported instruments.
//
// Defect 1, instrument types. Several instruments report an absolute current
// value that falls during normal operation (live actors, connected peers,
// child actors, stashed messages, uptime, time since last message), yet they
// were registered as monotonic Int64ObservableCounter, so an exporter that
// derives a rate or a delta treats every decrease as a counter reset. The
// sample asserts these instruments are now registered as Int64ObservableGauge
// and that the exported live actor count rises when actors are spawned and
// falls when they are stopped, the sequence a monotonic counter cannot report.
//
// Defect 2, deadletter scrape cost. The per-actor callback asked the deadletter
// actor for its count once per running actor per scrape, flooding a single
// mailbox at scale. It now asks once per scrape for a snapshot of all
// per-address counts. The deadletter actor is a system actor and is not
// reachable from an external program, so the per-scrape ask count is guarded by
// the actor-package test TestDeadletter; here the sample confirms the exported
// per-actor deadletter counts are still delivered correctly through the single
// snapshot, and that the system registers a constant two meter callbacks.
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"go.opentelemetry.io/otel"
	otelmetric "go.opentelemetry.io/otel/metric"
	noopmetric "go.opentelemetry.io/otel/metric/noop"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

const (
	// spawnedActors is the number of top-level worker actors spawned before
	// some are stopped to show the exported live actor count fall.
	spawnedActors = 10
	// stoppedActors is how many of the spawned workers are stopped.
	stoppedActors = 5
	// deadletterMessages is the number of messages sent to a black-hole actor
	// so its per-address deadletter count is non-zero.
	deadletterMessages = 6
	// expectedRegistrations is the constant number of meter callbacks a
	// non-cluster metrics-enabled system registers regardless of population.
	expectedRegistrations = 2
)

// gaugeInstruments lists the instruments that must be observable gauges because
// their value can fall during normal operation.
var gaugeInstruments = []string{
	"actorsystem.actors.count",
	"actorsystem.peers.count",
	"actorsystem.uptime",
	"actor.children.count",
	"actor.stash.size",
	"actor.uptime",
	"actor.last.received.duration",
}

// counterInstruments lists instruments that stay observable counters because
// they only accumulate.
var counterInstruments = []string{
	"actorsystem.deadletters.count",
	"actor.deadletters.count",
	"actor.processed.count",
	"actor.restart.count",
	"actor.failure.count",
	"actor.reinstate.count",
}

// worker is a minimal actor that handles every message, so it counts as running
// and processing during scrapes.
type worker struct{}

// PreStart implements actor.Actor.
func (w *worker) PreStart(*actor.Context) error { return nil }

// Receive implements actor.Actor.
func (w *worker) Receive(*actor.ReceiveContext) {}

// PostStop implements actor.Actor.
func (w *worker) PostStop(*actor.Context) error { return nil }

// blackhole marks every message unhandled, so each message sent to it becomes a
// deadletter recorded against its address.
type blackhole struct{}

// PreStart implements actor.Actor.
func (b *blackhole) PreStart(*actor.Context) error { return nil }

// Receive implements actor.Actor.
func (b *blackhole) Receive(ctx *actor.ReceiveContext) { ctx.Unhandled() }

// PostStop implements actor.Actor.
func (b *blackhole) PostStop(*actor.Context) error { return nil }

// gaugeInstrument wraps an observable gauge with its name so the recording
// observer can attribute observed values back to it.
type gaugeInstrument struct {
	otelmetric.Int64ObservableGauge
	name string
}

// counterInstrument wraps an observable counter with its name.
type counterInstrument struct {
	otelmetric.Int64ObservableCounter
	name string
}

// recordingMeter records the kind goakt registers each observable instrument as
// and captures the metric callbacks so the sample can drive a full scrape.
type recordingMeter struct {
	otelmetric.Meter
	kinds     map[string]string
	callbacks []otelmetric.Callback
}

// Int64ObservableGauge records the instrument as a gauge and returns a named
// wrapper.
func (m *recordingMeter) Int64ObservableGauge(name string, opts ...otelmetric.Int64ObservableGaugeOption) (otelmetric.Int64ObservableGauge, error) {
	inst, err := m.Meter.Int64ObservableGauge(name, opts...)
	if err != nil {
		return nil, err
	}

	m.kinds[name] = "gauge"
	return &gaugeInstrument{Int64ObservableGauge: inst, name: name}, nil
}

// Int64ObservableCounter records the instrument as a counter and returns a named
// wrapper.
func (m *recordingMeter) Int64ObservableCounter(name string, opts ...otelmetric.Int64ObservableCounterOption) (otelmetric.Int64ObservableCounter, error) {
	inst, err := m.Meter.Int64ObservableCounter(name, opts...)
	if err != nil {
		return nil, err
	}

	m.kinds[name] = "counter"
	return &counterInstrument{Int64ObservableCounter: inst, name: name}, nil
}

// RegisterCallback captures the callback so the sample can invoke a full scrape.
func (m *recordingMeter) RegisterCallback(cb otelmetric.Callback, _ ...otelmetric.Observable) (otelmetric.Registration, error) {
	m.callbacks = append(m.callbacks, cb)
	return noopmetric.Registration{}, nil
}

// recordingMeterProvider hands out its recordingMeter regardless of the
// requested meter name.
type recordingMeterProvider struct {
	otelmetric.MeterProvider
	meter *recordingMeter
}

// Meter implements otelmetric.MeterProvider.
func (p *recordingMeterProvider) Meter(string, ...otelmetric.MeterOption) otelmetric.Meter {
	return p.meter
}

// recordingObserver captures the values observed for each named instrument
// during a scrape.
type recordingObserver struct {
	otelmetric.Observer
	values map[string][]int64
}

// ObserveInt64 records the observed value under the instrument's name.
func (o *recordingObserver) ObserveInt64(inst otelmetric.Int64Observable, value int64, _ ...otelmetric.ObserveOption) {
	switch v := inst.(type) {
	case *gaugeInstrument:
		o.values[v.name] = append(o.values[v.name], value)
	case *counterInstrument:
		o.values[v.name] = append(o.values[v.name], value)
	}
}

// startSystem creates and starts a quiet metrics-enabled actor system.
func startSystem(ctx context.Context, name string) actor.ActorSystem {
	system, err := actor.NewActorSystem(name, actor.WithLogger(log.DiscardLogger), actor.WithMetrics())
	if err != nil {
		panic(err)
	}

	if err := system.Start(ctx); err != nil {
		panic(err)
	}

	return system
}

// scrape runs every captured metric callback once and returns the values
// observed, keyed by instrument name.
func scrape(ctx context.Context, meter *recordingMeter) map[string][]int64 {
	observer := &recordingObserver{Observer: noopmetric.Observer{}, values: make(map[string][]int64)}

	for _, cb := range meter.callbacks {
		if err := cb(ctx, observer); err != nil {
			panic(err)
		}
	}

	return observer.values
}

// single returns the sole value observed for name, or a boolean reporting that
// exactly one was not observed.
func single(values map[string][]int64, name string) (int64, bool) {
	observed := values[name]
	if len(observed) != 1 {
		return 0, false
	}

	return observed[0], true
}

// sum returns the total of every value observed for name.
func sum(values map[string][]int64, name string) int64 {
	var total int64
	for _, v := range values[name] {
		total += v
	}

	return total
}

func main() {
	ctx := context.Background()
	failed := false

	// check reports one guarded assertion and records a failure when it does
	// not hold.
	check := func(name string, ok bool, detail string) {
		verdict := "ok"

		if !ok {
			verdict = "FAIL"
			failed = true
		}

		fmt.Printf("  %-38s %-6s %s\n", name, verdict, detail)
	}

	delegate := noopmetric.NewMeterProvider()
	meter := &recordingMeter{Meter: delegate.Meter("recording"), kinds: make(map[string]string)}
	otel.SetMeterProvider(&recordingMeterProvider{MeterProvider: delegate, meter: meter})

	system := startSystem(ctx, "issue-1322")
	defer func() { _ = system.Stop(ctx) }()

	workers := make([]*actor.PID, spawnedActors)
	for i := range spawnedActors {
		pid, err := system.Spawn(ctx, fmt.Sprintf("worker-%d", i), &worker{}, actor.WithLongLived())
		if err != nil {
			panic(err)
		}

		workers[i] = pid
	}

	hole, err := system.Spawn(ctx, "blackhole", &blackhole{}, actor.WithLongLived())
	if err != nil {
		panic(err)
	}

	for range deadletterMessages {
		if err := actor.Tell(ctx, hole, new(testpb.TestSend)); err != nil {
			panic(err)
		}
	}

	// let the deadletters reach the deadletter actor before the first scrape.
	time.Sleep(time.Second)

	afterSpawn := scrape(ctx, meter)

	fmt.Println("instrument types (values that fall must be gauges):")
	for _, name := range gaugeInstruments {
		check(name, meter.kinds[name] == "gauge", meter.kinds[name])
	}

	for _, name := range counterInstruments {
		check(name, meter.kinds[name] == "counter", meter.kinds[name])
	}

	fmt.Println("\nexported live actor count rises then falls:")
	spawnedCount, okSpawned := single(afterSpawn, "actorsystem.actors.count")
	check("actors after spawn", okSpawned && spawnedCount == spawnedActors+1,
		fmt.Sprintf("%d actors", spawnedCount))

	for i := range stoppedActors {
		if err := workers[i].Shutdown(ctx); err != nil {
			panic(err)
		}
	}

	// let the live actor count settle after the stops before the next scrape.
	time.Sleep(time.Second)

	afterStop := scrape(ctx, meter)
	remainingCount, okRemaining := single(afterStop, "actorsystem.actors.count")
	check("actors after stop falls", okRemaining && remainingCount == spawnedCount-stoppedActors,
		fmt.Sprintf("%d actors", remainingCount))

	fmt.Println("\ndeadletter snapshot delivers per-actor counts, constant callbacks:")
	check("per-actor deadletters via snapshot", sum(afterSpawn, "actor.deadletters.count") == deadletterMessages,
		fmt.Sprintf("%d deadletters", sum(afterSpawn, "actor.deadletters.count")))
	check("constant meter registrations", len(meter.callbacks) == expectedRegistrations,
		fmt.Sprintf("%d registrations", len(meter.callbacks)))

	if failed {
		fmt.Println("\nFAIL: at least one metrics guard regressed")
		printKinds(meter.kinds)
		os.Exit(1)
	}

	fmt.Println("\nPASS: instruments are typed correctly and the scrape reads deadletters through one snapshot")
}

// printKinds dumps the recorded instrument kinds sorted by name, to help
// diagnose a failed run.
func printKinds(kinds map[string]string) {
	names := make([]string, 0, len(kinds))
	for name := range kinds {
		names = append(names, name)
	}

	sort.Strings(names)

	fmt.Println("\nrecorded instrument kinds:")
	for _, name := range names {
		fmt.Printf("  %-38s %s\n", name, kinds[name])
	}
}
