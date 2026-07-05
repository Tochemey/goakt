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
// delivers a given tick of a cluster-wide cron schedule (see actor.ScheduleWithCron).
const namespaceScheduleFire recordNamespace = "schedule-fire"

// ErrScheduleFireClaimed is returned by ClaimScheduleFire when another node has already
// won the race for the given key.
var ErrScheduleFireClaimed = errors.New("schedule fire already claimed")

// SupportsScheduleFireClaim reports whether cl can atomically arbitrate a cluster-wide
// schedule-fire claim. Only the builtin engine exposes the NX+EX primitive this needs; other
// Cluster implementations have no safe substitute, so callers must check this before relying
// on ClaimScheduleFire (see errors.ErrSingleFireUnsupported).
func SupportsScheduleFireClaim(cl Cluster) bool {
	_, ok := cl.(*cluster)
	return ok
}

// ClaimScheduleFire attempts to claim the exclusive right to deliver one trigger tick of a
// cluster-wide cron schedule. It uses the same atomic put-if-absent primitive that
// PutGrainIfAbsent uses to guarantee single grain activation across the cluster.
//
// key uniquely identifies the (schedule reference, trigger tick) pair being arbitrated: callers
// are expected to derive it from a value that is identical across every node racing for the same
// tick (e.g. the trigger's deterministic next-fire timestamp), so a fresh key is used per tick.
//
// ttl must exceed the worst-case clock/scheduling spread between nodes racing for the same
// tick, or a slower node could win the claim for what is effectively the following tick,
// causing that tick to be skipped cluster-wide. Scaling ttl to (at least) the cron period is
// the safe default: a node lagging by more than the period is indistinguishable from having
// missed the tick and moved on to the next one, so no correctness is lost by that clamp either.
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

	c, ok := cl.(*cluster)
	if !ok {
		// Callers must gate on SupportsScheduleFireClaim before scheduling; reaching this
		// means that guard was skipped, so fail loudly instead of silently degrading.
		return fmt.Errorf("cluster implementation does not support schedule-fire claims")
	}
	return c.claimScheduleFire(ctx, key, ttl)
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

// RemoveScheduleFire best-effort deletes a previously claimed schedule-fire key so it does not
// linger in the store until its TTL expires. It is safe to call even if the key was never
// claimed or the cluster implementation does not support schedule-fire claims at all.
func RemoveScheduleFire(ctx context.Context, cl Cluster, key string) error {
	if cl == nil || key == "" {
		return nil
	}

	c, ok := cl.(*cluster)
	if !ok {
		return nil
	}
	return c.removeScheduleFire(ctx, key)
}

// removeScheduleFire deletes a schedule-fire claim entry from the built-in cluster's store.
func (x *cluster) removeScheduleFire(ctx context.Context, key string) error {
	if !x.running.Load() {
		return ErrEngineNotRunning
	}

	x.mu.Lock()
	defer x.mu.Unlock()

	return x.deleteRecord(ctx, namespaceScheduleFire, key)
}
