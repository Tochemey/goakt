/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package actor

import (
	cheaps "container/heap"
	"context"
	"sync"
	"time"

	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v3/internal/registry"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/passivation"
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

	wake     chan registry.Unit
	stop     chan registry.Unit
	done     chan registry.Unit
	stopOnce sync.Once
	started  atomic.Bool

	messageTriggers chan *passivationEntry
	passivateFn     func(*passivationEntry) bool
}

// passivationEntry stores all scheduling metadata for a PID.
// Fields:
//   - pid/id:   PID pointer plus its stable ID string for map lookups.
//   - strategy: The selected passivation strategy (time-based or message-count).
//   - timeout:  Duration extracted from TimeBasedStrategy.
//   - deadline: Absolute timestamp for the next passivation attempt (time-based only).
//   - maxMessages: Threshold for MessagesCountBasedStrategy; <=0 means immediate passivation.
//   - baseline:    Processed count snapshot captured when the strategy is registered.
//   - index:       Current position within the heap; -1 means “not present in heap”. This lets
//     us call heap.Fix/Remove only when the entry is actually enqueued.
//   - paused:      Tracks whether scheduling is paused for this PID.
//   - pending:     Signals that a message-count trigger was raised but not yet processed.
//   - enqueued:    Guards against double-enqueueing onto messageTriggers.
type passivationEntry struct {
	pid         *PID
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
		wake:            make(chan registry.Unit, 1),
		stop:            make(chan registry.Unit),
		done:            make(chan registry.Unit),
		messageTriggers: make(chan *passivationEntry, 1024),
	}
}

func (m *passivationManager) Start(_ context.Context) {
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

// Register hooks a PID into the passivation scheduler using its selected strategy.
// Existing entries are updated in-place so swapping strategies at runtime remains safe.
func (m *passivationManager) Register(pid *PID, strategy passivation.Strategy) {
	key := pidKey(pid)
	if key == "" || strategy == nil || !m.started.Load() {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.entries[key]
	if !ok {
		entry = &passivationEntry{
			pid: pid,
			id:  key,
			// index starts at -1 to indicate the entry is not in the heap yet.
			// This prevents accidental heap.Fix/Remove calls before registration.
			index: -1,
		}
		m.entries[key] = entry
	} else if entry.index >= 0 {
		cheaps.Remove(&m.queue, entry.index)
		entry.index = -1
	}

	entry.strategy = strategy
	entry.paused = false
	entry.pending = false
	entry.enqueued = false

	switch strat := strategy.(type) {
	case *passivation.TimeBasedStrategy:
		entry.timeout = strat.Timeout()
		// Equivalent to running a ticker that periodically evaluates
		// elapsed := time.Since(pid.latestReceiveTime.Load()) and triggers
		// passivation when elapsed >= timeout. By storing the absolute
		// deadline (last activity + timeout) we avoid per-actor goroutines.
		entry.refreshDeadline()
		cheaps.Push(&m.queue, entry)
		m.notifyLocked()
	case *passivation.MessagesCountBasedStrategy:
		entry.maxMessages = strat.MaxMessages()
		entry.baseline = pid.processedCount.Load() + 1
	default:
		delete(m.entries, key)
	}
}

// Unregister removes a PID from any passivation bookkeeping.
func (m *passivationManager) Unregister(pid *PID) {
	key := pidKey(pid)
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

// Pause temporarily removes a PID from scheduling so passivation cannot fire.
func (m *passivationManager) Pause(pid *PID) {
	key := pidKey(pid)
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

// Resume reactivates scheduling for a paused PID.
// It requeues time-based entries onto the heap or drains any pending message-count trigger.
func (m *passivationManager) Resume(pid *PID) bool {
	key := pidKey(pid)
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
func (m *passivationManager) Touch(pid *PID) {
	key := pidKey(pid)
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
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}

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
				timer.Stop()
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
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			m.processMessageEntry(msgEntry)
		case <-m.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-m.stop:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
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

		wait := time.Until(entry.deadline)
		if wait < 0 {
			wait = 0
		}
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
	case m.wake <- registry.Unit{}:
	default:
	}
}

// refreshDeadline recomputes the absolute deadline for time-based passivation.
// It reads the PID's latestReceiveTime (which is updated after every message) and
// adds the configured timeout so the next passivation attempt only fires after an
// entire period of inactivity has elapsed.
func (entry *passivationEntry) refreshDeadline() {
	last := entry.pid.latestReceiveTime.Load()
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

	key := pidKey(pid)
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

	passivated := m.passivate(entry)

	m.mu.Lock()
	entry.enqueued = false
	current, ok := m.entries[entry.id]
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
	if entry == nil || entry.pid == nil {
		return false
	}
	return entry.pid.tryPassivation(passivationReason(entry))
}

// pidKey returns a stable string key for the PID map lookups.
func pidKey(pid *PID) string {
	if pid == nil || pid.Address() == nil {
		return ""
	}
	return pid.ID()
}

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
