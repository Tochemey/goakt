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

package actor

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
	noopmetric "go.opentelemetry.io/otel/metric/noop"

	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// callbackCapturingMeter records the metric callbacks a metrics-enabled system
// registers so a test can drive a full scrape by invoking them directly.
type callbackCapturingMeter struct {
	otelmetric.Meter
	callbacks []otelmetric.Callback
}

// RegisterCallback captures the callback and returns a no-op registration.
func (m *callbackCapturingMeter) RegisterCallback(cb otelmetric.Callback, _ ...otelmetric.Observable) (otelmetric.Registration, error) {
	m.callbacks = append(m.callbacks, cb)
	return noopmetric.Registration{}, nil
}

// callbackCapturingMeterProvider hands out its callbackCapturingMeter regardless
// of the requested meter name.
type callbackCapturingMeterProvider struct {
	otelmetric.MeterProvider
	meter *callbackCapturingMeter
}

// Meter implements otelmetric.MeterProvider.
func (p *callbackCapturingMeterProvider) Meter(string, ...otelmetric.MeterOption) otelmetric.Meter {
	return p.meter
}

// TestRegisterMetricsAsksDeadletterOncePerScrape guards the fix for issue #1322:
// the per-actor metrics callback must ask the deadletter actor for its counts
// once per scrape, not once per running actor. The deadletter actor increments
// its processed message count before it replies, so the delta across a single
// scrape over a whole population is the number of asks the scrape issued.
func TestRegisterMetricsAsksDeadletterOncePerScrape(t *testing.T) {
	ctx := context.TODO()

	delegate := noopmetric.NewMeterProvider()
	meter := &callbackCapturingMeter{Meter: delegate.Meter("capture")}
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(&callbackCapturingMeterProvider{MeterProvider: delegate, meter: meter})
	defer otel.SetMeterProvider(previous)

	sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger), WithMetrics())
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))

	// spawn a population large enough that a per-actor ask would be obvious as
	// a delta far greater than one.
	const population = 20
	for i := range population {
		_, err := sys.Spawn(ctx, fmt.Sprintf("worker-%d", i), &MockActor{}, WithLongLived())
		require.NoError(t, err)
	}

	// let every actor process its PostStart so the per-actor callback observes
	// them and reaches the deadletter lookup.
	pause.For(time.Second)

	system, ok := sys.(*actorSystem)
	require.True(t, ok)
	deadletter := system.getDeadletter()
	require.NotNil(t, deadletter)

	// two callbacks are registered for a non-cluster system: the system-level
	// one and the per-actor one. Only the per-actor callback asks the
	// deadletter actor.
	require.Len(t, meter.callbacks, 2)

	observer := noopmetric.Observer{}
	before := deadletter.ProcessedCount()

	for _, callback := range meter.callbacks {
		require.NoError(t, callback(ctx, observer))
	}

	after := deadletter.ProcessedCount()
	require.EqualValues(t, 1, after-before, "one scrape over %d actors must ask the deadletter actor once", population)

	require.NoError(t, sys.Stop(ctx))
}

