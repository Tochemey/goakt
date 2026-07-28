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

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/commands"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	mockcluster "github.com/tochemey/goakt/v4/mocks/cluster"
	"github.com/tochemey/goakt/v4/reentrancy"
	"github.com/tochemey/goakt/v4/supervisor"
	"github.com/tochemey/goakt/v4/test/data/testpb"
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

// TestRequestNameAcrossNodes exercises the full remote reentrancy round trip:
// the request envelope travels from node1 to node2 and the response envelope
// travels back. Both hops depend on the async envelope serializers, without
// which the send fails with "no serializer found".
func TestRequestNameAcrossNodes(t *testing.T) {
	ctx := context.TODO()
	srv := startNatsServer(t)

	node1, sd1 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node1)
	node2, sd2 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node2)

	pause.For(time.Second)

	_, err := node2.Spawn(ctx, "remote-responder", &reentrancyTestActor{receive: func(rctx *ReceiveContext) {
		switch rctx.Message().(type) {
		case *testpb.TestPing:
			rctx.Response(&testpb.TestCount{Value: 42})
		default:
			rctx.Unhandled()
		}
	}})
	require.NoError(t, err)

	replyCh := make(chan *testpb.TestCount, 1)
	errCh := make(chan error, 1)

	requester, err := node1.Spawn(ctx, "remote-requester", &reentrancyTestActor{receive: func(rctx *ReceiveContext) {
		if _, ok := rctx.Message().(*testpb.TestSend); !ok {
			return
		}

		call := rctx.RequestName("remote-responder", new(testpb.TestPing), WithRequestTimeout(5*time.Second))
		if call == nil {
			reportScenarioError(errCh, rctx.getError())
			return
		}

		call.Then(func(resp any, err error) {
			if err != nil {
				reportScenarioError(errCh, err)
				return
			}

			count, ok := resp.(*testpb.TestCount)
			if !ok {
				reportScenarioError(errCh, errors.New("unexpected reply type"))
				return
			}
			replyCh <- count
		})
	}}, WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))
	require.NoError(t, err)

	pause.For(time.Second)
	require.NoError(t, Tell(ctx, requester, new(testpb.TestSend)))

	select {
	case err := <-errCh:
		t.Fatalf("remote request failed: %v", err)
	case msg := <-replyCh:
		require.EqualValues(t, 42, msg.GetValue())
	case <-time.After(10 * time.Second):
		t.Fatal("expected reply from remote request")
	}

	require.NoError(t, node1.Stop(ctx))
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd1.Close())
	require.NoError(t, sd2.Close())
	srv.Shutdown()
}

