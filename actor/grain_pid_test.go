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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/atomic"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/internal/commands"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/xsync"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/reentrancy"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

func TestGrainPIDPassivationIDEmptyWithoutIdentity(t *testing.T) {
	pid := &grainPID{}
	require.Equal(t, "", pid.passivationID())
}

func TestGrainPIDPassivationTrySkipsWhenInactive(t *testing.T) {
	pid := &grainPID{
		logger:       log.DiscardLogger,
		dependencies: xsync.NewMap[string, extension.Dependency](),
	}
	pid.onPoisonPill.Store(false)
	pid.activated.Store(false)
	pid.deactivateAfter.Store(time.Second)
	require.False(t, pid.passivationTry("no-op"))
}

func TestGrainPIDPassivationTryFailsOnDeactivateError(t *testing.T) {
	pid := &grainPID{
		identity:           &GrainIdentity{kind: "Kind", name: "Name"},
		logger:             log.DiscardLogger,
		grain:              &MockGrainDeactivationFailure{},
		dependencies:       xsync.NewMap[string, extension.Dependency](),
		passivationManager: nil,
	}

	pid.activated.Store(true)
	pid.onPoisonPill.Store(false)
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
	require.Zero(t, pid.uptime())
	var panicErrResult *gerrors.PanicError
	require.ErrorAs(t, err, &panicErrResult)
	require.Same(t, panicErr, panicErrResult)
}

func TestGrainPIDDeactivateReturnsPanicErrorOnDeactivatePanic(t *testing.T) {
	pid := &grainPID{
		identity:     &GrainIdentity{kind: "Kind", name: "Name"},
		logger:       log.DiscardLogger,
		grain:        &MockPanickingActivateDeactivateGrain{},
		dependencies: xsync.NewMap[string, extension.Dependency](),
	}
	pid.onPoisonPill.Store(false)
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
		dependencies: xsync.NewMap[string, extension.Dependency](),
	}
	pid.onPoisonPill.Store(false)
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
		dependencies: xsync.NewMap[string, extension.Dependency](),
	}
	pid.onPoisonPill.Store(false)
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
		dependencies: xsync.NewMap[string, extension.Dependency](),
	}
	pid.onPoisonPill.Store(false)
	pid.activated.Store(true)

	grainContext := getGrainContext().build(
		context.Background(),
		pid,
		nil,
		pid.identity,
		&PoisonPill{},
		grainTell,
	)
	t.Cleanup(func() {
		releaseGrainContext(grainContext)
	})

	require.NotPanics(t, func() {
		pid.handlePoisonPill(grainContext)
	})

	err := <-grainContext.err
	require.Error(t, err)
	require.ErrorIs(t, err, gerrors.ErrGrainDeactivationFailure)
	var panicErr *gerrors.PanicError
	require.ErrorAs(t, err, &panicErr)
}

func TestToWireGrainDisableRelocation(t *testing.T) {
	ctx := t.Context()
	sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	t.Cleanup(func() { _ = sys.Stop(ctx) })

	identity := newGrainIdentity(&MockGrain{}, "wire-default")
	pid := newGrainPID(identity, &MockGrain{}, sys, newGrainConfig())
	wire, err := pid.toWireGrain()
	require.NoError(t, err)
	require.False(t, wire.GetDisableRelocation())

	identity = newGrainIdentity(&MockGrain{}, "wire-disabled")
	pid = newGrainPID(identity, &MockGrain{}, sys, newGrainConfig(WithGrainDisableRelocation()))
	wire, err = pid.toWireGrain()
	require.NoError(t, err)
	require.True(t, wire.GetDisableRelocation())
}

// reentrantRecordingGrain records what its OnReceive handles so the pause and
// envelope tests can assert exactly which messages were processed, in which
// order, and with which request metadata.
type reentrantRecordingGrain struct {
	mu      sync.Mutex
	records []recordedGrainMessage
}

type recordedGrainMessage struct {
	message   any
	requestID string
	replyTo   *commands.AsyncReplyTo
}

var _ Grain = (*reentrantRecordingGrain)(nil)

func (g *reentrantRecordingGrain) OnActivate(context.Context, *GrainProps) error { return nil }

func (g *reentrantRecordingGrain) OnDeactivate(context.Context, *GrainProps) error { return nil }

func (g *reentrantRecordingGrain) OnReceive(gctx *GrainContext) {
	g.mu.Lock()
	g.records = append(g.records, recordedGrainMessage{
		message:   gctx.Message(),
		requestID: gctx.requestID,
		replyTo:   gctx.requestReplyTo,
	})
	g.mu.Unlock()

	// Envelope contexts carry no channels; only ordinary messages ack.
	if gctx.err != nil {
		gctx.NoErr()
	}
}

func (g *reentrantRecordingGrain) recorded() []recordedGrainMessage {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]recordedGrainMessage(nil), g.records...)
}

func (g *reentrantRecordingGrain) messages() []any {
	recorded := g.recorded()
	messages := make([]any, 0, len(recorded))

	for _, record := range recorded {
		messages = append(messages, record.message)
	}
	return messages
}

// startReentrantGrainFixture starts a system, activates a recording grain and
// equips its pid with reentrancy state the way the config plumbing will. The
// logger discards but stays enabled so the debug and warning paths execute.
func startReentrantGrainFixture(t *testing.T, mode reentrancy.Mode) (*actorSystem, *grainPID, *reentrantRecordingGrain, *GrainIdentity) {
	t.Helper()
	ctx := context.Background()

	system, err := NewActorSystem("testSys", WithLogger(log.NewSlog(log.DebugLevel, io.Discard)))
	require.NoError(t, err)
	require.NoError(t, system.Start(ctx))

	t.Cleanup(func() {
		_ = system.Stop(context.Background())
	})

	grain := &reentrantRecordingGrain{}
	identity, err := system.GrainIdentity(ctx, "reentrantGrain", func(context.Context) (Grain, error) {
		return grain, nil
	})
	require.NoError(t, err)

	sys := system.(*actorSystem)
	pid, ok := sys.grains.Get(identity.String())
	require.True(t, ok)

	pid.reentrancy.Store(newReentrancyState(mode, 0))
	pid.responses = newGrainMailbox(0)

	return sys, pid, grain, identity
}

