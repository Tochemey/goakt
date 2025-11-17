package actor

import (
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
		logger:       log.DiscardLogger,
		onPoisonPill: atomic.NewBool(false),
		dependencies: collection.NewMap[string, extension.Dependency](),
	}
	pid.activated.Store(false)
	pid.deactivateAfter.Store(time.Second)
	require.False(t, pid.passivationTry("no-op"))
}

func TestGrainPIDPassivationTryFailsOnDeactivateError(t *testing.T) {
	pid := &grainPID{
		identity:           &GrainIdentity{kind: "Kind", name: "Name"},
		logger:             log.DiscardLogger,
		grain:              &MockGrainDeactivationFailure{},
		onPoisonPill:       atomic.NewBool(false),
		dependencies:       collection.NewMap[string, extension.Dependency](),
		passivationManager: nil,
	}

	pid.activated.Store(true)
	require.False(t, pid.passivationTry("deactivate failure"))
}

func TestGrainPIDStartPassivationSkipsWhenAutoDisabled(t *testing.T) {
	manager := newPassivationManager(log.DiscardLogger)
	manager.started.Store(true)

	pid := &grainPID{
		passivationManager: manager,
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
		logger:             log.DiscardLogger,
	}

	pid.deactivateAfter.Store(0)
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
		logger:             log.DiscardLogger,
	}

	pid.deactivateAfter.Store(time.Second)
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
	}
	pid.deactivateAfter.Store(time.Second)
	require.True(t, pid.shouldAutoPassivate())

	pid.passivationManager = nil
	require.False(t, pid.shouldAutoPassivate())
}
