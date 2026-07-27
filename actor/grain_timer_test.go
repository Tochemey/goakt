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
	"errors"
	"io"
	"testing"
	"time"

	"github.com/reugn/go-quartz/quartz"
	"github.com/stretchr/testify/require"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/log"
)

// everySecondCron fires on every second, the smallest granularity the Quartz
// cron format supports.
const everySecondCron = "* * * ? * *"

// newTestGrainTimers returns a registry whose deliveries land on the returned
// channel. The channel is buffered so a stray late fire can never block a timer
// goroutine after a test finishes.
func newTestGrainTimers() (*grainTimers, chan *grainTimerEntry) {
	deliveries := make(chan *grainTimerEntry, 16)
	timers := newGrainTimers(func(entry *grainTimerEntry) {
		deliveries <- entry
	})
	return timers, deliveries
}

// entryOf fetches the live entry registered under reference.
func entryOf(t *testing.T, timers *grainTimers, reference string) *grainTimerEntry {
	t.Helper()
	timers.mu.Lock()
	defer timers.mu.Unlock()

	entry, ok := timers.entries[reference]
	require.True(t, ok)
	return entry
}

// entriesLen reports the number of registered entries.
func entriesLen(timers *grainTimers) int {
	timers.mu.Lock()
	defer timers.mu.Unlock()
	return len(timers.entries)
}

// expectDelivery waits for one delivery.
func expectDelivery(t *testing.T, deliveries chan *grainTimerEntry) *grainTimerEntry {
	t.Helper()
	select {
	case entry := <-deliveries:
		return entry
	case <-time.After(2 * time.Second):
		t.Fatal("expected a timer tick delivery")
		return nil
	}
}

// expectNoDelivery asserts that nothing is delivered within the given window.
func expectNoDelivery(t *testing.T, deliveries chan *grainTimerEntry, window time.Duration) {
	t.Helper()
	select {
	case entry := <-deliveries:
		t.Fatalf("unexpected timer tick delivery: reference=%s", entry.reference)
	case <-time.After(window):
	}
}

func TestGrainTimersScheduleOnce(t *testing.T) {
	timers, deliveries := newTestGrainTimers()
	timers.start()

	message := "tick"
	reference, err := timers.scheduleOnce(message, 10*time.Millisecond)
	require.NoError(t, err)
	require.NotEmpty(t, reference)

	entry := expectDelivery(t, deliveries)
	require.Equal(t, message, entry.message)
	require.Equal(t, reference, entry.reference)

	// a one-shot is spent at fire time: it left the registry before delivery,
	// so cancelling it now reports it as gone
	require.Equal(t, 0, entriesLen(timers))
	require.ErrorIs(t, timers.cancel(reference), gerrors.ErrScheduledReferenceNotFound)
}

func TestGrainTimersScheduleOnceNonPositiveDelayFiresImmediately(t *testing.T) {
	timers, deliveries := newTestGrainTimers()
	timers.start()

	_, err := timers.scheduleOnce("tick", 0)
	require.NoError(t, err)

	expectDelivery(t, deliveries)
}

func TestGrainTimersScheduleInterval(t *testing.T) {
	timers, deliveries := newTestGrainTimers()
	timers.start()

	reference, err := timers.scheduleInterval("tick", 10*time.Millisecond)
	require.NoError(t, err)

	// fixed cadence: ticks keep coming without anyone acknowledging them
	first := expectDelivery(t, deliveries)
	second := expectDelivery(t, deliveries)
	require.Equal(t, reference, first.reference)
	require.Equal(t, reference, second.reference)

	// the entry survives ticks; it only goes away on cancel or close
	require.Equal(t, 1, entriesLen(timers))
	require.NoError(t, timers.cancel(reference))
	require.Equal(t, 0, entriesLen(timers))
}

func TestGrainTimersScheduleIntervalRejectsNonPositiveInterval(t *testing.T) {
	timers, _ := newTestGrainTimers()
	timers.start()

	_, err := timers.scheduleInterval("tick", 0)
	require.ErrorIs(t, err, gerrors.ErrInvalidTimerInterval)

	_, err = timers.scheduleInterval("tick", -time.Second)
	require.ErrorIs(t, err, gerrors.ErrInvalidTimerInterval)
}

