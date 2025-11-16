package actor

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v3/extension"
	"github.com/tochemey/goakt/v3/internal/collection"
	"github.com/tochemey/goakt/v3/log"
)

func TestGrainPIDPassivationIDEmptyWithoutIdentity(t *testing.T) {
	pid := &grainPID{}
	require.Equal(t, "", pid.passivationID())
}

func TestGrainPIDPassivationTrySkipsWhenInactive(t *testing.T) {
	pid := &grainPID{
		logger:          log.DiscardLogger,
		activated:       atomic.NewBool(false),
		onPoisonPill:    atomic.NewBool(false),
		dependencies:    collection.NewMap[string, extension.Dependency](),
		mu:              &sync.Mutex{},
		deactivateAfter: atomic.NewDuration(time.Second),
	}
	require.False(t, pid.passivationTry("no-op"))
}

func TestGrainPIDPassivationTryFailsOnDeactivateError(t *testing.T) {
	pid := &grainPID{
		identity:           &GrainIdentity{kind: "Kind", name: "Name"},
		logger:             log.DiscardLogger,
		grain:              &MockGrainDeactivationFailure{},
		activated:          atomic.NewBool(true),
		onPoisonPill:       atomic.NewBool(false),
		dependencies:       collection.NewMap[string, extension.Dependency](),
		mu:                 &sync.Mutex{},
		passivationManager: nil,
	}

	require.False(t, pid.passivationTry("deactivate failure"))
}

func TestGrainPIDStartPassivationSkipsWhenAutoDisabled(t *testing.T) {
	manager := newPassivationManager(log.DiscardLogger)
	manager.started.Store(true)

	pid := &grainPID{
		passivationManager: manager,
		deactivateAfter:    nil,
	}

	pid.startPassivation()

	manager.mu.Lock()
	defer manager.mu.Unlock()
	require.Zero(t, len(manager.entries))
}

func TestGrainPIDStartPassivationSkipsWhenTimeoutNonPositive(t *testing.T) {
	manager := newPassivationManager(log.DiscardLogger)
	manager.started.Store(true)

	pid := &grainPID{
		identity:           &GrainIdentity{kind: "Kind", name: "Name"},
		passivationManager: manager,
		deactivateAfter:    atomic.NewDuration(0),
		logger:             log.DiscardLogger,
		mu:                 &sync.Mutex{},
	}

	pid.startPassivation()

	manager.mu.Lock()
	defer manager.mu.Unlock()
	require.Zero(t, len(manager.entries))
}

func TestGrainPIDStartPassivationRegistersStrategy(t *testing.T) {
	manager := newPassivationManager(log.DiscardLogger)
	manager.started.Store(true)

	pid := &grainPID{
		identity:           &GrainIdentity{kind: "Kind", name: "Name"},
		passivationManager: manager,
		deactivateAfter:    atomic.NewDuration(time.Second),
		logger:             log.DiscardLogger,
		mu:                 &sync.Mutex{},
	}

	pid.startPassivation()

	manager.mu.Lock()
	defer manager.mu.Unlock()
	require.Contains(t, manager.entries, pid.identity.String())
}

func TestGrainPIDShouldAutoPassivate(t *testing.T) {
	manager := newPassivationManager(log.DiscardLogger)
	manager.started.Store(true)
	pid := &grainPID{
		passivationManager: manager,
		deactivateAfter:    atomic.NewDuration(time.Second),
	}
	require.True(t, pid.shouldAutoPassivate())

	pid.passivationManager = nil
	require.False(t, pid.shouldAutoPassivate())
}
