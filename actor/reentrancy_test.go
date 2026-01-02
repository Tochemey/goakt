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
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/address"
	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/pause"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

const (
	reentrancyReplyTimeout = time.Second
	reentrancyShortWait    = 50 * time.Millisecond
	reentrancyProcessWait  = 120 * time.Millisecond
	reentrancyDelay        = 200 * time.Millisecond
	reentrancyDispatchWait = 20 * time.Millisecond
)

type reentrancyTestActor struct {
	receive func(*ReceiveContext)
}

func (x *reentrancyTestActor) PreStart(*Context) error { return nil }

func (x *reentrancyTestActor) Receive(ctx *ReceiveContext) {
	if x.receive != nil {
		x.receive(ctx)
	}
}

func (x *reentrancyTestActor) PostStop(*Context) error { return nil }

// newReentrancySystem starts a minimal actor system for reentrancy tests.
func newReentrancySystem(t *testing.T) (ActorSystem, context.Context) {
	t.Helper()
	ctx := context.Background()
	sys, err := NewActorSystem("reentrancy-"+uuid.NewString(), WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	t.Cleanup(func() { _ = sys.Stop(ctx) })
	return sys, ctx
}

// spawnReentrancyActor creates a test actor with a custom Receive handler.
func spawnReentrancyActor(t *testing.T, sys ActorSystem, ctx context.Context, name string, receive func(*ReceiveContext), opts ...SpawnOption) *PID {
	t.Helper()
	pid, err := sys.Spawn(ctx, name, &reentrancyTestActor{receive: receive}, opts...)
	require.NoError(t, err)
	require.NotNil(t, pid)
	return pid
}

// responderWithDelay replies after a delay or remains silent for timeout tests.
func responderWithDelay(delay time.Duration, corrCh chan string) func(*ReceiveContext) {
	return func(ctx *ReceiveContext) {
		switch msg := ctx.Message().(type) {
		case *testpb.TestWait:
			if corrCh != nil {
				select {
				case corrCh <- ctx.CorrelationID():
				default:
				}
			}
			wait := delay
			if msg.GetDuration() > 0 {
				wait = time.Duration(msg.GetDuration()) * time.Millisecond
			}
			if wait > 0 {
				pause.For(wait)
			}
			ctx.Response(&testpb.Reply{Content: "ok"})
		case *testpb.TestTimeout:
			// intentionally no response
		default:
			ctx.Response(&testpb.Reply{Content: "ok"})
		}
	}
}

func waitForError(t *testing.T, errCh <-chan error, expected error, timeout time.Duration) {
	t.Helper()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, expected)
	case <-time.After(timeout):
		t.Fatalf("expected error: %v", expected)
	}
}

func waitForReply(t *testing.T, replyCh <-chan proto.Message, errCh <-chan error, timeout time.Duration) {
	t.Helper()
	select {
	case <-replyCh:
		return
	case err := <-errCh:
		t.Fatalf("unexpected error: %v", err)
	case <-time.After(timeout):
		t.Fatal("expected async reply")
	}
}

func waitForSignal(t *testing.T, sigCh <-chan struct{}, timeout time.Duration, message string) {
	t.Helper()
	select {
	case <-sigCh:
		return
	case <-time.After(timeout):
		t.Fatal(message)
	}
}

func assertNoSignal(t *testing.T, sigCh <-chan struct{}, errCh <-chan error, timeout time.Duration, message string) {
	t.Helper()
	select {
	case <-sigCh:
		t.Fatal(message)
	case err := <-errCh:
		t.Fatalf("unexpected error: %v", err)
	case <-time.After(timeout):
		return
	}
}

func waitForProcessedBeforeReply(t *testing.T, processedCh <-chan struct{}, replyCh <-chan proto.Message, errCh <-chan error, timeout time.Duration) {
	t.Helper()
	select {
	case <-processedCh:
		return
	case resp := <-replyCh:
		t.Fatalf("reply arrived before other message processed: %T", resp)
	case err := <-errCh:
		t.Fatalf("unexpected error: %v", err)
	case <-time.After(timeout):
		t.Fatal("expected other message to be processed while awaiting response")
	}
}

