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
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/goakt/v3/address"
	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/internal/commands"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/pause"
	"github.com/tochemey/goakt/v3/log"
	mockcluster "github.com/tochemey/goakt/v3/mocks/cluster"
	"github.com/tochemey/goakt/v3/reentrancy"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

const (
	reentrancyReplyTimeout = time.Second
	reentrancyShortWait    = 50 * time.Millisecond
	reentrancyProcessWait  = 120 * time.Millisecond
	reentrancyDelay        = 200 * time.Millisecond
	reentrancyDispatchWait = 20 * time.Millisecond
)

func TestReentrancyCycleAllowAll(t *testing.T) {
	// Scenario: user -> A (Request) -> B -> A (Ask) -> B (Response) -> A -> user.
	sys, ctx := newReentrancySystem(t)

	replyCh := make(chan *testpb.TestCount, 1)
	errCh := make(chan error, 1)

	user := spawnReentrancyActor(t, sys, ctx, "cycle-user", func(rctx *ReceiveContext) {
		switch msg := rctx.Message().(type) {
		case *testpb.TestCount:
			replyCh <- msg
		default:
			rctx.Unhandled()
		}
	})

	var actorA *PID
	actorB := spawnReentrancyActor(t, sys, ctx, "cycle-b", func(rctx *ReceiveContext) {
		switch rctx.Message().(type) {
		case *testpb.TestPing:
			resp, err := rctx.Self().Ask(rctx.Context(), actorA, new(testpb.TestGetCount), reentrancyReplyTimeout)
			if err != nil {
				reportScenarioError(errCh, err)
				return
			}
			rctx.Response(resp)
		default:
			rctx.Unhandled()
		}
	})

	actorA = spawnReentrancyActor(t, sys, ctx, "cycle-a", func(rctx *ReceiveContext) {
		switch rctx.Message().(type) {
		case *testpb.TestPing:
			sender := rctx.Sender()
			call := rctx.Request(actorB, new(testpb.TestPing), WithRequestTimeout(reentrancyReplyTimeout))
			if call == nil {
				reportScenarioError(errCh, rctx.getError())
				return
			}
			self := rctx.Self()
			call.Then(func(resp any, err error) {
				if err != nil {
					reportScenarioError(errCh, err)
					return
				}
				if err := self.Tell(context.Background(), sender, resp); err != nil {
					reportScenarioError(errCh, err)
				}
			})
		case *testpb.TestGetCount:
			rctx.Response(&testpb.TestCount{Value: 42})
		default:
			rctx.Unhandled()
		}
	}, WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))

	require.NoError(t, user.Tell(ctx, actorA, new(testpb.TestPing)))

	select {
	case err := <-errCh:
		t.Fatalf("unexpected error: %v", err)
	case msg := <-replyCh:
		require.EqualValues(t, 42, msg.GetValue())
	case <-time.After(reentrancyReplyTimeout):
		t.Fatal("expected reply from reentrant call cycle")
	}
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
	replyCh := make(chan any, 1)
	errCh := make(chan error, 1)

	requester := spawnReentrancyActor(t, sys, ctx, "allow-requester", func(rctx *ReceiveContext) {
		switch msg := rctx.Message().(type) {
		case *testpb.TestWait:
			call := rctx.Request(target, msg)
			if call == nil {
				errCh <- rctx.getError()
				return
			}
			call.Then(func(resp any, err error) {
				if err != nil {
					errCh <- err
					return
				}
				replyCh <- resp
			})
		case *testpb.TestSend:
			processedCh <- struct{}{}
		}
	}, WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))

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
	replyCh := make(chan any, 1)
	errCh := make(chan error, 1)

	requester := spawnReentrancyActor(t, sys, ctx, "stash-requester", func(rctx *ReceiveContext) {
		switch msg := rctx.Message().(type) {
		case *testpb.TestWait:
			call := rctx.Request(target, msg)
			if call == nil {
				errCh <- rctx.getError()
				return
			}
			call.Then(func(resp any, err error) {
				if err != nil {
					errCh <- err
					return
				}
				replyCh <- resp
			})
		case *testpb.TestSend:
			processedCh <- struct{}{}
		}
	}, WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.StashNonReentrant))))

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
	replyCh := make(chan any, 1)
	errCh := make(chan error, 1)

	requester := spawnReentrancyActor(t, sys, ctx, "override-requester", func(rctx *ReceiveContext) {
		switch msg := rctx.Message().(type) {
		case *testpb.TestWait:
			call := rctx.Request(target, msg, WithReentrancyMode(reentrancy.StashNonReentrant))
			if call == nil {
				errCh <- rctx.getError()
				return
			}
			call.Then(func(resp any, err error) {
				if err != nil {
					errCh <- err
					return
				}
				replyCh <- resp
			})
		case *testpb.TestSend:
			processedCh <- struct{}{}
		}
	}, WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))

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

	replyCh := make(chan any, 1)
	errCh := make(chan error, 1)

	requester := spawnReentrancyActor(t, sys, ctx, "name-requester", func(rctx *ReceiveContext) {
		switch msg := rctx.Message().(type) {
		case *testpb.TestWait:
			call := rctx.RequestName(targetName, msg)
			if call == nil {
				errCh <- rctx.getError()
				return
			}
			call.Then(func(resp any, err error) {
				if err != nil {
					errCh <- err
					return
				}
				replyCh <- resp
			})
		}
	}, WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))

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
			call.Then(func(_ any, err error) {
				errCh <- err
			})
		}
	}, WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))

	require.NoError(t, Tell(ctx, requester, &testpb.TestWait{Duration: 200}))
	waitForError(t, errCh, gerrors.ErrRequestTimeout, reentrancyReplyTimeout)
}