// registerGrainRequestState mirrors the admission bookkeeping the public
// request API will perform on-turn: Step 5 owns completion and teardown, so
// these tests seed in-flight states directly.
func registerGrainRequestState(pid *grainPID, correlationID string, mode reentrancy.Mode, callback func(any, error)) *requestState {
	state := newRequestState(correlationID, mode, pid)
	if callback != nil {
		state.setCallback(callback)
	}

	pid.reentrancy.Load().requestStates.Set(correlationID, state)
	pid.reentrancy.Load().inFlightCount.Inc()

	if mode == reentrancy.StashNonReentrant {
		pid.reentrancy.Load().blockingCount.Inc()
	}
	return state
}

func TestGrainAsyncResponseCompletesRequest(t *testing.T) {
	_, pid, _, _ := startReentrantGrainFixture(t, reentrancy.AllowAll)

	var (
		mu     sync.Mutex
		result any
		resErr error
	)
	done := make(chan struct{})

	registerGrainRequestState(pid, "corr-1", reentrancy.AllowAll, func(res any, err error) {
		mu.Lock()
		result = res
		resErr = err
		mu.Unlock()
		close(done)
	})

	reply := &testpb.Reply{Content: "pong"}
	require.NoError(t, pid.enqueueEnvelope(context.Background(), &commands.AsyncResponse{
		CorrelationID: "corr-1",
		Message:       reply,
	}))

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("continuation did not run")
	}

	mu.Lock()
	defer mu.Unlock()
	require.NoError(t, resErr)
	require.Same(t, reply, result)
	require.Zero(t, pid.reentrancy.Load().inFlightCount.Load())

	_, ok := pid.reentrancy.Load().requestStates.Get("corr-1")
	require.False(t, ok)
}

func TestGrainAsyncResponseRestoresErrorIdentity(t *testing.T) {
	_, pid, _, _ := startReentrantGrainFixture(t, reentrancy.AllowAll)

	var failure error
	done := make(chan struct{})

	registerGrainRequestState(pid, "corr-timeout", reentrancy.AllowAll, func(_ any, err error) {
		failure = err
		close(done)
	})

	require.NoError(t, pid.enqueueEnvelope(context.Background(), &commands.AsyncResponse{
		CorrelationID: "corr-timeout",
		Error:         gerrors.ErrRequestTimeout.Error(),
	}))

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("continuation did not run")
	}
	require.ErrorIs(t, failure, gerrors.ErrRequestTimeout)
}

// An empty response is the wire form of a NoErr reply: success without a
// payload.
func TestGrainAsyncResponseWithoutPayloadCompletesAsSuccess(t *testing.T) {
	_, pid, _, _ := startReentrantGrainFixture(t, reentrancy.AllowAll)

	type outcome struct {
		result any
		err    error
	}
	outcomes := make(chan outcome, 1)

	registerGrainRequestState(pid, "corr-empty", reentrancy.AllowAll, func(result any, err error) {
		outcomes <- outcome{result: result, err: err}
	})

	require.NoError(t, pid.enqueueEnvelope(context.Background(), &commands.AsyncResponse{
		CorrelationID: "corr-empty",
	}))

	select {
	case got := <-outcomes:
		require.NoError(t, got.err)
		require.Nil(t, got.result)
	case <-time.After(2 * time.Second):
		t.Fatal("continuation did not run")
	}
}

func TestGrainAsyncResponseUnknownCorrelationDropped(t *testing.T) {
	sys, pid, grain, identity := startReentrantGrainFixture(t, reentrancy.AllowAll)
	ctx := context.Background()

	require.NoError(t, pid.enqueueEnvelope(ctx, &commands.AsyncResponse{
		CorrelationID: "ghost",
		Message:       &testpb.Reply{},
	}))

	// The drop must not disturb ordinary traffic.
	require.NoError(t, sys.TellGrain(ctx, identity, new(testpb.TestSend)))
	require.Eventually(t, func() bool {
		return len(grain.recorded()) == 1
	}, 2*time.Second, 10*time.Millisecond)
}

func TestGrainStashPausesUserMailboxUntilCompletion(t *testing.T) {
	sys, pid, grain, identity := startReentrantGrainFixture(t, reentrancy.StashNonReentrant)

	done := make(chan struct{})
	registerGrainRequestState(pid, "blocking", reentrancy.StashNonReentrant, func(any, error) {
		close(done)
	})
	require.True(t, pid.paused())

	// Buffer user messages behind the pause. Each receive schedules a turn,
	// which must park without consuming the user mailbox.
	first := &testpb.Reply{Content: "first"}
	second := &testpb.Reply{Content: "second"}
	third := &testpb.Reply{Content: "third"}

	for _, message := range []*testpb.Reply{first, second, third} {
		gctx := getGrainContext().build(context.Background(), pid, sys, identity, message, grainTell)
		pid.receive(gctx)
	}

	pause.For(200 * time.Millisecond)
	require.Empty(t, grain.recorded())
	require.EqualValues(t, 3, pid.mailbox.Len())

	// The completion flows through the response queue, resumes consumption and
	// releases the buffered messages in exact arrival order.
	require.NoError(t, pid.enqueueEnvelope(context.Background(), &commands.AsyncResponse{
		CorrelationID: "blocking",
		Message:       &testpb.Reply{Content: "done"},
	}))

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("continuation did not run")
	}

	require.Eventually(t, func() bool {
		return len(grain.recorded()) == 3
	}, 2*time.Second, 10*time.Millisecond)
	require.Equal(t, []any{first, second, third}, grain.messages())
	require.False(t, pid.paused())
}

