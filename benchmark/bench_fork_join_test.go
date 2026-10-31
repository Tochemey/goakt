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
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// forkJoinActors is the number of actors BenchmarkForkJoin spreads its
// messages over.
const forkJoinActors = 60

// BenchmarkForkJoin measures one sender outside the system spreading b.N
// messages round-robin over forkJoinActors actors, timed until every actor
// has handled its share, as in the Savina fork-join throughput benchmark.
// Each message readies an idle actor, so the number is the cost of waking a
// worker for an actor with one message, many times over, with the sender
// never yielding.
func BenchmarkForkJoin(b *testing.B) {
	ctx := context.Background()

	actorSystem, err := actor.NewActorSystem("bench",
		actor.WithLogger(log.DiscardLogger),
		actor.WithActorInitMaxRetries(1))
	require.NoError(b, err)
	require.NoError(b, actorSystem.Start(ctx))
	b.Cleanup(func() { _ = actorSystem.Stop(ctx) })

	workers := make([]*actor.PID, 0, forkJoinActors)
	dones := make([]chan struct{}, 0, forkJoinActors)
	for i := range forkJoinActors {
		target := b.N / forkJoinActors
		if i < b.N%forkJoinActors {
			target++
		}

		if target == 0 {
			continue
		}

		done := make(chan struct{})
		pid, err := actorSystem.Spawn(ctx, "worker-"+strconv.Itoa(i), &forkJoinWorker{target: target, done: done})
		require.NoError(b, err)
		workers = append(workers, pid)
		dones = append(dones, done)
	}

	message := new(testpb.TestSend)

	b.ReportAllocs()
	b.ResetTimer()

	for i := range b.N {
		require.NoError(b, actor.Tell(ctx, workers[i%len(workers)], message))
	}

	for _, done := range dones {
		<-done
	}

	b.StopTimer()
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "messages/sec")
}