func TestRequestCallCancel(t *testing.T) {
	sys, ctx := newReentrancySystem(t)
	target := spawnReentrancyActor(t, sys, ctx, "cancel-target", responderWithDelay(reentrancyDelay, nil))

	errCh := make(chan error, 2)
	var call RequestCall

	requester := spawnReentrancyActor(t, sys, ctx, "cancel-requester", func(rctx *ReceiveContext) {
		switch msg := rctx.Message().(type) {
		case *testpb.TestWait:
			call = rctx.Request(target, msg)
			if call == nil {
				errCh <- rctx.getError()
				return
			}
			call.Then(func(_ any, err error) {
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
	}, WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))

	require.NoError(t, Tell(ctx, requester, &testpb.TestWait{Duration: 200}))
	pause.For(reentrancyDispatchWait)
	require.NoError(t, Tell(ctx, requester, new(testpb.TestSend)))

	waitForError(t, errCh, gerrors.ErrRequestCanceled, reentrancyReplyTimeout)
}

func TestRequestCallThenAfterCompletion(t *testing.T) {
	sys, ctx := newReentrancySystem(t)
	target := spawnReentrancyActor(t, sys, ctx, "after-target", responderWithDelay(0, nil))

	replyCh := make(chan any, 1)
	errCh := make(chan error, 1)
	var call RequestCall

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
			call.Then(func(resp any, err error) {
				if err == nil {
					replyCh <- resp
					return
				}
				errCh <- err
			})
		}
	}, WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))

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
			call := rctx.Request(target, msg, WithReentrancyMode(reentrancy.Mode(99)))
			if call != nil {
				return
			}
			errCh <- rctx.getError()
		}
	}, WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))

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
	}, WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll), reentrancy.WithMaxInFlight(1))))

	require.NoError(t, Tell(ctx, requester, &testpb.TestWait{Duration: 200}))
	pause.For(reentrancyDispatchWait)
	require.NoError(t, Tell(ctx, requester, new(testpb.TestSend)))

	waitForError(t, errCh, gerrors.ErrReentrancyInFlightLimit, reentrancyReplyTimeout)
}

func TestCancelInFlightRequests(t *testing.T) {
	sys, ctx := newReentrancySystem(t)
	target := spawnReentrancyActor(t, sys, ctx, "cancel-inflight-target", responderWithDelay(reentrancyDelay, nil))

	requester := spawnReentrancyActor(t, sys, ctx, "cancel-inflight-requester", func(*ReceiveContext) {}, WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))
	call, err := requester.request(ctx, target, &testpb.TestWait{Duration: 200})
	require.NoError(t, err)
	require.NotNil(t, call)

	state := call.(*requestHandle).state
	requester.cancelInFlightRequests(gerrors.ErrRequestCanceled)

	state.mu.Lock()
	completed := state.completed
	cerr := state.err
	state.mu.Unlock()

	require.True(t, completed)
	require.ErrorIs(t, cerr, gerrors.ErrRequestCanceled)
	require.EqualValues(t, 0, requester.reentrancy.inFlightCount.Load())
	require.EqualValues(t, 0, requester.reentrancy.blockingCount.Load())
	require.Zero(t, requester.reentrancy.requestStates.Len())
}

func TestToWireActorIncludesReentrancy(t *testing.T) {
	pid := &PID{
		actor:        NewMockActor(),
		address:      address.New("actor-reentrancy-wire", "testSys", "127.0.0.1", 0),
		fieldsLocker: sync.RWMutex{},
		reentrancy:   newReentrancyState(reentrancy.AllowAll, 3),
	}

	wire, err := pid.toWireActor()
	require.NoError(t, err)
	require.NotNil(t, wire.GetReentrancy())
	require.Equal(t, internalpb.ReentrancyMode_REENTRANCY_MODE_ALLOW_ALL, wire.GetReentrancy().GetMode())
	require.Equal(t, uint32(3), wire.GetReentrancy().GetMaxInFlight())
}

