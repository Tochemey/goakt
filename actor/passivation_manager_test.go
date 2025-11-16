package actor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/address"
	"github.com/tochemey/goakt/v3/internal/registry"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/passivation"
)

func MockPassivationPID(t *testing.T, name string, strategy passivation.Strategy) *PID {
	t.Helper()
	pid := &PID{
		address:             address.New(name, "test-system", "127.0.0.1", 0),
		passivationStrategy: strategy,
		logger:              log.DiscardLogger,
	}
	return pid
}

// nolint
func TestPassivationManager_TimeBasedTrigger(t *testing.T) {
	manager := newPassivationManager(log.DiscardLogger)
	defer manager.Stop(context.Background())

	triggered := make(chan struct{}, 1)
	manager.passivateFn = func(entry *passivationEntry) bool {
		select {
		case triggered <- registry.Unit{}:
		default:
		}
		return true
	}

	manager.Start(context.Background())

	timeout := 25 * time.Millisecond
	strategy := passivation.NewTimeBasedStrategy(timeout)
	pid := MockPassivationPID(t, "time-based", strategy)
	pid.latestReceiveTime.Store(time.Now().Add(-time.Minute))

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
		case triggered <- registry.Unit{}:
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
