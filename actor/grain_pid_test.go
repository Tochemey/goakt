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
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"
	"go.uber.org/atomic"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/commands"
	"github.com/tochemey/goakt/v4/internal/pause"
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
		actorSystem: &actorSystem{logger: log.DiscardLogger},
		config:      newGrainConfig(WithGrainDeactivateAfter(time.Second)),
	}
	pid.onPoisonPill.Store(false)
	pid.activated.Store(false)
	require.False(t, pid.passivationTry("no-op"))
}

func TestGrainPIDPassivationTryFailsOnDeactivateError(t *testing.T) {
	system := newRequestTestSystem(t)

	identity, err := system.GrainIdentity(context.Background(), "failing-grain", func(context.Context) (Grain, error) {
		return &MockDeactivationFailingGrain{}, nil
	}, WithGrainDeactivateAfter(time.Minute))
	require.NoError(t, err)

	pid, ok := system.grains.Get(identity.String())
	require.True(t, ok)

	// without reentrancy the decision also travels as a pill, and a failed
	// OnDeactivate is logged on the turn instead of panicking
	pid.latestReceiveTimeNano.Store(time.Now().Add(-2 * time.Minute).UnixNano())
	require.True(t, pid.passivationTry("deactivate failure"))
	require.Eventually(t, func() bool { return !pid.isActive() }, 2*time.Second, 10*time.Millisecond)
}

// newGatedGrainTestSystem starts an actor system with a short shutdown timeout,
// so a test that fails while a gated grain is stuck still stops promptly.
func newGatedGrainTestSystem(t *testing.T) *actorSystem {
	t.Helper()

	system, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger), WithShutdownTimeout(2*time.Second))
	require.NoError(t, err)
	require.NoError(t, system.Start(context.Background()))
	t.Cleanup(func() { _ = system.Stop(context.Background()) })

	return system.(*actorSystem)
}

func TestGrainPassivationWaitsForTheMessageInProgress(t *testing.T) {
	system := newGatedGrainTestSystem(t)
	state := newGatedGrainState(t)
	ctx := context.Background()

	identity, err := GrainOf[*MockGatedGrain](ctx, system, "gated-grain", WithGrainDeactivateAfter(100*time.Millisecond))
	require.NoError(t, err)

	pid, ok := system.grains.Get(identity.String())
	require.True(t, ok)

	replied := make(chan error, 1)
	go func() {
		_, err := system.AskGrain(ctx, identity, new(testpb.TestReply), 5*time.Second)
		replied <- err
	}()
	<-state.entered

	// the idle deadline passes while OnReceive is still running
	pause.For(500 * time.Millisecond)
	require.Zero(t, state.deactivations.Load(), "OnDeactivate must not run during OnReceive")
	require.True(t, pid.isActive())

	state.release()
	require.NoError(t, <-replied)

	require.Eventually(t, func() bool { return state.deactivations.Load() == 1 }, 2*time.Second, 10*time.Millisecond)
	require.False(t, state.deactivatedDuringReceive.Load())
}

func TestGrainMessagesQueuedBehindAPill(t *testing.T) {
	passivationPill := func(_ context.Context, pid *grainPID) bool { return pid.passivationTry("idle") }
	shutdownPill := func(ctx context.Context, pid *grainPID) bool { return pid.enqueuePoisonPill(ctx) != nil }
	stashNonReentrant := WithGrainReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.StashNonReentrant)))

	cases := []struct {
		name string
		pill func(ctx context.Context, pid *grainPID) bool
		opts []GrainOption
		// stopping marks the node as shutting down before the pill is handled.
		stopping bool
		// wantErr is the error every queued ask gets; nil means they are answered.
		wantErr error
		// wantActivations and wantDeactivations are the lifecycle calls expected.
		wantActivations   int32
		wantDeactivations int32
	}{
		{name: "passivation pill skipped while messages wait", pill: passivationPill, wantActivations: 1},
		{name: "passivation pill skipped while messages wait with reentrancy", pill: passivationPill, opts: []GrainOption{stashNonReentrant}, wantActivations: 1},
		{name: "deactivation forwards queued messages to a fresh activation", pill: shutdownPill, wantActivations: 2, wantDeactivations: 1},
		{name: "deactivation forwards queued messages to a fresh activation with reentrancy", pill: shutdownPill, opts: []GrainOption{stashNonReentrant}, wantActivations: 2, wantDeactivations: 1},
		{name: "shutdown refuses queued messages", pill: shutdownPill, stopping: true, wantErr: gerrors.ErrSystemShuttingDown, wantActivations: 1, wantDeactivations: 1},
		{name: "shutdown refuses queued messages with reentrancy", pill: shutdownPill, opts: []GrainOption{stashNonReentrant}, stopping: true, wantErr: gerrors.ErrSystemShuttingDown, wantActivations: 1, wantDeactivations: 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			system := newGatedGrainTestSystem(t)
			state := newGatedGrainState(t)
			ctx := context.Background()

			opts := append([]GrainOption{WithGrainDeactivateAfter(time.Minute)}, tc.opts...)
			identity, err := GrainOf[*MockGatedGrain](ctx, system, "queued-behind-pill", opts...)
			require.NoError(t, err)

			pid, ok := system.grains.Get(identity.String())
			require.True(t, ok)

			// hold a turn open so the pill and two asks queue behind it
			first := make(chan error, 1)
			go func() {
				_, err := system.AskGrain(ctx, identity, new(testpb.TestReply), 5*time.Second)
				first <- err
			}()
			<-state.entered

			// queuing the pill must not wait for the message in progress
			queuedPill := make(chan bool, 1)
			go func() { queuedPill <- tc.pill(ctx, pid) }()

			select {
			case ok := <-queuedPill:
				require.True(t, ok)
			case <-time.After(2 * time.Second):
				state.release()
				t.Fatal("the pill waited for the message in progress")
			}

			queued := make(chan error, 2)
			for range 2 {
				go func() {
					_, err := system.AskGrain(ctx, identity, new(testpb.TestReply), 5*time.Second)
					queued <- err
				}()
			}

			require.Eventually(t, func() bool {
				return (*embeddedGrainMailbox)(pid).Len() == 3
			}, 2*time.Second, 10*time.Millisecond)

			// the idle deadline has passed by the time the pill is handled
			pid.latestReceiveTimeNano.Store(time.Now().Add(-2 * time.Minute).UnixNano())
			if tc.stopping {
				system.shuttingDown.Store(true)
				t.Cleanup(func() { system.shuttingDown.Store(false) })
			}

			state.release()

			require.NoError(t, <-first)
			for range 2 {
				err := <-queued
				if tc.wantErr == nil {
					require.NoError(t, err)
					continue
				}

				require.ErrorIs(t, err, tc.wantErr)
			}

			require.Equal(t, tc.wantActivations, state.activations.Load())
			require.Equal(t, tc.wantDeactivations, state.deactivations.Load())
			require.False(t, state.receivedAfterDeactivate.Load(), "a deactivated instance must not handle queued messages")
		})
	}
}