func TestWithReentrancyDoesNotEnableStash(t *testing.T) {
	pid := &PID{}
	withReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll)))(pid)
	require.NotNil(t, pid.reentrancy)
	require.Nil(t, pid.stashState)

	pid = &PID{}
	withReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.StashNonReentrant)))(pid)
	require.NotNil(t, pid.reentrancy)
	require.Nil(t, pid.stashState)
}

func TestRequestConfigTimeoutClamp(t *testing.T) {
	cfg := newRequestConfig(WithRequestTimeout(-1))
	require.NotNil(t, cfg)
	require.Nil(t, cfg.timeout)

	cfg = newRequestConfig(WithRequestTimeout(0))
	require.NotNil(t, cfg)
	require.Nil(t, cfg.timeout)
}

func TestRequestCallCancelWithNilState(t *testing.T) {
	call := &requestHandle{}
	err := call.Cancel()
	require.ErrorIs(t, err, gerrors.ErrInvalidMessage)
	require.True(t, errors.Is(err, gerrors.ErrInvalidMessage))
}

func TestRequestValidationFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("dead requester", func(t *testing.T) {
		pid := &PID{}
		_, err := pid.request(ctx, &PID{}, new(testpb.TestSend))
		require.ErrorIs(t, err, gerrors.ErrDead)
	})

	t.Run("nil target", func(t *testing.T) {
		pid := &PID{}
		pid.setState(runningState, true)
		_, err := pid.request(ctx, nil, new(testpb.TestSend))
		require.ErrorIs(t, err, gerrors.ErrDead)
	})

	t.Run("nil message", func(t *testing.T) {
		pid := &PID{}
		pid.setState(runningState, true)
		target := &PID{}
		target.setState(runningState, true)
		_, err := pid.request(ctx, target, nil)
		require.ErrorIs(t, err, gerrors.ErrInvalidMessage)
	})

	t.Run("reentrancy off", func(t *testing.T) {
		pid := newRunningPIDWithReentrancy(reentrancy.Off, 0)
		target := &PID{}
		target.setState(runningState, true)
		_, err := pid.request(ctx, target, new(testpb.TestSend))
		require.ErrorIs(t, err, gerrors.ErrReentrancyDisabled)
	})
}

func TestRequestTellErrorDeregisters(t *testing.T) {
	ctx := context.Background()
	pid := newRunningPIDWithReentrancy(reentrancy.AllowAll, 0)
	target := &PID{}
	target.setState(runningState, false)

	_, err := pid.request(ctx, target, new(testpb.TestSend))
	require.ErrorIs(t, err, gerrors.ErrDead)
	require.Zero(t, pid.reentrancy.inFlightCount.Load())
	require.Zero(t, pid.reentrancy.requestStates.Len())
}

func TestRequestNameValidationFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("dead requester", func(t *testing.T) {
		pid := &PID{}
		_, err := pid.requestName(ctx, "actor", new(testpb.TestSend))
		require.ErrorIs(t, err, gerrors.ErrDead)
	})

	t.Run("nil message", func(t *testing.T) {
		pid := &PID{}
		pid.setState(runningState, true)
		pid.reentrancy = newReentrancyState(reentrancy.AllowAll, 0)
		_, err := pid.requestName(ctx, "actor", nil)
		require.ErrorIs(t, err, gerrors.ErrInvalidMessage)
	})

	t.Run("reentrancy disabled", func(t *testing.T) {
		pid := &PID{}
		pid.setState(runningState, true)
		_, err := pid.requestName(ctx, "actor", new(testpb.TestSend))
		require.ErrorIs(t, err, gerrors.ErrReentrancyDisabled)
	})

	t.Run("override off", func(t *testing.T) {
		pid := &PID{}
		pid.setState(runningState, true)
		pid.reentrancy = newReentrancyState(reentrancy.AllowAll, 0)
		_, err := pid.requestName(ctx, "actor", new(testpb.TestSend), WithReentrancyMode(reentrancy.Off))
		require.ErrorIs(t, err, gerrors.ErrReentrancyDisabled)
	})

	t.Run("invalid mode", func(t *testing.T) {
		pid := &PID{}
		pid.setState(runningState, true)
		pid.reentrancy = newReentrancyState(reentrancy.AllowAll, 0)
		_, err := pid.requestName(ctx, "actor", new(testpb.TestSend), WithReentrancyMode(reentrancy.Mode(99)))
		require.ErrorIs(t, err, gerrors.ErrInvalidReentrancyMode)
	})
}

