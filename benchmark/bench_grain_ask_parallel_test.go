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
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/tochemey/goakt/v4/actor"
)

func BenchmarkGrainAskParallel(b *testing.B) {
	ctx := context.Background()
	sys := sharedCluster(b)
	identities := make([]*actor.GrainIdentity, runtime.GOMAXPROCS(0))
	for i := range identities {
		identities[i] = activate(b, sys)
	}
	var next atomic.Int64
	msg := new(Ping)

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		identity := identities[int(next.Add(1)-1)%len(identities)]
		for pb.Next() {
			if _, err := sys.AskGrain(ctx, identity, msg, Timeout); err != nil {
				b.Error(err)
				return
			}
		}
	})
	ReportRate(b, b.N, MsgsPerSec)
}
