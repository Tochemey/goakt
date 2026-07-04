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

package testkit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/eventstream"
	"github.com/tochemey/goakt/v4/log"
)

// findLeader returns the name of the single node in nodes that currently reports
// itself as the cluster coordinator. It fails the test if zero or more than one
// node claims leadership at the time of the call.
func findLeader(ctx context.Context, t *testing.T, nodes map[string]*TestNode) (string, bool) {
	t.Helper()

	leaderName := ""
	leaders := 0
	for name, node := range nodes {
		isLeader, err := node.ActorSystem().IsLeader(ctx)
		if err != nil {
			return "", false
		}
		if isLeader {
			leaders++
			leaderName = name
		}
	}
	return leaderName, leaders == 1
}

func TestLeaderElection(t *testing.T) {
	ctx := context.Background()
	multi := NewMultiNodes(t, log.DiscardLogger, []actor.Actor{&pinger{}}, nil)
	multi.Start()
	t.Cleanup(multi.Stop)

	nodes := map[string]*TestNode{
		"leader-node-1": multi.StartNode(ctx, "leader-node-1"),
		"leader-node-2": multi.StartNode(ctx, "leader-node-2"),
		"leader-node-3": multi.StartNode(ctx, "leader-node-3"),
	}

	// the cluster must converge on exactly one coordinator, consistently observed
	// by every node
	var leaderName string
	require.Eventually(t, func() bool {
		name, ok := findLeader(ctx, t, nodes)
		if !ok {
			return false
		}
		leaderName = name
		return true
	}, 10*time.Second, 100*time.Millisecond)

	leaderAddress := nodes[leaderName].ActorSystem().PeersAddress()
	require.NotEmpty(t, leaderAddress)

	// every node must agree on who the leader is
	for name, node := range nodes {
		peer, err := node.ActorSystem().Leader(ctx)
		require.NoError(t, err, "node %s failed to resolve the cluster leader", name)
		require.NotNil(t, peer)
		require.Equal(t, leaderAddress, peer.PeersAddress(), "node %s disagrees on the cluster leader", name)
	}

	// subscribe on the surviving nodes before the coordinator departs
	subscribers := make(map[string]eventstream.Subscriber, len(nodes)-1)
	for name, node := range nodes {
		if name == leaderName {
			continue
		}
		subscribers[name] = node.Subscribe()
	}

	multi.StopNode(ctx, leaderName)
	delete(nodes, leaderName)

	// a new coordinator must be elected among the survivors
	require.Eventually(t, func() bool {
		_, ok := findLeader(ctx, t, nodes)
		return ok
	}, 10*time.Second, 100*time.Millisecond)

	// exactly one LeaderChanged(isLeader=true) must be observed across the whole
	// cluster for this transition. The internal cluster event pipeline that
	// drives LeaderChanged lags slightly behind the live membership view used
	// above, so drain the subscribers repeatedly until the event surfaces.
	var leaderChanges []*actor.LeaderChanged
	require.Eventually(t, func() bool {
		for _, subscriber := range subscribers {
			for event := range subscriber.Iterator() {
				if leaderChanged, ok := event.Payload().(*actor.LeaderChanged); ok {
					leaderChanges = append(leaderChanges, leaderChanged)
				}
			}
		}
		return len(leaderChanges) > 0
	}, 10*time.Second, 200*time.Millisecond)

	require.Len(t, leaderChanges, 1)
	require.True(t, leaderChanges[0].IsLeader())
	require.NotZero(t, leaderChanges[0].Timestamp())
}
