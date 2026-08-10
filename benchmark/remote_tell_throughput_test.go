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
	"fmt"
	"math/rand/v2"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/internal/address"
	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/remoteclient"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// replicaReceiveCount is incremented each time a replicaActor observes a
// TestSend message. Reset at the start of every run.
var replicaReceiveCount atomic.Int64

type replicaActor struct{}

func (*replicaActor) PreStart(*actor.Context) error { return nil }
func (*replicaActor) PostStop(*actor.Context) error { return nil }

func (*replicaActor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
		replicaReceiveCount.Add(1)
	default:
		ctx.Unhandled()
	}
}

type replicaEngine struct {
	system actor.ActorSystem
	host   string
	port   int
	actors []*address.Address
}

// BenchmarkRemoteTellThroughput: one shared remoting client fans
// fire-and-forget tells over 10 server actor systems (2000 actors each) on
// localhost, 20 concurrent senders for 10s. Reports msgs/sec as the
// receivers observe them (fully drained deliveries credited to the send
// window), the delivered-vs-sent ratio, and how long the receivers took to
// drain after the send window closed; receiver lag shows up in the last two
// instead of silently inflating the first. One benchmark op is one full run:
// invoke with -benchtime=1x.
func BenchmarkRemoteTellThroughput(b *testing.B) {
	const (
		engineCount     = 10
		actorsPerEngine = 2000
		senders         = 20
		duration        = 10 * time.Second
	)

	if b.N != 1 {
		b.Skip("one op is one full 10s run; invoke with -benchtime=1x")
	}

	if runtime.GOMAXPROCS(0) <= 1 {
		b.Skip("requires GOMAXPROCS > 1")
	}

	replicaReceiveCount.Store(0)
	ctx := context.Background()
	host := "127.0.0.1"
	ports := inet.Get(engineCount)

	engines := make([]*replicaEngine, engineCount)
	for i := range engineCount {
		port := ports[i]
		sys, err := actor.NewActorSystem(
			fmt.Sprintf("bench-%d", i),
			actor.WithLogger(log.DiscardLogger),
			actor.WithActorInitMaxRetries(1),
			actor.WithRemote(remote.NewConfig(host, port)),
		)
		if err != nil {
			b.Fatalf("new system %d: %v", i, err)
		}
		if err := sys.Start(ctx); err != nil {
			b.Fatalf("start system %d: %v", i, err)
		}
		engines[i] = &replicaEngine{
			system: sys,
			host:   host,
			port:   port,
			actors: make([]*address.Address, actorsPerEngine),
		}
	}
	b.Cleanup(func() {
		for _, e := range engines {
			_ = e.system.Stop(ctx)
		}
	})

	// Enable send coalescing to match the configuration that actor.setupRemoting
	// applies for production actor-system Tell traffic. The constant mirrors
	// actor.remoteSendCoalescingMaxBatch.
	const remoteSendCoalescingMaxBatch = 256
	client := remoteclient.NewClient(remoteclient.WithSendCoalescing(remoteSendCoalescingMaxBatch))
	b.Cleanup(client.Close)

	for i, e := range engines {
		for j := range actorsPerEngine {
			name := fmt.Sprintf("engine-%d-actor-%d", i, j)
			if _, err := e.system.Spawn(ctx, name, &replicaActor{}); err != nil {
				b.Fatalf("spawn %s: %v", name, err)
			}
			addr, err := client.RemoteLookup(ctx, e.host, e.port, name)
			if err != nil {
				b.Fatalf("lookup %s: %v", name, err)
			}
			e.actors[j] = addr
		}
	}

	b.Logf("spawned %d engines x %d actors = %d actors", engineCount, actorsPerEngine, engineCount*actorsPerEngine)

	var sendCount atomic.Int64
	var sendErrs atomic.Int64

	// Collect the setup garbage (20k spawns and lookups) before the timer so
	// its GC does not spend timed-window CPU.
	runtime.GC()

	// Snapshot process allocation counters so the report can price one
	// message end to end; matches what the harness's B/op and allocs/op
	// measure over the timed window.
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	b.ResetTimer()
	b.ReportAllocs()

	deadline := time.Now().Add(duration)

	// A transport stall must fail the run visibly, not hang it: without a
	// deadline a wedged send path blocks RemoteTell forever and the senders
	// never re-check the clock. The grace covers ordinary backpressure waits.
	sendCtx, cancel := context.WithDeadline(ctx, deadline.Add(30*time.Second))
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(senders)
	for range senders {
		go func() {
			defer wg.Done()
			msg := new(testpb.TestSend)
			for time.Now().Before(deadline) {
				tgt := engines[rand.IntN(engineCount)]         //nolint:gosec
				addr := tgt.actors[rand.IntN(actorsPerEngine)] //nolint:gosec
				if err := client.RemoteTell(sendCtx, address.NoSender(), addr, msg); err != nil {
					sendErrs.Add(1)
					continue
				}
				sendCount.Add(1)
			}
		}()
	}
	wg.Wait()

	// Drain until the receivers caught up with everything sent or delivery
	// progress stops, so delivered/sent measures delivery rather than
	// whatever a fixed sleep happened to catch. Three consecutive quiet
	// polls tolerate a GC pause without cutting a live drain short.
	sent := sendCount.Load()
	drainStart := time.Now()
	lastSeen := replicaReceiveCount.Load()
	quietPolls := 0

	for replicaReceiveCount.Load() < sent && quietPolls < 3 {
		time.Sleep(50 * time.Millisecond)

		seen := replicaReceiveCount.Load()
		if seen == lastSeen {
			quietPolls++
			continue
		}

		lastSeen = seen
		quietPolls = 0
	}
	drainTime := time.Since(drainStart)

	b.StopTimer()

	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)

	received := replicaReceiveCount.Load()
	errs := sendErrs.Load()

	deliveredRatio := 0.0
	bytesPerMsg := uint64(0)
	allocsPerMsg := 0.0

	if sent > 0 {
		deliveredRatio = float64(received) / float64(sent)
	}

	if received > 0 {
		bytesPerMsg = (memAfter.TotalAlloc - memBefore.TotalAlloc) / uint64(received)
		allocsPerMsg = float64(memAfter.Mallocs-memBefore.Mallocs) / float64(received)
	}

	b.ReportMetric(float64(received)/duration.Seconds(), "messages/sec")
	b.ReportMetric(deliveredRatio, "delivered/sent")
	b.ReportMetric(drainTime.Seconds(), "drain-seconds")

	b.Logf("\nreport:\n"+
		"  throughput    %s messages/sec (%d senders over %d systems x %s actors, %s send window)\n"+
		"  delivery      %s sent, %s received, %s errors, ratio %.3f (1.000 = nothing dropped or stranded)\n"+
		"  drain         %s for receivers to catch up after the senders stopped\n"+
		"  per message   %d B, %.1f allocs end to end (serialize, frame, send, receive, decode, deliver)\n"+
		"  reading tips  per-message cost is load-invariant, the rate is not: if B or allocs moved the\n"+
		"                code changed, if only messages/sec moved the machine did (check uptime)",
		humanCount(received/int64(duration.Seconds())),
		senders, engineCount, humanCount(actorsPerEngine),
		duration,
		humanCount(sent), humanCount(received), humanCount(errs), deliveredRatio,
		drainTime.Round(time.Millisecond),
		bytesPerMsg, allocsPerMsg)
}

// humanCount renders n with thousands separators for the benchmark report.
func humanCount(n int64) string {
	s := fmt.Sprintf("%d", n)

	if len(s) <= 3 {
		return s
	}

	var out []byte
	lead := len(s) % 3
	if lead > 0 {
		out = append(out, s[:lead]...)
	}

	for i := lead; i < len(s); i += 3 {
		if len(out) > 0 {
			out = append(out, ',')
		}
		out = append(out, s[i:i+3]...)
	}

	return string(out)
}