// TestGrainRequestEdgesAcrossNodes drives the three cross-kind request edges
// over a two-node cluster: grain to grain, grain to actor and actor to grain.
// Every request and reply envelope crosses the wire, because each requester
// lives on node1 while its target lives on node2.
func TestGrainRequestEdgesAcrossNodes(t *testing.T) {
	ctx := context.TODO()
	srv := startNatsServer(t)

	node1, sd1 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node1)
	node2, sd2 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node2)

	pause.For(time.Second)

	targetGrain := &scriptedGrain{receive: func(gctx *GrainContext) {
		switch gctx.Message().(type) {
		case *testpb.TestPing:
			gctx.Response(&testpb.TestCount{Value: 42})
		default:
			gctx.Unhandled()
		}
	}}
	targetID, err := node2.GrainIdentity(ctx, "edge-target-grain", func(context.Context) (Grain, error) {
		return targetGrain, nil
	})
	require.NoError(t, err)

	_, err = node2.Spawn(ctx, "edge-target-actor", &reentrancyTestActor{receive: func(rctx *ReceiveContext) {
		switch rctx.Message().(type) {
		case *testpb.TestPing:
			rctx.Response(&testpb.TestCount{Value: 42})
		default:
			rctx.Unhandled()
		}
	}})
	require.NoError(t, err)

	results := make(chan *testpb.TestCount, 1)
	errCh := make(chan error, 1)
	forward := func(result any, err error) {
		if err != nil {
			reportScenarioError(errCh, err)
			return
		}

		count, ok := result.(*testpb.TestCount)
		if !ok {
			reportScenarioError(errCh, errors.New("unexpected reply type"))
			return
		}
		results <- count
	}

	// TestSend drives the grain edge, TestBye the actor edge.
	requesterGrain := &scriptedGrain{receive: func(gctx *GrainContext) {
		switch gctx.Message().(type) {
		case *testpb.TestSend:
			gctx.RequestGrain(targetID, new(testpb.TestPing), WithRequestTimeout(5*time.Second)).Then(forward)
			gctx.NoErr()
		case *testpb.TestBye:
			gctx.RequestActor("edge-target-actor", new(testpb.TestPing), WithRequestTimeout(5*time.Second)).Then(forward)
			gctx.NoErr()
		default:
			gctx.Unhandled()
		}
	}}
	requesterID, err := node1.GrainIdentity(ctx, "edge-requester-grain", func(context.Context) (Grain, error) {
		return requesterGrain, nil
	}, WithGrainReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))
	require.NoError(t, err)

	expectCount := func(edge string) {
		t.Helper()

		select {
		case err := <-errCh:
			t.Fatalf("%s request failed: %v", edge, err)
		case count := <-results:
			require.EqualValues(t, 42, count.GetValue())
		case <-time.After(10 * time.Second):
			t.Fatalf("expected reply from the %s request", edge)
		}
	}

	require.NoError(t, node1.TellGrain(ctx, requesterID, new(testpb.TestSend)))
	expectCount("grain-to-grain")

	require.NoError(t, node1.TellGrain(ctx, requesterID, new(testpb.TestBye)))
	expectCount("grain-to-actor")

	requester, err := node1.Spawn(ctx, "edge-requester-actor", &reentrancyTestActor{receive: func(rctx *ReceiveContext) {
		if _, ok := rctx.Message().(*testpb.TestSend); !ok {
			return
		}

		call := rctx.RequestGrain(targetID, new(testpb.TestPing), WithRequestTimeout(5*time.Second))
		if call == nil {
			reportScenarioError(errCh, rctx.getError())
			return
		}
		call.Then(forward)
	}}, WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))
	require.NoError(t, err)

	pause.For(time.Second)
	require.NoError(t, Tell(ctx, requester, new(testpb.TestSend)))
	expectCount("actor-to-grain")

	require.NoError(t, node1.Stop(ctx))
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd1.Close())
	require.NoError(t, sd2.Close())
	srv.Shutdown()
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
	require.EqualValues(t, 0, requester.reentrancy.Load().inFlightCount.Load())
	require.EqualValues(t, 0, requester.reentrancy.Load().blockingCount.Load())
	require.Zero(t, requester.reentrancy.Load().requestStates.Len())
}

func TestToWireActorIncludesReentrancy(t *testing.T) {
	pid := &PID{
		actor:        NewMockActor(),
		address:      address.New("actor-reentrancy-wire", "testSys", "127.0.0.1", 0),
		fieldsLocker: sync.RWMutex{},
	}
	pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 3))

	wire, err := pid.toSerialize()
	require.NoError(t, err)
	require.NotNil(t, wire.GetReentrancy())
	require.Equal(t, internalpb.ReentrancyMode_REENTRANCY_MODE_ALLOW_ALL, wire.GetReentrancy().GetMode())
	require.Equal(t, uint32(3), wire.GetReentrancy().GetMaxInFlight())
}

func TestWithReentrancyDoesNotEnableStash(t *testing.T) {
	pid := &PID{}
	withReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll)))(pid)
	require.NotNil(t, pid.reentrancy.Load())
	require.Nil(t, pid.stashState)

	pid = &PID{}
	withReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.StashNonReentrant)))(pid)
	require.NotNil(t, pid.reentrancy.Load())
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
		pid := newRunningPIDWithReentrancy(t, reentrancy.Off, 0)
		target := &PID{}
		target.setState(runningState, true)
		_, err := pid.request(ctx, target, new(testpb.TestSend))
		require.ErrorIs(t, err, gerrors.ErrReentrancyDisabled)
	})
}

func TestRequestTellErrorDeregisters(t *testing.T) {
	ctx := context.Background()
	pid := newRunningPIDWithReentrancy(t, reentrancy.AllowAll, 0)
	target := &PID{}
	target.setState(runningState, false)

	_, err := pid.request(ctx, target, new(testpb.TestSend))
	require.ErrorIs(t, err, gerrors.ErrDead)
	require.Zero(t, pid.reentrancy.Load().inFlightCount.Load())
	require.Zero(t, pid.reentrancy.Load().requestStates.Len())
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
		pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 0))
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
		pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 0))
		_, err := pid.requestName(ctx, "actor", new(testpb.TestSend), WithReentrancyMode(reentrancy.Off))
		require.ErrorIs(t, err, gerrors.ErrReentrancyDisabled)
	})

	t.Run("invalid mode", func(t *testing.T) {
		pid := &PID{}
		pid.setState(runningState, true)
		pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 0))
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
	require.Zero(t, requester.reentrancy.Load().inFlightCount.Load())
	require.Zero(t, requester.reentrancy.Load().requestStates.Len())
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
	}
	pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 0))
	pid.setState(runningState, true)

	_, err := pid.requestName(context.Background(), "remote-actor", new(testpb.TestSend))
	require.ErrorIs(t, err, gerrors.ErrRemotingDisabled)
}

