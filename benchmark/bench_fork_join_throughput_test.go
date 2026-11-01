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
	"testing"

	"github.com/tochemey/goakt/v4/actor"
)

func BenchmarkForkJoinThroughput(b *testing.B) {
	ctx := context.Background()
	workers := make([]*actor.PID, 0, ForkJoinActors)
	latch := NewLatch(min(ForkJoinActors, b.N))
	for i := range ForkJoinActors {
		if target := Share(b.N, i, ForkJoinActors); target > 0 {
			workers = append(workers, spawn(b, &forkJoinWorker{latch: latch, target: target}))
		}
	}
	msg := new(Ping)

	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		if err := actor.Tell(ctx, workers[i%len(workers)], msg); err != nil {
			b.Fatal(err)
		}
	}
	latch.Wait(b)
	ReportRate(b, b.N, MsgsPerSec)
}