func TestGrainAsyncErrorWakesPausedGrain(t *testing.T) {
	sys, pid, grain, identity := startReentrantGrainFixture(t, reentrancy.StashNonReentrant)

	var failure error
	done := make(chan struct{})

	registerGrainRequestState(pid, "blocking", reentrancy.StashNonReentrant, func(_ any, err error) {
		failure = err
		close(done)
	})

	gctx := getGrainContext().build(context.Background(), pid, sys, identity, &testpb.Reply{Content: "waiting"}, grainTell)
	pid.receive(gctx)

	pause.For(100 * time.Millisecond)
	require.Empty(t, grain.recorded())

	// The timeout path is queue-routed, so it must wake the parked grain,
	// unpause it and let the buffered message process.
	require.NoError(t, pid.enqueueAsyncError(context.Background(), "blocking", gerrors.ErrRequestTimeout))

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout was not delivered")
	}
	require.ErrorIs(t, failure, gerrors.ErrRequestTimeout)

	require.Eventually(t, func() bool {
		return len(grain.recorded()) == 1
	}, 2*time.Second, 10*time.Millisecond)
	require.False(t, pid.paused())
}

func TestGrainPoisonPillDuringPauseCancelsInFlight(t *testing.T) {
	sys, pid, _, identity := startReentrantGrainFixture(t, reentrancy.StashNonReentrant)

	var failure error
	done := make(chan struct{})

	registerGrainRequestState(pid, "blocking", reentrancy.StashNonReentrant, func(_ any, err error) {
		failure = err
		close(done)
	})

	// The pill waits in the user mailbox behind the pause.
	gctx := getGrainContext().build(context.Background(), pid, sys, identity, new(PoisonPill), grainTell)
	pid.receive(gctx)

	pause.For(100 * time.Millisecond)
	require.True(t, pid.isActive())

	// Shutdown's pre-pass: queue-routed cancellations unpause the grain so the
	// pill can deactivate it.
	pid.enqueueInFlightCancellations()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not complete the request")
	}
	require.ErrorIs(t, failure, gerrors.ErrRequestCanceled)

	select {
	case <-pid.deactivated:
	case <-time.After(2 * time.Second):
		t.Fatal("grain did not deactivate")
	}

	require.False(t, pid.isActive())
	require.Zero(t, pid.reentrancy.Load().inFlightCount.Load())
	require.Zero(t, pid.reentrancy.Load().blockingCount.Load())
}

func TestGrainPoisonPillTearsDownInFlightInline(t *testing.T) {
	sys, pid, _, identity := startReentrantGrainFixture(t, reentrancy.AllowAll)

	var failure error
	done := make(chan struct{})

	state := registerGrainRequestState(pid, "in-flight", reentrancy.AllowAll, func(_ any, err error) {
		failure = err
		close(done)
	})
	state.startTimeout(time.Minute)

	gctx := getGrainContext().build(context.Background(), pid, sys, identity, new(PoisonPill), grainTell)
	pid.receive(gctx)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("teardown did not complete the request")
	}
	require.ErrorIs(t, failure, gerrors.ErrRequestCanceled)

	select {
	case <-pid.deactivated:
	case <-time.After(2 * time.Second):
		t.Fatal("grain did not deactivate")
	}

	require.Zero(t, pid.reentrancy.Load().inFlightCount.Load())
	require.Zero(t, pid.reentrancy.Load().blockingCount.Load())
	require.Empty(t, pid.reentrancy.Load().requestStates.Keys())
}

func TestGrainShutdownCancelsInFlightRequests(t *testing.T) {
	ctx := context.Background()
	system, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, system.Start(ctx))

	grain := &reentrantRecordingGrain{}
	identity, err := system.GrainIdentity(ctx, "reentrantGrain", func(context.Context) (Grain, error) {
		return grain, nil
	})
	require.NoError(t, err)

	sys := system.(*actorSystem)
	pid, ok := sys.grains.Get(identity.String())
	require.True(t, ok)

	pid.reentrancy.Store(newReentrancyState(reentrancy.StashNonReentrant, 0))
	pid.responses = newGrainMailbox(0)

	var failure error
	done := make(chan struct{})

	registerGrainRequestState(pid, "blocking", reentrancy.StashNonReentrant, func(_ any, err error) {
		failure = err
		close(done)
	})
	require.True(t, pid.paused())

	require.NoError(t, system.Stop(ctx))

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not cancel the request")
	}
	require.ErrorIs(t, failure, gerrors.ErrRequestCanceled)
	require.False(t, pid.isActive())
}

func TestGrainAsyncRequestDeliversInnerMessage(t *testing.T) {
	_, pid, grain, _ := startReentrantGrainFixture(t, reentrancy.AllowAll)

	replyTo := &commands.AsyncReplyTo{Kind: commands.ReplyToGrain, Grain: "MockGrain/other"}
	payload := &testpb.Reply{Content: "inner"}

	require.NoError(t, pid.enqueueEnvelope(context.Background(), &commands.AsyncRequest{
		CorrelationID: "req-1",
		ReplyTo:       replyTo,
		Message:       payload,
	}))

	require.Eventually(t, func() bool {
		return len(grain.recorded()) == 1
	}, 2*time.Second, 10*time.Millisecond)

	record := grain.recorded()[0]
	require.Same(t, payload, record.message)
	require.Equal(t, "req-1", record.requestID)
	require.Same(t, replyTo, record.replyTo)
}