// deactivatedGrainFixture activates a MockGrain, deactivates it, and returns
// the system, the grain identity and the deactivated instance.
func deactivatedGrainFixture(t *testing.T) (*actorSystem, *GrainIdentity, *grainPID) {
	t.Helper()

	system := newGatedGrainTestSystem(t)
	ctx := context.Background()

	identity, err := GrainOf[*MockGrain](ctx, system, "late-message-grain")
	require.NoError(t, err)

	pid, ok := system.grains.Get(identity.String())
	require.True(t, ok)
	require.NoError(t, pid.deactivate(ctx))
	return system, identity, pid
}

// sendLateMessage hands message to the deactivated instance as its mailbox would.
func sendLateMessage(ctx context.Context, system *actorSystem, identity *GrainIdentity, pid *grainPID, message any, mode grainContextMode) *GrainContext {
	grainContext := getGrainContext(pid.ctxShard).build(ctx, pid, system, identity, message, mode)
	pid.handleGrainContext(grainContext, time.Now())
	return grainContext
}

func TestGrainLateMessageForwarding(t *testing.T) {
	ctx := context.Background()

	t.Run("ask is answered by a fresh activation", func(t *testing.T) {
		system, identity, pid := deactivatedGrainFixture(t)
		grainContext := getGrainContext(pid.ctxShard).build(ctx, pid, system, identity, new(testpb.TestReply), grainAsk)
		response := grainContext.response
		pid.handleGrainContext(grainContext, time.Now())

		select {
		case reply := <-response:
			require.IsType(t, new(testpb.Reply), reply)
		case <-time.After(2 * time.Second):
			t.Fatal("the forwarded ask was not answered")
		}

		fresh, ok := system.grains.Get(identity.String())
		require.True(t, ok)
		require.NotSame(t, pid, fresh)
		require.True(t, fresh.isActive())
	})

	t.Run("ask failure reaches the caller", func(t *testing.T) {
		system, identity, pid := deactivatedGrainFixture(t)
		grainContext := getGrainContext(pid.ctxShard).build(ctx, pid, system, identity, new(testpb.TestLogin), grainAsk)
		response := grainContext.response
		pid.handleGrainContext(grainContext, time.Now())

		select {
		case reply := <-response:
			replyErr, ok := reply.(grainReplyError)
			require.True(t, ok)
			require.ErrorIs(t, replyErr.err, gerrors.ErrUnhanledMessage)
		case <-time.After(2 * time.Second):
			t.Fatal("the forwarded ask was not answered")
		}
	})

	t.Run("tell acknowledgement reaches the caller", func(t *testing.T) {
		cases := []struct {
			name    string
			message any
			wantErr error
		}{
			{"success", new(testpb.TestSend), nil},
			{"failure", new(testpb.TestLogin), gerrors.ErrUnhanledMessage},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				system, identity, pid := deactivatedGrainFixture(t)
				grainContext := getGrainContext(pid.ctxShard).build(ctx, pid, system, identity, tc.message, grainTell)
				ack := grainContext.err
				pid.handleGrainContext(grainContext, time.Now())

				select {
				case err := <-ack:
					if tc.wantErr == nil {
						require.NoError(t, err)
						return
					}

					require.ErrorIs(t, err, tc.wantErr)
				case <-time.After(2 * time.Second):
					t.Fatal("the forwarded tell was not acknowledged")
				}
			})
		}
	})

	t.Run("one-way message reaches a fresh activation", func(t *testing.T) {
		system, identity, pid := deactivatedGrainFixture(t)
		sendLateMessage(ctx, system, identity, pid, new(testpb.TestSend), grainOneWay)

		require.Eventually(t, func() bool {
			fresh, ok := system.grains.Get(identity.String())
			return ok && fresh != pid && fresh.isActive()
		}, 2*time.Second, 10*time.Millisecond)
	})

	t.Run("one-way message that cannot be forwarded becomes a deadletter", func(t *testing.T) {
		system, identity, pid := deactivatedGrainFixture(t)
		consumer, err := system.Subscribe()
		require.NoError(t, err)
		t.Cleanup(func() { _ = system.Unsubscribe(consumer) })

		// the fresh activation fails because the kind is no longer registered
		require.NoError(t, system.DeregisterGrainKind(ctx, &MockGrain{}))
		sendLateMessage(ctx, system, identity, pid, new(testpb.TestSend), grainOneWay)

		require.Eventually(t, func() bool {
			for message := range consumer.Iterator() {
				if deadletter, ok := message.Payload().(*Deadletter); ok && deadletter.Receiver().Name() == identity.String() {
					return true
				}
			}
			return false
		}, 2*time.Second, 50*time.Millisecond)
	})

	t.Run("shutdown refuses the late message", func(t *testing.T) {
		system, identity, pid := deactivatedGrainFixture(t)
		system.shuttingDown.Store(true)
		t.Cleanup(func() { system.shuttingDown.Store(false) })

		grainContext := getGrainContext(pid.ctxShard).build(ctx, pid, system, identity, new(testpb.TestReply), grainAsk)
		response := grainContext.response
		pid.handleGrainContext(grainContext, time.Now())

		reply := <-response
		replyErr, ok := reply.(grainReplyError)
		require.True(t, ok)
		require.ErrorIs(t, replyErr.err, gerrors.ErrSystemShuttingDown)
	})

	t.Run("late messages keep their arrival order", func(t *testing.T) {
		system := newGatedGrainTestSystem(t)
		identity, err := GrainOf[*MockOrderedGrain](ctx, system, "ordered-grain")
		require.NoError(t, err)

		pid, ok := system.grains.Get(identity.String())
		require.True(t, ok)
		require.NoError(t, pid.deactivate(ctx))

		for _, content := range []string{"first", "second", "third"} {
			sendLateMessage(ctx, system, identity, pid, testpb.Reply_builder{Content: content}.Build(), grainOneWay)
		}

		for _, want := range []string{"first", "second", "third"} {
			select {
			case got := <-orderedGrainMessages:
				require.Equal(t, want, got)
			case <-time.After(2 * time.Second):
				t.Fatalf("late message %q was not delivered", want)
			}
		}
	})
}