func waitForCorrelationID(t *testing.T, corrCh <-chan string, timeout time.Duration) string {
	t.Helper()
	select {
	case id := <-corrCh:
		require.NotEmpty(t, id)
		return id
	case <-time.After(timeout):
		t.Fatal("expected correlation id to be set")
	}
	return ""
}

func TestRequestRequiresReentrancy(t *testing.T) {
	sys, ctx := newReentrancySystem(t)
	target := spawnReentrancyActor(t, sys, ctx, "target", responderWithDelay(0, nil))

	errCh := make(chan error, 1)
	requester := spawnReentrancyActor(t, sys, ctx, "requester", func(rctx *ReceiveContext) {
		switch rctx.Message().(type) {
		case *testpb.TestSend:
			call := rctx.Request(target, &testpb.TestWait{Duration: 1})
			if call != nil {
				return
			}
			errCh <- rctx.getError()
		}
	})

	require.NoError(t, Tell(ctx, requester, new(testpb.TestSend)))
	waitForError(t, errCh, gerrors.ErrReentrancyDisabled, reentrancyReplyTimeout)
}

func TestRequestAllowAllProcessesOtherMessages(t *testing.T) {
	sys, ctx := newReentrancySystem(t)
	corrCh := make(chan string, 1)
	target := spawnReentrancyActor(t, sys, ctx, "allow-target", responderWithDelay(reentrancyDelay, corrCh))

	processedCh := make(chan struct{}, 1)
	replyCh := make(chan proto.Message, 1)
	errCh := make(chan error, 1)

	requester := spawnReentrancyActor(t, sys, ctx, "allow-requester", func(rctx *ReceiveContext) {
		switch msg := rctx.Message().(type) {
		case *testpb.TestWait:
			call := rctx.Request(target, msg)
			if call == nil {
				errCh <- rctx.getError()
				return
			}
			call.Then(func(resp proto.Message, err error) {
				if err != nil {
					errCh <- err
					return
				}
				replyCh <- resp
			})
		case *testpb.TestSend:
			processedCh <- struct{}{}
		}
	}, WithReentrancy(ReentrancyAllowAll))

	require.NoError(t, Tell(ctx, requester, &testpb.TestWait{Duration: 200}))
	require.NoError(t, Tell(ctx, requester, new(testpb.TestSend)))

	waitForProcessedBeforeReply(t, processedCh, replyCh, errCh, reentrancyProcessWait)
	waitForReply(t, replyCh, errCh, reentrancyReplyTimeout)
	_ = waitForCorrelationID(t, corrCh, reentrancyDispatchWait)
}

func TestRequestStashNonReentrant(t *testing.T) {
	sys, ctx := newReentrancySystem(t)
	target := spawnReentrancyActor(t, sys, ctx, "stash-target", responderWithDelay(reentrancyDelay, nil))

	processedCh := make(chan struct{}, 1)
	replyCh := make(chan proto.Message, 1)
	errCh := make(chan error, 1)

	requester := spawnReentrancyActor(t, sys, ctx, "stash-requester", func(rctx *ReceiveContext) {
		switch msg := rctx.Message().(type) {
		case *testpb.TestWait:
			call := rctx.Request(target, msg)
			if call == nil {
				errCh <- rctx.getError()
				return
			}
			call.Then(func(resp proto.Message, err error) {
				if err != nil {
					errCh <- err
					return
				}
				replyCh <- resp
			})
		case *testpb.TestSend:
			processedCh <- struct{}{}
		}
	}, WithReentrancy(ReentrancyStashNonReentrant))

	require.NoError(t, Tell(ctx, requester, &testpb.TestWait{Duration: 200}))
	require.NoError(t, Tell(ctx, requester, new(testpb.TestSend)))

	assertNoSignal(t, processedCh, errCh, reentrancyShortWait, "unexpected message processed while awaiting response")
	waitForReply(t, replyCh, errCh, reentrancyReplyTimeout)
	waitForSignal(t, processedCh, reentrancyReplyTimeout, "expected stashed message to process after reply")
}

