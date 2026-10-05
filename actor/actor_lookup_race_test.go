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

package actor

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/log"
)

// TestActorLookupWhileActorsLeaveTheTree hammers ActorOf, ActorExists and Kill with
// lookups of names whose actors are being stopped and spawned again. Without the
// nil-PID guard a lookup that finds a node just before its PID slot is cleared
// panics in PID.IsStopping.
func TestActorLookupWhileActorsLeaveTheTree(t *testing.T) {
	ctx := context.Background()
	sys, err := NewActorSystem("race-repro", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	t.Cleanup(func() { _ = sys.Stop(ctx) })

	const names = 8
	var (
		stop   atomic.Bool
		wg     sync.WaitGroup
		panics atomic.Int64
		first  atomic.Value
	)
	guard := func() {
		if r := recover(); r != nil {
			panics.Add(1)
			first.CompareAndSwap(nil, fmt.Sprint(r))
		}
	}

	// churn: stop and respawn the same names as fast as possible
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer guard()
			for n := 0; !stop.Load(); n = (n + 1) % names {
				name := fmt.Sprintf("churn-%d", n)
				_, _ = sys.Spawn(ctx, name, NewMockActor())
				_ = sys.Kill(ctx, name)
			}
		}()
	}

	// readers: look the same names up
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer guard()
			for n := 0; !stop.Load(); n = (n + 1) % names {
				name := fmt.Sprintf("churn-%d", n)
				_, _ = sys.ActorOf(ctx, name)
				_, _ = sys.ActorExists(ctx, name)
			}
		}()
	}

	time.Sleep(3 * time.Second)
	stop.Store(true)
	wg.Wait()

	if v := first.Load(); v != nil {
		t.Fatalf("%d panics, first: %v", panics.Load(), v)
	}
}