func TestGrainLateMessageEnvelopeForwardFailureReachesTheCaller(t *testing.T) {
	system := newGatedGrainTestSystem(t)
	state := newGatedGrainState(t)
	ctx := context.Background()

	stashNonReentrant := WithGrainReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.StashNonReentrant)))
	identity, err := GrainOf[*MockGatedGrain](ctx, system, "envelope-forward-failure", WithGrainDeactivateAfter(time.Minute), stashNonReentrant)
	require.NoError(t, err)

	pid, ok := system.grains.Get(identity.String())
	require.True(t, ok)

	first := make(chan error, 1)
	go func() {
		_, err := system.AskGrain(ctx, identity, new(testpb.TestReply), 5*time.Second)
		first <- err
	}()
	<-state.entered

	require.NotNil(t, pid.enqueuePoisonPill(ctx))

	queued := make(chan error, 1)
	go func() {
		_, err := system.AskGrain(ctx, identity, new(testpb.TestReply), 5*time.Second)
		queued <- err
	}()

	require.Eventually(t, func() bool {
		return (*embeddedGrainMailbox)(pid).Len() == 2
	}, 2*time.Second, 10*time.Millisecond)

	// the fresh activation fails because the kind is no longer registered
	require.NoError(t, system.DeregisterGrainKind(ctx, &MockGatedGrain{}))
	state.release()

	require.NoError(t, <-first)
	err = <-queued
	require.Error(t, err)
	require.NotErrorIs(t, err, gerrors.ErrDead)
}

func TestGrainLateMessageSendTimeout(t *testing.T) {
	t.Run("uses the caller's timeout first", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()

		late := &GrainContext{ctx: ctx, timeout: 30 * time.Second}
		require.Equal(t, 30*time.Second, late.lateSendTimeout())
	})

	t.Run("uses the time left on the caller's context", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()

		timeout := (&GrainContext{ctx: ctx}).lateSendTimeout()
		require.Greater(t, timeout, 50*time.Second)
		require.LessOrEqual(t, timeout, time.Minute)
	})

	t.Run("falls back to the default grain request timeout", func(t *testing.T) {
		require.Equal(t, DefaultGrainRequestTimeout, (&GrainContext{ctx: context.Background()}).lateSendTimeout())
	})
}

func TestGrainLateAskHonorsTheCallerTimeout(t *testing.T) {
	system := newGatedGrainTestSystem(t)
	newGatedGrainState(t)
	ctx := context.Background()

	identity, err := GrainOf[*MockGatedGrain](ctx, system, "late-ask-timeout")
	require.NoError(t, err)

	pid, ok := system.grains.Get(identity.String())
	require.True(t, ok)
	require.NoError(t, pid.deactivate(ctx))

	// the fresh activation holds the message at the gate, so the forward can
	// only end on the caller's timeout, which is far shorter than the default
	grainContext := getGrainContext(pid.ctxShard).build(ctx, pid, system, identity, new(testpb.TestReply), grainAsk)
	grainContext.timeout = 100 * time.Millisecond
	response := grainContext.response
	started := time.Now()
	pid.handleGrainContext(grainContext, time.Now())

	select {
	case reply := <-response:
		replyErr, ok := reply.(grainReplyError)
		require.True(t, ok)
		require.ErrorIs(t, replyErr.err, gerrors.ErrRequestTimeout)
		require.Less(t, time.Since(started), DefaultGrainRequestTimeout)
	case <-time.After(DefaultGrainRequestTimeout):
		t.Fatal("the forwarded ask did not honor the caller's timeout")
	}
}

func TestGrainPIDStartPassivationSkipsWhenAutoDisabled(t *testing.T) {
	manager := newPassivationManager(log.DiscardLogger)
	manager.started.Store(true)

	pid := &grainPID{
		passivationManager: manager,
		config:             newGrainConfig(WithLongLivedGrain()),
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
		actorSystem:        &actorSystem{logger: log.DiscardLogger},
		config:             newGrainConfig(WithGrainDeactivateAfter(0)),
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
		actorSystem:        &actorSystem{logger: log.DiscardLogger},
		config:             newGrainConfig(WithGrainDeactivateAfter(time.Second)),
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
		config:             newGrainConfig(WithGrainDeactivateAfter(time.Second)),
	}
	require.True(t, pid.shouldAutoPassivate())

	pid.passivationManager = nil
	require.False(t, pid.shouldAutoPassivate())
}