// TestRegisterMetricsObservesRuntimeCounters drives one full collection over a
// metrics-enabled system and asserts every observation the runtime counters
// add: the per-actor unhandled total, the per-kind spawn, stop and passivation
// totals, and the message.type breakdown of the per-actor deadletter total.
func TestRegisterMetricsObservesRuntimeCounters(t *testing.T) {
	ctx := context.Background()

	previous := otel.GetMeterProvider()
	meterProvider := newRecordingMeterProvider()
	otel.SetMeterProvider(meterProvider)
	t.Cleanup(func() { otel.SetMeterProvider(previous) })

	sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger), WithMetrics())
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))

	pid, err := sys.Spawn(ctx, "blackhole", &MockUnhandled{}, WithLongLived())
	require.NoError(t, err)
	require.NotNil(t, pid)

	pause.For(time.Second)

	// the actor rejects everything, so each message becomes both an unhandled
	// message and a deadletter of its own type
	for range 3 {
		require.NoError(t, Tell(ctx, pid, new(testpb.TestSend)))
	}

	for range 2 {
		require.NoError(t, Tell(ctx, pid, new(testpb.TestReply)))
	}

	require.Eventually(t, func() bool {
		return pid.unhandledCount.Load() == 5
	}, 3*time.Second, 20*time.Millisecond)

	// the deadletter actor records asynchronously, so wait for its registry to
	// carry every rejection before scraping
	pause.For(time.Second)

	observer := scrapeOnce(t, ctx, meterProvider)

	require.EqualValues(t, 5, groupRecordsByActor(observer.records)["blackhole"]["actor.unhandled.count"])

	require.Equal(t, map[string]int64{
		types.NameOf(new(testpb.TestSend)):  3,
		types.NameOf(new(testpb.TestReply)): 2,
	}, deadlettersByMessageType(observer.records, "blackhole"))

	kind := types.Name(&MockUnhandled{})
	lifecycle := lifecycleCountsByKind(observer.records, kind)
	require.EqualValues(t, 1, lifecycle["actor.spawned.count"])
	require.EqualValues(t, 0, lifecycle["actor.stopped.count"])
	require.EqualValues(t, 0, lifecycle["actor.passivated.count"])

	lifecycleAttrs := lifecycleAttributes(observer.records, kind)
	systemAttr, ok := lifecycleAttrs.Value(attribute.Key("actor.system"))
	require.True(t, ok)
	require.Equal(t, "testSys", systemAttr.AsString())

	// a shutdown moves the same kind's stopped total, and only that one
	require.NoError(t, pid.Shutdown(ctx))
	pause.For(500 * time.Millisecond)

	observer = scrapeOnce(t, ctx, meterProvider)
	lifecycle = lifecycleCountsByKind(observer.records, kind)
	require.EqualValues(t, 1, lifecycle["actor.spawned.count"])
	require.EqualValues(t, 1, lifecycle["actor.stopped.count"])
	require.EqualValues(t, 0, lifecycle["actor.passivated.count"])

	require.NoError(t, sys.Stop(ctx))
}