func TestRequestStashOverride(t *testing.T) {
	sys, ctx := newReentrancySystem(t)
	target := spawnReentrancyActor(t, sys, ctx, "override-target", responderWithDelay(reentrancyDelay, nil))

	processedCh := make(chan struct{}, 1)
	replyCh := make(chan proto.Message, 1)
	errCh := make(chan error, 1)

	requester := spawnReentrancyActor(t, sys, ctx, "override-requester", func(rctx *ReceiveContext) {
		switch msg := rctx.Message().(type) {
		case *testpb.TestWait:
			call := rctx.Request(target, msg, WithReentrancyMode(ReentrancyStashNonReentrant))
			if call == nil {
				errCh <- rctx.getError()
				return
			}
			call.Then(func(resp proto.Message, err error) {
				if err != nil {
					errCh <- err
					return
				}
				replyCh <- resp
			})
		case *testpb.TestSend:
			processedCh <- struct{}{}
		}
	}, WithReentrancy(ReentrancyAllowAll))

	require.NoError(t, Tell(ctx, requester, &testpb.TestWait{Duration: 200}))
	require.NoError(t, Tell(ctx, requester, new(testpb.TestSend)))

	assertNoSignal(t, processedCh, errCh, reentrancyShortWait, "unexpected message processed while awaiting response")
	waitForReply(t, replyCh, errCh, reentrancyReplyTimeout)
	waitForSignal(t, processedCh, reentrancyReplyTimeout, "expected stashed message to process after reply")
}

func TestRequestName(t *testing.T) {
	sys, ctx := newReentrancySystem(t)
	targetName := "name-target"
	_ = spawnReentrancyActor(t, sys, ctx, targetName, responderWithDelay(0, nil))

	replyCh := make(chan proto.Message, 1)
	errCh := make(chan error, 1)

	requester := spawnReentrancyActor(t, sys, ctx, "name-requester", func(rctx *ReceiveContext) {
		switch msg := rctx.Message().(type) {
		case *testpb.TestWait:
			call := rctx.RequestName(targetName, msg)
			if call == nil {
				errCh <- rctx.getError()
				return
			}
			call.Then(func(resp proto.Message, err error) {
				if err != nil {
					errCh <- err
					return
				}
				replyCh <- resp
			})
		}
	}, WithReentrancy(ReentrancyAllowAll))

	require.NoError(t, Tell(ctx, requester, &testpb.TestWait{Duration: 1}))
	waitForReply(t, replyCh, errCh, reentrancyReplyTimeout)
}

func TestRequestTimeout(t *testing.T) {
	sys, ctx := newReentrancySystem(t)
	target := spawnReentrancyActor(t, sys, ctx, "timeout-target", responderWithDelay(reentrancyDelay, nil))

	errCh := make(chan error, 1)
	requester := spawnReentrancyActor(t, sys, ctx, "timeout-requester", func(rctx *ReceiveContext) {
		switch msg := rctx.Message().(type) {
		case *testpb.TestWait:
			call := rctx.Request(target, msg, WithRequestTimeout(reentrancyDispatchWait))
			if call == nil {
				errCh <- rctx.getError()
				return
			}
			call.Then(func(_ proto.Message, err error) {
				errCh <- err
			})
		}
	}, WithReentrancy(ReentrancyAllowAll))

	require.NoError(t, Tell(ctx, requester, &testpb.TestWait{Duration: 200}))
	waitForError(t, errCh, gerrors.ErrRequestTimeout, reentrancyReplyTimeout)
}

func TestRequestHandleCancel(t *testing.T) {
	sys, ctx := newReentrancySystem(t)
	target := spawnReentrancyActor(t, sys, ctx, "cancel-target", responderWithDelay(reentrancyDelay, nil))

	errCh := make(chan error, 2)
	var call RequestHandle

	requester := spawnReentrancyActor(t, sys, ctx, "cancel-requester", func(rctx *ReceiveContext) {
		switch msg := rctx.Message().(type) {
		case *testpb.TestWait:
			call = rctx.Request(target, msg)
			if call == nil {
				errCh <- rctx.getError()
				return
			}
			call.Then(func(_ proto.Message, err error) {
				errCh <- err
			})
		case *testpb.TestSend:
			if call == nil {
				return
			}
			if err := call.Cancel(); err != nil {
				errCh <- err
				return
			}
			if err := call.Cancel(); err != nil {
				errCh <- err
			}
		}
	}, WithReentrancy(ReentrancyAllowAll))

	require.NoError(t, Tell(ctx, requester, &testpb.TestWait{Duration: 200}))
	pause.For(reentrancyDispatchWait)
	require.NoError(t, Tell(ctx, requester, new(testpb.TestSend)))

	waitForError(t, errCh, gerrors.ErrRequestCanceled, reentrancyReplyTimeout)
}