func TestGrainAsyncRequestMalformedDropped(t *testing.T) {
	sys, pid, grain, identity := startReentrantGrainFixture(t, reentrancy.AllowAll)
	ctx := context.Background()

	envelopes := []*commands.AsyncRequest{
		{Message: &testpb.Reply{}}, // missing correlation ID
		{CorrelationID: "req-1"},   // missing payload
		{CorrelationID: "req-2", Message: &testpb.Reply{}, ReplyTo: &commands.AsyncReplyTo{Kind: commands.ReplyToGrain}}, // invalid reply target
	}

	for _, envelope := range envelopes {
		require.NoError(t, pid.enqueueEnvelope(ctx, envelope))
	}

	pause.For(200 * time.Millisecond)
	require.Empty(t, grain.recorded())

	// The grain remains live after the drops.
	require.NoError(t, sys.TellGrain(ctx, identity, new(testpb.TestSend)))
	require.Eventually(t, func() bool {
		return len(grain.recorded()) == 1
	}, 2*time.Second, 10*time.Millisecond)
}

func TestGrainAsyncResponsePanicInContinuationIsContained(t *testing.T) {
	sys, pid, grain, identity := startReentrantGrainFixture(t, reentrancy.AllowAll)
	ctx := context.Background()

	registerGrainRequestState(pid, "boom", reentrancy.AllowAll, func(any, error) {
		panic("continuation exploded")
	})

	require.NoError(t, pid.enqueueEnvelope(ctx, &commands.AsyncResponse{
		CorrelationID: "boom",
		Message:       &testpb.Reply{},
	}))

	// The worker survived the panic and keeps serving the grain.
	require.NoError(t, sys.TellGrain(ctx, identity, new(testpb.TestSend)))
	require.Eventually(t, func() bool {
		return len(grain.recorded()) == 1
	}, 2*time.Second, 10*time.Millisecond)
}

func TestGrainEnqueueAsyncErrorValidation(t *testing.T) {
	t.Run("empty correlation id", func(t *testing.T) {
		pid := &grainPID{}
		require.ErrorIs(t, pid.enqueueAsyncError(context.Background(), "", errors.New("boom")), gerrors.ErrInvalidMessage)
	})

	t.Run("nil error is a no-op", func(t *testing.T) {
		pid := &grainPID{responses: newGrainMailbox(0)}
		pid.activated.Store(true)
		require.NoError(t, pid.enqueueAsyncError(context.Background(), "corr", nil))
		require.True(t, pid.responses.IsEmpty())
	})

	t.Run("inactive grain", func(t *testing.T) {
		pid := &grainPID{}
		require.ErrorIs(t, pid.enqueueAsyncError(context.Background(), "corr", errors.New("boom")), gerrors.ErrDead)
	})

	t.Run("no response queue", func(t *testing.T) {
		pid := &grainPID{}
		pid.activated.Store(true)
		require.ErrorIs(t, pid.enqueueAsyncError(context.Background(), "corr", errors.New("boom")), gerrors.ErrReentrancyDisabled)
	})
}

func TestGrainEnqueueEnvelopeValidation(t *testing.T) {
	t.Run("unknown envelope type", func(t *testing.T) {
		pid := &grainPID{}
		pid.activated.Store(true)
		require.ErrorIs(t, pid.enqueueEnvelope(context.Background(), "bogus"), gerrors.ErrInvalidMessage)
	})

	t.Run("full mailbox", func(t *testing.T) {
		pid := &grainPID{
			identity: &GrainIdentity{kind: "Kind", name: "Name"},
			mailbox:  newGrainMailbox(1),
		}
		pid.activated.Store(true)
		require.NoError(t, pid.mailbox.Enqueue(new(GrainContext)))

		err := pid.enqueueEnvelope(context.Background(), &commands.AsyncRequest{
			CorrelationID: "req",
			Message:       &testpb.Reply{},
		})
		require.ErrorIs(t, err, gerrors.ErrMailboxFull)
	})
}

func TestGrainInFlightCancellationFailureLogged(t *testing.T) {
	// An inactive grain rejects the queue-routed cancellation; the failure is
	// logged and must not panic the shutdown pre-pass.
	pid := &grainPID{
		identity: &GrainIdentity{kind: "Kind", name: "Name"},
		logger:   log.NewSlog(log.DebugLevel, io.Discard),
	}
	pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 0))

	registerGrainRequestState(pid, "corr", reentrancy.AllowAll, nil)

	require.NotPanics(t, func() {
		pid.enqueueInFlightCancellations()
	})
}

func TestGrainRequestHelpersWithoutReentrancy(t *testing.T) {
	pid := &grainPID{}

	require.False(t, pid.completeRequest("corr", nil, nil))
	require.NotPanics(t, func() {
		pid.deregisterRequestState(nil)
		pid.teardownInFlightRequests()
		pid.enqueueInFlightCancellations()
	})
}

func TestGrainCompleteRequestDuplicate(t *testing.T) {
	pid := &grainPID{}
	pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 0))

	state := newRequestState("dup", reentrancy.AllowAll, pid)
	_, completed := state.complete(&testpb.Reply{}, nil)
	require.True(t, completed)

	pid.reentrancy.Load().requestStates.Set("dup", state)
	pid.reentrancy.Load().inFlightCount.Inc()

	// The duplicate completion reports true without touching the counters.
	require.True(t, pid.completeRequest("dup", nil, nil))
	require.EqualValues(t, 1, pid.reentrancy.Load().inFlightCount.Load())
}

func TestGrainDeregisterRequestStateUnknown(t *testing.T) {
	pid := &grainPID{}
	pid.reentrancy.Store(newReentrancyState(reentrancy.StashNonReentrant, 0))
	pid.reentrancy.Load().inFlightCount.Inc()
	pid.reentrancy.Load().blockingCount.Inc()

	// A state that is not in the map must not touch the counters.
	pid.deregisterRequestState(newRequestState("ghost", reentrancy.StashNonReentrant, pid))
	require.EqualValues(t, 1, pid.reentrancy.Load().inFlightCount.Load())
	require.EqualValues(t, 1, pid.reentrancy.Load().blockingCount.Load())
}