func TestGrainPIDMarkActivityCoalescesTouch(t *testing.T) {
	manager := newPassivationManager(log.DiscardLogger)
	manager.started.Store(true)

	pid := &grainPID{
		identity:           &GrainIdentity{kind: "Kind", name: "Name"},
		passivationManager: manager,
		actorSystem:        &actorSystem{logger: log.DiscardLogger},
	}

	base := time.Now()

	// The first activity always touches: the coalescing window is empty.
	pid.markActivity(base)
	require.Equal(t, base.UnixNano(), pid.lastPassivationTouch.Load())
	require.Equal(t, base.UnixNano(), pid.latestReceiveTimeNano.Load())

	// Activity inside the interval skips the manager Touch but still
	// records the receive timestamp.
	inside := base.Add(time.Duration(passivationTouchInterval) / 2)
	pid.markActivity(inside)
	require.Equal(t, base.UnixNano(), pid.lastPassivationTouch.Load())
	require.Equal(t, inside.UnixNano(), pid.latestReceiveTimeNano.Load())

	// Activity one full interval later touches again.
	outside := base.Add(time.Duration(passivationTouchInterval))
	pid.markActivity(outside)
	require.Equal(t, outside.UnixNano(), pid.lastPassivationTouch.Load())
	require.Equal(t, outside.UnixNano(), pid.latestReceiveTimeNano.Load())
}