func TestRequestHandleThenAfterCompletion(t *testing.T) {
	sys, ctx := newReentrancySystem(t)
	target := spawnReentrancyActor(t, sys, ctx, "after-target", responderWithDelay(0, nil))

	replyCh := make(chan proto.Message, 1)
	errCh := make(chan error, 1)
	var call RequestHandle

	requester := spawnReentrancyActor(t, sys, ctx, "after-requester", func(rctx *ReceiveContext) {
		switch msg := rctx.Message().(type) {
		case *testpb.TestWait:
			call = rctx.Request(target, msg)
			if call == nil {
				errCh <- rctx.getError()
			}
		case *testpb.TestSend:
			if call == nil {
				return
			}
			call.Then(func(resp proto.Message, err error) {
				if err == nil {
					replyCh <- resp
					return
				}
				errCh <- err
			})
		}
	}, WithReentrancy(ReentrancyAllowAll))

	require.NoError(t, Tell(ctx, requester, &testpb.TestWait{Duration: 1}))
	pause.For(reentrancyDispatchWait)
	require.NoError(t, Tell(ctx, requester, new(testpb.TestSend)))

	waitForReply(t, replyCh, errCh, reentrancyReplyTimeout)
}

func TestRequestInvalidMode(t *testing.T) {
	sys, ctx := newReentrancySystem(t)
	target := spawnReentrancyActor(t, sys, ctx, "invalid-target", responderWithDelay(0, nil))

	errCh := make(chan error, 1)
	requester := spawnReentrancyActor(t, sys, ctx, "invalid-requester", func(rctx *ReceiveContext) {
		switch msg := rctx.Message().(type) {
		case *testpb.TestWait:
			call := rctx.Request(target, msg, WithReentrancyMode(ReentrancyMode(99)))
			if call != nil {
				return
			}
			errCh <- rctx.getError()
		}
	}, WithReentrancy(ReentrancyAllowAll))

	require.NoError(t, Tell(ctx, requester, &testpb.TestWait{Duration: 1}))
	waitForError(t, errCh, gerrors.ErrInvalidReentrancyMode, reentrancyReplyTimeout)
}

func TestRequestMaxInFlight(t *testing.T) {
	sys, ctx := newReentrancySystem(t)
	target := spawnReentrancyActor(t, sys, ctx, "limit-target", responderWithDelay(reentrancyDelay, nil))

	errCh := make(chan error, 1)
	requester := spawnReentrancyActor(t, sys, ctx, "limit-requester", func(rctx *ReceiveContext) {
		switch msg := rctx.Message().(type) {
		case *testpb.TestWait:
			_ = rctx.Request(target, msg)
		case *testpb.TestSend:
			call := rctx.Request(target, &testpb.TestWait{Duration: 200})
			if call != nil {
				return
			}
			errCh <- rctx.getError()
		}
	}, WithReentrancy(ReentrancyAllowAll, WithMaxInFlight(1)))

	require.NoError(t, Tell(ctx, requester, &testpb.TestWait{Duration: 200}))
	pause.For(reentrancyDispatchWait)
	require.NoError(t, Tell(ctx, requester, new(testpb.TestSend)))

	waitForError(t, errCh, gerrors.ErrReentrancyInFlightLimit, reentrancyReplyTimeout)
}

func TestCancelInFlightRequests(t *testing.T) {
	sys, ctx := newReentrancySystem(t)
	target := spawnReentrancyActor(t, sys, ctx, "cancel-inflight-target", responderWithDelay(reentrancyDelay, nil))

	requester := spawnReentrancyActor(t, sys, ctx, "cancel-inflight-requester", func(*ReceiveContext) {}, WithReentrancy(ReentrancyAllowAll))
	call, err := requester.request(ctx, target, &testpb.TestWait{Duration: 200})
	require.NoError(t, err)
	require.NotNil(t, call)

	state := call.(*asyncCall).state
	requester.cancelInFlightRequests(gerrors.ErrRequestCanceled)

	state.mu.Lock()
	completed := state.completed
	cerr := state.err
	state.mu.Unlock()

	require.True(t, completed)
	require.ErrorIs(t, cerr, gerrors.ErrRequestCanceled)
	require.EqualValues(t, 0, requester.reentrancy.inFlightCount.Load())
	require.EqualValues(t, 0, requester.reentrancy.blockingCount.Load())
	require.Zero(t, requester.reentrancy.calls.Len())
}

