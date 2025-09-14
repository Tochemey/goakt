/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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

// Metric defines the actor system metric
type Metric struct {
	// DeadlettersCount returns the total number of deadletter
	deadlettersCount int64
	// ActorsCount returns the total number of actors in the system
	actorsCount int64
	// Uptime returns the number of seconds since the actor/system started
	uptime int64
	// memSize returns the total memory of the system in bytes
	memSize uint64
	// memAvail returns the free memory of the system in bytes
	memAvail uint64
	// memUsed returns the used memory of the system in bytes
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

// ActorMetric defines actor specific metrics
type ActorMetric struct { //nolint:revive
	// DeadlettersCount returns the total number of deadletter
	// deadlettersCount returns the total number of deadletter
	deadlettersCount uint64
	// childrenCount returns the total number of child actor given a specific PID
	childrenCount uint64
	// uptime returns the number of seconds since the actor/system started
	uptime int64
	// lastProcessingDuration returns the duration of the latest message processed
	latestProcessedDuration time.Duration
	// restartCount returns the total number of re-starts by the given PID
	restartCount uint64
	// processedCount returns the total number of messages processed at a given time
	processedCount uint64
	// stashSize returns the stash size at a given time
	stashSize uint64
}

// LatestProcessedDuration returns the duration of the latest message processed duration
func (x ActorMetric) LatestProcessedDuration() time.Duration {
	return x.latestProcessedDuration
}

// RestartCount returns the total number of re-starts by the given PID
func (x ActorMetric) RestartCount() uint64 {
	return x.restartCount
}

// DeadlettersCount returns the total number of deadletter
func (x ActorMetric) DeadlettersCount() uint64 {
	return x.deadlettersCount
}

// ChidrenCount returns the total number of child actor given a specific PID
func (x ActorMetric) ChidrenCount() uint64 {
	return x.childrenCount
}

// Uptime returns the number of seconds since the actor/system started
func (x ActorMetric) Uptime() int64 {
	return x.uptime
}

// ProcessedCount returns the total number of messages processed at a given time
func (x ActorMetric) ProcessedCount() uint64 {
	return x.processedCount
}

// StashSize returns the stash size at a given time
func (x ActorMetric) StashSize() uint64 {
	return x.stashSize
}
