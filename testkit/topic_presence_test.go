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

// TestTopicPresence exercises TopicSubscriberCount, TopicSubscribers and Topics
// across a real cluster: subscribers spread over different nodes, an
// unsubscribe on one node, and a node leaving the cluster altogether.
func TestTopicPresence(t *testing.T) {
	ctx := context.Background()

	multi := NewMultiNodes(t, log.DiscardLogger, []actor.Actor{&pinger{}}, nil)
	multi.Start()
	t.Cleanup(multi.Stop)

	node1 := multi.StartNode(ctx, "topic-presence-node-1")
	node2 := multi.StartNode(ctx, "topic-presence-node-2")
	node3 := multi.StartNode(ctx, "topic-presence-node-3")

	// give the cluster time to settle before subscribing
	pause.For(2 * time.Second)

	const topic = "presence-topic"

	node1.Spawn(ctx, "subscriber-1", &pinger{})
	node2.Spawn(ctx, "subscriber-2", &pinger{})
	node3.Spawn(ctx, "subscriber-3", &pinger{})

	sub1, err := node1.ActorSystem().ActorOf(ctx, "subscriber-1")
	require.NoError(t, err)
	sub2, err := node2.ActorSystem().ActorOf(ctx, "subscriber-2")
	require.NoError(t, err)
	sub3, err := node3.ActorSystem().ActorOf(ctx, "subscriber-3")
	require.NoError(t, err)

	require.NoError(t, sub1.Tell(ctx, node1.ActorSystem().TopicActor(), actor.NewSubscribe(topic)))
	require.NoError(t, sub2.Tell(ctx, node2.ActorSystem().TopicActor(), actor.NewSubscribe(topic)))
	require.NoError(t, sub3.Tell(ctx, node3.ActorSystem().TopicActor(), actor.NewSubscribe(topic)))

	// the count and membership are visible cluster-wide regardless of which
	// node the query is issued from, once all three subscriptions land.
	require.Eventually(t, func() bool {
		count, err := node1.ActorSystem().TopicSubscriberCount(ctx, topic, 5*time.Second)
		return err == nil && count == 3
	}, 15*time.Second, 300*time.Millisecond)

	subscribers, err := node2.ActorSystem().TopicSubscribers(ctx, topic, 5*time.Second)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{sub1.ID(), sub2.ID(), sub3.ID()}, subscribers)

	topics, err := node3.ActorSystem().Topics(ctx, 5*time.Second)
	require.NoError(t, err)
	require.Equal(t, []string{topic}, topics)

	// unsubscribing on one node is reflected in the cluster-wide count queried
	// from a different node.
	require.NoError(t, sub2.Tell(ctx, node2.ActorSystem().TopicActor(), actor.NewUnsubscribe(topic)))

	require.Eventually(t, func() bool {
		subs, err := node1.ActorSystem().TopicSubscribers(ctx, topic, 5*time.Second)
		return err == nil && len(subs) == 2
	}, 15*time.Second, 300*time.Millisecond)

	subscribers, err = node1.ActorSystem().TopicSubscribers(ctx, topic, 5*time.Second)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{sub1.ID(), sub3.ID()}, subscribers)

	// node3 leaving the cluster removes its subscriber from the aggregated view.
	multi.StopNode(ctx, "topic-presence-node-3")

	require.Eventually(t, func() bool {
		subs, err := node1.ActorSystem().TopicSubscribers(ctx, topic, 5*time.Second)
		return err == nil && len(subs) == 1 && subs[0] == sub1.ID()
	}, 20*time.Second, 300*time.Millisecond)
}
