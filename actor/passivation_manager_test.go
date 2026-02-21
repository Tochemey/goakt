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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/passivation"
)

// nolint
func TestPassivationManager_TimeBasedTrigger(t *testing.T) {
	manager := newPassivationManager(log.DiscardLogger)
	defer manager.Stop(context.Background())

	triggered := make(chan struct{}, 1)
	manager.passivateFn = func(entry *passivationEntry) bool {
		select {
		case triggered <- struct{}{}:
		default:
		}
		return true
	}

	manager.Start(context.Background())

	timeout := 25 * time.Millisecond
	strategy := passivation.NewTimeBasedStrategy(timeout)
	pid := MockPassivationPID(t, "time-based", strategy)
	pid.latestReceiveTimeNano.Store(time.Now().Add(-time.Minute).UnixNano())

	manager.Register(pid, strategy)

	select {
	case <-triggered:
	case <-time.After(time.Second):
		t.Fatal("expected time-based passivation to trigger")
	}
}

// nolint
func TestPassivationManager_MessageCountTrigger(t *testing.T) {
	manager := newPassivationManager(log.DiscardLogger)
	defer manager.Stop(context.Background())

	triggered := make(chan struct{}, 1)
	manager.passivateFn = func(entry *passivationEntry) bool {
		select {
		case triggered <- struct{}{}:
		default:
		}
		return true
	}

	manager.Start(context.Background())

	strategy := passivation.NewMessageCountBasedStrategy(2)
	pid := MockPassivationPID(t, "message-based", strategy)
	manager.Register(pid, strategy)

	// Simulate the actor having processed enough messages to cross the threshold.
	pid.processedCount.Store(3)
	manager.MessageProcessed(pid)

	select {
	case <-triggered:
	case <-time.After(time.Second):
		t.Fatal("expected message-count passivation to trigger")
	}

	// Ensure that once passivation has completed the entry is removed.
	require.Eventually(t, func() bool {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		_, ok := manager.entries[pid.ID()]
		return !ok
	}, time.Second, 5*time.Millisecond)
}

func TestPassivationManager_MessageProcessedGuards(t *testing.T) {
	t.Run("skips enqueue when entry paused", func(t *testing.T) {
		manager := newPassivationManager(log.DiscardLogger)
		manager.started.Store(true)

		strategy := passivation.NewMessageCountBasedStrategy(1)
		pid := MockPassivationPID(t, "paused-entry", strategy)
		manager.Register(pid, strategy)

		manager.mu.Lock()
		entry := manager.entries[pid.ID()]
		entry.paused = true
		threshold := entry.baseline + int64(entry.maxMessages)
		manager.mu.Unlock()

		pid.processedCount.Store(threshold)

		manager.MessageProcessed(pid)

		manager.mu.Lock()
		require.True(t, entry.pending, "pending flag should be set")
		require.False(t, entry.enqueued, "paused entry must not enqueue")
		manager.mu.Unlock()
		require.Equal(t, 0, len(manager.messageTriggers))
	})

	t.Run("skips enqueue when already enqueued", func(t *testing.T) {
		manager := newPassivationManager(log.DiscardLogger)
		manager.started.Store(true)

		strategy := passivation.NewMessageCountBasedStrategy(1)
		pid := MockPassivationPID(t, "enqueued-entry", strategy)
		manager.Register(pid, strategy)

		manager.mu.Lock()
		entry := manager.entries[pid.ID()]
		entry.enqueued = true
		threshold := entry.baseline + int64(entry.maxMessages)
		manager.mu.Unlock()

		pid.processedCount.Store(threshold)

		manager.MessageProcessed(pid)

		manager.mu.Lock()
		require.True(t, entry.pending, "pending flag should stay set")
		require.True(t, entry.enqueued, "entry should remain enqueued")
		manager.mu.Unlock()
		require.Equal(t, 0, len(manager.messageTriggers))
	})
}