func TestGrainPIDActivateReturnsPanicErrorOnActivatePanic(t *testing.T) {
	config := newGrainConfig()
	pid := &grainPID{
		identity:    &GrainIdentity{kind: "Kind", name: "Name"},
		actorSystem: &actorSystem{logger: log.DiscardLogger},
		grain:       &MockLifecyclePanickingGrain{activatePanic: "activate panic"},
		config:      config,
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
		identity:    &GrainIdentity{kind: "Kind", name: "Name"},
		actorSystem: &actorSystem{logger: log.DiscardLogger},
		grain:       &MockLifecyclePanickingGrain{activatePanic: panicErr},
		config:      config,
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
		identity:    &GrainIdentity{kind: "Kind", name: "Name"},
		actorSystem: &actorSystem{logger: log.DiscardLogger},
		grain:       &MockLifecyclePanickingGrain{activatePanic: panicErr},
		config:      config,
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

func TestGrainPIDDeactivateReportsFailedRegistryRelease(t *testing.T) {
	ctx := context.Background()
	grain := NewMockGrain()
	sys, cl, _, identity := newActivationTestSystem(t, grain, "deactivate-release-error", true)

	// an error-level logger, so the failed release is reported
	var logs bytes.Buffer
	sys.logger = log.NewSlog(log.ErrorLevel, &logs)

	pid := newGrainPID(identity, grain, sys, newGrainConfig())
	require.NoError(t, pid.activate(ctx))
	sys.grains.Set(identity.String(), pid)

	// the record is released only while it still names this node, and a
	// failed release fails the deactivation
	releaseErr := errors.New("release failed")
	cl.EXPECT().ReleaseGrain(ctx, identity.String(), address.FormatHostPort(sys.Host(), sys.Port())).Return(nil, releaseErr).Once()

	err := pid.deactivate(ctx)
	require.ErrorIs(t, err, gerrors.ErrGrainDeactivationFailure)
	require.ErrorIs(t, err, releaseErr)
	require.Contains(t, logs.String(), "failed to release grain="+identity.String())

	_, ok := sys.grains.Get(identity.String())
	require.False(t, ok)
}

func TestGrainPIDDeactivateReturnsPanicErrorOnDeactivatePanic(t *testing.T) {
	pid := &grainPID{
		identity:    &GrainIdentity{kind: "Kind", name: "Name"},
		actorSystem: &actorSystem{logger: log.DiscardLogger},
		grain:       &MockLifecyclePanickingGrain{},
		config:      newGrainConfig(),
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
		identity:    &GrainIdentity{kind: "Kind", name: "Name"},
		actorSystem: &actorSystem{logger: log.DiscardLogger},
		grain:       &MockLifecyclePanickingGrain{deactivatePanic: panicErr},
		config:      newGrainConfig(),
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
		identity:    &GrainIdentity{kind: "Kind", name: "Name"},
		actorSystem: &actorSystem{logger: log.DiscardLogger},
		grain:       &MockLifecyclePanickingGrain{deactivatePanic: panicErr},
		config:      newGrainConfig(),
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
		identity:    &GrainIdentity{kind: "Kind", name: "Name"},
		actorSystem: &actorSystem{logger: log.DiscardLogger},
		grain:       &MockLifecyclePanickingGrain{},
		config:      newGrainConfig(),
	}
	pid.onPoisonPill.Store(false)
	pid.activated.Store(true)

	grainContext := getGrainContext(0).build(
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

func TestToWireGrainActivationRole(t *testing.T) {
	ctx := t.Context()
	sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	t.Cleanup(func() { _ = sys.Stop(ctx) })

	// a grain without an activation role carries none on the wire
	identity := newGrainIdentity(&MockGrain{}, "wire-no-role")
	pid := newGrainPID(identity, &MockGrain{}, sys, newGrainConfig())
	wire, err := pid.toWireGrain()
	require.NoError(t, err)
	require.False(t, wire.HasRole())

	// the activation role survives serialization so relocation and remote
	// activation preserve role-constrained placement
	identity = newGrainIdentity(&MockGrain{}, "wire-role")
	pid = newGrainPID(identity, &MockGrain{}, sys, newGrainConfig(WithActivationRole("game-worker")))
	wire, err = pid.toWireGrain()
	require.NoError(t, err)
	require.True(t, wire.HasRole())
	require.Equal(t, "game-worker", wire.GetRole())
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

	reply := testpb.Reply_builder{Content: "pong"}.Build()
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
	first := testpb.Reply_builder{Content: "first"}.Build()
	second := testpb.Reply_builder{Content: "second"}.Build()
	third := testpb.Reply_builder{Content: "third"}.Build()

	for _, message := range []*testpb.Reply{first, second, third} {
		gctx := getGrainContext(0).build(context.Background(), pid, sys, identity, message, grainTell)
		pid.receive(gctx)
	}

	pause.For(200 * time.Millisecond)
	require.Empty(t, grain.recorded())
	require.EqualValues(t, 3, (*embeddedGrainMailbox)(pid).Len())

	// The completion flows through the response queue, resumes consumption and
	// releases the buffered messages in exact arrival order.
	require.NoError(t, pid.enqueueEnvelope(context.Background(), &commands.AsyncResponse{
		CorrelationID: "blocking",
		Message:       testpb.Reply_builder{Content: "done"}.Build(),
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

	gctx := getGrainContext(0).build(context.Background(), pid, sys, identity, testpb.Reply_builder{Content: "waiting"}.Build(), grainTell)
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
	_, pid, _, _ := startReentrantGrainFixture(t, reentrancy.StashNonReentrant)

	var failure error
	done := make(chan struct{})

	registerGrainRequestState(pid, "blocking", reentrancy.StashNonReentrant, func(_ any, err error) {
		failure = err
		close(done)
	})

	// The pill waits in the user mailbox behind the pause.
	ack := pid.enqueuePoisonPill(context.Background())

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
	case err := <-ack:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("grain did not deactivate")
	}

	require.False(t, pid.isActive())
	require.Zero(t, pid.reentrancy.Load().inFlightCount.Load())
	require.Zero(t, pid.reentrancy.Load().blockingCount.Load())
}

func TestGrainPoisonPillTearsDownInFlightInline(t *testing.T) {
	_, pid, _, _ := startReentrantGrainFixture(t, reentrancy.AllowAll)

	var failure error
	done := make(chan struct{})

	state := registerGrainRequestState(pid, "in-flight", reentrancy.AllowAll, func(_ any, err error) {
		failure = err
		close(done)
	})
	state.startTimeout(time.Minute)

	ack := pid.enqueuePoisonPill(context.Background())

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("teardown did not complete the request")
	}
	require.ErrorIs(t, failure, gerrors.ErrRequestCanceled)

	select {
	case err := <-ack:
		require.NoError(t, err)
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

	grain := &MockReentrantRecordingGrain{}
	identity, err := system.GrainIdentity(ctx, "reentrantGrain", func(context.Context) (Grain, error) {
		return grain, nil
	})
	require.NoError(t, err)

	sys := system.(*actorSystem)
	pid, ok := sys.grains.Get(identity.String())
	require.True(t, ok)

	pid.reentrancy.Store(newReentrancyState(reentrancy.StashNonReentrant, 0))
	pid.attachResponseQueue()

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
	payload := testpb.Reply_builder{Content: "inner"}.Build()

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
		pid := &grainPID{}
		pid.attachResponseQueue()
		pid.activated.Store(true)
		require.NoError(t, pid.enqueueAsyncError(context.Background(), "corr", nil))
		require.True(t, pid.responses.Load().IsEmpty())
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
		}
		pid.attachMailbox(1)
		pid.activated.Store(true)
		require.NoError(t, pid.boundedMailbox.Enqueue(new(GrainContext)))

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
		identity:    &GrainIdentity{kind: "Kind", name: "Name"},
		actorSystem: &actorSystem{logger: log.NewSlog(log.DebugLevel, io.Discard)},
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
		pid := &grainPID{}
		pid.attachMailbox(0)
		require.False(t, pid.hasPendingWork())
	})

	t.Run("user messages pending", func(t *testing.T) {
		pid := &grainPID{}
		pid.attachMailbox(0)
		require.NoError(t, pid.enqueueMessage(new(GrainContext)))
		require.True(t, pid.hasPendingWork())
	})

	t.Run("paused hides user messages", func(t *testing.T) {
		pid := &grainPID{}
		pid.attachMailbox(0)
		pid.attachResponseQueue()
		pid.reentrancy.Store(newReentrancyState(reentrancy.StashNonReentrant, 0))
		pid.reentrancy.Load().blockingCount.Inc()
		require.NoError(t, pid.enqueueMessage(new(GrainContext)))
		require.False(t, pid.hasPendingWork())
	})

	t.Run("responses always count", func(t *testing.T) {
		pid := &grainPID{}
		pid.attachMailbox(0)
		pid.attachResponseQueue()
		pid.reentrancy.Store(newReentrancyState(reentrancy.StashNonReentrant, 0))
		pid.reentrancy.Load().blockingCount.Inc()
		require.NoError(t, pid.responses.Load().Enqueue(new(GrainContext)))
		require.True(t, pid.hasPendingWork())
	})
}

// TestNewGrainPIDWithoutReentrancyHasNoResponseQueue asserts that a grain
// built without a reentrancy policy never gets a response queue, and that the
// turn-loop readers cope with its absence.
func TestNewGrainPIDWithoutReentrancyHasNoResponseQueue(t *testing.T) {
	ctx := t.Context()
	sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	t.Cleanup(func() { _ = sys.Stop(ctx) })

	pid := newGrainPID(&GrainIdentity{kind: "Kind", name: "plain"}, NewMockGrain(), sys, newGrainConfig())

	require.Nil(t, pid.responses.Load())
	require.Nil(t, pid.dequeueResponse())
	require.False(t, pid.hasPendingWork())
}

// TestGrainAttachResponseQueue asserts that the response queue is attached
// exactly once, sequentially and under concurrent attaches.
func TestGrainAttachResponseQueue(t *testing.T) {
	t.Run("attaches once", func(t *testing.T) {
		pid := &grainPID{}
		pid.attachResponseQueue()

		queue := pid.responses.Load()
		require.NotNil(t, queue)

		// A second attach keeps the queue the grain already published.
		pid.attachResponseQueue()
		require.Same(t, queue, pid.responses.Load())
	})

	t.Run("concurrent attach yields one queue", func(t *testing.T) {
		pid := &grainPID{}
		observed := make([]*grainMailbox, 8)

		var wg sync.WaitGroup
		for i := range observed {
			wg.Add(1)

			go func() {
				defer wg.Done()
				pid.attachResponseQueue()
				observed[i] = pid.responses.Load()
			}()
		}

		wg.Wait()

		queue := pid.responses.Load()
		require.NotNil(t, queue)

		for _, seen := range observed {
			require.Same(t, queue, seen)
		}
	})
}

// TestGrainEnableReentrancyAttachesResponseQueue asserts that a runtime
// reentrancy install attaches the response queue once and keeps it across a
// retune, and that an invalid config attaches nothing.
func TestGrainEnableReentrancyAttachesResponseQueue(t *testing.T) {
	ctx := t.Context()
	sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	t.Cleanup(func() { _ = sys.Stop(ctx) })

	newPID := func() *grainPID {
		return newGrainPID(&GrainIdentity{kind: "Kind", name: "runtime"}, NewMockGrain(), sys, newGrainConfig())
	}

	t.Run("valid config attaches the queue", func(t *testing.T) {
		pid := newPID()
		require.Nil(t, pid.responses.Load())

		require.NoError(t, pid.enableReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))
		require.NotNil(t, pid.reentrancy.Load())
		require.NotNil(t, pid.responses.Load())
	})

	t.Run("retune keeps the queue", func(t *testing.T) {
		pid := newPID()
		require.NoError(t, pid.enableReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))

		queue := pid.responses.Load()
		require.NotNil(t, queue)

		require.NoError(t, pid.enableReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.StashNonReentrant))))
		require.Same(t, queue, pid.responses.Load())
	})

	t.Run("invalid config installs no state", func(t *testing.T) {
		pid := newPID()
		require.ErrorIs(t, pid.enableReentrancy(nil), gerrors.ErrInvalidReentrancyMode)
		require.Nil(t, pid.reentrancy.Load())
	})
}

