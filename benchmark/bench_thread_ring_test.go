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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
)

// threadRingActors is the size of the ring BenchmarkThreadRing passes its
// token around.
const threadRingActors = 100

// BenchmarkThreadRing measures message latency through a long chain of
// actors: one token passed around a ring of threadRingActors actors, one hop
// per message, until it has made b.N hops. Only one actor is runnable at any
// time, so the number is the cost of handing the turn from one actor to the
// next, as in the Savina ThreadRing benchmark.
func BenchmarkThreadRing(b *testing.B) {
	ctx := context.Background()

	actorSystem, err := actor.NewActorSystem("bench",
		actor.WithLogger(log.DiscardLogger),
		actor.WithActorInitMaxRetries(1))
	require.NoError(b, err)
	require.NoError(b, actorSystem.Start(ctx))
	b.Cleanup(func() { _ = actorSystem.Stop(ctx) })

	done := make(chan struct{})
	ring := make([]*actor.PID, threadRingActors)
	for i := len(ring) - 1; i >= 0; i-- {
		var next *actor.PID
		if i < len(ring)-1 {
			next = ring[i+1]
		}

		ring[i], err = actorSystem.Spawn(ctx, "ring-"+strconv.Itoa(i), &ringActor{next: next, done: done})
		require.NoError(b, err)
	}

	// the last actor learns its successor before the token can reach it
	require.NoError(b, actor.Tell(ctx, ring[len(ring)-1], &ringNext{next: ring[0]}))

	b.ReportAllocs()
	b.ResetTimer()

	require.NoError(b, actor.Tell(ctx, ring[0], &ringToken{hops: b.N}))
	<-done

	b.StopTimer()
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "hops/sec")
}