func TestRequestNameActorOfError(t *testing.T) {
	sys, ctx := newReentrancySystem(t)
	requester := spawnReentrancyActor(t, sys, ctx, "actorof-requester", func(*ReceiveContext) {}, WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))

	_, err := requester.requestName(ctx, "missing-actor", new(testpb.TestSend))
	require.ErrorIs(t, err, gerrors.ErrActorNotFound)
}

func TestRequestNameRegisterLimit(t *testing.T) {
	sys, ctx := newReentrancySystem(t)
	_ = spawnReentrancyActor(t, sys, ctx, "limit-target", func(*ReceiveContext) {})
	requester := spawnReentrancyActor(t, sys, ctx, "limit-requester", func(*ReceiveContext) {}, WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll), reentrancy.WithMaxInFlight(1))))

	state := newRequestState("limit", reentrancy.AllowAll, requester)
	require.NoError(t, requester.registerRequestState(state))
	t.Cleanup(func() { requester.deregisterRequestState(state) })

	_, err := requester.requestName(ctx, "limit-target", new(testpb.TestSend))
	require.ErrorIs(t, err, gerrors.ErrReentrancyInFlightLimit)
}

func TestRequestNameTimeoutStarts(t *testing.T) {
	sys, ctx := newReentrancySystem(t)
	_ = spawnReentrancyActor(t, sys, ctx, "timeout-target", func(*ReceiveContext) {})
	requester := spawnReentrancyActor(t, sys, ctx, "timeout-requester", func(*ReceiveContext) {}, WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))

	call, err := requester.requestName(ctx, "timeout-target", new(testpb.TestWait), WithRequestTimeout(reentrancyReplyTimeout))
	require.NoError(t, err)

	state := call.(*requestHandle).state
	require.NotNil(t, state.stopTimeout)
	requester.deregisterRequestState(state)
}

func TestRequestNameTellErrorOnStoppedTarget(t *testing.T) {
	sys, ctx := newReentrancySystem(t)
	target := spawnReentrancyActor(t, sys, ctx, "tell-stop-target", func(*ReceiveContext) {})
	requester := spawnReentrancyActor(t, sys, ctx, "tell-stop-requester", func(*ReceiveContext) {}, WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))

	require.NoError(t, target.Shutdown(ctx))
	pause.For(500 * time.Millisecond)

	_, err := requester.requestName(ctx, "tell-stop-target", new(testpb.TestSend))
	require.Error(t, err)
	require.Zero(t, requester.reentrancy.inFlightCount.Load())
	require.Zero(t, requester.reentrancy.requestStates.Len())
}

func TestRequestNameRemoteTellError(t *testing.T) {
	clusterMock := mockcluster.NewCluster(t)
	sys := MockReplicationTestSystem(clusterMock)
	sys.actors = newTree()

	remoteAddr := address.New("remote-actor", "remote-system", "127.0.0.1", 9001).String()
	clusterMock.EXPECT().GetActor(mock.Anything, "remote-actor").Return(&internalpb.Actor{Address: remoteAddr}, nil)

	pid := &PID{
		actorSystem: sys,
		logger:      log.DiscardLogger,
		reentrancy:  newReentrancyState(reentrancy.AllowAll, 0),
	}
	pid.setState(runningState, true)

	_, err := pid.requestName(context.Background(), "remote-actor", new(testpb.TestSend))
	require.ErrorIs(t, err, gerrors.ErrRemotingDisabled)
}

func TestProcessStashErrorPath(t *testing.T) {
	pid := &PID{
		mailbox:    NewUnboundedMailbox(),
		logger:     log.DiscardLogger,
		reentrancy: newReentrancyState(reentrancy.AllowAll, 0),
	}
	pid.reentrancy.blockingCount.Store(1)

	receiveCtx := getContext()
	receiveCtx.build(context.Background(), pid, pid, new(testpb.TestSend), true)
	pid.doReceive(receiveCtx)

	require.Eventually(t, func() bool {
		return pid.processing.Load() == idle && pid.mailbox.IsEmpty()
	}, reentrancyReplyTimeout, reentrancyShortWait)
}

func TestHandleAsyncRequestValidationErrors(t *testing.T) {
	pid := &PID{logger: log.DiscardLogger}

	t.Run("nil request", func(t *testing.T) {
		pid.handleAsyncRequest(nil, nil)
	})

	t.Run("missing fields", func(t *testing.T) {
		received := newReceiveContext(context.Background(), pid, pid, new(testpb.TestSend))
		pid.handleAsyncRequest(received, &commands.AsyncRequest{})
	})
}

