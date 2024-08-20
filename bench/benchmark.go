/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package bench

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v2/actors"
	"github.com/tochemey/goakt/v2/bench/benchmarkpb"
	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/log"
)

const receivingTimeout = 100 * time.Millisecond

var (
	totalSent *atomic.Int64
	totalRecv *atomic.Int64
)

type Benchmarker struct {
}

func (p *Benchmarker) PreStart(context.Context) error {
	return nil
}

func (p *Benchmarker) Receive(ctx actors.ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *benchmarkpb.BenchTell:
		totalRecv.Inc()
	case *benchmarkpb.BenchRequest:
		totalRecv.Inc()
		ctx.Response(&benchmarkpb.BenchResponse{})
	default:
		ctx.Unhandled()
	}
}

func (p *Benchmarker) PostStop(context.Context) error {
	return nil
}

// Benchmark defines a load testing engine
type Benchmark struct {
	// actorsCount defines the number of actors to create
	// on each actor system created by the loader
	actorsCount int
	// workersCount define the number of message senders
	workersCount int
	// duration specifies how long the load testing will run
	duration time.Duration
	pids     []actors.PID
	system   actors.ActorSystem
}

// NewBenchmark creates an instance of Loader
func NewBenchmark(actorsCount, workersCount int, duration time.Duration) *Benchmark {
	return &Benchmark{
		actorsCount:  actorsCount,
		workersCount: workersCount,
		duration:     duration,
		pids:         make([]actors.PID, 0, actorsCount),
	}
}

// Start starts the Benchmark
func (b *Benchmark) Start(ctx context.Context) error {
	// create the benchmark actor system
	name := "benchmark-system"
	b.system, _ = actors.NewActorSystem(name,
		actors.WithLogger(log.DiscardLogger),
		actors.WithActorInitMaxRetries(1),
		actors.WithSupervisorDirective(actors.NewStopDirective()),
		actors.WithReplyTimeout(receivingTimeout))

	if err := b.system.Start(ctx); err != nil {
		return err
	}

	// wait for the actor system to properly start
	time.Sleep(time.Second)

	for i := 0; i < b.actorsCount; i++ {
		actorName := fmt.Sprintf("actor-%d", i)
		pid, err := b.system.Spawn(ctx, actorName, &Benchmarker{})
		if err != nil {
			return err
		}
		b.pids = append(b.pids, pid)
	}
	// wait for the actors to properly start
	time.Sleep(time.Second)
	return nil
}

// Stop stops the benchmark
func (b *Benchmark) Stop(ctx context.Context) error {
	return b.system.Stop(ctx)
}

// BenchTell sends messages to a random actor
func (b *Benchmark) BenchTell(ctx context.Context) error {
	wg := sync.WaitGroup{}
	wg.Add(b.workersCount)
	deadline := time.Now().Add(b.duration)
	for i := 0; i < b.workersCount; i++ {
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				// randomly pick and actor
				pid := b.pids[rand.IntN(len(b.pids))] //nolint:gosec
				// send a message
				_ = actors.Tell(ctx, pid, new(benchmarkpb.BenchTell))
				// increase sent counter
				totalSent.Inc()
			}
		}()
	}

	// wait for the messages to be delivered
	wg.Wait()
	time.Sleep(time.Minute)
	if totalSent.Load() != totalRecv.Load() {
		return fmt.Errorf("send count and receive count does not match: %d != %d", totalSent.Load(), totalRecv.Load())
	}
	return nil
}

// BenchAsk sends messages to a random actor
func (b *Benchmark) BenchAsk(ctx context.Context) error {
	wg := sync.WaitGroup{}
	wg.Add(b.workersCount)
	deadline := time.Now().Add(b.duration)
	for i := 0; i < b.workersCount; i++ {
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				// randomly pick and actor
				pid := b.pids[rand.IntN(len(b.pids))] //nolint:gosec
				// send a message
				_, _ = actors.Ask(ctx, pid, new(benchmarkpb.BenchRequest), receivingTimeout)
				// increase sent counter
				totalSent.Add(1)
			}
		}()
	}

	// wait for the messages to be delivered
	wg.Wait()
	time.Sleep(500 * time.Millisecond)
	if totalSent.Load() != totalRecv.Load() {
		return fmt.Errorf("send count and receive count does not match: %d != %d", totalSent.Load(), totalRecv.Load())
	}
	return nil
}