func TestToWireActorIncludesReentrancy(t *testing.T) {
	pid := &PID{
		actor:        NewMockActor(),
		address:      address.New("actor-reentrancy-wire", "testSys", "127.0.0.1", 0),
		fieldsLocker: sync.RWMutex{},
		reentrancy:   newReentrancyState(ReentrancyAllowAll, 3),
	}

	wire, err := pid.toWireActor()
	require.NoError(t, err)
	require.NotNil(t, wire.GetReentrancy())
	require.Equal(t, internalpb.ReentrancyMode_REENTRANCY_MODE_ALLOW_ALL, wire.GetReentrancy().GetMode())
	require.Equal(t, uint32(3), wire.GetReentrancy().GetMaxInFlight())
}

func TestReentrancyModeMappings(t *testing.T) {
	assert.True(t, isValidReentrancyMode(ReentrancyAllowAll))
	assert.True(t, isValidReentrancyMode(ReentrancyStashNonReentrant))
	assert.True(t, isValidReentrancyMode(ReentrancyOff))
	assert.False(t, isValidReentrancyMode(ReentrancyMode(99)))

	assert.Equal(t, internalpb.ReentrancyMode_REENTRANCY_MODE_ALLOW_ALL, toInternalReentrancyMode(ReentrancyAllowAll))
	assert.Equal(t, internalpb.ReentrancyMode_REENTRANCY_MODE_STASH_NON_REENTRANT, toInternalReentrancyMode(ReentrancyStashNonReentrant))
	assert.Equal(t, internalpb.ReentrancyMode_REENTRANCY_MODE_OFF, toInternalReentrancyMode(ReentrancyOff))

	assert.Equal(t, ReentrancyAllowAll, fromInternalReentrancyMode(internalpb.ReentrancyMode_REENTRANCY_MODE_ALLOW_ALL))
	assert.Equal(t, ReentrancyStashNonReentrant, fromInternalReentrancyMode(internalpb.ReentrancyMode_REENTRANCY_MODE_STASH_NON_REENTRANT))
	assert.Equal(t, ReentrancyOff, fromInternalReentrancyMode(internalpb.ReentrancyMode_REENTRANCY_MODE_OFF))
	assert.Equal(t, ReentrancyOff, fromInternalReentrancyMode(internalpb.ReentrancyMode(99)))
}

func TestReentrancyConfigConversions(t *testing.T) {
	cfg := &reentrancyConfig{mode: ReentrancyStashNonReentrant, maxInFlight: 7}
	wire := cfg.toProto()
	require.NotNil(t, wire)
	assert.Equal(t, internalpb.ReentrancyMode_REENTRANCY_MODE_STASH_NON_REENTRANT, wire.GetMode())
	assert.Equal(t, uint32(7), wire.GetMaxInFlight())

	roundTrip := reentrancyConfigFromProto(&internalpb.ReentrancyConfig{
		Mode:        internalpb.ReentrancyMode_REENTRANCY_MODE_ALLOW_ALL,
		MaxInFlight: 5,
	})
	require.NotNil(t, roundTrip)
	assert.Equal(t, ReentrancyAllowAll, roundTrip.mode)
	assert.Equal(t, 5, roundTrip.maxInFlight)
}

func TestRequestConfigTimeoutClamp(t *testing.T) {
	cfg := newRequestConfig(WithRequestTimeout(-1))
	require.NotNil(t, cfg)
	require.Nil(t, cfg.timeout)

	cfg = newRequestConfig(WithRequestTimeout(0))
	require.NotNil(t, cfg)
	require.Nil(t, cfg.timeout)
}

func TestRequestHandleCancelWithNilState(t *testing.T) {
	call := &asyncCall{}
	err := call.Cancel()
	require.ErrorIs(t, err, gerrors.ErrInvalidMessage)
	require.True(t, errors.Is(err, gerrors.ErrInvalidMessage))
}
