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
	system  actor.ActorSystem
	host    string
	port    int
	actors  []*address.Address
	targets []*replicaEngine
}

// BenchmarkRemoteTellThroughput: 10 actor systems,
// 2000 actors per system, 20 fire-and-forget senders for 10s over the remote
// transport on localhost. Reports msgs/sec as the receiver sees them, plus
// the delivered-vs-sent ratio so backlog growth on the receiver mailboxes is
// visible in the result line.
func BenchmarkRemoteTellThroughput(b *testing.B) {
	const (
		engineCount     = 10
		actorsPerEngine = 2000
		senders         = 20
		duration        = 10 * time.Second
		basePort        = 4500
	)

	if runtime.GOMAXPROCS(0) <= 1 {
		b.Skip("requires GOMAXPROCS > 1")
	}

	replicaReceiveCount.Store(0)
	ctx := context.Background()
	host := "127.0.0.1"

	engines := make([]*replicaEngine, engineCount)
	for i := range engineCount {
		port := basePort + i
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

	for i, e := range engines {
		for j, other := range engines {
			if i != j {
				e.targets = append(e.targets, other)
			}
		}
	}

	b.Logf("spawned %d engines x %d actors = %d actors", engineCount, actorsPerEngine, engineCount*actorsPerEngine)

	var sendCount atomic.Int64
	var sendErrs atomic.Int64

	b.ResetTimer()
	b.ReportAllocs()

	var wg sync.WaitGroup
	deadline := time.Now().Add(duration)
	wg.Add(senders)
	for range senders {
		go func() {
			defer wg.Done()
			msg := new(testpb.TestSend)
			for time.Now().Before(deadline) {
				src := engines[rand.IntN(engineCount)]          //nolint:gosec
				tgt := src.targets[rand.IntN(len(src.targets))] //nolint:gosec
				addr := tgt.actors[rand.IntN(actorsPerEngine)]  //nolint:gosec
				if err := client.RemoteTell(ctx, address.NoSender(), addr, msg); err != nil {
					sendErrs.Add(1)
					continue
				}
				sendCount.Add(1)
			}
		}()
	}
	wg.Wait()
	time.Sleep(time.Second)

	b.StopTimer()

	sent := sendCount.Load()
	received := replicaReceiveCount.Load()
	errs := sendErrs.Load()

	deliveredRatio := 0.0
	if sent > 0 {
		deliveredRatio = float64(received) / float64(sent)
	}

	b.ReportMetric(float64(received)/duration.Seconds(), "messages/sec")
	b.ReportMetric(deliveredRatio, "delivered/sent")
	b.Logf("senders=%d sent=%d received=%d errors=%d delivered_ratio=%.3f duration=%s",
		senders, sent, received, errs, deliveredRatio, duration)
}