func TestProcessStashErrorPath(t *testing.T) {
	d := newDispatcher(dispatcherWorkerCount(), dispatcherThroughput)
	d.start()
	t.Cleanup(d.signalStop)

	pid := &PID{
		mailbox:       NewUnboundedMailbox(),
		systemMailbox: NewUnboundedMailbox(),
		logger:        log.DiscardLogger,
		dispatcher:    d,
	}
	pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 0))
	pid.reentrancy.Load().blockingCount.Store(1)

	receiveCtx := getContext()
	receiveCtx.build(context.Background(), pid, pid, new(testpb.TestSend), true)
	pid.doReceive(receiveCtx)

	require.Eventually(t, func() bool {
		return pid.schedState.Load() == dispatchIdle && pid.mailbox.IsEmpty()
	}, reentrancyReplyTimeout, reentrancyShortWait)
}

func TestHandleAsyncRequestValidationErrors(t *testing.T) {
	pid := &PID{logger: log.DiscardLogger}

	t.Run("nil request", func(t *testing.T) {
		pid.handleAsyncRequest(nil, nil, time.Now())
	})

	t.Run("missing fields", func(t *testing.T) {
		received := newReceiveContext(context.Background(), pid, pid, new(testpb.TestSend))
		pid.handleAsyncRequest(received, &commands.AsyncRequest{}, time.Now())
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
		pid := newRunningPIDWithReentrancy(t, reentrancy.AllowAll, 0)
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
		require.Zero(t, pid.reentrancy.Load().requestStates.Len())
	})

	t.Run("error response unknown", func(t *testing.T) {
		pid := newRunningPIDWithReentrancy(t, reentrancy.AllowAll, 0)
		pid.handleAsyncResponse(nil, &commands.AsyncResponse{
			CorrelationID: "missing",
			Error:         "boom",
		})
	})

	// An empty response is the wire form of a grain's NoErr: success without a
	// payload.
	t.Run("nil message completes as success", func(t *testing.T) {
		pid := newRunningPIDWithReentrancy(t, reentrancy.AllowAll, 0)
		state := newRequestState("nil-msg", reentrancy.AllowAll, pid)
		require.NoError(t, pid.registerRequestState(state))

		type outcome struct {
			result any
			err    error
		}
		outcomes := make(chan outcome, 1)

		state.setCallback(func(result any, err error) {
			outcomes <- outcome{result: result, err: err}
		})

		pid.handleAsyncResponse(nil, &commands.AsyncResponse{CorrelationID: state.id})

		select {
		case got := <-outcomes:
			require.NoError(t, got.err)
			require.Nil(t, got.result)
		case <-time.After(reentrancyReplyTimeout):
			t.Fatal("expected success callback")
		}
		require.Zero(t, pid.reentrancy.Load().requestStates.Len())
	})

	t.Run("nil message unknown", func(t *testing.T) {
		pid := newRunningPIDWithReentrancy(t, reentrancy.AllowAll, 0)
		pid.handleAsyncResponse(nil, &commands.AsyncResponse{CorrelationID: "missing"})
	})

	t.Run("any message passes through", func(t *testing.T) {
		pid := newRunningPIDWithReentrancy(t, reentrancy.AllowAll, 0)
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
		require.Zero(t, pid.reentrancy.Load().requestStates.Len())
	})

	t.Run("invalid any unknown", func(t *testing.T) {
		pid := newRunningPIDWithReentrancy(t, reentrancy.AllowAll, 0)
		pid.handleAsyncResponse(nil, &commands.AsyncResponse{
			CorrelationID: "missing",
			Message:       &anypb.Any{TypeUrl: "type.googleapis.com/nope.Nope", Value: []byte("bad")},
		})
	})

	t.Run("success completes", func(t *testing.T) {
		pid := newRunningPIDWithReentrancy(t, reentrancy.AllowAll, 0)
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
		require.Zero(t, pid.reentrancy.Load().requestStates.Len())
	})

	t.Run("success unknown", func(t *testing.T) {
		pid := newRunningPIDWithReentrancy(t, reentrancy.AllowAll, 0)
		payload, err := anypb.New(&testpb.Reply{Content: "ok"})
		require.NoError(t, err)
		pid.handleAsyncResponse(nil, &commands.AsyncResponse{
			CorrelationID: "missing",
			Message:       payload,
		})
	})
}