func TestGrainTimersScheduleCronRejectsInvalidExpression(t *testing.T) {
	timers, _ := newTestGrainTimers()
	timers.start()

	_, err := timers.scheduleCron("tick", "not a cron expression")
	require.Error(t, err)
	require.Equal(t, 0, entriesLen(timers))
}

func TestGrainTimersCronFireRearmsAndDelivers(t *testing.T) {
	timers, deliveries := newTestGrainTimers()

	// registered on a registry that has not started so ticks can be driven
	// deterministically through fire without racing a real timer
	reference, err := timers.scheduleCron("tick", everySecondCron)
	require.NoError(t, err)
	entry := entryOf(t, timers, reference)

	// every fire re-arms for the next cron instant and delivers
	timers.fire(entry)
	expectDelivery(t, deliveries)
	require.True(t, entryTimerStarted(timers, reference))

	timers.fire(entry)
	expectDelivery(t, deliveries)
	require.Equal(t, 1, entriesLen(timers))
}

func TestGrainTimersCancelBeforeFire(t *testing.T) {
	timers, deliveries := newTestGrainTimers()
	timers.start()

	reference, err := timers.scheduleOnce("tick", 80*time.Millisecond)
	require.NoError(t, err)

	require.NoError(t, timers.cancel(reference))
	require.Equal(t, 0, entriesLen(timers))

	expectNoDelivery(t, deliveries, 200*time.Millisecond)
}

func TestGrainTimersCancelDuringInFlightTick(t *testing.T) {
	timers, deliveries := newTestGrainTimers()

	reference, err := timers.scheduleInterval("tick", time.Hour)
	require.NoError(t, err)
	entry := entryOf(t, timers, reference)

	// deliver a tick, then cancel while it is still being processed: the
	// cancelled flag is what makes the tick handler drop the in-flight tick
	timers.fire(entry)
	expectDelivery(t, deliveries)
	require.NoError(t, timers.cancel(reference))
	require.True(t, entry.cancelled.Load())

	require.Equal(t, 0, entriesLen(timers))
	expectNoDelivery(t, deliveries, 50*time.Millisecond)
}

func TestGrainTimersCancelUnknownReference(t *testing.T) {
	timers, _ := newTestGrainTimers()
	timers.start()

	require.ErrorIs(t, timers.cancel("unknown"), gerrors.ErrScheduledReferenceNotFound)
}

func TestGrainTimersDuplicateReferenceReplaces(t *testing.T) {
	timers, deliveries := newTestGrainTimers()
	timers.start()

	_, err := timers.scheduleOnce("old", time.Hour, WithTimerReference("dup"))
	require.NoError(t, err)
	replaced := entryOf(t, timers, "dup")

	_, err = timers.scheduleOnce("new", 10*time.Millisecond, WithTimerReference("dup"))
	require.NoError(t, err)

	require.True(t, replaced.cancelled.Load())
	require.Equal(t, 1, entriesLen(timers))

	entry := expectDelivery(t, deliveries)
	require.Equal(t, "new", entry.message)
}

func TestGrainTimersDeferredStart(t *testing.T) {
	timers, deliveries := newTestGrainTimers()

	// registered before start: nothing may fire yet, even past the delay
	_, err := timers.scheduleOnce("tick", 10*time.Millisecond)
	require.NoError(t, err)
	expectNoDelivery(t, deliveries, 100*time.Millisecond)

	timers.start()
	expectDelivery(t, deliveries)
}

func TestGrainTimersStartIsIdempotent(t *testing.T) {
	timers, deliveries := newTestGrainTimers()

	_, err := timers.scheduleOnce("tick", 20*time.Millisecond)
	require.NoError(t, err)

	timers.start()
	timers.start()

	expectDelivery(t, deliveries)
	expectNoDelivery(t, deliveries, 100*time.Millisecond)
}

