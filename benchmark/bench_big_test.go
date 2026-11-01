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

func BenchmarkBig(b *testing.B) {
	ctx := context.Background()
	rounds := max(b.N/BigActors, 1)
	exchanges := rounds * BigActors
	latch := NewLatch(BigActors)
	peers := make([]*actor.PID, BigActors)
	actors := make([]*bigActor, BigActors)
	for i := range peers {
		actors[i] = &bigActor{index: i, peers: peers, rounds: rounds, latch: latch, ping: new(Ping), pong: new(Pong)}
		peers[i] = spawn(b, actors[i])
	}

	b.ReportAllocs()
	b.ResetTimer()
	for _, pid := range peers {
		if err := actor.Tell(ctx, pid, new(BigRound)); err != nil {
			b.Fatal(err)
		}
	}
	latch.Wait(b)
	ReportRate(b, 2*exchanges, MsgsPerSec)
}