// recordingErrorSink is a minimal asyncErrorSink that is not a process. It
// proves the request machinery depends only on the sink contract, which is what
// lets grains own in-flight requests without duplicating any of this state.
type recordingErrorSink struct {
	errs chan error
	ret  error
}

func newRecordingErrorSink(ret error) *recordingErrorSink {
	return &recordingErrorSink{errs: make(chan error, 1), ret: ret}
}

func (s *recordingErrorSink) enqueueAsyncError(_ context.Context, _ string, err error) error {
	select {
	case s.errs <- err:
	default:
	}
	return s.ret
}

func TestRequestStateRequesterContract(t *testing.T) {
	t.Run("cancel routes through the requester", func(t *testing.T) {
		sink := newRecordingErrorSink(nil)
		state := newRequestState("corr", reentrancy.AllowAll, sink)

		require.NoError(t, state.cancel())

		select {
		case err := <-sink.errs:
			require.ErrorIs(t, err, gerrors.ErrRequestCanceled)
		case <-time.After(reentrancyReplyTimeout):
			t.Fatal("expected cancellation to reach the requester")
		}
	})

	t.Run("cancel surfaces the requester error", func(t *testing.T) {
		sink := newRecordingErrorSink(gerrors.ErrDead)
		state := newRequestState("corr", reentrancy.AllowAll, sink)

		require.ErrorIs(t, state.cancel(), gerrors.ErrDead)
	})

	t.Run("timeout routes through the requester", func(t *testing.T) {
		sink := newRecordingErrorSink(nil)
		state := newRequestState("corr", reentrancy.AllowAll, sink)

		state.startTimeout(10 * time.Millisecond)

		select {
		case err := <-sink.errs:
			require.ErrorIs(t, err, gerrors.ErrRequestTimeout)
		case <-time.After(reentrancyReplyTimeout):
			t.Fatal("expected timeout to reach the requester")
		}
	})

	t.Run("stopped timeout never reaches the requester", func(t *testing.T) {
		sink := newRecordingErrorSink(nil)
		state := newRequestState("corr", reentrancy.AllowAll, sink)

		state.startTimeout(time.Hour)
		state.stopTimeoutIfSet()

		select {
		case err := <-sink.errs:
			t.Fatalf("unexpected error after the timeout was stopped: %v", err)
		case <-time.After(reentrancyShortWait):
		}
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

	pid = &PID{}
	pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 0))
	err = pid.registerRequestState(nil)
	require.ErrorIs(t, err, gerrors.ErrInvalidMessage)
}

func TestRegisterRequestStateTracksCounts(t *testing.T) {
	pid := &PID{}
	pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 1))
	state := newRequestState("id", reentrancy.AllowAll, pid)
	require.NoError(t, pid.registerRequestState(state))
	require.EqualValues(t, 1, pid.reentrancy.Load().inFlightCount.Load())
	require.Nil(t, pid.stashState)
	pid.deregisterRequestState(state)

	pid = &PID{}
	pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 0))
	state = newRequestState("stash", reentrancy.StashNonReentrant, pid)
	require.NoError(t, pid.registerRequestState(state))
	require.EqualValues(t, 1, pid.reentrancy.Load().inFlightCount.Load())
	require.EqualValues(t, 1, pid.reentrancy.Load().blockingCount.Load())
	require.NotNil(t, pid.stashState)
	require.NotNil(t, pid.stashState.box)
}

func TestRegisterRequestStatePreservesExistingStash(t *testing.T) {
	pid := &PID{
		stashState: &stashState{box: NewUnboundedMailbox()},
	}
	pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 0))
	existing := pid.stashState
	state := newRequestState("stash", reentrancy.StashNonReentrant, pid)
	require.NoError(t, pid.registerRequestState(state))
	require.Same(t, existing, pid.stashState)
	pid.deregisterRequestState(state)
}

func TestRegisterRequestStateLimit(t *testing.T) {
	pid := &PID{}
	pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 1))
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

	pid = &PID{}
	pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 0))
	state := newRequestState("missing", reentrancy.AllowAll, pid)
	pid.deregisterRequestState(state)
}

func TestDeregisterRequestStateUnstashOnLastBlocking(t *testing.T) {
	pid := &PID{
		logger: log.DiscardLogger,
	}
	pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 0))
	state := newRequestState("id", reentrancy.StashNonReentrant, pid)
	pid.reentrancy.Load().requestStates.Set(state.id, state)
	pid.reentrancy.Load().inFlightCount.Store(1)
	pid.reentrancy.Load().blockingCount.Store(1)

	pid.deregisterRequestState(state)

	require.Zero(t, pid.reentrancy.Load().inFlightCount.Load())
	require.Zero(t, pid.reentrancy.Load().blockingCount.Load())
	require.Zero(t, pid.reentrancy.Load().requestStates.Len())
}

