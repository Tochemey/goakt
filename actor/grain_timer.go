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
	"sync"
	"time"

	"github.com/reugn/go-quartz/quartz"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v4/errors"
)

// grainTimerKind discriminates the three firing disciplines a grain timer can have.
type grainTimerKind int

const (
	// oneShotGrainTimer fires once after its delay and leaves the registry at
	// fire time.
	oneShotGrainTimer grainTimerKind = iota
	// intervalGrainTimer fires repeatedly at a fixed cadence, like the actor
	// scheduler's Schedule. Ticks of a handler slower than the interval queue up
	// in the mailbox; execution itself never overlaps because the grain is
	// single-threaded.
	intervalGrainTimer
	// cronGrainTimer fires at wall-clock instants computed from a cron expression.
	cronGrainTimer
)

// grainTimerEntry is a single registered grain timer. Entries are created by the
// schedule methods of grainTimers and flow through delivery as the payload of a
// tick.
type grainTimerEntry struct {
	reference string
	message   any
	kind      grainTimerKind
	// every is the delay before a one-shot fires, or the period of an interval timer.
	every time.Duration
	// trigger computes the wall-clock fire instants; nil unless kind is cronGrainTimer.
	trigger quartz.Trigger
	// keepAlive marks ticks of this timer as passivation activity. By default a
	// tick does not keep the grain alive, matching Orleans timer semantics.
	keepAlive bool

	// cancelled flips exactly once, when the entry is cancelled or replaced or the
	// registry stops. It is atomic because the tick handler reads it outside the
	// registry lock to drop a tick that was already in the mailbox when its timer
	// was cancelled.
	cancelled atomic.Bool
	// timer is the entry's single owned fire trigger, created when the entry first
	// starts and reset for every subsequent fire so steady-state interval and cron
	// ticks allocate nothing. Nil until the registry starts the entry; registrations
	// made before activation completes stay dormant until then. Guarded by the
	// registry lock.
	timer *time.Timer
	// tick is the entry's reusable mailbox envelope, built once at registration.
	// It is immutable, so concurrent in-flight ticks can share it safely and
	// steady-state deliveries allocate no envelope.
	tick *grainTimerTick
}

// grainTimerTick is the internal mailbox envelope of a due grain timer tick. The
// tick handler unwraps it so OnReceive sees the timer's message, and uses the
// entry to drop ticks of cancelled timers. It is a local-only message and is
// never serialized.
type grainTimerTick struct {
	entry *grainTimerEntry
}

// grainTimers holds the timers of a single grain activation.
//
// The registry is activation-scoped: activate installs a fresh registry and
// starts it once activation completes, and deactivate stops it before OnDeactivate
// runs. A stopped registry rejects every operation, so late registrations from a
// deactivating grain cannot fire ticks into a dead grain.
//
// References are unique per grain: registering a timer under a reference that is
// already in use cancels and replaces the existing timer.
type grainTimers struct {
	mu      sync.Mutex
	entries map[string]*grainTimerEntry
	// started flips once activation completes. Entries registered before that (from
	// OnActivate) stay dormant so a short-delay tick cannot fire into the
	// not-yet-active grain and be silently dropped.
	started bool
	// stopped flips once the grain deactivates or its activation fails; a
	// stopped registry rejects every operation and is never restarted.
	stopped bool
	// deliver hands a due tick to the owning grain. Re-arming happens at fire
	// time, before delivery, so a failed delivery only loses that one tick; the
	// grain logs the drop.
	deliver func(*grainTimerEntry)
}

// newGrainTimers creates a new, not-yet-started registry delivering ticks through deliver.
func newGrainTimers(deliver func(*grainTimerEntry)) *grainTimers {
	return &grainTimers{
		entries: make(map[string]*grainTimerEntry),
		deliver: deliver,
	}
}

// scheduleOnce registers a timer firing message exactly once after delay.
// A non-positive delay fires as soon as the registry starts.
// It returns the timer reference to use with cancel.
func (x *grainTimers) scheduleOnce(message any, delay time.Duration, opts ...GrainTimerOption) (string, error) {
	config := newGrainTimerConfig(opts...)

	return x.register(&grainTimerEntry{
		reference: config.reference,
		message:   message,
		kind:      oneShotGrainTimer,
		every:     delay,
		keepAlive: config.keepAlive,
	})
}

// scheduleInterval registers a timer firing message repeatedly at the given
// fixed interval. It returns the timer reference to use with cancel.
func (x *grainTimers) scheduleInterval(message any, interval time.Duration, opts ...GrainTimerOption) (string, error) {
	if interval <= 0 {
		return "", errors.ErrInvalidTimerInterval
	}

	config := newGrainTimerConfig(opts...)

	return x.register(&grainTimerEntry{
		reference: config.reference,
		message:   message,
		kind:      intervalGrainTimer,
		every:     interval,
		keepAlive: config.keepAlive,
	})
}

