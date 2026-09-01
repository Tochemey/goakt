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

	// three callbacks are registered for a non-cluster system: the
	// system-level one, the per-actor one and the scheduler one. Only the
	// per-actor callback asks the deadletter actor.
	require.Len(t, meter.callbacks, 3)

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

// TestRegisterMetricsObservesSubsystemCounters drives one full collection over
// a metrics-enabled non-cluster system and asserts the subsystem observations:
// the live grain gauge, the scheduler totals, and the absence of the cluster
// membership instruments outside cluster mode.
func TestRegisterMetricsObservesSubsystemCounters(t *testing.T) {
	ctx := context.Background()

	previous := otel.GetMeterProvider()
	meterProvider := newRecordingMeterProvider()
	otel.SetMeterProvider(meterProvider)
	t.Cleanup(func() { otel.SetMeterProvider(previous) })

	sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger), WithMetrics())
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))

	pause.For(time.Second)

	identity, err := sys.GrainIdentity(ctx, "counterGrain", func(_ context.Context) (Grain, error) {
		return NewMockGrain(), nil
	})
	require.NoError(t, err)
	require.NotNil(t, identity)

	pid, err := sys.Spawn(ctx, "receiver", NewMockActor(), WithLongLived())
	require.NoError(t, err)
	require.NotNil(t, pid)

	message := new(testpb.TestSend)
	require.NoError(t, sys.ScheduleOnce(ctx, message, pid, time.Hour, WithReference("subsystem-once")))
	require.NoError(t, sys.Schedule(ctx, message, pid, time.Hour, WithReference("subsystem-interval")))
	require.NoError(t, sys.CancelSchedule("subsystem-interval"))

	observer := scrapeOnce(t, ctx, meterProvider)

	grains, ok := recordValue(observer.records, "actorsystem.grains.count")
	require.True(t, ok)
	require.EqualValues(t, 1, grains)

	scheduled, ok := recordValue(observer.records, "scheduler.scheduled.count")
	require.True(t, ok)
	require.EqualValues(t, 2, scheduled)

	cancelled, ok := recordValue(observer.records, "scheduler.cancelled.count")
	require.True(t, ok)
	require.EqualValues(t, 1, cancelled)

	// the membership instruments belong to cluster mode only
	_, ok = recordValue(observer.records, "cluster.members.joined.count")
	require.False(t, ok)
	_, ok = recordValue(observer.records, "cluster.members.left.count")
	require.False(t, ok)

	require.NoError(t, sys.Stop(ctx))
}

// recordValue returns the value the scrape observed for the named instrument,
// and whether the instrument was observed at all.
func recordValue(records []attrObserveRecord, instrument string) (int64, bool) {
	for _, record := range records {
		if record.instrument == instrument {
			return record.value, true
		}
	}

	return 0, false
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

// TestRegisterMetricsObservesMailboxSize drives one full collection over a
// metrics-enabled system and asserts what actor.mailbox.size reports in the
// default per-actor mode: the backlog of a running actor next to every other
// per-actor instrument, the backlog of a suspended actor, which the live-only
// gate hides from all of them, and nothing at all for the runtime's own system
// actors.
func TestRegisterMetricsObservesMailboxSize(t *testing.T) {
	ctx := context.Background()

	previous := otel.GetMeterProvider()
	meterProvider := newRecordingMeterProvider()
	otel.SetMeterProvider(meterProvider)
	t.Cleanup(func() { otel.SetMeterProvider(previous) })

	sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger), WithMetrics())
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))

	// both actors park their turn on their first message, so everything sent
	// after it stays queued and the scrape reads a backlog at rest.
	backlogged := newMailboxBlockingActor()
	backloggedPID, err := sys.Spawn(ctx, "backlogged", backlogged, WithLongLived())
	require.NoError(t, err)

	halted := newMailboxBlockingActor()
	haltedPID, err := sys.Spawn(ctx, "halted", halted, WithLongLived())
	require.NoError(t, err)

	require.NoError(t, Tell(ctx, backloggedPID, new(testpb.TestSend)))
	<-backlogged.entered

	require.NoError(t, Tell(ctx, haltedPID, new(testpb.TestSend)))
	<-halted.entered

	const backlog = 4
	for range backlog {
		require.NoError(t, Tell(ctx, backloggedPID, new(testpb.TestSend)))
	}

	haltedPID.suspend("mailbox size scrape test")
	require.True(t, haltedPID.IsSuspended())

	// Tell refuses a suspended target, so its backlog is handed to the delivery
	// entry point every sender funnels into.
	const haltedBacklog = 2
	for range haltedBacklog {
		haltedPID.doReceive(toReceiveContext(ctx, sys.NoSender(), haltedPID, new(testpb.TestSend), true))
	}

	require.Eventually(t, func() bool {
		return backloggedPID.observedMailboxSize() == backlog &&
			haltedPID.observedMailboxSize() == haltedBacklog
	}, 3*time.Second, 20*time.Millisecond)

	observer := scrapeOnce(t, ctx, meterProvider)

	// only the two user actors report a mailbox: the runtime's system actors are
	// left out, exactly as they are in the per-kind mode.
	require.Equal(t, map[string]int64{
		"backlogged": backlog,
		"halted":     haltedBacklog,
	}, mailboxSizesByActor(observer.records))

	records := groupRecordsByActor(observer.records)

	// the running actor reports its backlog alongside every other instrument
	require.EqualValues(t, backlog, records["backlogged"]["actor.mailbox.size"])
	require.Contains(t, records["backlogged"], "actor.processed.count")

	// the suspended one reports its backlog and nothing else: the live-only gate
	// still holds for every other instrument.
	require.Equal(t, map[string]int64{"actor.mailbox.size": haltedBacklog}, records["halted"])

	close(backlogged.release)
	close(halted.release)

	require.NoError(t, sys.Stop(ctx))
}

