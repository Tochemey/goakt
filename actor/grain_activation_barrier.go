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
	"sync"
	"time"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/types"
)

// grainActivationBarrier is a startup coordination gate that delays grain activation
// until the cluster is considered ready.
//
// Role:
//   - Prevents rare "split-brain" activations during cluster bootstrap, where multiple
//     nodes can concurrently activate the same grain before membership converges.
//   - Provides a deterministic point after which activation proceeds normally with no
//     additional coordination overhead.
//
// When it is needed:
//   - Clustered deployments where early requests can arrive before all peers are visible.
//   - Workloads with side-effectful grains (e.g., external registrations, timers, or
//     state initialization) where duplicate startup activations are unacceptable.
//   - Scenarios using random/least-load activation strategies during bootstrap.
//
// When it is not needed:
//   - Single-node deployments or clusters that do not receive grain traffic until
//     membership is stable.
//   - Systems where duplicate early activations are acceptable or idempotent.
//
// Behavior:
//   - The barrier opens once the minimum peers quorum is reached.
//   - If a timeout is configured, activation attempts will return an error when the
//     timeout elapses without readiness.
//   - After opening, checks are effectively free (a closed channel read).
type grainActivationBarrier struct {
	enabled  bool
	minPeers uint32
	timeout  time.Duration
	ready    chan types.Unit
	once     sync.Once
}

func newGrainActivationBarrier(minPeers uint32, timeout time.Duration) *grainActivationBarrier {
	if minPeers == 0 {
		minPeers = 1
	}
	return &grainActivationBarrier{
		enabled:  true,
		minPeers: minPeers,
		timeout:  timeout,
		ready:    make(chan types.Unit),
	}
}

func (b *grainActivationBarrier) open() {
	if b == nil || !b.enabled {
		return
	}
	b.once.Do(func() { close(b.ready) })
}

func (b *grainActivationBarrier) wait(ctx context.Context) error {
	if b == nil || !b.enabled {
		return nil
	}

	select {
	case <-b.ready:
		return nil
	default:
	}

	if b.timeout > 0 {
		timer := time.NewTimer(b.timeout)
		defer timer.Stop()
		select {
		case <-b.ready:
			return nil
		case <-timer.C:
			return gerrors.ErrGrainActivationBarrierTimeout
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	select {
	case <-b.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