func TestCompleteRequest(t *testing.T) {
	t.Run("no reentrancy", func(t *testing.T) {
		pid := &PID{}
		require.False(t, pid.completeRequest("id", nil, nil))
	})

	t.Run("unknown correlation", func(t *testing.T) {
		pid := &PID{}
		pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 0))
		require.False(t, pid.completeRequest("missing", nil, nil))
	})

	t.Run("idempotent completion", func(t *testing.T) {
		pid := &PID{}
		pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 0))
		state := newRequestState("idempotent", reentrancy.AllowAll, pid)
		pid.reentrancy.Load().requestStates.Set(state.id, state)
		state.complete(nil, nil)

		require.True(t, pid.completeRequest(state.id, &testpb.Reply{}, nil))
		_, exists := pid.reentrancy.Load().requestStates.Get(state.id)
		require.True(t, exists)
	})

	t.Run("completes and invokes callback", func(t *testing.T) {
		pid := &PID{
			logger: log.DiscardLogger,
		}
		pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 0))
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

		require.Zero(t, pid.reentrancy.Load().requestStates.Len())
		require.Zero(t, pid.reentrancy.Load().inFlightCount.Load())
	})
}

func TestEnqueueAsyncError(t *testing.T) {
	ctx := context.Background()
	pid := newRunningPIDWithReentrancy(t, reentrancy.AllowAll, 0)

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

func TestCancelInFlightRequestsBranches(t *testing.T) {
	t.Run("nil reentrancy", func(t *testing.T) {
		pid := &PID{}
		pid.cancelInFlightRequests(gerrors.ErrRequestCanceled)
	})

	t.Run("skips nil and completed states", func(t *testing.T) {
		pid := &PID{}
		pid.reentrancy.Store(newReentrancyState(reentrancy.AllowAll, 0))
		pid.reentrancy.Load().requestStates.Set("nil", nil)

		completed := newRequestState("done", reentrancy.AllowAll, pid)
		completed.complete(nil, nil)
		pid.reentrancy.Load().requestStates.Set("done", completed)

		stash := newRequestState("stash", reentrancy.StashNonReentrant, pid)
		pid.reentrancy.Load().requestStates.Set("stash", stash)

		pid.reentrancy.Load().inFlightCount.Store(1)
		pid.reentrancy.Load().blockingCount.Store(1)

		pid.cancelInFlightRequests(gerrors.ErrRequestCanceled)

		require.Zero(t, pid.reentrancy.Load().inFlightCount.Load())
		require.Zero(t, pid.reentrancy.Load().blockingCount.Load())
		_, ok := pid.reentrancy.Load().requestStates.Get("stash")
		require.False(t, ok)
		_, ok = pid.reentrancy.Load().requestStates.Get("done")
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

func newRunningPIDWithReentrancy(t *testing.T, mode reentrancy.Mode, maxInFlight int) *PID {
	t.Helper()
	d := newDispatcher(dispatcherWorkerCount(), dispatcherThroughput)
	d.start()
	t.Cleanup(d.signalStop)

	pid := &PID{
		logger:        log.DiscardLogger,
		mailbox:       NewUnboundedMailbox(),
		systemMailbox: NewUnboundedMailbox(),
		dispatcher:    d,
	}
	pid.reentrancy.Store(newReentrancyState(mode, maxInFlight))
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

// TestActorRequestGrain covers the actor-to-grain request edge: the actor's
// request travels as an envelope into the grain, and the grain's reply comes
// back through the actor's mailbox like any other async response.
func TestActorRequestGrain(t *testing.T) {
	sys, ctx := newReentrancySystem(t)
	system := sys.(*actorSystem)

	grain := &scriptedGrain{receive: func(gctx *GrainContext) {
		gctx.Response(&testpb.TestCount{Value: 21})
	}}
	identity := activateReentrantGrain(t, system, grain, "answering-grain")

	results := make(chan *testpb.TestCount, 1)
	failures := make(chan error, 1)

	requester := spawnReentrancyActor(t, sys, ctx, "grain-requester", func(rctx *ReceiveContext) {
		if _, ok := rctx.Message().(*testpb.TestSend); !ok {
			return
		}

		call := rctx.RequestGrain(identity, new(testpb.TestPing), WithRequestTimeout(reentrancyReplyTimeout))
		if call == nil {
			failures <- rctx.getError()
			return
		}

		call.Then(func(result any, err error) {
			if err != nil {
				failures <- err
				return
			}
			results <- result.(*testpb.TestCount)
		})
	}, WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))

	require.NoError(t, Tell(ctx, requester, new(testpb.TestSend)))

	select {
	case count := <-results:
		require.EqualValues(t, 21, count.GetValue())
	case err := <-failures:
		t.Fatalf("request failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("continuation never ran")
	}
}

// TestActorRequestGrainRequiresReentrancy pins the gate: without reentrancy
// the actor's RequestGrain returns nil and records ErrReentrancyDisabled.
func TestActorRequestGrainRequiresReentrancy(t *testing.T) {
	sys, ctx := newReentrancySystem(t)
	system := sys.(*actorSystem)

	grain := &scriptedGrain{receive: func(gctx *GrainContext) { gctx.NoErr() }}
	identity := activateReentrantGrain(t, system, grain, "gated-grain")

	failures := make(chan error, 1)

	plain := spawnReentrancyActor(t, sys, ctx, "plain-requester", func(rctx *ReceiveContext) {
		if _, ok := rctx.Message().(*testpb.TestSend); !ok {
			return
		}

		if call := rctx.RequestGrain(identity, new(testpb.TestPing)); call == nil {
			failures <- rctx.getError()
		}
	})

	require.NoError(t, Tell(ctx, plain, new(testpb.TestSend)))

	select {
	case err := <-failures:
		require.ErrorIs(t, err, gerrors.ErrReentrancyDisabled)
	case <-time.After(2 * time.Second):
		t.Fatal("gate never reported")
	}
}

func TestRequestGrainValidationFailures(t *testing.T) {
	ctx := context.Background()
	identity := &GrainIdentity{kind: "Kind", name: "name"}

	t.Run("dead requester", func(t *testing.T) {
		pid := &PID{}
		_, err := pid.requestGrain(ctx, identity, new(testpb.TestSend))
		require.ErrorIs(t, err, gerrors.ErrDead)
	})

	t.Run("nil message", func(t *testing.T) {
		pid := &PID{}
		pid.setState(runningState, true)
		_, err := pid.requestGrain(ctx, identity, nil)
		require.ErrorIs(t, err, gerrors.ErrInvalidMessage)
	})

	t.Run("reentrancy disabled", func(t *testing.T) {
		pid := &PID{}
		pid.setState(runningState, true)
		_, err := pid.requestGrain(ctx, identity, new(testpb.TestSend))
		require.ErrorIs(t, err, gerrors.ErrReentrancyDisabled)
	})

	t.Run("nil identity", func(t *testing.T) {
		pid := newRunningPIDWithReentrancy(t, reentrancy.AllowAll, 0)
		_, err := pid.requestGrain(ctx, nil, new(testpb.TestSend))
		require.ErrorIs(t, err, gerrors.ErrInvalidGrainIdentity)
	})

	t.Run("invalid identity", func(t *testing.T) {
		pid := newRunningPIDWithReentrancy(t, reentrancy.AllowAll, 0)
		_, err := pid.requestGrain(ctx, &GrainIdentity{}, new(testpb.TestSend))
		require.ErrorIs(t, err, gerrors.ErrInvalidGrainIdentity)
	})

	t.Run("off mode", func(t *testing.T) {
		pid := newRunningPIDWithReentrancy(t, reentrancy.AllowAll, 0)
		_, err := pid.requestGrain(ctx, identity, new(testpb.TestSend), WithReentrancyMode(reentrancy.Off))
		require.ErrorIs(t, err, gerrors.ErrReentrancyDisabled)
	})

	t.Run("invalid mode", func(t *testing.T) {
		pid := newRunningPIDWithReentrancy(t, reentrancy.AllowAll, 0)
		_, err := pid.requestGrain(ctx, identity, new(testpb.TestSend), WithReentrancyMode(reentrancy.Mode(99)))
		require.ErrorIs(t, err, gerrors.ErrInvalidReentrancyMode)
	})
}

// TestActorRequestGrainDeliveryFailure pins the deregistration on a failed
// envelope delivery: the target kind is unknown, the caller gets the error and
// no in-flight state leaks.
func TestActorRequestGrainDeliveryFailure(t *testing.T) {
	sys, ctx := newReentrancySystem(t)

	unknown := newGrainIdentity(&MockGrainActivationFailure{}, "never-registered")
	failures := make(chan error, 1)

	requester := spawnReentrancyActor(t, sys, ctx, "orphan-requester", func(rctx *ReceiveContext) {
		if _, ok := rctx.Message().(*testpb.TestSend); !ok {
			return
		}

		if call := rctx.RequestGrain(unknown, new(testpb.TestPing)); call == nil {
			failures <- rctx.getError()
		}
	}, WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))

	require.NoError(t, Tell(ctx, requester, new(testpb.TestSend)))

	select {
	case err := <-failures:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("delivery failure never reported")
	}
	require.Zero(t, requester.reentrancy.Load().inFlightCount.Load())
}

// TestActorEnableReentrancyAtRuntime covers the runtime toggle: an actor
// spawned without reentrancy enables it during message processing, requests,
// disables it again, and re-enables it.
func TestActorEnableReentrancyAtRuntime(t *testing.T) {
	sys, ctx := newReentrancySystem(t)

	target := spawnReentrancyActor(t, sys, ctx, "runtime-target", func(rctx *ReceiveContext) {
		if _, ok := rctx.Message().(*testpb.TestPing); ok {
			rctx.Response(&testpb.TestCount{Value: 11})
		}
	})

	results := make(chan *testpb.TestCount, 2)
	failures := make(chan error, 4)

	// TestSend enables, TestBye disables, TestPing issues a request.
	requester := spawnReentrancyActor(t, sys, ctx, "runtime-requester", func(rctx *ReceiveContext) {
		switch rctx.Message().(type) {
		case *testpb.TestSend:
			if err := rctx.EnableReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))); err != nil {
				failures <- err
			}
		case *testpb.TestBye:
			rctx.DisableReentrancy()
		case *testpb.TestPing:
			call := rctx.Request(target, new(testpb.TestPing), WithRequestTimeout(reentrancyReplyTimeout))
			if call == nil {
				failures <- rctx.getError()
				return
			}

			call.Then(func(result any, err error) {
				if err != nil {
					failures <- err
					return
				}
				results <- result.(*testpb.TestCount)
			})
		}
	}, WithSupervisor(supervisor.NewSupervisor(supervisor.WithAnyErrorDirective(supervisor.ResumeDirective))))

	expectFailure := func(want error) {
		t.Helper()
		select {
		case err := <-failures:
			require.ErrorIs(t, err, want)
		case <-time.After(reentrancyReplyTimeout):
			t.Fatal("expected a failure")
		}
	}

	expectResult := func() {
		t.Helper()
		select {
		case count := <-results:
			require.EqualValues(t, 11, count.GetValue())
		case err := <-failures:
			t.Fatalf("request failed: %v", err)
		case <-time.After(2 * time.Second):
			t.Fatal("request never completed")
		}
	}

	// Without reentrancy the request is rejected.
	require.NoError(t, Tell(ctx, requester, new(testpb.TestPing)))
	expectFailure(gerrors.ErrReentrancyDisabled)

	// Enabled at runtime: the request completes.
	require.NoError(t, Tell(ctx, requester, new(testpb.TestSend)))
	require.NoError(t, Tell(ctx, requester, new(testpb.TestPing)))
	expectResult()

	// Disabled again: rejected again.
	require.NoError(t, Tell(ctx, requester, new(testpb.TestBye)))
	require.NoError(t, Tell(ctx, requester, new(testpb.TestPing)))
	expectFailure(gerrors.ErrReentrancyDisabled)

	// Re-enabled: works again.
	require.NoError(t, Tell(ctx, requester, new(testpb.TestSend)))
	require.NoError(t, Tell(ctx, requester, new(testpb.TestPing)))
	expectResult()
}

// TestActorDisableReentrancyKeepsInFlight pins the disable semantics: requests
// already in flight complete normally while new ones are rejected.
func TestActorDisableReentrancyKeepsInFlight(t *testing.T) {
	sys, ctx := newReentrancySystem(t)

	release := make(chan struct{})
	target := spawnReentrancyActor(t, sys, ctx, "slow-target", func(rctx *ReceiveContext) {
		if _, ok := rctx.Message().(*testpb.TestPing); ok {
			sender := rctx.Sender()
			self := rctx.Self()
			requestID := rctx.requestID
			replyTo := rctx.requestReplyTo

			go func() {
				<-release
				_ = self.ActorSystem().routeAsyncReply(context.Background(), self, replyTo, requestID, &testpb.TestCount{Value: 5}, nil)
				_ = sender
			}()
		}
	})

	results := make(chan *testpb.TestCount, 1)
	failures := make(chan error, 2)

	requester := spawnReentrancyActor(t, sys, ctx, "toggling-requester", func(rctx *ReceiveContext) {
		switch rctx.Message().(type) {
		case *testpb.TestPing:
			call := rctx.Request(target, new(testpb.TestPing), WithRequestTimeout(5*time.Second))
			if call == nil {
				failures <- rctx.getError()
				return
			}

			call.Then(func(result any, err error) {
				if err != nil {
					failures <- err
					return
				}
				results <- result.(*testpb.TestCount)
			})
		case *testpb.TestBye:
			rctx.DisableReentrancy()
		case *testpb.TestSend:
			if call := rctx.Request(target, new(testpb.TestPing)); call == nil {
				failures <- rctx.getError()
			}
		}
	}, WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))),
		WithSupervisor(supervisor.NewSupervisor(supervisor.WithAnyErrorDirective(supervisor.ResumeDirective))))

	// Start a request, then disable while it is in flight.
	require.NoError(t, Tell(ctx, requester, new(testpb.TestPing)))
	require.NoError(t, Tell(ctx, requester, new(testpb.TestBye)))

	// New requests are rejected while disabled.
	require.NoError(t, Tell(ctx, requester, new(testpb.TestSend)))
	select {
	case err := <-failures:
		require.ErrorIs(t, err, gerrors.ErrReentrancyDisabled)
	case <-time.After(reentrancyReplyTimeout):
		t.Fatal("expected rejection while disabled")
	}

	// The in-flight request still completes.
	close(release)
	select {
	case count := <-results:
		require.EqualValues(t, 5, count.GetValue())
	case err := <-failures:
		t.Fatalf("in-flight request failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight request never completed")
	}
}