func TestHandleAsyncResponsePaths(t *testing.T) {
	t.Run("nil response", func(t *testing.T) {
		pid := &PID{logger: log.DiscardLogger}
		pid.handleAsyncResponse(nil, nil)
	})

	t.Run("empty correlation", func(t *testing.T) {
		pid := &PID{logger: log.DiscardLogger}
		pid.handleAsyncResponse(nil, &commands.AsyncResponse{CorrelationID: "  "})
	})

	t.Run("error response completes", func(t *testing.T) {
		pid := newRunningPIDWithReentrancy(reentrancy.AllowAll, 0)
		state := newRequestState("err", reentrancy.AllowAll, pid)
		require.NoError(t, pid.registerRequestState(state))

		errCh := make(chan error, 1)
		state.setCallback(func(_ any, err error) {
			errCh <- err
		})

		resp := &commands.AsyncResponse{
			CorrelationID: state.id,
			Error:         gerrors.ErrRequestTimeout.Error(),
		}
		pid.handleAsyncResponse(nil, resp)

		select {
		case err := <-errCh:
			require.ErrorIs(t, err, gerrors.ErrRequestTimeout)
		case <-time.After(reentrancyReplyTimeout):
			t.Fatal("expected error callback")
		}
		require.Zero(t, pid.reentrancy.requestStates.Len())
	})

	t.Run("error response unknown", func(t *testing.T) {
		pid := newRunningPIDWithReentrancy(reentrancy.AllowAll, 0)
		pid.handleAsyncResponse(nil, &commands.AsyncResponse{
			CorrelationID: "missing",
			Error:         "boom",
		})
	})

	t.Run("nil message completes", func(t *testing.T) {
		pid := newRunningPIDWithReentrancy(reentrancy.AllowAll, 0)
		state := newRequestState("nil-msg", reentrancy.AllowAll, pid)
		require.NoError(t, pid.registerRequestState(state))

		errCh := make(chan error, 1)
		state.setCallback(func(_ any, err error) {
			errCh <- err
		})

		pid.handleAsyncResponse(nil, &commands.AsyncResponse{CorrelationID: state.id})

		select {
		case err := <-errCh:
			require.ErrorIs(t, err, gerrors.ErrInvalidMessage)
		case <-time.After(reentrancyReplyTimeout):
			t.Fatal("expected invalid message callback")
		}
		require.Zero(t, pid.reentrancy.requestStates.Len())
	})

	t.Run("nil message unknown", func(t *testing.T) {
		pid := newRunningPIDWithReentrancy(reentrancy.AllowAll, 0)
		pid.handleAsyncResponse(nil, &commands.AsyncResponse{CorrelationID: "missing"})
	})

	t.Run("any message passes through", func(t *testing.T) {
		pid := newRunningPIDWithReentrancy(reentrancy.AllowAll, 0)
		state := newRequestState("any-msg", reentrancy.AllowAll, pid)
		require.NoError(t, pid.registerRequestState(state))

		respCh := make(chan any, 1)
		state.setCallback(func(msg any, err error) {
			if err == nil {
				respCh <- msg
			}
		})

		anyMsg := &anypb.Any{TypeUrl: "type.googleapis.com/nope.Nope", Value: []byte("bad")}
		pid.handleAsyncResponse(nil, &commands.AsyncResponse{
			CorrelationID: state.id,
			Message:       anyMsg,
		})

		select {
		case msg := <-respCh:
			_, ok := msg.(*anypb.Any)
			require.True(t, ok)
		case <-time.After(reentrancyReplyTimeout):
			t.Fatal("expected callback with passthrough message")
		}
		require.Zero(t, pid.reentrancy.requestStates.Len())
	})

	t.Run("invalid any unknown", func(t *testing.T) {
		pid := newRunningPIDWithReentrancy(reentrancy.AllowAll, 0)
		pid.handleAsyncResponse(nil, &commands.AsyncResponse{
			CorrelationID: "missing",
			Message:       &anypb.Any{TypeUrl: "type.googleapis.com/nope.Nope", Value: []byte("bad")},
		})
	})

	t.Run("success completes", func(t *testing.T) {
		pid := newRunningPIDWithReentrancy(reentrancy.AllowAll, 0)
		state := newRequestState("ok", reentrancy.AllowAll, pid)
		require.NoError(t, pid.registerRequestState(state))

		respCh := make(chan any, 1)
		state.setCallback(func(msg any, err error) {
			if err == nil {
				respCh <- msg
			}
		})

		payload, err := anypb.New(&testpb.Reply{Content: "ok"})
		require.NoError(t, err)
		pid.handleAsyncResponse(nil, &commands.AsyncResponse{
			CorrelationID: state.id,
			Message:       payload,
		})

		select {
		case msg := <-respCh:
			anyMsg, ok := msg.(*anypb.Any)
			require.True(t, ok)
			reply := new(testpb.Reply)
			require.NoError(t, anyMsg.UnmarshalTo(reply))
			require.Equal(t, "ok", reply.GetContent())
		case <-time.After(reentrancyReplyTimeout):
			t.Fatal("expected success callback")
		}
		require.Zero(t, pid.reentrancy.requestStates.Len())
	})

	t.Run("success unknown", func(t *testing.T) {
		pid := newRunningPIDWithReentrancy(reentrancy.AllowAll, 0)
		payload, err := anypb.New(&testpb.Reply{Content: "ok"})
		require.NoError(t, err)
		pid.handleAsyncResponse(nil, &commands.AsyncResponse{
			CorrelationID: "missing",
			Message:       payload,
		})
	})
}

