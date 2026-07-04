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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// TestPubSubBridgeCrossNode verifies that ActorSystem.SubscribeTopic (the non-actor
// pub/sub bridge) receives messages published on a different cluster node, reusing
// the same cross-node dissemination path as actor-to-actor pub/sub.
func TestPubSubBridgeCrossNode(t *testing.T) {
	ctx := context.Background()
	multi := NewMultiNodes(t, log.DiscardLogger, []actor.Actor{&pinger{}}, nil)
	multi.Start()
	t.Cleanup(multi.Stop)

	node1 := multi.StartNode(ctx, "node-1")
	node2 := multi.StartNode(ctx, "node-2")

	topic := "cross-node-topic"

	var mu sync.Mutex
	var received []*testpb.TestLog

	sub, err := node1.ActorSystem().SubscribeTopic(topic, func(_ context.Context, message proto.Message) {
		if msg, ok := message.(*testpb.TestLog); ok {
			mu.Lock()
			received = append(received, msg)
			mu.Unlock()
		}
	})
	require.NoError(t, err)
	require.NotNil(t, sub)
	require.Equal(t, topic, sub.Topic())
	t.Cleanup(func() { _ = sub.Close() })

	publisher, err := node2.ActorSystem().Spawn(ctx, "publisher", &pinger{})
	require.NoError(t, err)

	message := actor.NewPublish("cross-node-message-1", topic, &testpb.TestLog{Text: "hello from node-2"})
	require.NoError(t, publisher.Tell(ctx, node2.ActorSystem().TopicActor(), message))

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(received) == 1
	}, 10*time.Second, 100*time.Millisecond)

	mu.Lock()
	require.Equal(t, "hello from node-2", received[0].GetText())
	mu.Unlock()

	// unsubscribing stops further delivery to this node's bridge
	require.NoError(t, sub.Close())

	message2 := actor.NewPublish("cross-node-message-2", topic, &testpb.TestLog{Text: "should not arrive"})
	require.NoError(t, publisher.Tell(ctx, node2.ActorSystem().TopicActor(), message2))

	pause.For(2 * time.Second)

	mu.Lock()
	require.Len(t, received, 1)
	mu.Unlock()
}
