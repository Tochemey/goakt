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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/test/data/testpb"
)

func reportScenarioError(errCh chan<- error, err error) {
	if err == nil {
		return
	}
	select {
	case errCh <- err:
	default:
	}
}

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
			call.Then(func(resp proto.Message, err error) {
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
	}, WithReentrancy(ReentrancyAllowAll))

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