func TestGrainTimersStop(t *testing.T) {
	timers, deliveries := newTestGrainTimers()
	timers.start()

	_, err := timers.scheduleOnce("tick", 50*time.Millisecond)
	require.NoError(t, err)
	reference, err := timers.scheduleInterval("tick", 50*time.Millisecond)
	require.NoError(t, err)

	timers.stop()
	require.Equal(t, 0, entriesLen(timers))
	expectNoDelivery(t, deliveries, 150*time.Millisecond)

	// every operation on a stopped registry is rejected
	_, err = timers.scheduleOnce("tick", time.Second)
	require.ErrorIs(t, err, gerrors.ErrGrainTimersStopped)
	_, err = timers.scheduleInterval("tick", time.Second)
	require.ErrorIs(t, err, gerrors.ErrGrainTimersStopped)
	_, err = timers.scheduleCron("tick", everySecondCron)
	require.ErrorIs(t, err, gerrors.ErrGrainTimersStopped)
	require.ErrorIs(t, timers.cancel(reference), gerrors.ErrGrainTimersStopped)

	// close and start stay no-ops afterwards
	timers.stop()
	timers.start()
}

func TestGrainTimersStopBeforeStart(t *testing.T) {
	timers, deliveries := newTestGrainTimers()

	_, err := timers.scheduleOnce("tick", 10*time.Millisecond)
	require.NoError(t, err)

	timers.stop()
	timers.start()
	expectNoDelivery(t, deliveries, 100*time.Millisecond)
}

func TestGrainTimersFireOnCancelledOrClosed(t *testing.T) {
	timers, deliveries := newTestGrainTimers()

	reference, err := timers.scheduleOnce("tick", time.Hour)
	require.NoError(t, err)
	entry := entryOf(t, timers, reference)

	require.NoError(t, timers.cancel(reference))
	timers.fire(entry)
	expectNoDelivery(t, deliveries, 50*time.Millisecond)

	timers.stop()
	timers.fire(entry)
	expectNoDelivery(t, deliveries, 50*time.Millisecond)
}

func TestGrainTimersCronWithoutNextFireTimeIsRemoved(t *testing.T) {
	timers, deliveries := newTestGrainTimers()

	reference, err := timers.scheduleCron("tick", everySecondCron)
	require.NoError(t, err)
	entry := entryOf(t, timers, reference)

	// an expired run-once trigger errors on NextFireTime, standing in for a cron
	// trigger that has no future fire instants
	expired := quartz.NewRunOnceTrigger(time.Second)
	_, err = expired.NextFireTime(quartz.NowNano())
	require.NoError(t, err)
	entry.trigger = expired

	// the last due tick is still delivered, but the entry cannot fire again and
	// is removed when its next fire is scheduled
	timers.fire(entry)
	expectDelivery(t, deliveries)
	require.Equal(t, 0, entriesLen(timers))
}

func TestGrainTimersKeepAlivePropagates(t *testing.T) {
	timers, _ := newTestGrainTimers()

	reference, err := timers.scheduleOnce("tick", time.Hour, WithTimerKeepAlive())
	require.NoError(t, err)
	require.True(t, entryOf(t, timers, reference).keepAlive)

	reference, err = timers.scheduleInterval("tick", time.Hour)
	require.NoError(t, err)
	require.False(t, entryOf(t, timers, reference).keepAlive)
}

// timerProbeGrain records every message OnReceive sees. The messages "panic" and
// "fail" make the handler panic and report an error respectively, to exercise the
// tick failure paths. The optional hooks run inside OnActivate and OnDeactivate to
// exercise timer registration from the lifecycle methods.
type timerProbeGrain struct {
	received     chan any
	onActivate   func(*GrainProps) error
	onDeactivate func(*GrainProps) error
	// onReceive, when set and returning true, handles the message in place of
	// the default behavior.
	onReceive func(*GrainContext) bool
}

var _ Grain = (*timerProbeGrain)(nil)

func newTimerProbeGrain() *timerProbeGrain {
	return &timerProbeGrain{received: make(chan any, 64)}
}

func (g *timerProbeGrain) OnActivate(_ context.Context, props *GrainProps) error {
	if g.onActivate != nil {
		return g.onActivate(props)
	}
	return nil
}

func (g *timerProbeGrain) OnDeactivate(_ context.Context, props *GrainProps) error {
	if g.onDeactivate != nil {
		return g.onDeactivate(props)
	}
	return nil
}

func (g *timerProbeGrain) OnReceive(ctx *GrainContext) {
	message := ctx.Message()

	select {
	case g.received <- message:
	default:
	}

	if g.onReceive != nil && g.onReceive(ctx) {
		return
	}

	switch message {
	case "panic":
		panic("boom")
	case "fail":
		ctx.Err(errors.New("boom"))
	default:
		ctx.NoErr()
	}
}