func TestAsyncErrorFromString(t *testing.T) {
	require.ErrorIs(t, asyncErrorFromString(gerrors.ErrRequestTimeout.Error()), gerrors.ErrRequestTimeout)
	require.ErrorIs(t, asyncErrorFromString(gerrors.ErrRequestCanceled.Error()), gerrors.ErrRequestCanceled)
	require.EqualError(t, asyncErrorFromString("boom"), "boom")
}

func TestRegisterRequestStateValidation(t *testing.T) {
	pid := &PID{}
	state := newRequestState("id", reentrancy.AllowAll, pid)
	err := pid.registerRequestState(state)
	require.ErrorIs(t, err, gerrors.ErrReentrancyDisabled)

	pid = &PID{reentrancy: newReentrancyState(reentrancy.AllowAll, 0)}
	err = pid.registerRequestState(nil)
	require.ErrorIs(t, err, gerrors.ErrInvalidMessage)
}

func TestRegisterRequestStateTracksCounts(t *testing.T) {
	pid := &PID{reentrancy: newReentrancyState(reentrancy.AllowAll, 1)}
	state := newRequestState("id", reentrancy.AllowAll, pid)
	require.NoError(t, pid.registerRequestState(state))
	require.EqualValues(t, 1, pid.reentrancy.inFlightCount.Load())
	require.Nil(t, pid.stashState)
	pid.deregisterRequestState(state)

	pid = &PID{reentrancy: newReentrancyState(reentrancy.AllowAll, 0)}
	state = newRequestState("stash", reentrancy.StashNonReentrant, pid)
	require.NoError(t, pid.registerRequestState(state))
	require.EqualValues(t, 1, pid.reentrancy.inFlightCount.Load())
	require.EqualValues(t, 1, pid.reentrancy.blockingCount.Load())
	require.NotNil(t, pid.stashState)
	require.NotNil(t, pid.stashState.box)
}

func TestRegisterRequestStatePreservesExistingStash(t *testing.T) {
	pid := &PID{
		reentrancy: newReentrancyState(reentrancy.AllowAll, 0),
		stashState: &stashState{box: NewUnboundedMailbox()},
	}
	existing := pid.stashState
	state := newRequestState("stash", reentrancy.StashNonReentrant, pid)
	require.NoError(t, pid.registerRequestState(state))
	require.Same(t, existing, pid.stashState)
	pid.deregisterRequestState(state)
}

func TestRegisterRequestStateLimit(t *testing.T) {
	pid := &PID{reentrancy: newReentrancyState(reentrancy.AllowAll, 1)}
	state1 := newRequestState("id1", reentrancy.AllowAll, pid)
	state2 := newRequestState("id2", reentrancy.AllowAll, pid)

	require.NoError(t, pid.registerRequestState(state1))
	t.Cleanup(func() { pid.deregisterRequestState(state1) })
	err := pid.registerRequestState(state2)
	require.ErrorIs(t, err, gerrors.ErrReentrancyInFlightLimit)
}

func TestDeregisterRequestStateNoop(t *testing.T) {
	pid := &PID{}
	pid.deregisterRequestState(nil)

	pid = &PID{reentrancy: newReentrancyState(reentrancy.AllowAll, 0)}
	state := newRequestState("missing", reentrancy.AllowAll, pid)
	pid.deregisterRequestState(state)
}

func TestDeregisterRequestStateUnstashOnLastBlocking(t *testing.T) {
	pid := &PID{
		logger:     log.DiscardLogger,
		reentrancy: newReentrancyState(reentrancy.AllowAll, 0),
	}
	state := newRequestState("id", reentrancy.StashNonReentrant, pid)
	pid.reentrancy.requestStates.Set(state.id, state)
	pid.reentrancy.inFlightCount.Store(1)
	pid.reentrancy.blockingCount.Store(1)

	pid.deregisterRequestState(state)

	require.Zero(t, pid.reentrancy.inFlightCount.Load())
	require.Zero(t, pid.reentrancy.blockingCount.Load())
	require.Zero(t, pid.reentrancy.requestStates.Len())
}