func TestPassivationManager_ProcessMessageEntry_PostPassivate(t *testing.T) {
	t.Run("returns early when paused after passivate", func(t *testing.T) {
		manager := newPassivationManager(log.DiscardLogger)
		manager.started.Store(true)

		entry := &passivationEntry{
			target:   &MockPassivationParticipant{id: "paused-post", last: time.Now()},
			id:       "paused-post",
			strategy: passivation.NewMessageCountBasedStrategy(1),
			pending:  true,
			enqueued: true,
		}
		manager.entries[entry.id] = entry
		manager.passivateFn = func(pe *passivationEntry) bool {
			pe.paused = true
			return false
		}

		manager.processMessageEntry(entry)

		manager.mu.Lock()
		require.True(t, entry.paused)
		require.False(t, entry.enqueued)
		manager.mu.Unlock()
		require.Equal(t, 0, len(manager.messageTriggers))
	})

	t.Run("re-enqueues pending entry after skipped passivation", func(t *testing.T) {
		manager := newPassivationManager(log.DiscardLogger)
		manager.started.Store(true)

		entry := &passivationEntry{
			target:   &MockPassivationParticipant{id: "pending-requeue", last: time.Now()},
			id:       "pending-requeue",
			strategy: passivation.NewMessageCountBasedStrategy(1),
			pending:  true,
		}
		manager.entries[entry.id] = entry
		manager.passivateFn = func(*passivationEntry) bool { return false }

		manager.processMessageEntry(entry)

		manager.mu.Lock()
		require.True(t, entry.enqueued, "entry should be marked as enqueued")
		manager.mu.Unlock()

		select {
		case got := <-manager.messageTriggers:
			require.Equal(t, entry, got)
		default:
			t.Fatal("expected entry to be scheduled again")
		}
	})
}

func TestPassivationManager_SignalMessageEntryDefault(t *testing.T) {
	manager := newPassivationManager(log.DiscardLogger)
	manager.started.Store(true)
	manager.messageTriggers = make(chan *passivationEntry, 1)

	first := &passivationEntry{id: "first"}
	second := &passivationEntry{id: "second"}

	// Fill the buffer so signalMessageEntry must take the default branch.
	manager.messageTriggers <- first

	manager.signalMessageEntry(second)

	require.Equal(t, 1, len(manager.messageTriggers), "channel should remain full until drained")

	select {
	case got := <-manager.messageTriggers:
		require.Equal(t, first, got)
	case <-time.After(time.Second):
		t.Fatal("expected to receive buffered entry")
	}

	select {
	case got := <-manager.messageTriggers:
		require.Equal(t, second, got)
	case <-time.After(time.Second):
		t.Fatal("expected default branch to schedule entry asynchronously")
	}
}

func TestPassivationManager_RegisterGuards(t *testing.T) {
	t.Run("ignores when not started", func(t *testing.T) {
		manager := newPassivationManager(log.DiscardLogger)
		strategy := passivation.NewTimeBasedStrategy(time.Second)
		participant := &MockPassivationParticipant{id: "guarded", last: time.Now()}

		manager.Register(participant, strategy)

		manager.mu.Lock()
		defer manager.mu.Unlock()
		require.Empty(t, manager.entries)
	})

	t.Run("ignores empty id", func(t *testing.T) {
		manager := newPassivationManager(log.DiscardLogger)
		manager.Start(context.Background())
		defer manager.Stop(context.Background())

		strategy := passivation.NewTimeBasedStrategy(time.Second)
		manager.Register(&MockPassivationParticipant{last: time.Now()}, strategy)

		manager.mu.Lock()
		defer manager.mu.Unlock()
		require.Empty(t, manager.entries)
	})

	t.Run("ignores nil strategy", func(t *testing.T) {
		manager := newPassivationManager(log.DiscardLogger)
		manager.Start(context.Background())
		defer manager.Stop(context.Background())

		manager.Register(&MockPassivationParticipant{id: "no-strategy", last: time.Now()}, nil)

		manager.mu.Lock()
		defer manager.mu.Unlock()
		require.Empty(t, manager.entries)
	})
}

func TestPassivationManager_RegisterStrategies(t *testing.T) {
	t.Run("updates existing time-based entry", func(t *testing.T) {
		manager := newPassivationManager(log.DiscardLogger)
		manager.Start(context.Background())
		defer manager.Stop(context.Background())

		strategy := passivation.NewTimeBasedStrategy(time.Minute)
		pid := MockPassivationPID(t, "time-entry", strategy)
		pid.latestReceiveTimeNano.Store(time.Now().UnixNano())

		manager.Register(pid, strategy)

		manager.mu.Lock()
		entry := manager.entries[pid.ID()]
		require.NotNil(t, entry)
		require.Equal(t, 0, entry.index)
		manager.mu.Unlock()

		pid.latestReceiveTimeNano.Store(time.Now().Add(time.Second).UnixNano())
		manager.Register(pid, strategy)

		manager.mu.Lock()
		entry = manager.entries[pid.ID()]
		require.NotNil(t, entry)
		require.Equal(t, 1, len(manager.queue))
		require.Equal(t, 0, entry.index)
		manager.mu.Unlock()
	})

	t.Run("records baseline for non pid message strategy", func(t *testing.T) {
		manager := newPassivationManager(log.DiscardLogger)
		manager.Start(context.Background())
		defer manager.Stop(context.Background())

		strategy := passivation.NewMessageCountBasedStrategy(5)
		participant := &MockPassivationParticipant{id: "custom", last: time.Now()}
		manager.Register(participant, strategy)

		manager.mu.Lock()
		entry := manager.entries[participant.id]
		manager.mu.Unlock()

		require.NotNil(t, entry)
		require.Equal(t, int64(0), entry.baseline)
	})

	t.Run("removes unknown strategies", func(t *testing.T) {
		manager := newPassivationManager(log.DiscardLogger)
		manager.Start(context.Background())
		defer manager.Stop(context.Background())

		participant := &MockPassivationParticipant{id: "fake", last: time.Now()}
		manager.Register(participant, &MockFakePassivationStrategy{})

		manager.mu.Lock()
		_, ok := manager.entries[participant.id]
		manager.mu.Unlock()

		require.False(t, ok)
	})
}

