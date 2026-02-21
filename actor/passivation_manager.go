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
	cheaps "container/heap"
	"context"
	"sync"
	"time"

	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/passivation"
)

// passivationManager centralizes all passivation scheduling for the actor system.
//   - Time-based strategies register a deadline and are tracked in a min-heap so
//     the manager can always fire the next expiring actor with O(log n) updates.
//     This is functionally identical to checking `elapsed := time.Since(pid.latestReceiveTime.Load())`
//     on every interval and passivating when `elapsed >= timeout`, but without per-actor goroutines.
//   - Message-count strategies record a baseline counter and push work onto
//     messageTriggers once their thresholds are reached. This avoids per-actor
//     goroutines and collapses both strategies into a single coordinator.
type passivationManager struct {
	logger log.Logger

	mu      sync.Mutex
	entries map[string]*passivationEntry
	queue   passivationHeap

	wake     chan types.Unit
	stop     chan types.Unit
	done     chan types.Unit
	stopOnce sync.Once
	started  atomic.Bool

	messageTriggers chan *passivationEntry
	passivateFn     func(*passivationEntry) bool
}

type passivationParticipant interface {
	passivationID() string
	passivationLatestActivity() time.Time
	passivationTry(reason string) bool
}

// passivationEntry stores all scheduling metadata for a participant.
// Fields:
//   - target/id: participant plus its stable ID string for map lookups.
//   - strategy: The selected passivation strategy (time-based or message-count).
//   - timeout:  Duration extracted from TimeBasedStrategy.
//   - deadline: Absolute timestamp for the next passivation attempt (time-based only).
//   - maxMessages: Threshold for MessagesCountBasedStrategy; <=0 means immediate passivation.
//   - baseline:    Processed count snapshot captured when the strategy is registered.
//   - index:       Current position within the heap; -1 means “not present in heap”.
//   - paused:      Tracks whether scheduling is paused for this participant.
//   - pending:     Signals that a message-count trigger was raised but not yet processed.
//   - enqueued:    Guards against double-enqueueing onto messageTriggers.
type passivationEntry struct {
	target      passivationParticipant
	id          string
	strategy    passivation.Strategy
	timeout     time.Duration
	deadline    time.Time
	maxMessages int
	baseline    int64
	index       int
	paused      bool
	pending     bool
	enqueued    bool
}

func newPassivationManager(logger log.Logger) *passivationManager {
	return &passivationManager{
		logger:          logger,
		entries:         make(map[string]*passivationEntry),
		queue:           passivationHeap{},
		wake:            make(chan types.Unit, 1),
		stop:            make(chan types.Unit),
		done:            make(chan types.Unit),
		messageTriggers: make(chan *passivationEntry, 1024),
	}
}

func (m *passivationManager) Start(context.Context) {
	if !m.started.CompareAndSwap(false, true) {
		return
	}

	go m.run()
}

func (m *passivationManager) Stop(context.Context) {
	if !m.started.Load() {
		return
	}

	m.stopOnce.Do(func() {
		close(m.stop)
	})
	<-m.done
	m.started.Store(false)
}