// expectGrainMessage waits for the grain to receive one message.
func expectGrainMessage(t *testing.T, grain *timerProbeGrain) any {
	t.Helper()
	select {
	case message := <-grain.received:
		return message
	case <-time.After(2 * time.Second):
		t.Fatal("expected the grain to receive a message")
		return nil
	}
}

// expectNoGrainMessage asserts the grain receives nothing within the given window.
func expectNoGrainMessage(t *testing.T, grain *timerProbeGrain, window time.Duration) {
	t.Helper()
	select {
	case message := <-grain.received:
		t.Fatalf("unexpected message received by the grain: %v", message)
	case <-time.After(window):
	}
}

// grainTimerFixture bundles everything the grain timer end-to-end tests interact with.
type grainTimerFixture struct {
	system   ActorSystem
	identity *GrainIdentity
	grain    *timerProbeGrain
	pid      *grainPID
	// props exposes the public scheduling API against the spawned grain, the
	// same surface OnActivate and OnDeactivate receive.
	props *GrainProps
}

// spawnTimerProbeGrain starts a standalone actor system and activates a probe
// grain with the given options.
func spawnTimerProbeGrain(t *testing.T, opts ...GrainOption) *grainTimerFixture {
	t.Helper()
	ctx := context.Background()

	sys, err := NewActorSystem("grainTimersSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	t.Cleanup(func() { _ = sys.Stop(context.Background()) })

	grain := newTimerProbeGrain()
	identity, err := sys.GrainIdentity(ctx, "timer-probe", func(context.Context) (Grain, error) {
		return grain, nil
	}, opts...)
	require.NoError(t, err)

	pid, ok := sys.(*actorSystem).grains.Get(identity.String())
	require.True(t, ok)
	require.True(t, pid.isActive())

	return &grainTimerFixture{
		system:   sys,
		identity: identity,
		grain:    grain,
		pid:      pid,
		props:    newGrainProps(identity, sys, nil, pid),
	}
}

// newTestGrainPID builds a minimal grainPID whose activate/deactivate can be
// driven directly, with a single fast activation attempt.
func newTestGrainPID(grain Grain, name string) *grainPID {
	config := newGrainConfig()
	config.initMaxRetries.Store(1)
	config.initTimeout.Store(100 * time.Millisecond)

	return &grainPID{
		grain:        grain,
		identity:     newGrainIdentity(grain, name),
		logger:       log.DiscardLogger,
		config:       config,
		dependencies: config.dependencies,
	}
}

// entryTimerStarted reports whether the entry registered under reference has its
// fire trigger armed.
func entryTimerStarted(timers *grainTimers, reference string) bool {
	timers.mu.Lock()
	defer timers.mu.Unlock()

	entry, ok := timers.entries[reference]
	return ok && entry.timer != nil
}

func TestGrainTimerScheduleOnceDeliversToGrain(t *testing.T) {
	fx := spawnTimerProbeGrain(t)

	_, err := fx.props.ScheduleOnce("tick-once", 20*time.Millisecond)
	require.NoError(t, err)

	require.Equal(t, "tick-once", expectGrainMessage(t, fx.grain))

	// a one-shot is spent at fire time: once its tick arrived, it is gone
	require.Equal(t, 0, entriesLen(fx.pid.getTimers()))
}

func TestGrainTimerIntervalDeliversUntilCancelled(t *testing.T) {
	fx := spawnTimerProbeGrain(t)

	reference, err := fx.props.Schedule("beat", 30*time.Millisecond)
	require.NoError(t, err)

	for range 3 {
		require.Equal(t, "beat", expectGrainMessage(t, fx.grain))
	}

	require.NoError(t, fx.props.CancelSchedule(reference))

	// drain any tick already in flight, then the beat must stop
	time.Sleep(100 * time.Millisecond)
	for len(fx.grain.received) > 0 {
		<-fx.grain.received
	}
	expectNoGrainMessage(t, fx.grain, 200*time.Millisecond)
}

func TestGrainTimerTickDoesNotPreventPassivation(t *testing.T) {
	fx := spawnTimerProbeGrain(t, WithGrainDeactivateAfter(300*time.Millisecond))

	_, err := fx.props.Schedule("beat", 50*time.Millisecond)
	require.NoError(t, err)

	// ticks are flowing, yet they do not reset the passivation clock, so the
	// grain must still passivate on schedule
	require.Equal(t, "beat", expectGrainMessage(t, fx.grain))
	require.Eventually(t, func() bool {
		return !fx.pid.isActive()
	}, 3*time.Second, 20*time.Millisecond)

	// deactivation stopped the registry: the beat must stop
	time.Sleep(100 * time.Millisecond)
	for len(fx.grain.received) > 0 {
		<-fx.grain.received
	}
	expectNoGrainMessage(t, fx.grain, 200*time.Millisecond)
}

func TestGrainTimerKeepAliveTickPreventsPassivation(t *testing.T) {
	fx := spawnTimerProbeGrain(t, WithGrainDeactivateAfter(400*time.Millisecond))

	reference, err := fx.props.Schedule("beat", 50*time.Millisecond, WithTimerKeepAlive())
	require.NoError(t, err)

	// each tick resets the passivation clock: well past the deactivate-after
	// window the grain must still be active
	require.Equal(t, "beat", expectGrainMessage(t, fx.grain))
	time.Sleep(3 * 400 * time.Millisecond)
	require.True(t, fx.pid.isActive())

	// once the keep-alive beat stops, passivation proceeds
	require.NoError(t, fx.props.CancelSchedule(reference))
	require.Eventually(t, func() bool {
		return !fx.pid.isActive()
	}, 3*time.Second, 20*time.Millisecond)
}

func TestGrainTimerTickPanicKeepsTimerRunning(t *testing.T) {
	fx := spawnTimerProbeGrain(t)

	_, err := fx.props.Schedule("panic", 30*time.Millisecond)
	require.NoError(t, err)

	// a panicking handler is recovered like any other turn and must not stop
	// the interval timer
	require.Equal(t, "panic", expectGrainMessage(t, fx.grain))
	require.Equal(t, "panic", expectGrainMessage(t, fx.grain))
}

func TestGrainTimerTickErrorKeepsTimerRunning(t *testing.T) {
	fx := spawnTimerProbeGrain(t)

	_, err := fx.props.Schedule("fail", 30*time.Millisecond)
	require.NoError(t, err)

	// a handler reporting an error only gets it logged; the timer keeps firing
	require.Equal(t, "fail", expectGrainMessage(t, fx.grain))
	require.Equal(t, "fail", expectGrainMessage(t, fx.grain))
}

func TestGrainActivationFailureClosesTimers(t *testing.T) {
	ctx := context.Background()
	grain := newTimerProbeGrain()
	pid := newTestGrainPID(grain, "activation-failure")

	// OnActivate registers a timer and then fails: the timer must never fire
	grain.onActivate = func(props *GrainProps) error {
		_, err := props.ScheduleOnce("never", 10*time.Millisecond)
		require.NoError(t, err)
		return errors.New("boom")
	}

	require.ErrorIs(t, pid.activate(ctx), gerrors.ErrGrainActivationFailure)

	// the registry stopped: the entry is gone and late registrations are rejected
	registry := pid.getTimers()
	require.Equal(t, 0, entriesLen(registry))
	_, err := registry.scheduleOnce("late", time.Second)
	require.ErrorIs(t, err, gerrors.ErrGrainTimersStopped)
}

func TestGrainActivationPanicClosesTimers(t *testing.T) {
	ctx := context.Background()
	grain := newTimerProbeGrain()
	pid := newTestGrainPID(grain, "activation-panic")

	grain.onActivate = func(props *GrainProps) error {
		_, err := props.ScheduleOnce("never", 10*time.Millisecond)
		require.NoError(t, err)
		panic("boom")
	}

	require.ErrorIs(t, pid.activate(ctx), gerrors.ErrGrainActivationFailure)

	registry := pid.getTimers()
	require.Equal(t, 0, entriesLen(registry))
	_, err := registry.scheduleOnce("late", time.Second)
	require.ErrorIs(t, err, gerrors.ErrGrainTimersStopped)
}

func TestGrainTimerRegisteredInOnActivateStaysDormantUntilActive(t *testing.T) {
	ctx := context.Background()
	grain := newTimerProbeGrain()
	pid := newTestGrainPID(grain, "dormant-until-active")

	// registered from OnActivate the timer must stay dormant: were it started
	// right away, a short delay could fire into the not-yet-active grain
	var reference string
	grain.onActivate = func(props *GrainProps) error {
		var err error
		reference, err = props.ScheduleOnce("tick", time.Hour)
		require.NoError(t, err)
		require.False(t, entryTimerStarted(pid.getTimers(), reference))
		return nil
	}

	require.NoError(t, pid.activate(ctx))
	require.True(t, entryTimerStarted(pid.getTimers(), reference))
}

func TestGrainTimerLateRegistrationFromOnDeactivateRejected(t *testing.T) {
	ctx := context.Background()
	fx := spawnTimerProbeGrain(t)

	// the registry closes before OnDeactivate runs, so the hook cannot plant a
	// timer into the deactivated grain
	var hookErr error
	fx.grain.onDeactivate = func(*GrainProps) error {
		_, hookErr = fx.props.ScheduleOnce("late", time.Second)
		return nil
	}

	require.NoError(t, fx.pid.deactivate(ctx))
	require.ErrorIs(t, hookErr, gerrors.ErrGrainTimersStopped)
}

func TestGrainTimerFreshRegistryAcrossReactivation(t *testing.T) {
	ctx := context.Background()
	fx := spawnTimerProbeGrain(t)

	oldRegistry := fx.pid.getTimers()
	_, err := oldRegistry.scheduleInterval("beat", time.Hour)
	require.NoError(t, err)

	// a reused grainPID must not carry the previous activation's registry
	require.NoError(t, fx.pid.deactivate(ctx))
	require.NoError(t, fx.pid.activate(ctx))

	newRegistry := fx.pid.getTimers()
	require.NotSame(t, oldRegistry, newRegistry)
	require.Equal(t, 0, entriesLen(newRegistry))

	// the old registry stays stopped while the new one is fully usable
	_, err = oldRegistry.scheduleOnce("stale", time.Second)
	require.ErrorIs(t, err, gerrors.ErrGrainTimersStopped)
	_, err = newRegistry.scheduleOnce("fresh", time.Hour)
	require.NoError(t, err)
}

func TestGrainTimerStopsOnSystemShutdown(t *testing.T) {
	fx := spawnTimerProbeGrain(t)

	_, err := fx.props.Schedule("beat", 30*time.Millisecond)
	require.NoError(t, err)
	require.Equal(t, "beat", expectGrainMessage(t, fx.grain))

	// shutdown deactivates the grain through the PoisonPill path, which must
	// close the registry like any other deactivation
	require.NoError(t, fx.system.Stop(context.Background()))
	require.False(t, fx.pid.isActive())

	_, err = fx.props.ScheduleOnce("late", time.Second)
	require.ErrorIs(t, err, gerrors.ErrGrainTimersStopped)
}

func TestGrainTimerScheduledFromOnActivate(t *testing.T) {
	ctx := context.Background()

	sys, err := NewActorSystem("grainTimersSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	t.Cleanup(func() { _ = sys.Stop(context.Background()) })

	// the canonical heartbeat pattern: OnActivate starts the periodic timer
	grain := newTimerProbeGrain()
	grain.onActivate = func(props *GrainProps) error {
		_, err := props.Schedule("activate-beat", 30*time.Millisecond)
		return err
	}

	_, err = sys.GrainIdentity(ctx, "on-activate-timer", func(context.Context) (Grain, error) {
		return grain, nil
	})
	require.NoError(t, err)

	require.Equal(t, "activate-beat", expectGrainMessage(t, grain))
	require.Equal(t, "activate-beat", expectGrainMessage(t, grain))
}

func TestGrainTimerScheduleFromOnReceive(t *testing.T) {
	fx := spawnTimerProbeGrain(t)

	// a grain schedules a timer to itself from its own message handler
	scheduled := make(chan error, 1)
	fx.grain.onReceive = func(ctx *GrainContext) bool {
		if ctx.Message() == "kick" {
			_, err := ctx.ScheduleOnce("ctx-tick", 20*time.Millisecond)
			scheduled <- err
			ctx.NoErr()
			return true
		}
		return false
	}

	require.NoError(t, fx.system.TellGrain(context.Background(), fx.identity, "kick"))
	require.NoError(t, <-scheduled)

	require.Equal(t, "kick", expectGrainMessage(t, fx.grain))
	require.Equal(t, "ctx-tick", expectGrainMessage(t, fx.grain))
}

func TestGrainContextTimerAPI(t *testing.T) {
	fx := spawnTimerProbeGrain(t)

	gctx := getGrainContext().build(context.Background(), fx.pid, fx.system, fx.identity, "probe", false)

	reference, err := gctx.ScheduleOnce("once", time.Hour, WithTimerReference("ctx-once"))
	require.NoError(t, err)
	require.Equal(t, "ctx-once", reference)
	require.NoError(t, gctx.CancelSchedule(reference))

	reference, err = gctx.Schedule("beat", time.Hour)
	require.NoError(t, err)
	require.NoError(t, gctx.CancelSchedule(reference))

	_, err = gctx.Schedule("beat", 0)
	require.ErrorIs(t, err, gerrors.ErrInvalidTimerInterval)

	reference, err = gctx.ScheduleWithCron("cron", everySecondCron)
	require.NoError(t, err)
	require.NoError(t, gctx.CancelSchedule(reference))

	_, err = gctx.ScheduleWithCron("cron", "not a cron expression")
	require.Error(t, err)
}

func TestGrainPropsTimerAPI(t *testing.T) {
	fx := spawnTimerProbeGrain(t)

	reference, err := fx.props.ScheduleOnce("once", time.Hour, WithTimerReference("props-once"))
	require.NoError(t, err)
	require.Equal(t, "props-once", reference)
	require.NoError(t, fx.props.CancelSchedule(reference))

	reference, err = fx.props.Schedule("beat", time.Hour)
	require.NoError(t, err)
	require.NoError(t, fx.props.CancelSchedule(reference))

	reference, err = fx.props.ScheduleWithCron("cron", everySecondCron)
	require.NoError(t, err)
	require.NoError(t, fx.props.CancelSchedule(reference))
}

func TestGrainContextTimersOnDeactivatedGrain(t *testing.T) {
	ctx := context.Background()
	fx := spawnTimerProbeGrain(t)

	require.NoError(t, fx.pid.deactivate(ctx))

	// every scheduling operation on a deactivated grain is rejected
	gctx := getGrainContext().build(ctx, fx.pid, fx.system, fx.identity, "probe", false)

	_, err := gctx.ScheduleOnce("tick", time.Second)
	require.ErrorIs(t, err, gerrors.ErrGrainTimersStopped)

	_, err = gctx.Schedule("tick", time.Second)
	require.ErrorIs(t, err, gerrors.ErrGrainTimersStopped)

	_, err = gctx.ScheduleWithCron("tick", everySecondCron)
	require.ErrorIs(t, err, gerrors.ErrGrainTimersStopped)

	require.ErrorIs(t, gctx.CancelSchedule("tick"), gerrors.ErrGrainTimersStopped)
}

func TestGrainContextTimersOnNeverActivatedGrain(t *testing.T) {
	grain := newTimerProbeGrain()
	pid := newTestGrainPID(grain, "never-activated")

	// a grain process that never activated has no registry to schedule into
	gctx := getGrainContext().build(context.Background(), pid, nil, pid.getIdentity(), "probe", false)

	_, err := gctx.ScheduleOnce("tick", time.Second)
	require.ErrorIs(t, err, gerrors.ErrGrainTimersStopped)

	_, err = gctx.Schedule("tick", time.Second)
	require.ErrorIs(t, err, gerrors.ErrGrainTimersStopped)

	_, err = gctx.ScheduleWithCron("tick", everySecondCron)
	require.ErrorIs(t, err, gerrors.ErrGrainTimersStopped)

	require.ErrorIs(t, gctx.CancelSchedule("tick"), gerrors.ErrGrainTimersStopped)
}

func TestGrainPIDDeliverTimerTickInactiveGrain(t *testing.T) {
	pid := &grainPID{mailbox: newGrainMailbox(0)}

	// an inactive grain drops the tick before it ever reaches the mailbox
	pid.deliverTimerTick(&grainTimerEntry{reference: "tick"})
	require.True(t, pid.mailbox.IsEmpty())
}

func TestGrainPIDDeliverTimerTickMailboxFull(t *testing.T) {
	grain := newTimerProbeGrain()
	pid := &grainPID{
		grain:    grain,
		identity: newGrainIdentity(grain, "mailbox-full"),
		mailbox:  newGrainMailbox(1),
		logger:   log.NewSlog(log.WarningLevel, io.Discard),
	}
	pid.activated.Store(true)

	// a full mailbox loses only this tick; the drop is logged
	require.NoError(t, pid.mailbox.Enqueue(&GrainContext{}))
	pid.deliverTimerTick(&grainTimerEntry{reference: "tick"})
	require.EqualValues(t, 1, pid.mailbox.Len())
}

func TestGrainPIDReportTimerTickFailure(t *testing.T) {
	grain := newTimerProbeGrain()
	pid := &grainPID{
		grain:    grain,
		identity: newGrainIdentity(grain, "tick-failure"),
		logger:   log.NewSlog(log.WarningLevel, io.Discard),
	}

	entry := &grainTimerEntry{reference: "tick"}
	grainContext := getGrainContext()
	grainContext.build(context.Background(), pid, nil, pid.getIdentity(), &grainTimerTick{entry: entry}, false)

	// an error reported during the tick is drained and logged
	grainContext.Err(errors.New("boom"))
	pid.reportTimerTickFailure(grainContext, entry)

	// nothing pending is a no-op
	pid.reportTimerTickFailure(grainContext, entry)
}

func TestGrainPIDHandleTimerTickDrops(t *testing.T) {
	grain := newTimerProbeGrain()
	pid := &grainPID{
		grain:    grain,
		identity: newGrainIdentity(grain, "drops"),
		logger:   log.DiscardLogger,
	}
	pid.activated.Store(true)

	tickContext := func(entry *grainTimerEntry) *GrainContext {
		grainContext := getGrainContext()
		return grainContext.build(context.Background(), pid, nil, pid.getIdentity(), &grainTimerTick{entry: entry}, false)
	}

	// cancelled entry: the tick was in the mailbox when its timer was cancelled
	cancelled := &grainTimerEntry{reference: "tick", message: "m"}
	cancelled.cancelled.Store(true)
	pid.handleTimerTick(tickContext(cancelled))
	require.Empty(t, grain.received)

	// inactive grain: the tick arrived while the grain deactivates
	pid.activated.Store(false)
	pid.handleTimerTick(tickContext(&grainTimerEntry{reference: "tick", message: "m"}))
	require.Empty(t, grain.received)

	require.Zero(t, pid.processedCount.Load())
}

func TestGrainPIDHandleTimerTickActivityMarking(t *testing.T) {
	grain := newTimerProbeGrain()
	pid := &grainPID{
		grain:    grain,
		identity: newGrainIdentity(grain, "activity"),
		logger:   log.DiscardLogger,
	}
	pid.activated.Store(true)

	tickContext := func(entry *grainTimerEntry) *GrainContext {
		grainContext := getGrainContext()
		return grainContext.build(context.Background(), pid, nil, pid.getIdentity(), &grainTimerTick{entry: entry}, false)
	}

	// a plain tick is delivered with the timer's message but does not count as
	// passivation activity
	pid.handleTimerTick(tickContext(&grainTimerEntry{reference: "plain", message: "hello"}))
	require.Equal(t, "hello", <-grain.received)
	require.Zero(t, pid.latestReceiveTimeNano.Load())

	// a keep-alive tick does
	pid.handleTimerTick(tickContext(&grainTimerEntry{reference: "keep", message: "hello", keepAlive: true}))
	require.Equal(t, "hello", <-grain.received)
	require.NotZero(t, pid.latestReceiveTimeNano.Load())
	require.EqualValues(t, 2, pid.processedCount.Load())
}

func TestGrainTimersCronEndToEnd(t *testing.T) {
	timers, deliveries := newTestGrainTimers()
	timers.start()

	// a real cron fire takes up to a full second to come due
	reference, err := timers.scheduleCron("tick", everySecondCron)
	require.NoError(t, err)

	entry := expectDelivery(t, deliveries)
	require.Equal(t, reference, entry.reference)

	// the entry survives ticks and stays registered
	require.Equal(t, 1, entriesLen(timers))
	require.NoError(t, timers.cancel(reference))
}