func TestGrainHasPendingWork(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		pid := &grainPID{mailbox: newGrainMailbox(0)}
		require.False(t, pid.hasPendingWork())
	})

	t.Run("user messages pending", func(t *testing.T) {
		pid := &grainPID{mailbox: newGrainMailbox(0)}
		require.NoError(t, pid.mailbox.Enqueue(new(GrainContext)))
		require.True(t, pid.hasPendingWork())
	})

	t.Run("paused hides user messages", func(t *testing.T) {
		pid := &grainPID{
			mailbox:   newGrainMailbox(0),
			responses: newGrainMailbox(0),
		}
		pid.reentrancy.Store(newReentrancyState(reentrancy.StashNonReentrant, 0))
		pid.reentrancy.Load().blockingCount.Inc()
		require.NoError(t, pid.mailbox.Enqueue(new(GrainContext)))
		require.False(t, pid.hasPendingWork())
	})

	t.Run("responses always count", func(t *testing.T) {
		pid := &grainPID{
			mailbox:   newGrainMailbox(0),
			responses: newGrainMailbox(0),
		}
		pid.reentrancy.Store(newReentrancyState(reentrancy.StashNonReentrant, 0))
		pid.reentrancy.Load().blockingCount.Inc()
		require.NoError(t, pid.responses.Enqueue(new(GrainContext)))
		require.True(t, pid.hasPendingWork())
	})
}

func TestGrainRunTurnBudgetExhaustionReschedules(t *testing.T) {
	// A throughput of one forces the budget-exhaustion tail: the turn yields
	// after the first message and the worker reschedules the grain for the
	// second.
	grain := &reentrantRecordingGrain{}
	d := newDispatcher(1, 1)
	d.start()
	t.Cleanup(d.signalStop)

	pid := &grainPID{
		grain:      grain,
		mailbox:    newGrainMailbox(0),
		logger:     log.DiscardLogger,
		dispatcher: d,
	}
	pid.activated.Store(true)

	ctx := context.Background()
	identity := &GrainIdentity{kind: "TestKind", name: "TestID"}

	pid.receive(getGrainContext().build(ctx, pid, nil, identity, &testpb.Reply{Content: "first"}, grainTell))
	pid.receive(getGrainContext().build(ctx, pid, nil, identity, &testpb.Reply{Content: "second"}, grainTell))

	require.Eventually(t, func() bool {
		return len(grain.recorded()) == 2
	}, 2*time.Second, 10*time.Millisecond)
}

func TestGrainFinishOrReclaimResumesOnPendingWork(t *testing.T) {
	pid := &grainPID{mailbox: newGrainMailbox(0)}
	require.True(t, pid.schedState.TrySchedule())
	require.True(t, pid.schedState.TakeForProcessing())

	// Work arrived: the turn must reclaim ownership and keep draining.
	require.NoError(t, pid.mailbox.Enqueue(new(GrainContext)))
	require.False(t, pid.finishOrReclaim())

	// Nothing left: the turn must park.
	require.NotNil(t, pid.mailbox.Dequeue())
	require.True(t, pid.finishOrReclaim())
}

func TestGrainRecoveryWrapsPlainErrorPanic(t *testing.T) {
	pid := &grainPID{
		identity: &GrainIdentity{kind: "Kind", name: "Name"},
		logger:   log.DiscardLogger,
	}

	grainContext := getGrainContext().build(context.Background(), pid, nil, pid.identity, &testpb.Reply{}, grainTell)
	t.Cleanup(func() {
		releaseGrainContext(grainContext)
	})

	func() {
		defer pid.recovery(grainContext)
		panic(errors.New("plain failure"))
	}()

	err := <-grainContext.err
	var panicErr *gerrors.PanicError
	require.ErrorAs(t, err, &panicErr)
	require.Contains(t, err.Error(), "plain failure")
}

func TestGrainRecoveryKeepsPanicErrorIdentity(t *testing.T) {
	pid := &grainPID{
		identity: &GrainIdentity{kind: "Kind", name: "Name"},
		logger:   log.DiscardLogger,
	}

	grainContext := getGrainContext().build(context.Background(), pid, nil, pid.identity, &testpb.Reply{}, grainTell)
	t.Cleanup(func() {
		releaseGrainContext(grainContext)
	})

	panicErr := gerrors.NewPanicError(errors.New("already wrapped"))

	func() {
		defer pid.recovery(grainContext)
		panic(panicErr)
	}()

	err := <-grainContext.err
	require.Same(t, panicErr, err)
}

func TestGrainPoisonPillTeardownContainsPanickingContinuation(t *testing.T) {
	sys, pid, _, identity := startReentrantGrainFixture(t, reentrancy.AllowAll)

	registerGrainRequestState(pid, "boom", reentrancy.AllowAll, func(any, error) {
		panic("continuation exploded during teardown")
	})

	gctx := getGrainContext().build(context.Background(), pid, sys, identity, new(PoisonPill), grainTell)
	pid.receive(gctx)

	// The panic is contained: the pill still deactivates the grain.
	select {
	case <-pid.deactivated:
	case <-time.After(2 * time.Second):
		t.Fatal("panicking continuation blocked deactivation")
	}

	require.False(t, pid.isActive())
	require.Zero(t, pid.reentrancy.Load().inFlightCount.Load())
}

func TestGrainRegisterRequestStateValidation(t *testing.T) {
	pid := &grainPID{}
	require.ErrorIs(t, pid.registerRequestState(newRequestState("id", reentrancy.AllowAll, pid)), gerrors.ErrReentrancyDisabled)

	pid = &grainPID{}
	pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 0))
	require.ErrorIs(t, pid.registerRequestState(nil), gerrors.ErrInvalidMessage)
}