// TestRegisterMetricsAggregatesPerActorKind drives one full collection over a
// system started with WithLowCardinalityMetrics and asserts that the per-actor
// instruments are reported once per actor kind: the counters summed over the
// kind, actor.uptime reduced to the age of its oldest live actor,
// actor.last.received.duration to the time since it last processed a message,
// and actor.deadletters.count summed per kind and message type. No observation
// may name an individual actor.
func TestRegisterMetricsAggregatesPerActorKind(t *testing.T) {
	ctx := context.Background()

	previous := otel.GetMeterProvider()
	meterProvider := newRecordingMeterProvider()
	otel.SetMeterProvider(meterProvider)
	t.Cleanup(func() { otel.SetMeterProvider(previous) })

	sys, err := NewActorSystem("testSys",
		WithLogger(log.DiscardLogger),
		WithMetrics(WithLowCardinalityMetrics()))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))

	// the oldest worker is spawned first so the kind holds actors of visibly
	// different ages, which is what makes the max reduction on actor.uptime
	// distinguishable from a sum or a min.
	oldest, err := sys.Spawn(ctx, "worker-0", NewMockActor(), WithLongLived())
	require.NoError(t, err)

	pause.For(2 * time.Second)

	workers := []*PID{oldest}
	for i := 1; i < 3; i++ {
		worker, err := sys.Spawn(ctx, fmt.Sprintf("worker-%d", i), NewMockActor(), WithLongLived())
		require.NoError(t, err)
		workers = append(workers, worker)
	}

	// the second kind rejects everything it receives, so it feeds both the
	// unhandled total and the dead-letter breakdown.
	blackholes := make([]*PID, 0, 2)
	for i := range 2 {
		blackhole, err := sys.Spawn(ctx, fmt.Sprintf("blackhole-%d", i), &MockUnhandled{}, WithLongLived())
		require.NoError(t, err)
		blackholes = append(blackholes, blackhole)
	}

	pause.For(time.Second)

	for _, worker := range workers {
		for range 2 {
			require.NoError(t, Tell(ctx, worker, new(testpb.TestSend)))
		}
	}

	for _, blackhole := range blackholes {
		require.NoError(t, Tell(ctx, blackhole, new(testpb.TestSend)))
		require.NoError(t, Tell(ctx, blackhole, new(testpb.TestReply)))
	}

	require.Eventually(t, func() bool {
		return blackholes[0].unhandledCount.Load() == 2 && blackholes[1].unhandledCount.Load() == 2
	}, 3*time.Second, 20*time.Millisecond)

	// the deadletter actor records asynchronously, and the pause also ages every
	// actor's last processed message.
	pause.For(2 * time.Second)

	// only the oldest worker receives a late message, so the kind's minimum time
	// since a message was processed belongs to it alone.
	require.NoError(t, Tell(ctx, oldest, new(testpb.TestSend)))
	require.Eventually(t, func() bool {
		return oldest.ProcessedCount() == 4
	}, 3*time.Second, 20*time.Millisecond)

	observer := scrapeOnce(t, ctx, meterProvider)

	// no observation names an actor: that is the cardinality the mode buys.
	require.Empty(t, actorNamesFromRecords(observer.records))

	workerKind := types.Name(NewMockActor())
	workerCounts := aggregatedCountsByKind(observer.records, workerKind)

	// three workers, each past its PostStart, the oldest one message ahead
	require.EqualValues(t, 7, workerCounts["actor.processed.count"])
	require.EqualValues(t, 0, workerCounts["actor.children.count"])
	require.EqualValues(t, 0, workerCounts["actor.stash.size"])
	require.EqualValues(t, 0, workerCounts["actor.restart.count"])
	require.EqualValues(t, 0, workerCounts["actor.failure.count"])
	require.EqualValues(t, 0, workerCounts["actor.reinstate.count"])
	require.EqualValues(t, 0, workerCounts["actor.unhandled.count"])

	// actor.uptime is the age of the oldest live actor of the kind: above the
	// youngest one's age, and far below the sum of all three.
	var uptimeSum int64
	for _, worker := range workers {
		uptimeSum += worker.Uptime()
	}

	require.GreaterOrEqual(t, workerCounts["actor.uptime"], oldest.Uptime()-1)
	require.LessOrEqual(t, workerCounts["actor.uptime"], oldest.Uptime()+1)
	require.Greater(t, workerCounts["actor.uptime"], workers[1].Uptime())
	require.Less(t, workerCounts["actor.uptime"], uptimeSum)

	// actor.last.received.duration is the shortest of the kind: the late message
	// to the oldest worker, not the stale one the others last processed.
	require.Less(t, workerCounts["actor.last.received.duration"], workers[1].LatestProcessedDuration().Milliseconds())

	// a kind that dropped nothing still reports a single zero, without the
	// message.type attribute.
	deadletters, reported := workerCounts["actor.deadletters.count"]
	require.True(t, reported)
	require.Zero(t, deadletters)
	require.Empty(t, deadlettersByKindAndMessageType(observer.records, workerKind))

	blackholeKind := types.Name(&MockUnhandled{})
	blackholeCounts := aggregatedCountsByKind(observer.records, blackholeKind)

	require.EqualValues(t, 4, blackholeCounts["actor.processed.count"])
	require.EqualValues(t, 4, blackholeCounts["actor.unhandled.count"])

	// both actors of the kind dropped one message of each type
	require.Equal(t, map[string]int64{
		types.NameOf(new(testpb.TestSend)):  2,
		types.NameOf(new(testpb.TestReply)): 2,
	}, deadlettersByKindAndMessageType(observer.records, blackholeKind))

	// every per-kind observation carries the actor system and the kind, nothing
	// else
	attrs := aggregatedAttributes(observer.records, blackholeKind)
	require.Equal(t, 2, attrs.Len())

	systemAttr, ok := attrs.Value(attribute.Key("actor.system"))
	require.True(t, ok)
	require.Equal(t, "testSys", systemAttr.AsString())

	kindAttr, ok := attrs.Value(attribute.Key("actor.kind"))
	require.True(t, ok)
	require.Equal(t, blackholeKind, kindAttr.AsString())

	// system actors are left out of the per-kind mode entirely: the scrape must
	// not aggregate them, and it must not register their kinds, which would make
	// the lifecycle callback emit zero-valued series for kinds the lifecycle
	// counters deliberately exclude. Exactly the two user kinds may exist.
	system, isConcrete := sys.(*actorSystem)
	require.True(t, isConcrete)
	require.EqualValues(t, 2, system.actorKinds.Len())

	_, ok = system.actorKinds.Get(workerKind)
	require.True(t, ok)
	_, ok = system.actorKinds.Get(blackholeKind)
	require.True(t, ok)

	require.NoError(t, sys.Stop(ctx))
}