func TestEnableReentrancyValidation(t *testing.T) {
	pid := &PID{}

	require.ErrorIs(t, pid.enableReentrancy(nil), gerrors.ErrInvalidReentrancyMode)
	require.ErrorIs(t, pid.enableReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.Mode(99)))), gerrors.ErrInvalidReentrancyMode)

	// Install, then retune.
	require.NoError(t, pid.enableReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll), reentrancy.WithMaxInFlight(2))))
	reentrant := pid.reentrancy.Load()
	require.NotNil(t, reentrant)
	require.Equal(t, reentrancy.AllowAll, reentrant.getMode())
	require.EqualValues(t, 2, reentrant.maxInFlight.Load())

	require.NoError(t, pid.enableReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.StashNonReentrant), reentrancy.WithMaxInFlight(9))))
	require.Same(t, reentrant, pid.reentrancy.Load())
	require.Equal(t, reentrancy.StashNonReentrant, reentrant.getMode())
	require.EqualValues(t, 9, reentrant.maxInFlight.Load())

	// Disable flips the mode; the state object survives.
	pid.disableReentrancy()
	require.Same(t, reentrant, pid.reentrancy.Load())
	require.Equal(t, reentrancy.Off, reentrant.getMode())

	// Disabling a never-enabled process is a no-op.
	fresh := &PID{}
	require.NotPanics(t, fresh.disableReentrancy)
	require.Nil(t, fresh.reentrancy.Load())
}