func TestCompleteRequest(t *testing.T) {
	t.Run("no reentrancy", func(t *testing.T) {
		pid := &PID{}
		require.False(t, pid.completeRequest("id", nil, nil))
	})

	t.Run("unknown correlation", func(t *testing.T) {
		pid := &PID{reentrancy: newReentrancyState(reentrancy.AllowAll, 0)}
		require.False(t, pid.completeRequest("missing", nil, nil))
	})

	t.Run("idempotent completion", func(t *testing.T) {
		pid := &PID{reentrancy: newReentrancyState(reentrancy.AllowAll, 0)}
		state := newRequestState("idempotent", reentrancy.AllowAll, pid)
		pid.reentrancy.requestStates.Set(state.id, state)
		state.complete(nil, nil)

		require.True(t, pid.completeRequest(state.id, &testpb.Reply{}, nil))
		_, exists := pid.reentrancy.requestStates.Get(state.id)
		require.True(t, exists)
	})

	t.Run("completes and invokes callback", func(t *testing.T) {
		pid := &PID{
			logger:     log.DiscardLogger,
			reentrancy: newReentrancyState(reentrancy.AllowAll, 0),
		}
		state := newRequestState("done", reentrancy.AllowAll, pid)
		require.NoError(t, pid.registerRequestState(state))

		respCh := make(chan any, 1)
		state.setCallback(func(msg any, err error) {
			if err == nil {
				respCh <- msg
			}
		})

		payload := &testpb.Reply{Content: "ok"}
		require.True(t, pid.completeRequest(state.id, payload, nil))

		select {
		case msg := <-respCh:
			require.Equal(t, payload, msg)
		case <-time.After(reentrancyReplyTimeout):
			t.Fatal("expected completion callback")
		}

		require.Zero(t, pid.reentrancy.requestStates.Len())
		require.Zero(t, pid.reentrancy.inFlightCount.Load())
	})
}

func TestEnqueueAsyncError(t *testing.T) {
	ctx := context.Background()
	pid := newRunningPIDWithReentrancy(reentrancy.AllowAll, 0)

	require.ErrorIs(t, pid.enqueueAsyncError(ctx, "", gerrors.ErrRequestTimeout), gerrors.ErrInvalidMessage)
	require.NoError(t, pid.enqueueAsyncError(ctx, "noop", nil))

	state := newRequestState("corr", reentrancy.AllowAll, pid)
	require.NoError(t, pid.registerRequestState(state))

	errCh := make(chan error, 1)
	state.setCallback(func(_ any, err error) {
		errCh <- err
	})

	require.NoError(t, pid.enqueueAsyncError(ctx, "corr", gerrors.ErrRequestTimeout))

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, gerrors.ErrRequestTimeout)
	case <-time.After(reentrancyReplyTimeout):
		t.Fatal("expected async error callback")
	}
}

