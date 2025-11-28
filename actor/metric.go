/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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

package actor

import "time"

// Metric is an immutable snapshot of node-level statistics returned by ActorSystem.Metric.
//
// The values are gathered locally (not aggregated across a cluster) and represent a single
// point in time. Use it to drive dashboards, health checks, or lightweight telemetry without
// wiring up OpenTelemetry. Callers typically fetch a Metric just before inspecting fields;
// values will not update after the struct is created.
type Metric struct {
	// DeadlettersCount returns the total number of messages dropped to deadletters on this node.
	deadlettersCount int64
	// ActorsCount returns the total number of live actors on this node.
	actorsCount int64
	// Uptime returns the number of seconds since the actor system on this node started.
	uptime int64
	// memSize is the total system memory in bytes, as reported by the host.
	memSize uint64
	// memAvail is the currently available memory in bytes.
	memAvail uint64
	// memUsed is the currently used memory in bytes.
	memUsed uint64
}

// MemoryUsed returns the used memory of the system in bytes
func (m Metric) MemoryUsed() uint64 {
	return m.memUsed
}

// MemorySize returns the total memory of the system in bytes
func (m Metric) MemorySize() uint64 {
	return m.memSize
}

// MemoryAvailable returns the free memory of the system in bytes
func (m Metric) MemoryAvailable() uint64 {
	return m.memAvail
}

// DeadlettersCount returns the total number of deadletter
func (m Metric) DeadlettersCount() int64 {
	return m.deadlettersCount
}

// ActorsCount returns the total number of actors either in the system
// or the total number of child actor given a specific PID
func (m Metric) ActorsCount() int64 {
	return m.actorsCount
}

// Uptime returns the number of seconds since the actor/system started
func (m Metric) Uptime() int64 {
	return m.uptime
}

// ActorMetric is a point-in-time view of a single actor’s counters and timings.
//
// It is returned by PID.Metric(ctx) and reflects only the local actor state; no cluster
// aggregation is performed. Metrics are captured at the moment Metric is called and will
// not update afterwards. If the actor is not running when requested, PID.Metric returns nil.
type ActorMetric struct { //nolint:revive
	// deadlettersCount is the total number of messages this actor sent to deadletters.
	deadlettersCount uint64
	// childrenCount is the total number of direct child actors for this actor.
	childrenCount uint64
	// uptime is the number of seconds this actor has been alive (resets after restarts).
	uptime int64
	// latestProcessedDuration is the duration of the most recent message processing.
	latestProcessedDuration time.Duration
	// restartCount is the total number of restarts the actor has undergone.
	restartCount uint64
	// processedCount is the total number of messages processed so far.
	processedCount uint64
	// stashSize is the current number of stashed messages.
	stashSize uint64
	// failureCount is the total number of failures observed for this actor.
	failureCount uint64
	// reinstateCount is the total number of reinstatements (suspended -> resumed transitions).
	reinstateCount uint64
}

// LatestProcessedDuration returns the duration of the latest message processing.
// Unit: time.Duration (nanoseconds). Useful for latency observations on the last handled message.
func (x ActorMetric) LatestProcessedDuration() time.Duration {
	return x.latestProcessedDuration
}

// RestartCount returns the cumulative number of restarts for this actor (PID).
// Increments when supervision restarts the actor. Does not include normal stops.
func (x ActorMetric) RestartCount() uint64 {
	return x.restartCount
}

// DeadlettersCount returns the total number of messages sent to deadletters by this actor.
// Indicates local delivery failures or unhandled messages.
func (x ActorMetric) DeadlettersCount() uint64 {
	return x.deadlettersCount
}

// ChidrenCount returns the current number of child actors owned by this actor.
// Represents the actor’s immediate descendants (not a transitive count).
func (x ActorMetric) ChidrenCount() uint64 {
	return x.childrenCount
}

// Uptime returns the elapsed time this actor has been alive, in seconds.
// Resets to zero on actor restart.
func (x ActorMetric) Uptime() int64 {
	return x.uptime
}

// ProcessedCount returns the cumulative number of messages this actor has processed.
// Increments after successful handling; excludes stashed or dropped messages.
func (x ActorMetric) ProcessedCount() uint64 {
	return x.processedCount
}

// StashSize returns the current number of messages stashed by this actor.
// Fluctuates as messages are stashed/unstashed under behaviors like become/unbecome.
func (x ActorMetric) StashSize() uint64 {
	return x.stashSize
}

// FailureCount returns the cumulative number of failures observed by this actor.
// Typically increments on panics or errors that trigger supervision actions.
func (x ActorMetric) FailureCount() uint64 {
	return x.failureCount
}

// ReinstateCount returns the cumulative number of reinstatements for this actor.
// A reinstatement occurs when an actor transitions from suspended to resumed state.
func (x ActorMetric) ReinstateCount() uint64 {
	return x.reinstateCount
}