// scrapeOnce invokes every registered metrics callback once and returns the
// observer that captured the resulting observations.
func scrapeOnce(t *testing.T, ctx context.Context, provider *recordingMeterProvider) *attrObserver {
	t.Helper()

	observer := &attrObserver{}
	for _, callback := range provider.meter.callbacks {
		require.NoError(t, callback(ctx, observer))
	}

	return observer
}

// deadlettersByMessageType returns the actor.deadletters.count observations
// made for the named actor, indexed by their message.type attribute.
func deadlettersByMessageType(records []attrObserveRecord, actorName string) map[string]int64 {
	counts := make(map[string]int64)

	for _, record := range records {
		if record.instrument != "actor.deadletters.count" {
			continue
		}

		name, ok := record.attrs.Value(attribute.Key("actor.name"))
		if !ok || name.AsString() != actorName {
			continue
		}

		if messageType, ok := record.attrs.Value(attribute.Key("message.type")); ok {
			counts[messageType.AsString()] = record.value
		}
	}

	return counts
}

// aggregatedRecords returns the observations a low cardinality scrape made for
// the given actor kind, excluding the dead-letter observations broken down by
// message type, whose values would otherwise overwrite one another.
func aggregatedRecords(records []attrObserveRecord, kind string) []attrObserveRecord {
	out := make([]attrObserveRecord, 0, len(records))

	for _, record := range records {
		value, ok := record.attrs.Value(attribute.Key("actor.kind"))
		if !ok || value.AsString() != kind {
			continue
		}

		if _, typed := record.attrs.Value(attribute.Key("message.type")); typed {
			continue
		}

		out = append(out, record)
	}

	return out
}

// aggregatedCountsByKind indexes a kind's aggregated observations by instrument
// name. In the low cardinality mode the per-actor instruments carry the same
// actor.system and actor.kind attribute set as the lifecycle counters, so both
// land in the same index under their own instrument names.
func aggregatedCountsByKind(records []attrObserveRecord, kind string) map[string]int64 {
	counts := make(map[string]int64)
	for _, record := range aggregatedRecords(records, kind) {
		counts[record.instrument] = record.value
	}

	return counts
}

// aggregatedAttributes returns the attribute set carried by a kind's aggregated
// observations.
func aggregatedAttributes(records []attrObserveRecord, kind string) attribute.Set {
	for _, record := range aggregatedRecords(records, kind) {
		return record.attrs
	}

	return *attribute.EmptySet()
}

// deadlettersByKindAndMessageType returns the actor.deadletters.count
// observations made for the given actor kind, indexed by their message.type
// attribute.
func deadlettersByKindAndMessageType(records []attrObserveRecord, kind string) map[string]int64 {
	counts := make(map[string]int64)

	for _, record := range records {
		if record.instrument != "actor.deadletters.count" {
			continue
		}

		value, ok := record.attrs.Value(attribute.Key("actor.kind"))
		if !ok || value.AsString() != kind {
			continue
		}

		if messageType, ok := record.attrs.Value(attribute.Key("message.type")); ok {
			counts[messageType.AsString()] = record.value
		}
	}

	return counts
}

// lifecycleRecords returns the observations carrying the given actor.kind and
// no actor.name, which is exactly the system-level per-kind lifecycle series.
func lifecycleRecords(records []attrObserveRecord, kind string) []attrObserveRecord {
	out := make([]attrObserveRecord, 0, len(records))

	for _, record := range records {
		value, ok := record.attrs.Value(attribute.Key("actor.kind"))
		if !ok || value.AsString() != kind {
			continue
		}

		if _, named := record.attrs.Value(attribute.Key("actor.name")); named {
			continue
		}

		out = append(out, record)
	}

	return out
}

// lifecycleCountsByKind indexes the per-kind lifecycle observations by
// instrument name.
func lifecycleCountsByKind(records []attrObserveRecord, kind string) map[string]int64 {
	counts := make(map[string]int64)
	for _, record := range lifecycleRecords(records, kind) {
		counts[record.instrument] = record.value
	}

	return counts
}

// lifecycleAttributes returns the attribute set carried by the per-kind
// lifecycle observations of the given kind.
func lifecycleAttributes(records []attrObserveRecord, kind string) attribute.Set {
	for _, record := range lifecycleRecords(records, kind) {
		return record.attrs
	}

	return *attribute.EmptySet()
}