// Register hooks a participant into the passivation scheduler using its selected strategy.
// Existing entries are updated in-place so swapping strategies at runtime remains safe.
func (m *passivationManager) Register(participant passivationParticipant, strategy passivation.Strategy) {
	key := participant.passivationID()
	if key == "" || strategy == nil || !m.started.Load() {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.entries[key]
	if !ok {
		entry = &passivationEntry{
			target: participant,
			id:     key,
			index:  -1,
		}
		m.entries[key] = entry
	} else if entry.index >= 0 {
		cheaps.Remove(&m.queue, entry.index)
		entry.index = -1
	}

	entry.target = participant
	entry.strategy = strategy
	entry.paused = false
	entry.pending = false
	entry.enqueued = false

	switch s := strategy.(type) {
	case *passivation.TimeBasedStrategy:
		entry.timeout = s.Timeout()
		// Equivalent to running a ticker that periodically evaluates
		// elapsed := time.Since(participant.passivationLatestActivity()) and triggers
		// passivation when elapsed >= timeout. By storing the absolute
		// deadline (last activity + timeout) we avoid per-actor goroutines.
		entry.refreshDeadline()
		cheaps.Push(&m.queue, entry)
		m.notifyLocked()
	case *passivation.MessagesCountBasedStrategy:
		entry.maxMessages = s.MaxMessages()
		if pid, ok := participant.(*PID); ok {
			entry.baseline = pid.processedCount.Load() + 1
		} else {
			entry.baseline = 0
		}
	default:
		delete(m.entries, key)
	}
}

// Unregister removes a participant from any passivation bookkeeping.
func (m *passivationManager) Unregister(participant passivationParticipant) {
	key := participant.passivationID()
	if key == "" {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.entries[key]
	if !ok {
		return
	}

	if entry.index >= 0 {
		cheaps.Remove(&m.queue, entry.index)
		entry.index = -1
	}
	delete(m.entries, key)
}

// Pause temporarily removes a participant from scheduling so passivation cannot fire.
func (m *passivationManager) Pause(participant passivationParticipant) {
	key := participant.passivationID()
	if key == "" {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.entries[key]
	if !ok || entry.paused {
		return
	}

	entry.paused = true
	if entry.index >= 0 {
		cheaps.Remove(&m.queue, entry.index)
		entry.index = -1
	}
}

// Resume reactivates scheduling for a paused participant.
// It requeues time-based entries onto the heap or drains any pending message-count trigger.
func (m *passivationManager) Resume(participant passivationParticipant) bool {
	key := participant.passivationID()
	if key == "" {
		return false
	}

	m.mu.Lock()
	entry, ok := m.entries[key]
	if !ok || !entry.paused {
		m.mu.Unlock()
		return ok
	}

	entry.paused = false

	var messageEntry *passivationEntry
	if _, ok := entry.strategy.(*passivation.TimeBasedStrategy); ok {
		entry.refreshDeadline()
		cheaps.Push(&m.queue, entry)
		m.notifyLocked()
	} else if entry.pending && !entry.enqueued {
		entry.enqueued = true
		messageEntry = entry
	}
	m.mu.Unlock()

	if messageEntry != nil {
		m.signalMessageEntry(messageEntry)
	}
	return true
}

// Touch refreshes the inactivity deadline for time-based strategies after a message was processed.
func (m *passivationManager) Touch(participant passivationParticipant) {
	key := participant.passivationID()
	if key == "" {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.entries[key]
	if !ok || entry.paused {
		return
	}

	if _, ok := entry.strategy.(*passivation.TimeBasedStrategy); !ok || entry.index < 0 {
		return
	}

	entry.refreshDeadline()
	cheaps.Fix(&m.queue, entry.index)
	m.notifyLocked()
}

// run multiplexes between timeouts, message-count triggers, and shutdown signals.
// Only one goroutine is needed for all actors, drastically reducing footprint.
func (m *passivationManager) run() {
	// Initialize the timer with an arbitrary long duration. We stop it right away
	// so the first iteration can safely Reset() to the actual wait time without
	// allocating a new timer per loop. This pattern avoids both extra heap churn
	// and subtle races where Reset is called on an uninitialized timer.
	timer := time.NewTimer(time.Hour)
	stopTimer(timer)

	for {
		entry, wait := m.nextEntry()
		if entry == nil {
			select {
			case <-m.wake:
				continue
			case msgEntry := <-m.messageTriggers:
				m.processMessageEntry(msgEntry)
				continue
			case <-m.stop:
				stopTimer(timer)
				close(m.done)
				return
			}
		}

		if wait <= 0 {
			m.trigger(entry)
			continue
		}

		timer.Reset(wait)

		select {
		case <-timer.C:
			m.trigger(entry)
		case msgEntry := <-m.messageTriggers:
			stopTimer(timer)
			m.processMessageEntry(msgEntry)
		case <-m.wake:
			stopTimer(timer)
		case <-m.stop:
			stopTimer(timer)
			close(m.done)
			return
		}
	}
}

func (m *passivationManager) nextEntry() (*passivationEntry, time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for len(m.queue) > 0 {
		entry := m.queue[0]
		if entry.paused {
			cheaps.Remove(&m.queue, entry.index)
			entry.index = -1
			continue
		}

		wait := max(time.Until(entry.deadline), 0)
		return entry, wait
	}

	return nil, 0
}

func (m *passivationManager) trigger(expected *passivationEntry) {
	for {
		m.mu.Lock()
		if len(m.queue) == 0 {
			m.mu.Unlock()
			return
		}

		entry := m.queue[0]
		if entry != expected {
			m.mu.Unlock()
			return
		}

		now := time.Now()
		if entry.deadline.After(now) {
			m.mu.Unlock()
			return
		}

		cheaps.Pop(&m.queue)
		entry.index = -1
		m.mu.Unlock()

		passivated := m.passivate(entry)

		m.mu.Lock()
		current, ok := m.entries[entry.id]
		if !ok || current != entry {
			m.mu.Unlock()
			return
		}

		if passivated {
			delete(m.entries, entry.id)
			m.mu.Unlock()
			return
		}

		if entry.paused {
			m.mu.Unlock()
			return
		}

		entry.refreshDeadline()
		cheaps.Push(&m.queue, entry)
		m.mu.Unlock()
		m.notify()
	}
}

func (m *passivationManager) notify() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notifyLocked()
}

func (m *passivationManager) notifyLocked() {
	select {
	case m.wake <- types.Unit{}:
	default:
	}
}

// refreshDeadline recomputes the absolute deadline for time-based passivation.
// It reads the participant's latest activity timestamp (which is updated after every message) and
// adds the configured timeout so the next passivation attempt only fires after an
// entire period of inactivity has elapsed.
func (entry *passivationEntry) refreshDeadline() {
	last := entry.target.passivationLatestActivity()
	if last.IsZero() {
		last = time.Now()
	}
	entry.deadline = last.Add(entry.timeout)
}

// MessageProcessed checks message-count strategies after each processed message.
// Once the delta between the current counter and the baseline reaches the configured
// maxMessages the entry is enqueued for passivation.
func (m *passivationManager) MessageProcessed(pid *PID) {
	if pid == nil || !m.started.Load() {
		return
	}

	key := pid.passivationID()
	if key == "" {
		return
	}

	m.mu.Lock()
	entry, ok := m.entries[key]
	if !ok {
		m.mu.Unlock()
		return
	}

	if _, ok := entry.strategy.(*passivation.MessagesCountBasedStrategy); !ok {
		m.mu.Unlock()
		return
	}

	current := int64(pid.ProcessedCount())
	threshold := entry.baseline + int64(entry.maxMessages)

	if current < threshold {
		m.mu.Unlock()
		return
	}

	entry.pending = true
	if entry.paused || entry.enqueued {
		m.mu.Unlock()
		return
	}

	entry.enqueued = true
	m.mu.Unlock()
	m.signalMessageEntry(entry)
}

// processMessageEntry services the messageTriggers channel.
// It attempts passivation and, if the attempt was skipped (e.g., due to a pause/reinstate),
// re-enqueues the entry until a definitive outcome is reached.
func (m *passivationManager) processMessageEntry(entry *passivationEntry) {
	if entry == nil {
		return
	}

	m.mu.Lock()
	current, ok := m.entries[entry.id]
	if !ok || current != entry {
		m.mu.Unlock()
		return
	}
	if entry.paused {
		entry.enqueued = false
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	passivated := m.passivate(entry)

	m.mu.Lock()
	entry.enqueued = false
	current, ok = m.entries[entry.id]
	if !ok || current != entry {
		m.mu.Unlock()
		return
	}

	if passivated {
		delete(m.entries, entry.id)
		entry.pending = false
		m.mu.Unlock()
		return
	}

	if entry.paused {
		m.mu.Unlock()
		return
	}

	if entry.pending && !entry.enqueued {
		entry.enqueued = true
		m.mu.Unlock()
		m.signalMessageEntry(entry)
		return
	}

	m.mu.Unlock()
}

// signalMessageEntry schedules a message-count entry for asynchronous processing.
// The buffered channel avoids blocking the critical path, and we fall back to a goroutine
// when the buffer is full to preserve ordering without deadlocking.
func (m *passivationManager) signalMessageEntry(entry *passivationEntry) {
	if entry == nil || !m.started.Load() {
		return
	}

	select {
	case m.messageTriggers <- entry:
	default:
		go func() {
			m.messageTriggers <- entry
		}()
	}
}

// passivate delegates to the PID and returns whether the passivation succeeded.
// A false result usually means a reinstate/pause occurred and the entry should be retried.
func (m *passivationManager) passivate(entry *passivationEntry) bool {
	if m.passivateFn != nil {
		return m.passivateFn(entry)
	}
	if entry == nil || entry.target == nil {
		return false
	}
	return entry.target.passivationTry(passivationReason(entry))
}

// passivationReason extracts a human-readable reason from the entry's strategy.
func passivationReason(entry *passivationEntry) string {
	if entry == nil || entry.strategy == nil {
		return ""
	}
	return entry.strategy.Name()
}

type passivationHeap []*passivationEntry

func (h passivationHeap) Len() int { return len(h) }

func (h passivationHeap) Less(i, j int) bool {
	return h[i].deadline.Before(h[j].deadline)
}

func (h passivationHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *passivationHeap) Push(x any) {
	entry := x.(*passivationEntry)
	entry.index = len(*h)
	*h = append(*h, entry)
}

func (h *passivationHeap) Pop() any {
	old := *h
	n := len(old)
	entry := old[n-1]
	entry.index = -1
	*h = old[:n-1]
	return entry
}

// stopTimer stops a timer and drains its channel when the timer has already fired.
// This mirrors the recommended pattern from the Go time package to ensure timers
// can be safely reused after Stop reports false.
func stopTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}
