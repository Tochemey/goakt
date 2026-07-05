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
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
)

// TestTopicStats exercises TopicStats across a real cluster: subscribers
// spread over different nodes, an unsubscribe on one node, and a node
// leaving the cluster altogether.
func TestTopicStats(t *testing.T) {
	ctx := context.Background()

	multi := NewMultiNodes(t, log.DiscardLogger, []actor.Actor{&pinger{}}, nil)
	multi.Start()
	t.Cleanup(multi.Stop)

	node1 := multi.StartNode(ctx, "topic-stats-node-1")
	node2 := multi.StartNode(ctx, "topic-stats-node-2")
	node3 := multi.StartNode(ctx, "topic-stats-node-3")

	// give the cluster time to settle before subscribing
	pause.For(2 * time.Second)

	const topic = "stats-topic"

	node1.Spawn(ctx, "subscriber-1", &pinger{})
	node2.Spawn(ctx, "subscriber-2a", &pinger{})
	node2.Spawn(ctx, "subscriber-2b", &pinger{})
	node3.Spawn(ctx, "subscriber-3", &pinger{})

	sub1, err := node1.ActorSystem().ActorOf(ctx, "subscriber-1")
	require.NoError(t, err)
	sub2a, err := node2.ActorSystem().ActorOf(ctx, "subscriber-2a")
	require.NoError(t, err)
	sub2b, err := node2.ActorSystem().ActorOf(ctx, "subscriber-2b")
	require.NoError(t, err)
	sub3, err := node3.ActorSystem().ActorOf(ctx, "subscriber-3")
	require.NoError(t, err)

	require.NoError(t, sub1.Tell(ctx, node1.ActorSystem().TopicActor(), actor.NewSubscribe(topic)))
	require.NoError(t, sub2a.Tell(ctx, node2.ActorSystem().TopicActor(), actor.NewSubscribe(topic)))
	require.NoError(t, sub2b.Tell(ctx, node2.ActorSystem().TopicActor(), actor.NewSubscribe(topic)))
	require.NoError(t, sub3.Tell(ctx, node3.ActorSystem().TopicActor(), actor.NewSubscribe(topic)))

	// node2 has two local subscribers; the cluster-wide instance count only
	// reflects the number of nodes with subscribers (three), not the total
	// subscriber count (four).
	require.Eventually(t, func() bool {
		stats, err := node2.ActorSystem().TopicStats(ctx, topic, 5*time.Second)
		return err == nil && stats.LocalSubscriberCount == 2 && stats.TopicInstanceCount == 3
	}, 15*time.Second, 300*time.Millisecond)

	stats1, err := node1.ActorSystem().TopicStats(ctx, topic, 5*time.Second)
	require.NoError(t, err)
	require.Equal(t, topic, stats1.Topic)
	require.Equal(t, 1, stats1.LocalSubscriberCount)
	require.Equal(t, 3, stats1.TopicInstanceCount)

	stats3, err := node3.ActorSystem().TopicStats(ctx, topic, 5*time.Second)
	require.NoError(t, err)
	require.Equal(t, 1, stats3.LocalSubscriberCount)
	require.Equal(t, 3, stats3.TopicInstanceCount)

	// unsubscribing node3's only subscriber drops the cluster-wide instance
	// count, visible from a different node.
	require.NoError(t, sub3.Tell(ctx, node3.ActorSystem().TopicActor(), actor.NewUnsubscribe(topic)))

	require.Eventually(t, func() bool {
		stats, err := node1.ActorSystem().TopicStats(ctx, topic, 5*time.Second)
		return err == nil && stats.TopicInstanceCount == 2
	}, 15*time.Second, 300*time.Millisecond)

	// node2 leaving the cluster degrades the aggregation gracefully: the
	// remaining nodes silently skip the unreachable peer instead of failing.
	multi.StopNode(ctx, "topic-stats-node-2")

	require.Eventually(t, func() bool {
		stats, err := node1.ActorSystem().TopicStats(ctx, topic, 5*time.Second)
		return err == nil && stats.TopicInstanceCount == 1 && stats.LocalSubscriberCount == 1
	}, 20*time.Second, 300*time.Millisecond)
}