// scheduleCron registers a timer firing message at the instants described by
// cronExpression, evaluated in the process's local timezone. Grain timers need no
// cluster-wide arbitration because a grain has exactly one activation cluster-wide.
// It returns the timer reference to use with cancel.
func (x *grainTimers) scheduleCron(message any, cronExpression string, opts ...GrainTimerOption) (string, error) {
	trigger, err := quartz.NewCronTriggerWithLoc(cronExpression, time.Now().Location())
	if err != nil {
		return "", err
	}

	config := newGrainTimerConfig(opts...)

	return x.register(&grainTimerEntry{
		reference: config.reference,
		message:   message,
		kind:      cronGrainTimer,
		trigger:   trigger,
		keepAlive: config.keepAlive,
	})
}

// cancel stops the timer registered under reference and removes it. A tick of the
// cancelled timer already sitting in the mailbox is dropped by the tick handler.
// A one-shot that already fired has left the registry, so cancelling it returns
// ErrScheduledReferenceNotFound, matching the actor scheduler's CancelSchedule.
func (x *grainTimers) cancel(reference string) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if x.stopped {
		return errors.ErrGrainTimersStopped
	}

	entry, ok := x.entries[reference]
	if !ok {
		return errors.ErrScheduledReferenceNotFound
	}

	x.cancelEntryLocked(entry)
	delete(x.entries, reference)
	return nil
}

// start starts the timers of every entry registered so far and makes subsequent
// registrations start immediately. Called once activation completes; idempotent,
// and a no-op on a stopped registry.
func (x *grainTimers) start() {
	x.mu.Lock()
	defer x.mu.Unlock()

	if x.stopped || x.started {
		return
	}

	x.started = true

	for _, entry := range x.entries {
		x.startEntryLocked(entry)
	}
}

// stop cancels every timer and rejects all further operations. Called by
// deactivate before OnDeactivate runs, and by activate on activation failure to
// discard timers registered by a failed OnActivate. A grainPID reused across
// activations installs a fresh registry rather than restarting a stopped one.
func (x *grainTimers) stop() {
	x.mu.Lock()
	defer x.mu.Unlock()

	if x.stopped {
		return
	}

	x.stopped = true

	for reference, entry := range x.entries {
		x.cancelEntryLocked(entry)
		delete(x.entries, reference)
	}
}

// register inserts entry, cancelling and replacing any existing timer registered
// under the same reference, and starts it right away when the registry is already
// started. It returns the entry's reference.
func (x *grainTimers) register(entry *grainTimerEntry) (string, error) {
	x.mu.Lock()
	defer x.mu.Unlock()

	if x.stopped {
		return "", errors.ErrGrainTimersStopped
	}

	if existing, ok := x.entries[entry.reference]; ok {
		x.cancelEntryLocked(existing)
	}

	entry.tick = &grainTimerTick{entry: entry}
	x.entries[entry.reference] = entry
	if x.started {
		x.startEntryLocked(entry)
	}

	return entry.reference, nil
}

// fire runs on the timer goroutine when entry comes due. Unless the entry was
// cancelled or the registry stopped, it settles the entry's future first and then
// delivers the tick: a one-shot is spent and leaves the registry, an interval
// timer re-arms for a fixed cadence, and a cron timer re-arms for its next
// wall-clock instant.
func (x *grainTimers) fire(entry *grainTimerEntry) {
	x.mu.Lock()

	if x.stopped || entry.cancelled.Load() {
		x.mu.Unlock()
		return
	}

	switch entry.kind {
	case oneShotGrainTimer:
		delete(x.entries, entry.reference)
	case intervalGrainTimer:
		x.fireAfterLocked(entry, entry.every)
	case cronGrainTimer:
		x.scheduleNextCronFireLocked(entry)
	}

	x.mu.Unlock()

	x.deliver(entry)
}

// cancelEntryLocked marks entry cancelled and stops its fire trigger.
// Callers own x.mu and the removal of the entry from the map where applicable.
func (x *grainTimers) cancelEntryLocked(entry *grainTimerEntry) {
	entry.cancelled.Store(true)

	if entry.timer != nil {
		entry.timer.Stop()
	}
}

// startEntryLocked starts entry's timer so it fires on schedule. Callers own x.mu.
func (x *grainTimers) startEntryLocked(entry *grainTimerEntry) {
	switch entry.kind {
	case oneShotGrainTimer, intervalGrainTimer:
		x.fireAfterLocked(entry, entry.every)
	case cronGrainTimer:
		x.scheduleNextCronFireLocked(entry)
	}
}

// scheduleNextCronFireLocked schedules entry's timer for its next cron instant.
// An entry whose trigger has no future fire time can never fire again and is
// removed. Callers own x.mu.
func (x *grainTimers) scheduleNextCronFireLocked(entry *grainTimerEntry) {
	now := quartz.NowNano()

	next, err := entry.trigger.NextFireTime(now)
	if err != nil {
		delete(x.entries, entry.reference)
		return
	}

	x.fireAfterLocked(entry, time.Duration(next-now))
}

// fireAfterLocked makes entry fire after delay, creating the entry's single owned
// timer on first use and resetting it afterwards. Reset on an AfterFunc timer
// reschedules the same callback, so repeat fires allocate nothing. Re-arming only
// happens from within fire itself, so it never races a pending callback of the
// same entry. Callers own x.mu.
func (x *grainTimers) fireAfterLocked(entry *grainTimerEntry, delay time.Duration) {
	if entry.timer != nil {
		entry.timer.Reset(delay)
		return
	}

	entry.timer = time.AfterFunc(delay, func() { x.fire(entry) })
}