// deactivationCountingGrain counts OnDeactivate invocations so the tests can
// assert it runs exactly once.
type deactivationCountingGrain struct {
	deactivations atomic.Int32
}

var _ Grain = (*deactivationCountingGrain)(nil)

func (g *deactivationCountingGrain) OnActivate(context.Context, *GrainProps) error { return nil }

func (g *deactivationCountingGrain) OnDeactivate(context.Context, *GrainProps) error {
	g.deactivations.Inc()
	return nil
}

func (g *deactivationCountingGrain) OnReceive(gctx *GrainContext) { gctx.NoErr() }

// passivationEntryState reports whether the manager tracks the grain and
// whether its entry is paused.
func passivationEntryState(system *actorSystem, pid *grainPID) (exists, paused bool) {
	manager := system.passivationManager()
	manager.mu.Lock()
	defer manager.mu.Unlock()

	entry, ok := manager.entries[pid.passivationID()]
	if !ok {
		return false, false
	}
	return true, entry.paused
}

// TestGrainPassivationWaitsForInFlight is the end-to-end Step 9 flow: a
// pending request pauses the passivation manager past the idle deadline
// without deactivation or spinning, and completion resumes the lifecycle so
// the grain passivates normally afterward.
func TestGrainPassivationWaitsForInFlight(t *testing.T) {
	system := newRequestTestSystem(t)
	ctx := context.Background()

	silent := &scriptedGrain{receive: func(gctx *GrainContext) {
		if gctx.CorrelationID() != "" {
			return // never reply to requests; completion comes from cancellation
		}
		gctx.NoErr()
	}}
	silentID, err := system.GrainIdentity(ctx, "silent-target", func(context.Context) (Grain, error) {
		return silent, nil
	}, WithGrainReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))
	require.NoError(t, err)

	calls := make(chan RequestCall, 1)
	requester := &scriptedGrain{receive: func(gctx *GrainContext) {
		calls <- gctx.RequestGrain(silentID, new(testpb.TestPing), WithRequestTimeout(0))
		gctx.NoErr()
	}}

	identity, err := system.GrainIdentity(ctx, "waiting-grain", func(context.Context) (Grain, error) {
		return requester, nil
	},
		WithGrainReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))),
		WithGrainDeactivateAfter(300*time.Millisecond))
	require.NoError(t, err)

	pid, ok := system.grains.Get(identity.String())
	require.True(t, ok)

	require.NoError(t, system.TellGrain(ctx, identity, new(testpb.TestSend)))
	call := <-calls
	require.NotNil(t, call)

	// Far past the idle deadline the grain is still active: the manager entry
	// is paused, not firing, not spinning.
	pause.For(600 * time.Millisecond)
	require.True(t, pid.isActive())

	exists, paused := passivationEntryState(system, pid)
	require.True(t, exists)
	require.True(t, paused)

	// Completion (via cancellation) resumes the passivation lifecycle; the
	// grain deactivates after a fresh idle period.
	require.NoError(t, call.Cancel())

	require.Eventually(t, func() bool {
		return !pid.isActive()
	}, 3*time.Second, 20*time.Millisecond)
}

func TestGrainPassivationPillChecks(t *testing.T) {
	newFixture := func(t *testing.T) (*actorSystem, *grainPID) {
		t.Helper()
		system := newRequestTestSystem(t)

		grain := &scriptedGrain{receive: func(gctx *GrainContext) { gctx.NoErr() }}
		identity, err := system.GrainIdentity(context.Background(), "pill-grain", func(context.Context) (Grain, error) {
			return grain, nil
		},
			WithGrainReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))),
			WithGrainDeactivateAfter(time.Minute))
		require.NoError(t, err)

		pid, ok := system.grains.Get(identity.String())
		require.True(t, ok)
		return system, pid
	}

	t.Run("in-flight requests drop the pill without re-registering", func(t *testing.T) {
		system, pid := newFixture(t)

		state := newRequestState("in-flight", reentrancy.AllowAll, pid)
		require.NoError(t, pid.registerRequestState(state))

		t.Cleanup(func() { pid.deregisterRequestState(state) })

		// Registration paused the manager entry.
		exists, paused := passivationEntryState(system, pid)
		require.True(t, exists)
		require.True(t, paused)

		// A stale pill fired before the registration must not deactivate the
		// grain nor touch the paused entry: re-entry belongs to completion.
		pid.handlePassivationPill()
		require.True(t, pid.isActive())

		exists, paused = passivationEntryState(system, pid)
		require.True(t, exists)
		require.True(t, paused)
	})

	t.Run("a paused grain is never deactivated", func(t *testing.T) {
		_, pid := newFixture(t)

		state := newRequestState("blocking", reentrancy.StashNonReentrant, pid)
		require.NoError(t, pid.registerRequestState(state))

		t.Cleanup(func() { pid.deregisterRequestState(state) })

		pid.handlePassivationPill()
		require.True(t, pid.isActive())
	})

	t.Run("a recently active grain re-registers instead of deactivating", func(t *testing.T) {
		system, pid := newFixture(t)

		// Simulate the manager entry deleted by the fired pill.
		system.passivationManager().Unregister(pid)
		pid.markActivity(time.Now())

		pid.handlePassivationPill()
		require.True(t, pid.isActive())

		exists, paused := passivationEntryState(system, pid)
		require.True(t, exists)
		require.False(t, paused)
	})

	t.Run("an expired idle grain deactivates on the pill", func(t *testing.T) {
		_, pid := newFixture(t)

		pid.latestReceiveTimeNano.Store(time.Now().Add(-2 * time.Minute).UnixNano())
		pid.handlePassivationPill()
		require.False(t, pid.isActive())
	})

	t.Run("inactive and poisoning grains drop the pill", func(t *testing.T) {
		system, pid := newFixture(t)

		pid.onPoisonPill.Store(true)
		pid.handlePassivationPill()
		require.True(t, pid.isActive())
		pid.onPoisonPill.Store(false)

		pid.activated.Store(false)
		system.passivationManager().Unregister(pid)
		pid.handlePassivationPill()

		exists, _ := passivationEntryState(system, pid)
		require.False(t, exists)
		pid.activated.Store(true)
	})
}

