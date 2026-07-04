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

package benchmark

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// BenchmarkPubSubBridgeDelivery measures end-to-end throughput of the
// non-actor pub/sub bridge (ActorSystem.SubscribeTopic): a Publish sent to
// the topic actor, forwarded to the bridge's internal subscriber actor, and
// delivered to a plain callback. It is the SubscribeTopic counterpart of
// BenchmarkTell — same dispatch path plus one extra hop through topicActor.
func BenchmarkPubSubBridgeDelivery(b *testing.B) {
	ctx := context.Background()

	actorSystem, err := actor.NewActorSystem("bench",
		actor.WithLogger(log.DiscardLogger),
		actor.WithActorInitMaxRetries(1),
		actor.WithPubSub())
	if err != nil {
		b.Fatalf("failed to create actor system: %v", err)
	}
	if err := actorSystem.Start(ctx); err != nil {
		b.Fatalf("failed to start actor system: %v", err)
	}
	b.Cleanup(func() { _ = actorSystem.Stop(ctx) })

	const topic = "bench-topic"

	var received int64
	target := int64(b.N)
	done := make(chan struct{})

	sub, err := actorSystem.SubscribeTopic(topic, func(context.Context, proto.Message) {
		if atomic.AddInt64(&received, 1) == target {
			close(done)
		}
	})
	if err != nil {
		b.Fatalf("failed to subscribe to topic: %v", err)
	}
	b.Cleanup(func() { _ = sub.Close() })

	publisher, err := actorSystem.Spawn(ctx, "publisher", new(Actor))
	if err != nil {
		b.Fatalf("failed to spawn publisher: %v", err)
	}

	var seq int64

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			id := strconv.FormatInt(atomic.AddInt64(&seq, 1), 10)
			message := actor.NewPublish(id, topic, new(testpb.TestCount))
			if err := publisher.Tell(ctx, actorSystem.TopicActor(), message); err != nil {
				b.Fatal(err)
			}
		}
	})
	<-done
	b.StopTimer()
	messagesPerSec := float64(b.N) / b.Elapsed().Seconds()
	b.ReportMetric(messagesPerSec, "messages/sec")
}