func TestSendAsyncResponsePaths(t *testing.T) {
	t.Run("invalid correlation", func(t *testing.T) {
		pid := &PID{}
		err := pid.sendAsyncResponse(context.Background(), "reply", "", new(testpb.TestSend), nil)
		require.ErrorIs(t, err, gerrors.ErrInvalidMessage)
	})

	t.Run("invalid replyTo", func(t *testing.T) {
		pid := &PID{}
		err := pid.sendAsyncResponse(context.Background(), "", "corr", new(testpb.TestSend), nil)
		require.ErrorIs(t, err, gerrors.ErrInvalidMessage)
	})

	t.Run("nil message", func(t *testing.T) {
		pid := &PID{}
		err := pid.sendAsyncResponse(context.Background(), "reply", "corr", nil, nil)
		require.ErrorIs(t, err, gerrors.ErrInvalidMessage)
	})

	t.Run("marshal error", func(t *testing.T) {
		pid := &PID{}
		err := pid.sendAsyncResponse(context.Background(), "reply", "corr", &testpb.Reply{Content: invalidUTF8String()}, nil)
		require.Error(t, err)
	})

	t.Run("address parse error", func(t *testing.T) {
		pid := &PID{}
		err := pid.sendAsyncResponse(context.Background(), "not-an-addr", "corr", &testpb.Reply{Content: "ok"}, nil)
		require.Error(t, err)
	})

	t.Run("actor system not started", func(t *testing.T) {
		pid := &PID{logger: log.DiscardLogger}
		replyTo := address.New("actor", "sys", "127.0.0.1", 9000).String()
		err := pid.sendAsyncResponse(context.Background(), replyTo, "corr", &testpb.Reply{Content: "ok"}, nil)
		require.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)
	})

	t.Run("local success and error", func(t *testing.T) {
		sys, ctx := newReentrancySystem(t)
		receiver := spawnReentrancyActor(t, sys, ctx, "reply-receiver", func(*ReceiveContext) {}, WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))
		sender := spawnReentrancyActor(t, sys, ctx, "reply-sender", func(*ReceiveContext) {})

		okState := newRequestState("corr-ok", reentrancy.AllowAll, receiver)
		require.NoError(t, receiver.registerRequestState(okState))
		t.Cleanup(func() { receiver.deregisterRequestState(okState) })

		okCh := make(chan any, 1)
		okState.setCallback(func(msg any, err error) {
			if err == nil {
				okCh <- msg
			}
		})

		require.NoError(t, sender.sendAsyncResponse(ctx, receiver.Address().String(), "corr-ok", &testpb.Reply{Content: "ok"}, nil))
		select {
		case msg := <-okCh:
			reply, ok := msg.(*testpb.Reply)
			require.True(t, ok)
			require.Equal(t, "ok", reply.GetContent())
		case <-time.After(reentrancyReplyTimeout):
			t.Fatal("expected async response")
		}

		errState := newRequestState("corr-err", reentrancy.AllowAll, receiver)
		require.NoError(t, receiver.registerRequestState(errState))
		t.Cleanup(func() { receiver.deregisterRequestState(errState) })

		errCh := make(chan error, 1)
		errState.setCallback(func(_ any, err error) {
			if err != nil {
				errCh <- err
			}
		})

		require.NoError(t, sender.sendAsyncResponse(ctx, receiver.Address().String(), "corr-err", nil, errors.New("boom")))
		select {
		case err := <-errCh:
			require.EqualError(t, err, "boom")
		case <-time.After(reentrancyReplyTimeout):
			t.Fatal("expected error response")
		}

		require.Zero(t, receiver.reentrancy.requestStates.Len())
	})

	t.Run("remote error", func(t *testing.T) {
		sys, ctx := newReentrancySystem(t)
		sender := spawnReentrancyActor(t, sys, ctx, "remote-sender", func(*ReceiveContext) {})
		replyTo := address.New("remote", "remote-system", "127.0.0.1", 9002).String()
		err := sender.sendAsyncResponse(ctx, replyTo, "corr", &testpb.Reply{Content: "ok"}, nil)
		require.ErrorIs(t, err, gerrors.ErrRemotingDisabled)
	})
}

func TestCancelInFlightRequestsBranches(t *testing.T) {
	t.Run("nil reentrancy", func(t *testing.T) {
		pid := &PID{}
		pid.cancelInFlightRequests(gerrors.ErrRequestCanceled)
	})

	t.Run("skips nil and completed states", func(t *testing.T) {
		pid := &PID{reentrancy: newReentrancyState(reentrancy.AllowAll, 0)}
		pid.reentrancy.requestStates.Set("nil", nil)

		completed := newRequestState("done", reentrancy.AllowAll, pid)
		completed.complete(nil, nil)
		pid.reentrancy.requestStates.Set("done", completed)

		stash := newRequestState("stash", reentrancy.StashNonReentrant, pid)
		pid.reentrancy.requestStates.Set("stash", stash)

		pid.reentrancy.inFlightCount.Store(1)
		pid.reentrancy.blockingCount.Store(1)

		pid.cancelInFlightRequests(gerrors.ErrRequestCanceled)

		require.Zero(t, pid.reentrancy.inFlightCount.Load())
		require.Zero(t, pid.reentrancy.blockingCount.Load())
		_, ok := pid.reentrancy.requestStates.Get("stash")
		require.False(t, ok)
		_, ok = pid.reentrancy.requestStates.Get("done")
		require.True(t, ok)
	})
}

func reportScenarioError(errCh chan<- error, err error) {
	if err == nil {
		return
	}
	select {
	case errCh <- err:
	default:
	}
}

func invalidUTF8String() string {
	return string([]byte{0xff})
}

func newRunningPIDWithReentrancy(mode reentrancy.Mode, maxInFlight int) *PID {
	pid := &PID{
		logger:     log.DiscardLogger,
		mailbox:    NewUnboundedMailbox(),
		reentrancy: newReentrancyState(mode, maxInFlight),
	}
	pid.setState(runningState, true)
	return pid
}

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

func waitForReply(t *testing.T, replyCh <-chan any, errCh <-chan error, timeout time.Duration) {
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

func waitForProcessedBeforeReply(t *testing.T, processedCh <-chan struct{}, replyCh <-chan any, errCh <-chan error, timeout time.Duration) {
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