func TestGrainResumePassivationFallback(t *testing.T) {
	system := newRequestTestSystem(t)

	grain := &scriptedGrain{receive: func(gctx *GrainContext) { gctx.NoErr() }}
	identity, err := system.GrainIdentity(context.Background(), "fallback-grain", func(context.Context) (Grain, error) {
		return grain, nil
	},
		WithGrainReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))),
		WithGrainDeactivateAfter(time.Minute))
	require.NoError(t, err)

	pid, ok := system.grains.Get(identity.String())
	require.True(t, ok)

	state := newRequestState("orphaned", reentrancy.AllowAll, pid)
	require.NoError(t, pid.registerRequestState(state))

	// The pill fired while the request was in flight and deleted the entry;
	// the last completion must register fresh instead of resuming nothing.
	system.passivationManager().Unregister(pid)
	pid.deregisterRequestState(state)

	exists, paused := passivationEntryState(system, pid)
	require.True(t, exists)
	require.False(t, paused)
}

func TestGrainPassivationPillThenPoisonPillDeactivatesOnce(t *testing.T) {
	system := newRequestTestSystem(t)
	ctx := context.Background()

	grain := &deactivationCountingGrain{}
	identity, err := system.GrainIdentity(ctx, "counting-grain", func(context.Context) (Grain, error) {
		return grain, nil
	},
		WithGrainReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))),
		WithGrainDeactivateAfter(time.Minute))
	require.NoError(t, err)

	pid, ok := system.grains.Get(identity.String())
	require.True(t, ok)

	// Expired idle grain: the manager fires the pill, then shutdown poisons.
	pid.latestReceiveTimeNano.Store(time.Now().Add(-2 * time.Minute).UnixNano())
	require.True(t, pid.passivationTry("idle"))

	gctx := getGrainContext().build(ctx, pid, system, identity, new(PoisonPill), grainTell)
	pid.receive(gctx)

	select {
	case <-pid.deactivated:
	case <-time.After(2 * time.Second):
		t.Fatal("grain did not deactivate")
	}

	require.Eventually(t, func() bool {
		return pid.mailbox.IsEmpty()
	}, 2*time.Second, 10*time.Millisecond)
	require.EqualValues(t, 1, grain.deactivations.Load())
	require.False(t, pid.isActive())
}

func TestGrainPassivationPillRejectedByFullMailbox(t *testing.T) {
	pid := &grainPID{
		identity: &GrainIdentity{kind: "Kind", name: "full"},
		mailbox:  newGrainMailbox(1),
		logger:   log.DiscardLogger,
	}
	pid.activated.Store(true)
	pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 0))
	require.NoError(t, pid.mailbox.Enqueue(new(GrainContext)))

	before := time.Now().UnixNano()
	require.False(t, pid.passivationTry("idle"))

	// The refused pill touches activity so the manager's refreshed deadline
	// lands a full deactivateAfter away instead of hot-looping.
	require.GreaterOrEqual(t, pid.latestReceiveTimeNano.Load(), before)
	require.True(t, pid.isActive())
}

func TestGrainPassivationPillDeactivationFailureLogged(t *testing.T) {
	system := newRequestTestSystem(t)

	identity, err := system.GrainIdentity(context.Background(), "failing-grain", func(context.Context) (Grain, error) {
		return &MockGrainDeactivationFailure{}, nil
	},
		WithGrainReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))),
		WithGrainDeactivateAfter(time.Minute))
	require.NoError(t, err)

	pid, ok := system.grains.Get(identity.String())
	require.True(t, ok)

	pid.latestReceiveTimeNano.Store(time.Now().Add(-2 * time.Minute).UnixNano())

	require.NotPanics(t, pid.handlePassivationPill)
	require.False(t, pid.isActive())
}

func TestGrainResumePassivationWithoutManager(t *testing.T) {
	pid := &grainPID{}
	pid.activated.Store(true)
	pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 0))

	state := newRequestState("no-manager", reentrancy.AllowAll, pid)
	require.NoError(t, pid.registerRequestState(state))
	require.NotPanics(t, func() {
		pid.deregisterRequestState(state)
	})
	require.Zero(t, pid.reentrancy.Load().inFlightCount.Load())
}