// TestGrainEnqueueEnvelopeResponseWithoutReentrancy walks a response envelope
// through both states of the queue: a grain that never enabled reentrancy has
// none and rejects the envelope, and the same grain accepts it once the queue
// is attached. The dispatcher is never started, so nothing drains the queue
// behind the assertion.
func TestGrainEnqueueEnvelopeResponseWithoutReentrancy(t *testing.T) {
	pid := &grainPID{
		identity:    &GrainIdentity{kind: "Kind", name: "Name"},
		actorSystem: &actorSystem{logger: log.DiscardLogger},
		dispatcher:  newDispatcher(1, 1),
	}
	pid.attachMailbox(0)
	pid.activated.Store(true)

	ctx := context.Background()
	response := &commands.AsyncResponse{CorrelationID: "corr"}
	require.ErrorIs(t, pid.enqueueEnvelope(ctx, response), gerrors.ErrReentrancyDisabled)

	pid.attachResponseQueue()
	pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 0))

	require.NoError(t, pid.enqueueEnvelope(ctx, response))
	require.False(t, pid.responses.Load().IsEmpty())
}

func TestGrainRunTurnBudgetExhaustionReschedules(t *testing.T) {
	// A throughput of one forces the budget-exhaustion tail: the turn yields
	// after the first message and the worker reschedules the grain for the
	// second.
	grain := &MockReentrantRecordingGrain{}
	d := newDispatcher(1, 1)
	d.start()
	t.Cleanup(d.signalStop)

	pid := &grainPID{
		grain:       grain,
		actorSystem: &actorSystem{logger: log.DiscardLogger},
		dispatcher:  d,
	}
	pid.attachMailbox(0)
	pid.activated.Store(true)

	ctx := context.Background()
	identity := &GrainIdentity{kind: "TestKind", name: "TestID"}

	pid.receive(getGrainContext(0).build(ctx, pid, nil, identity, testpb.Reply_builder{Content: "first"}.Build(), grainTell))
	pid.receive(getGrainContext(0).build(ctx, pid, nil, identity, testpb.Reply_builder{Content: "second"}.Build(), grainTell))

	require.Eventually(t, func() bool {
		return len(grain.recorded()) == 2
	}, 2*time.Second, 10*time.Millisecond)
}

func TestGrainFinishOrReclaimResumesOnPendingWork(t *testing.T) {
	pid := &grainPID{}
	pid.attachMailbox(0)
	require.True(t, pid.schedState.TrySchedule())
	require.True(t, pid.schedState.TakeForProcessing())

	// Work arrived: the turn must reclaim ownership and keep draining.
	require.NoError(t, pid.enqueueMessage(new(GrainContext)))
	require.False(t, pid.finishOrReclaim())

	// Nothing left: the turn must park.
	require.NotNil(t, pid.dequeueMessage())
	require.True(t, pid.finishOrReclaim())
}

