package actor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/atomic"

	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/extension"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/ds"
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
		dependencies: ds.NewMap[string, extension.Dependency](),
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
		dependencies:       ds.NewMap[string, extension.Dependency](),
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

func TestGrainPIDActivateReturnsPanicErrorOnActivatePanic(t *testing.T) {
	config := newGrainConfig()
	pid := &grainPID{
		identity:     &GrainIdentity{kind: "Kind", name: "Name"},
		logger:       log.DiscardLogger,
		grain:        &MockPanickingActivateDeactivateGrain{activatePanicValue: "activate panic"},
		dependencies: config.dependencies,
		config:       config,
	}

	var err error
	require.NotPanics(t, func() {
		err = pid.activate(context.Background())
	})
	require.Error(t, err)
	require.ErrorIs(t, err, gerrors.ErrGrainActivationFailure)
	var panicErr *gerrors.PanicError
	require.ErrorAs(t, err, &panicErr)
}

func TestGrainPIDActivateReturnsPanicErrorOnActivateErrorPanic(t *testing.T) {
	config := newGrainConfig()
	panicErr := errors.New("activate error panic")
	pid := &grainPID{
		identity:     &GrainIdentity{kind: "Kind", name: "Name"},
		logger:       log.DiscardLogger,
		grain:        &MockPanickingActivateDeactivateGrain{activatePanicValue: panicErr},
		dependencies: config.dependencies,
		config:       config,
	}

	err := pid.activate(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, gerrors.ErrGrainActivationFailure)
	require.ErrorIs(t, err, panicErr)
	var panicErrResult *gerrors.PanicError
	require.ErrorAs(t, err, &panicErrResult)
}

func TestGrainPIDActivateReturnsPanicErrorOnActivatePanicError(t *testing.T) {
	config := newGrainConfig()
	panicErr := gerrors.NewPanicError(errors.New("activate panic error"))
	pid := &grainPID{
		identity:     &GrainIdentity{kind: "Kind", name: "Name"},
		logger:       log.DiscardLogger,
		grain:        &MockPanickingActivateDeactivateGrain{activatePanicValue: panicErr},
		dependencies: config.dependencies,
		config:       config,
	}

	err := pid.activate(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, gerrors.ErrGrainActivationFailure)
	require.ErrorIs(t, err, panicErr)
	var panicErrResult *gerrors.PanicError
	require.ErrorAs(t, err, &panicErrResult)
	require.Same(t, panicErr, panicErrResult)
}

func TestGrainPIDDeactivateReturnsPanicErrorOnDeactivatePanic(t *testing.T) {
	pid := &grainPID{
		identity:     &GrainIdentity{kind: "Kind", name: "Name"},
		logger:       log.DiscardLogger,
		grain:        &MockPanickingActivateDeactivateGrain{},
		onPoisonPill: atomic.NewBool(false),
		dependencies: ds.NewMap[string, extension.Dependency](),
	}
	pid.activated.Store(true)

	var err error
	require.NotPanics(t, func() {
		err = pid.deactivate(context.Background())
	})
	require.Error(t, err)
	require.ErrorIs(t, err, gerrors.ErrGrainDeactivationFailure)
	var panicErr *gerrors.PanicError
	require.ErrorAs(t, err, &panicErr)
}

func TestGrainPIDDeactivateReturnsPanicErrorOnDeactivateErrorPanic(t *testing.T) {
	panicErr := errors.New("deactivate error panic")
	pid := &grainPID{
		identity:     &GrainIdentity{kind: "Kind", name: "Name"},
		logger:       log.DiscardLogger,
		grain:        &MockPanickingActivateDeactivateGrain{panicValue: panicErr},
		onPoisonPill: atomic.NewBool(false),
		dependencies: ds.NewMap[string, extension.Dependency](),
	}
	pid.activated.Store(true)

	err := pid.deactivate(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, gerrors.ErrGrainDeactivationFailure)
	require.ErrorIs(t, err, panicErr)
	var panicErrResult *gerrors.PanicError
	require.ErrorAs(t, err, &panicErrResult)
}

func TestGrainPIDDeactivateReturnsPanicErrorOnDeactivatePanicError(t *testing.T) {
	panicErr := gerrors.NewPanicError(errors.New("deactivate panic error"))
	pid := &grainPID{
		identity:     &GrainIdentity{kind: "Kind", name: "Name"},
		logger:       log.DiscardLogger,
		grain:        &MockPanickingActivateDeactivateGrain{panicValue: panicErr},
		onPoisonPill: atomic.NewBool(false),
		dependencies: ds.NewMap[string, extension.Dependency](),
	}
	pid.activated.Store(true)

	err := pid.deactivate(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, gerrors.ErrGrainDeactivationFailure)
	require.ErrorIs(t, err, panicErr)
	var panicErrResult *gerrors.PanicError
	require.ErrorAs(t, err, &panicErrResult)
	require.Same(t, panicErr, panicErrResult)
}

func TestGrainPIDHandlePoisonPillRecoversDeactivatePanic(t *testing.T) {
	pid := &grainPID{
		identity:     &GrainIdentity{kind: "Kind", name: "Name"},
		logger:       log.DiscardLogger,
		grain:        &MockPanickingActivateDeactivateGrain{},
		onPoisonPill: atomic.NewBool(false),
		dependencies: ds.NewMap[string, extension.Dependency](),
	}
	pid.activated.Store(true)

	grainContext := getGrainContext().build(
		context.Background(),
		pid,
		nil,
		pid.identity,
		&goaktpb.PoisonPill{},
		false,
	)
	t.Cleanup(func() {
		releaseGrainContext(grainContext)
	})

	require.NotPanics(t, func() {
		pid.handlePoisonPill(grainContext)
	})

	err := <-grainContext.getError()
	require.Error(t, err)
	require.ErrorIs(t, err, gerrors.ErrGrainDeactivationFailure)
	var panicErr *gerrors.PanicError
	require.ErrorAs(t, err, &panicErr)
}
