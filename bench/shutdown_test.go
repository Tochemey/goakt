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

package bench

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/actor"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

// BenchmarkShutdownLatency_100Actors measures shutdown latency with 100 actors
func BenchmarkShutdownLatency_100Actors(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping shutdown benchmark in short mode")
	}

	benchmarkShutdownLatency(b, 100, 0)
}

// BenchmarkShutdownLatency_1000Actors measures shutdown latency with 1,000 actors
func BenchmarkShutdownLatency_1000Actors(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping shutdown benchmark in short mode")
	}

	benchmarkShutdownLatency(b, 1000, 0)
}

// BenchmarkShutdownLatency_10000Actors measures shutdown latency with 10,000 actors
func BenchmarkShutdownLatency_10000Actors(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping shutdown benchmark in short mode")
	}

	benchmarkShutdownLatency(b, 10000, 0)
}

// BenchmarkShutdownLatency_100Grains measures shutdown latency with 100 grains
func BenchmarkShutdownLatency_100Grains(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping shutdown benchmark in short mode")
	}

	benchmarkShutdownLatency(b, 0, 100)
}

// BenchmarkShutdownLatency_1000Grains measures shutdown latency with 1,000 grains
func BenchmarkShutdownLatency_1000Grains(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping shutdown benchmark in short mode")
	}

	benchmarkShutdownLatency(b, 0, 1000)
}

// BenchmarkShutdownLatency_Mixed measures shutdown latency with 500 actors and 500 grains
func BenchmarkShutdownLatency_Mixed(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping shutdown benchmark in short mode")
	}

	benchmarkShutdownLatency(b, 500, 500)
}

// benchmarkShutdownLatency is a helper function that creates actors/grains and measures shutdown time
func benchmarkShutdownLatency(b *testing.B, actorCount, grainCount int) {
	ctx := context.Background()

	// Create and start the actor system
	system, err := actor.NewActorSystem("shutdown-bench",
		actor.WithLogger(log.DiscardLogger),
		actor.WithActorInitMaxRetries(1))
	require.NoError(b, err)
	require.NoError(b, system.Start(ctx))

	// Create actors
	pids := make([]*actor.PID, 0, actorCount)
	for i := 0; i < actorCount; i++ {
		pid, err := system.Spawn(ctx, benchActorName(i), new(noopActor))
		require.NoError(b, err)
		pids = append(pids, pid)
	}

	// Create grains
	identities := make([]*actor.GrainIdentity, 0, grainCount)
	for i := 0; i < grainCount; i++ {
		identity, err := system.GrainIdentity(ctx, benchGrainName(i), func(_ context.Context) (actor.Grain, error) {
			return newMockGrain(), nil
		})
		require.NoError(b, err)
		identities = append(identities, identity)

		// Activate the grain by sending a message
		_, err = system.AskGrain(ctx, identity, new(testpb.TestReply), time.Second)
		require.NoError(b, err)
	}

	// Reset timer before measuring shutdown
	b.ResetTimer()

	// Measure shutdown time
	start := time.Now()
	err = system.Stop(ctx)
	shutdownDuration := time.Since(start)

	require.NoError(b, err)

	// Report metrics
	b.ReportMetric(float64(shutdownDuration.Nanoseconds())/1e6, "ms/shutdown")
	b.ReportMetric(float64(shutdownDuration.Nanoseconds())/float64(actorCount+grainCount), "ns/entity")

	// Log summary
	if actorCount > 0 && grainCount > 0 {
		fmt.Printf("\nShutdown: Actors=%d Grains=%d Duration=%v (%.2f ms)\n",
			actorCount, grainCount, shutdownDuration, float64(shutdownDuration.Nanoseconds())/1e6)
	} else if actorCount > 0 {
		fmt.Printf("\nShutdown: Actors=%d Duration=%v (%.2f ms)\n",
			actorCount, shutdownDuration, float64(shutdownDuration.Nanoseconds())/1e6)
	} else {
		fmt.Printf("\nShutdown: Grains=%d Duration=%v (%.2f ms)\n",
			grainCount, shutdownDuration, float64(shutdownDuration.Nanoseconds())/1e6)
	}
}

func benchGrainName(i int) string {
	return "bench-grain-" + strconv.Itoa(i)
}

// mockGrain is a simple grain implementation for benchmarking
type mockGrain struct{}

func newMockGrain() *mockGrain {
	return &mockGrain{}
}

func (m *mockGrain) OnActivate(ctx context.Context, props *actor.GrainProps) error {
	return nil
}

func (m *mockGrain) OnDeactivate(ctx context.Context, props *actor.GrainProps) error {
	return nil
}

func (m *mockGrain) OnReceive(ctx *actor.GrainContext) {
	switch ctx.Message().(type) {
	case *testpb.TestReply:
		ctx.Response(&testpb.Reply{Content: "reply"})
	default:
		ctx.Unhandled()
	}
}