func TestPassivationManager_GuardMethods(t *testing.T) {
	manager := newPassivationManager(log.DiscardLogger)

	require.False(t, manager.Resume(&MockPassivationParticipant{}))
	manager.Unregister(&MockPassivationParticipant{})
	manager.Touch(&MockPassivationParticipant{})
}

func TestPassivationManager_PauseIgnoresEmptyID(t *testing.T) {
	manager := newPassivationManager(log.DiscardLogger)
	manager.Start(context.Background())
	t.Cleanup(func() {
		manager.Stop(context.Background())
	})

	manager.Pause(&MockPassivationParticipant{})

	manager.mu.Lock()
	defer manager.mu.Unlock()
	require.Empty(t, manager.entries)
}

func TestPassivationManager_ResumeMessageStrategy(t *testing.T) {
	manager := newPassivationManager(log.DiscardLogger)
	manager.started.Store(true)

	manager.messageTriggers = make(chan *passivationEntry, 1)

	strategy := passivation.NewMessageCountBasedStrategy(1)
	pid := MockPassivationPID(t, "resume-message", strategy)
	manager.Register(pid, strategy)

	manager.mu.Lock()
	entry := manager.entries[pid.ID()]
	entry.paused = true
	entry.pending = true
	manager.mu.Unlock()

	resumed := manager.Resume(pid)
	require.True(t, resumed, "resume should succeed for paused entry")

	manager.mu.Lock()
	require.True(t, entry.enqueued, "entry should be marked as enqueued")
	manager.mu.Unlock()

	select {
	case got := <-manager.messageTriggers:
		require.Equal(t, entry, got)
	case <-time.After(time.Second):
		t.Fatal("expected message entry to be signaled")
	}

	// Ensure the channel is empty for the next phase
	require.Equal(t, 0, len(manager.messageTriggers))
}

func TestPassivationManager_ResumeSignalsWhenChannelFull(t *testing.T) {
	manager := newPassivationManager(log.DiscardLogger)
	manager.started.Store(true)
	manager.messageTriggers = make(chan *passivationEntry, 1)

	strategy := passivation.NewMessageCountBasedStrategy(1)
	pid := MockPassivationPID(t, "resume-message-full-channel", strategy)
	manager.Register(pid, strategy)

	manager.mu.Lock()
	entry := manager.entries[pid.ID()]
	entry.paused = true
	entry.pending = true
	manager.mu.Unlock()

	// Fill the channel so Resume must rely on the signalMessageEntry default branch.
	blocker := &passivationEntry{id: "blocker"}
	manager.messageTriggers <- blocker

	require.True(t, manager.Resume(pid))

	// Drain the blocker to free capacity and expect our entry to arrive.
	require.Equal(t, blocker, <-manager.messageTriggers)

	select {
	case got := <-manager.messageTriggers:
		require.Equal(t, entry, got)
	case <-time.After(time.Second):
		t.Fatal("expected pending entry to be signaled once capacity freed")
	}
}

func TestPassivationManager_NextEntrySkipsPaused(t *testing.T) {
	manager := newPassivationManager(log.DiscardLogger)

	paused := &passivationEntry{
		id:       "paused",
		deadline: time.Now().Add(time.Hour),
		paused:   true,
	}
	active := &passivationEntry{
		id:       "active",
		deadline: time.Now().Add(2 * time.Hour),
	}

	cheaps.Push(&manager.queue, paused)
	cheaps.Push(&manager.queue, active)

	entry, wait := manager.nextEntry()
	require.Equal(t, active, entry)
	require.Greater(t, wait, time.Duration(0))
	require.Equal(t, -1, paused.index)
}