func TestGrainRecoveryWrapsPlainErrorPanic(t *testing.T) {
	pid := &grainPID{
		identity:    &GrainIdentity{kind: "Kind", name: "Name"},
		actorSystem: &actorSystem{logger: log.DiscardLogger},
	}

	grainContext := getGrainContext(0).build(context.Background(), pid, nil, pid.identity, &testpb.Reply{}, grainTell)
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

func TestGrainRecoveryDeliversPanicToAskCaller(t *testing.T) {
	pid := &grainPID{
		identity:    &GrainIdentity{kind: "Kind", name: "Name"},
		actorSystem: &actorSystem{logger: log.DiscardLogger},
	}

	// An ask context has no err channel: the panic must reach the caller
	// through the reply channel instead of being swallowed as channel-less.
	grainContext := getGrainContext(0).build(context.Background(), pid, nil, pid.identity, &testpb.Reply{}, grainAsk)
	t.Cleanup(func() {
		releaseGrainContext(grainContext)
	})

	func() {
		defer pid.recovery(grainContext)
		panic(errors.New("ask failure"))
	}()

	reply, ok := (<-grainContext.response).(grainReplyError)
	require.True(t, ok)

	var panicErr *gerrors.PanicError
	require.ErrorAs(t, reply.err, &panicErr)
	require.Contains(t, reply.err.Error(), "ask failure")
}

func TestGrainRecoveryKeepsPanicErrorIdentity(t *testing.T) {
	pid := &grainPID{
		identity:    &GrainIdentity{kind: "Kind", name: "Name"},
		actorSystem: &actorSystem{logger: log.DiscardLogger},
	}

	grainContext := getGrainContext(0).build(context.Background(), pid, nil, pid.identity, &testpb.Reply{}, grainTell)
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
	_, pid, _, _ := startReentrantGrainFixture(t, reentrancy.AllowAll)

	registerGrainRequestState(pid, "boom", reentrancy.AllowAll, func(any, error) {
		panic("continuation exploded during teardown")
	})

	ack := pid.enqueuePoisonPill(context.Background())

	// The panic is contained: the pill still deactivates the grain.
	select {
	case err := <-ack:
		require.NoError(t, err)
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

// TestGrainPassivationWaitsForInFlight is the end-to-end Step 9 flow: a
// pending request pauses the passivation manager past the idle deadline
// without deactivation or spinning, and completion resumes the lifecycle so
// the grain passivates normally afterward.
func TestGrainPassivationWaitsForInFlight(t *testing.T) {
	system := newRequestTestSystem(t)
	ctx := context.Background()

	silent := &MockScriptedGrain{receive: func(gctx *GrainContext) {
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
	requester := &MockScriptedGrain{receive: func(gctx *GrainContext) {
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

		grain := &MockScriptedGrain{receive: func(gctx *GrainContext) { gctx.NoErr() }}
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

	grain := &MockScriptedGrain{receive: func(gctx *GrainContext) { gctx.NoErr() }}
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

	grain := &MockDeactivationCountingGrain{}
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

	ack := pid.enqueuePoisonPill(ctx)

	select {
	case err := <-ack:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("grain did not deactivate")
	}

	require.Eventually(t, func() bool {
		return pid.mailboxEmpty()
	}, 2*time.Second, 10*time.Millisecond)
	require.EqualValues(t, 1, grain.deactivations.Load())
	require.False(t, pid.isActive())
}

func TestGrainPassivationPillRejectedByFullMailbox(t *testing.T) {
	pid := &grainPID{
		identity:    &GrainIdentity{kind: "Kind", name: "full"},
		actorSystem: &actorSystem{logger: log.DiscardLogger},
	}
	pid.attachMailbox(1)
	pid.activated.Store(true)
	pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 0))
	require.NoError(t, pid.boundedMailbox.Enqueue(new(GrainContext)))

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
		return &MockDeactivationFailingGrain{}, nil
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
	target := &MockScriptedGrain{receive: func(gctx *GrainContext) {
		replies <- gctx.DeferResponse()
	}}

	targetID, err := system.GrainIdentity(ctx, "tick-target", func(context.Context) (Grain, error) {
		return target, nil
	})
	require.NoError(t, err)

	ticks := atomic.NewInt64(0)
	completions := make(chan error, 1)
	failures := make(chan error, 1)

	stasher := &MockScriptedGrain{receive: func(gctx *GrainContext) {
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
	target := &MockScriptedGrain{receive: func(gctx *GrainContext) {
		replies <- gctx.DeferResponse()
	}}

	targetID, err := system.GrainIdentity(ctx, "pause-tell-target", func(context.Context) (Grain, error) {
		return target, nil
	})
	require.NoError(t, err)

	processed := make(chan struct{}, 1)
	stasher := &MockScriptedGrain{receive: func(gctx *GrainContext) {
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

	silent := &MockScriptedGrain{receive: func(*GrainContext) {}}
	silentID, err := system.GrainIdentity(ctx, "repause-silent", func(context.Context) (Grain, error) {
		return silent, nil
	})
	require.NoError(t, err)

	entered := make(chan struct{})
	release := make(chan struct{})
	outcomes := make(chan error, 1)

	stasher := &MockScriptedGrain{receive: func(gctx *GrainContext) {
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

	ack := pid.enqueuePoisonPill(ctx)
	close(release)

	select {
	case err := <-outcomes:
		require.ErrorIs(t, err, gerrors.ErrRequestTimeout)
	case <-time.After(2 * time.Second):
		t.Fatal("the fresh request never completed")
	}

	select {
	case err := <-ack:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("grain did not deactivate after the pause lifted")
	}

	require.False(t, pid.isActive())
}

// TestGrainAttachMailbox verifies the two mailbox shapes a grain can get: an
// unbounded grain runs on the embedded queue, whose two words are seeded with a
// single shared sentinel and which needs no standalone object, while a capacity
// gives it a standalone bounded mailbox and leaves the embedded words nil.
func TestGrainAttachMailbox(t *testing.T) {
	t.Run("unbounded runs embedded", func(t *testing.T) {
		pid := &grainPID{}
		pid.attachMailbox(0)

		require.Nil(t, pid.boundedMailbox)
		sentinel := pid.mailboxHead.Load()
		require.NotNil(t, sentinel)
		require.Same(t, sentinel, pid.mailboxTail.Load())
	})

	t.Run("capacity runs standalone", func(t *testing.T) {
		pid := &grainPID{}
		pid.attachMailbox(1)

		require.NotNil(t, pid.boundedMailbox)
		require.EqualValues(t, 1, pid.boundedMailbox.Capacity())
		require.Nil(t, pid.mailboxHead.Load())
		require.Nil(t, pid.mailboxTail.Load())
	})
}

// TestGrainMailboxDispatch verifies that the process routes every user-mailbox
// operation to the queue the grain actually has: the embedded queue accepts
// without bound, the standalone one rejects past its capacity.
func TestGrainMailboxDispatch(t *testing.T) {
	t.Run("embedded", func(t *testing.T) {
		pid := &grainPID{}
		pid.attachMailbox(0)
		require.True(t, pid.mailboxEmpty())

		grainContext := new(GrainContext)
		require.NoError(t, pid.enqueueMessage(grainContext))
		require.False(t, pid.mailboxEmpty())

		require.Same(t, grainContext, pid.dequeueMessage())
		require.True(t, pid.mailboxEmpty())
		require.Nil(t, pid.dequeueMessage())
	})

	t.Run("bounded rejects past capacity", func(t *testing.T) {
		pid := &grainPID{}
		pid.attachMailbox(1)

		require.NoError(t, pid.enqueueMessage(new(GrainContext)))
		require.ErrorIs(t, pid.enqueueMessage(new(GrainContext)), gerrors.ErrMailboxFull)
		require.False(t, pid.mailboxEmpty())
		require.NotNil(t, pid.dequeueMessage())
	})
}

// TestGrainPIDStaysInItsSizeClass pins the grain process struct at three whole
// cache lines, the size the per-line field grouping is built on.
func TestGrainPIDStaysInItsSizeClass(t *testing.T) {
	require.EqualValues(t, 192, unsafe.Sizeof(grainPID{}), "grainPID must stay 192 bytes, three whole cache lines, so every process starts on a line boundary")
}

// TestGrainGetLogger checks that the process reports the logger of the actor
// system it was built with.
func TestGrainGetLogger(t *testing.T) {
	pid := &grainPID{actorSystem: &actorSystem{logger: log.DiscardLogger}}
	require.Equal(t, log.DiscardLogger, pid.getLogger())
}

// TestGrainEnqueuePoisonPill covers the three answers a poison pill gets: the
// ack after OnDeactivate ran, the immediate ack for a grain that was never
// activated, and the rejection from a full bounded mailbox.
func TestGrainEnqueuePoisonPill(t *testing.T) {
	ctx := context.Background()

	t.Run("active grain deactivates and acks", func(t *testing.T) {
		sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, sys.Start(ctx))
		t.Cleanup(func() { _ = sys.Stop(ctx) })

		identity, err := sys.GrainIdentity(ctx, "poisoned", func(context.Context) (Grain, error) {
			return NewMockGrain(), nil
		})
		require.NoError(t, err)

		pid, ok := sys.(*actorSystem).grains.Get(identity.String())
		require.True(t, ok)

		ack := pid.enqueuePoisonPill(ctx)

		select {
		case err := <-ack:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("the pill was never acknowledged")
		}

		require.False(t, pid.isActive())
	})

	t.Run("inactive grain acks immediately", func(t *testing.T) {
		sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, sys.Start(ctx))
		t.Cleanup(func() { _ = sys.Stop(ctx) })

		// The process is built but never activated: handlePoisonPill answers
		// on its behalf instead of leaving the caller without an ack.
		identity := newGrainIdentity(NewMockGrain(), "never-activated")
		pid := newGrainPID(identity, NewMockGrain(), sys.(*actorSystem), newGrainConfig())

		ack := pid.enqueuePoisonPill(ctx)

		select {
		case err := <-ack:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("the pill was never acknowledged")
		}

		require.False(t, pid.isActive())
	})

	t.Run("full mailbox acks the rejection", func(t *testing.T) {
		// The dispatcher is never started, so nothing drains the mailbox and
		// the rejection is the only thing that can reach the ack channel.
		pid := &grainPID{
			identity:    &GrainIdentity{kind: "Kind", name: "full"},
			actorSystem: &actorSystem{logger: log.DiscardLogger},
			dispatcher:  newDispatcher(1, 1),
		}
		pid.attachMailbox(1)
		pid.activated.Store(true)
		require.NoError(t, pid.boundedMailbox.Enqueue(new(GrainContext)))

		ack := pid.enqueuePoisonPill(ctx)
		require.ErrorIs(t, <-ack, gerrors.ErrMailboxFull)
	})
}

// TestGrainPIDGettersNeedNoLock checks that getGrain and getIdentity stay
// readable without mu while the registry is created under that same mutex.
func TestGrainPIDGettersNeedNoLock(t *testing.T) {
	ctx := context.Background()
	grain := NewMockGrain()
	pid := newTestGrainPID(grain, "getters")
	identity := pid.identity

	require.Same(t, grain, pid.getGrain())
	require.Same(t, identity, pid.getIdentity())
	require.NoError(t, pid.activate(ctx))

	// the unlocked reads must stay clean while the registry is being created
	// under the same mutex they used to take
	stable := make([]bool, 4)

	var wg sync.WaitGroup
	for i := range stable {
		wg.Add(1)

		go func() {
			defer wg.Done()

			same := true
			for range 100 {
				same = same && pid.getIdentity() == identity && pid.getGrain() == grain
			}

			stable[i] = same
		}()
	}

	registry, err := pid.timerRegistry()
	require.NoError(t, err)
	require.NotNil(t, registry)

	wg.Wait()
	require.Equal(t, []bool{true, true, true, true}, stable)
}

func TestGrainPIDReceiveReturnsEnqueueOutcome(t *testing.T) {
	t.Run("returns ErrDead when the grain is not active", func(t *testing.T) {
		grain := NewMockGrain()
		sys, _, _, identity := newActivationTestSystem(t, grain, "receive-inactive-grain", false)
		pid := newGrainPID(identity, grain, sys, newGrainConfig())

		grainContext := getGrainContext(pid.ctxShard)
		grainContext.build(context.Background(), pid, sys, identity, new(testpb.TestSend), grainOneWay)

		require.ErrorIs(t, pid.receive(grainContext), gerrors.ErrDead)
	})
}