// TestRegisterMetricsAggregatesMailboxSizePerKind asserts the per-kind reduction
// of actor.mailbox.size in the low cardinality mode: the backlogs of every actor
// of a kind are summed, including those of the members the live-only gate hides
// from the other instruments, and a kind with no live actor left reports its
// backlog alone.
func TestRegisterMetricsAggregatesMailboxSizePerKind(t *testing.T) {
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

	running := newMailboxBlockingActor()
	runningPID, err := sys.Spawn(ctx, "worker-0", running, WithLongLived())
	require.NoError(t, err)

	halted := newMailboxBlockingActor()
	haltedPID, err := sys.Spawn(ctx, "worker-1", halted, WithLongLived())
	require.NoError(t, err)

	require.NoError(t, Tell(ctx, runningPID, new(testpb.TestSend)))
	<-running.entered

	require.NoError(t, Tell(ctx, haltedPID, new(testpb.TestSend)))
	<-halted.entered

	const runningBacklog = 3
	for range runningBacklog {
		require.NoError(t, Tell(ctx, runningPID, new(testpb.TestSend)))
	}

	haltedPID.suspend("mailbox size scrape test")

	const haltedBacklog = 2
	for range haltedBacklog {
		haltedPID.doReceive(toReceiveContext(ctx, sys.NoSender(), haltedPID, new(testpb.TestSend), true))
	}

	// a second kind whose only actor ends up suspended, so no member of it
	// passes the full gate
	idlePID, err := sys.Spawn(ctx, "blackhole", &MockUnhandled{}, WithLongLived())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return idlePID.ProcessedCount() >= 1
	}, 3*time.Second, 20*time.Millisecond)

	idlePID.suspend("mailbox size scrape test")

	require.Eventually(t, func() bool {
		return runningPID.observedMailboxSize() == runningBacklog &&
			haltedPID.observedMailboxSize() == haltedBacklog
	}, 3*time.Second, 20*time.Millisecond)

	observer := scrapeOnce(t, ctx, meterProvider)

	workerKind := types.Name(newMailboxBlockingActor())
	workerCounts := aggregatedCountsByKind(observer.records, workerKind)

	// the suspended member's backlog folds into the kind's total even though the
	// live-only gate keeps it out of every other instrument, which the live
	// member still reports.
	require.EqualValues(t, runningBacklog+haltedBacklog, workerCounts["actor.mailbox.size"])
	require.Contains(t, workerCounts, "actor.processed.count")

	// the kind with no live actor left reports its mailbox and nothing else
	idleCounts := aggregatedCountsByKind(observer.records, types.Name(&MockUnhandled{}))
	require.Contains(t, idleCounts, "actor.mailbox.size")
	require.NotContains(t, idleCounts, "actor.processed.count")
	require.NotContains(t, idleCounts, "actor.uptime")

	close(running.release)
	close(halted.release)

	require.NoError(t, sys.Stop(ctx))
}

// mailboxSizesByActor returns the actor.mailbox.size observations of a default
// mode scrape, indexed by the actor they name.
func mailboxSizesByActor(records []attrObserveRecord) map[string]int64 {
	sizes := make(map[string]int64)

	for _, record := range records {
		if record.instrument != "actor.mailbox.size" {
			continue
		}

		if name, ok := record.attrs.Value(attribute.Key("actor.name")); ok {
			sizes[name.AsString()] = record.value
		}
	}

	return sizes
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