// TestDisableReentrancyPerCallOverride pins the disable semantics against the
// pre-existing Off-policy contract: disable restores a default-Off policy, and
// exactly like a policy configured Off at spawn, an explicit per-call mode
// override still admits an individual request.
func TestDisableReentrancyPerCallOverride(t *testing.T) {
	sys, ctx := newReentrancySystem(t)

	target := spawnReentrancyActor(t, sys, ctx, "override-target", func(rctx *ReceiveContext) {
		if _, ok := rctx.Message().(*testpb.TestPing); ok {
			rctx.Response(&testpb.TestCount{Value: 3})
		}
	})

	results := make(chan *testpb.TestCount, 1)
	failures := make(chan error, 2)

	requester := spawnReentrancyActor(t, sys, ctx, "override-requester", func(rctx *ReceiveContext) {
		switch rctx.Message().(type) {
		case *testpb.TestBye:
			rctx.DisableReentrancy()
		case *testpb.TestPing:
			call := rctx.Request(target, new(testpb.TestPing),
				WithReentrancyMode(reentrancy.AllowAll), WithRequestTimeout(reentrancyReplyTimeout))
			if call == nil {
				failures <- rctx.getError()
				return
			}

			call.Then(func(result any, err error) {
				if err != nil {
					failures <- err
					return
				}
				results <- result.(*testpb.TestCount)
			})
		}
	}, WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))

	require.NoError(t, Tell(ctx, requester, new(testpb.TestBye)))
	require.NoError(t, Tell(ctx, requester, new(testpb.TestPing)))

	select {
	case count := <-results:
		require.EqualValues(t, 3, count.GetValue())
	case err := <-failures:
		t.Fatalf("override request failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("override request never completed")
	}
}
