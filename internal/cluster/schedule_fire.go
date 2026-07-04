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

package cluster

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tochemey/olric"
)

// namespaceScheduleFire stores the short-lived fire claims used to arbitrate which node
// delivers a given tick of a cluster-single-fire schedule (see actor.WithClusterSingleFire).
const namespaceScheduleFire recordNamespace = "schedule-fire"

// ErrScheduleFireClaimed is returned by ClaimScheduleFire when another node has already
// won the race for the given key.
var ErrScheduleFireClaimed = errors.New("schedule fire already claimed")

// ClaimScheduleFire attempts to claim the exclusive right to deliver one trigger tick of a
// cluster-single-fire schedule. It uses the same atomic put-if-absent primitive that
// PutGrainIfAbsent uses to guarantee single grain activation across the cluster.
//
// key uniquely identifies the (schedule reference, trigger tick) pair being arbitrated: callers
// are expected to derive it from a value that is identical across every node racing for the same
// tick (e.g. the trigger's deterministic next-fire timestamp), so a fresh key is used per tick.
// ttl bounds how long the claim entry survives in the cluster store; it exists purely to keep
// the store from growing unbounded over the lifetime of a long-running schedule; correctness
// never depends on it, since every tick already claims a distinct key.
//
// Returns nil for the caller that wins the race for key (it must proceed to deliver), and
// ErrScheduleFireClaimed for every other caller racing for the same key (it must skip delivery
// for that tick silently).
func ClaimScheduleFire(ctx context.Context, cl Cluster, key string, ttl time.Duration) error {
	if cl == nil {
		return errors.New("cluster is nil")
	}
	if key == "" {
		return fmt.Errorf("schedule fire key is empty")
	}

	// Fast path for the built-in implementation: a single atomic NX+EX write on the
	// unified map.
	if c, ok := cl.(*cluster); ok {
		return c.claimScheduleFire(ctx, key, ttl)
	}

	// Best-effort fallback for other Cluster implementations, built only from the
	// generic job-key primitives the interface already exposes. It is still subject to
	// races (no atomic NX there) and cannot expire the claim on its own; each tick using
	// a fresh key still prevents unbounded skew from a stuck claim.
	jobID := string(namespaceScheduleFire) + namespaceSeparator + key
	if _, err := cl.JobKey(ctx, jobID); err == nil {
		return ErrScheduleFireClaimed
	}
	return cl.PutJobKey(ctx, jobID, []byte{1})
}

// claimScheduleFire performs the atomic NX+EX write backing ClaimScheduleFire for the
// built-in cluster implementation.
func (x *cluster) claimScheduleFire(ctx context.Context, key string, ttl time.Duration) error {
	if !x.running.Load() {
		return ErrEngineNotRunning
	}

	x.mu.Lock()
	defer x.mu.Unlock()

	ctx = context.WithoutCancel(ctx)
	ctx, cancel := context.WithTimeout(ctx, x.writeTimeout)
	defer cancel()

	err := x.dmap.Put(ctx, composeKey(namespaceScheduleFire, key), []byte{1}, olric.NX(), olric.EX(ttl))
	if err != nil {
		if errors.Is(err, olric.ErrKeyFound) {
			return ErrScheduleFireClaimed
		}
		return err
	}
	return nil
}