// TestGrainStashHoldsTimerTicksDuringPause registers an interval timer and
// then pauses the grain with a blocking request. Ticks keep arriving in the
// user mailbox but must wait unprocessed until the reply lifts the pause,
// while the response itself still completes through the paused grain.
func TestGrainStashHoldsTimerTicksDuringPause(t *testing.T) {
	system := newRequestTestSystem(t)
	ctx := context.Background()

	replies := make(chan *GrainReply, 1)
	target := &scriptedGrain{receive: func(gctx *GrainContext) {
		replies <- gctx.DeferResponse()
	}}

	targetID, err := system.GrainIdentity(ctx, "tick-target", func(context.Context) (Grain, error) {
		return target, nil
	})
	require.NoError(t, err)

	ticks := atomic.NewInt64(0)
	completions := make(chan error, 1)
	failures := make(chan error, 1)

	stasher := &scriptedGrain{receive: func(gctx *GrainContext) {
		switch gctx.Message().(type) {
		case *testpb.TestSend:
			if _, err := gctx.Schedule(new(testpb.TestBye), 50*time.Millisecond); err != nil {
				failures <- err
				return
			}

			gctx.RequestGrain(targetID, new(testpb.TestPing), WithRequestTimeout(0)).Then(func(_ any, err error) {
				completions <- err
			})
			gctx.NoErr()
		case *testpb.TestBye:
			ticks.Inc()
		}
	}}

	identity, err := system.GrainIdentity(ctx, "tick-stasher", func(context.Context) (Grain, error) {
		return stasher, nil
	}, WithGrainReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.StashNonReentrant))))
	require.NoError(t, err)

	require.NoError(t, system.TellGrain(ctx, identity, new(testpb.TestSend)))

	var reply *GrainReply

	select {
	case reply = <-replies:
	case err := <-failures:
		t.Fatalf("timer registration failed: %v", err)
	case <-time.After(time.Second):
		t.Fatal("target never received the blocking request")
	}

	// Several intervals elapse while the grain is paused: ticks accumulate in
	// the mailbox but none may process.
	pause.For(300 * time.Millisecond)
	require.Zero(t, ticks.Load())

	reply.NoErr()

	select {
	case err := <-completions:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("blocking request never completed")
	}

	require.Eventually(t, func() bool {
		return ticks.Load() > 0
	}, time.Second, 10*time.Millisecond)
}

// TestGrainTellAgainstPausedGrain pins decision 12's consequence: a TellGrain
// toward a grain paused in StashNonReentrant mode times out on its
// acknowledgement even though the message is delivered and processes normally
// once the pause lifts.
func TestGrainTellAgainstPausedGrain(t *testing.T) {
	system := newRequestTestSystem(t)
	ctx := context.Background()

	replies := make(chan *GrainReply, 1)
	target := &scriptedGrain{receive: func(gctx *GrainContext) {
		replies <- gctx.DeferResponse()
	}}

	targetID, err := system.GrainIdentity(ctx, "pause-tell-target", func(context.Context) (Grain, error) {
		return target, nil
	})
	require.NoError(t, err)

	processed := make(chan struct{}, 1)
	stasher := &scriptedGrain{receive: func(gctx *GrainContext) {
		switch gctx.Message().(type) {
		case *testpb.TestPing:
			gctx.RequestGrain(targetID, new(testpb.TestSend), WithRequestTimeout(0))
			gctx.NoErr()
		case *testpb.TestBye:
			processed <- struct{}{}
			gctx.NoErr()
		}
	}}

	identity, err := system.GrainIdentity(ctx, "pause-tell-stasher", func(context.Context) (Grain, error) {
		return stasher, nil
	}, WithGrainReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.StashNonReentrant))))
	require.NoError(t, err)

	require.NoError(t, system.TellGrain(ctx, identity, new(testpb.TestPing)))

	var reply *GrainReply

	select {
	case reply = <-replies:
	case <-time.After(time.Second):
		t.Fatal("target never received the blocking request")
	}

	// The acknowledgement wait runs against a paused mailbox: the caller
	// observes ErrRequestTimeout even though the message was enqueued.
	tellCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, system.TellGrain(tellCtx, identity, new(testpb.TestBye)), gerrors.ErrRequestTimeout)

	select {
	case <-processed:
		t.Fatal("paused grain processed a user message")
	default:
	}

	// Lifting the pause replays the buffered tell.
	reply.NoErr()

	select {
	case <-processed:
	case <-time.After(time.Second):
		t.Fatal("stashed message never processed after resume")
	}
}

// TestGrainShutdownRePauseWindow reproduces the shutdown race the plan calls
// the re-pause window: a user message queued ahead of the PoisonPill starts a
// fresh blocking request after the cancellation pre-pass already ran. The
// request's own timeout must lift the pause so the pill still deactivates the
// grain.
func TestGrainShutdownRePauseWindow(t *testing.T) {
	system := newRequestTestSystem(t)
	ctx := context.Background()

	silent := &scriptedGrain{receive: func(*GrainContext) {}}
	silentID, err := system.GrainIdentity(ctx, "repause-silent", func(context.Context) (Grain, error) {
		return silent, nil
	})
	require.NoError(t, err)

	entered := make(chan struct{})
	release := make(chan struct{})
	outcomes := make(chan error, 1)

	stasher := &scriptedGrain{receive: func(gctx *GrainContext) {
		if _, ok := gctx.Message().(*testpb.TestPing); !ok {
			return
		}

		entered <- struct{}{}
		<-release

		gctx.RequestGrain(silentID, new(testpb.TestSend), WithRequestTimeout(500*time.Millisecond)).Then(func(_ any, err error) {
			outcomes <- err
		})
		gctx.NoErr()
	}}

	identity, err := system.GrainIdentity(ctx, "repause-stasher", func(context.Context) (Grain, error) {
		return stasher, nil
	}, WithGrainReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.StashNonReentrant))))
	require.NoError(t, err)

	go func() { _ = system.TellGrain(ctx, identity, new(testpb.TestPing)) }()
	<-entered

	pid, ok := system.grains.Get(identity.String())
	require.True(t, ok)

	// Mirror poisonAllGrains while the user message still holds the turn: the
	// cancellation pre-pass finds nothing in flight, then the pill queues
	// behind the message about to start a request.
	pid.enqueueInFlightCancellations()

	pill := getGrainContext().build(ctx, pid, system, identity, new(PoisonPill), grainTell)
	pid.receive(pill)
	close(release)

	select {
	case err := <-outcomes:
		require.ErrorIs(t, err, gerrors.ErrRequestTimeout)
	case <-time.After(2 * time.Second):
		t.Fatal("the fresh request never completed")
	}

	select {
	case <-pid.deactivated:
	case <-time.After(2 * time.Second):
		t.Fatal("grain did not deactivate after the pause lifted")
	}

	require.False(t, pid.isActive())
}