func TestPassivationManager_TriggerPaths(t *testing.T) {
	t.Run("ignores mismatched entry", func(t *testing.T) {
		manager := newPassivationManager(log.DiscardLogger)
		entry := &passivationEntry{
			id:       "expected",
			deadline: time.Now().Add(-time.Second),
		}
		manager.entries[entry.id] = entry
		cheaps.Push(&manager.queue, entry)

		manager.trigger(&passivationEntry{id: "other"})

		manager.mu.Lock()
		defer manager.mu.Unlock()
		require.Equal(t, entry, manager.queue[0])
	})

	t.Run("requeues when passivation skipped", func(t *testing.T) {
		manager := newPassivationManager(log.DiscardLogger)
		stub := &MockPassivationParticipant{
			id:   "requeue",
			last: time.Now(),
		}
		entry := &passivationEntry{
			target:   stub,
			id:       stub.id,
			strategy: passivation.NewTimeBasedStrategy(time.Second),
			timeout:  time.Second,
			deadline: time.Now().Add(-time.Second),
		}
		manager.entries[entry.id] = entry
		cheaps.Push(&manager.queue, entry)
		manager.passivateFn = func(*passivationEntry) bool { return false }

		manager.trigger(entry)

		manager.mu.Lock()
		require.Equal(t, entry, manager.queue[0])
		require.Equal(t, 0, entry.index)
		manager.mu.Unlock()

		select {
		case <-manager.wake:
		case <-time.After(time.Second):
			t.Fatal("expected notify to signal wake channel")
		}
	})

	t.Run("returns early when entry paused mid-trigger", func(t *testing.T) {
		manager := newPassivationManager(log.DiscardLogger)
		stub := &MockPassivationParticipant{
			id:   "paused-mid-trigger",
			last: time.Now(),
		}
		entry := &passivationEntry{
			target:   stub,
			id:       stub.id,
			strategy: passivation.NewTimeBasedStrategy(time.Second),
			timeout:  time.Second,
			deadline: time.Now().Add(-time.Second),
		}
		manager.entries[entry.id] = entry
		cheaps.Push(&manager.queue, entry)
		manager.passivateFn = func(pe *passivationEntry) bool {
			pe.paused = true
			return false
		}

		manager.trigger(entry)

		require.Equal(t, -1, entry.index)
		manager.mu.Lock()
		_, tracked := manager.entries[entry.id]
		manager.mu.Unlock()
		require.True(t, tracked)
	})
}

func TestPassivationManager_Notify(t *testing.T) {
	manager := newPassivationManager(log.DiscardLogger)

	manager.notify()

	select {
	case <-manager.wake:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("expected notify to signal wake channel")
	}
}

func TestPassivationManager_RunHandlesChannels(t *testing.T) {
	manager := newPassivationManager(log.DiscardLogger)
	passivated := make(chan string, 1)
	manager.passivateFn = func(entry *passivationEntry) bool {
		passivated <- entry.id
		return true
	}

	manager.Start(context.Background())
	t.Cleanup(func() {
		manager.Stop(context.Background())
	})

	timeStrategy := passivation.NewTimeBasedStrategy(2 * time.Second)
	timePID := MockPassivationPID(t, "timer", timeStrategy)
	timePID.latestReceiveTimeNano.Store(time.Now().UnixNano())
	manager.Register(timePID, timeStrategy)

	msgStrategy := passivation.NewMessageCountBasedStrategy(1)
	msgPID := MockPassivationPID(t, "message", msgStrategy)
	manager.Register(msgPID, msgStrategy)

	msgPID.processedCount.Store(2)
	manager.MessageProcessed(msgPID)

	require.Eventually(t, func() bool {
		select {
		case id := <-passivated:
			return id == msgPID.ID()
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond, "expected message entry passivation")

	time.Sleep(10 * time.Millisecond)
	manager.notify()
	require.Eventually(t, func() bool {
		return len(manager.wake) == 0
	}, time.Second, time.Millisecond, "wake signal not drained")

	manager.Stop(context.Background())
}

func TestStopTimerDrainsExpiredTimer(t *testing.T) {
	timer := time.NewTimer(5 * time.Millisecond)
	time.Sleep(10 * time.Millisecond) // ensure the timer fires

	stopTimer(timer)

	select {
	case <-timer.C:
		t.Fatal("timer channel should have been drained")
	default:
	}
}

func TestStopTimerHandlesActiveTimer(t *testing.T) {
	timer := time.NewTimer(time.Hour)

	stopTimer(timer)

	select {
	case <-timer.C:
		t.Fatal("timer should not fire after stop")
	default:
	}
}
