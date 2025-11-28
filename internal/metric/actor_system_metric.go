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

package metric

import "go.opentelemetry.io/otel/metric"

// ActorSystemMetric groups OpenTelemetry instruments that describe
// actor‑system health and capacity at a coarse (system) level.
//
// Instruments:
//   - actorsystem.deadletters.count  (Int64ObservableCounter)
//   - actorsystem.pids.count         (Int64ObservableCounter)
//   - actorsystem.peers.count        (Int64ObservableCounter) — optional; see PeersCount
//   - actorsystem.uptime             (Int64ObservableCounter, unit: seconds)
type ActorSystemMetric struct {
	deadlettersCount metric.Int64ObservableCounter
	pidsCount        metric.Int64ObservableCounter
	peersCount       metric.Int64ObservableCounter
	uptime           metric.Int64ObservableCounter
}

// NewActorSystemMetric creates the system‑level instruments using the provided
// Meter. It initializes:
//   - actorsystem.deadletters.count (Int64ObservableCounter)
//   - actorsystem.pids.count        (Int64ObservableCounter)
//   - actorsystem.uptime            (Float64Histogram, unit "s")
//
// It returns an error if any instrument cannot be created so telemetry
// initialization failures are surfaced early.
func NewActorSystemMetric(meter metric.Meter) (*ActorSystemMetric, error) {
	var instruments ActorSystemMetric
	var err error

	if instruments.deadlettersCount, err = meter.Int64ObservableCounter(
		"actorsystem.deadletters.count",
		metric.WithDescription("Total number of deadletters in the actor system"),
	); err != nil {
		return nil, err
	}

	if instruments.pidsCount, err = meter.Int64ObservableCounter(
		"actorsystem.actors.count",
		metric.WithDescription("Total number of PIDs in the actor system"),
	); err != nil {
		return nil, err
	}

	if instruments.uptime, err = meter.Int64ObservableCounter(
		"actorsystem.uptime",
		metric.WithDescription("Uptime of the actor system in seconds"),
		metric.WithUnit("s"),
	); err != nil {
		return nil, err
	}

	if instruments.peersCount, err = meter.Int64ObservableCounter(
		"actorsystem.peers.count",
		metric.WithDescription("Total number of connected peers in the actor system"),
	); err != nil {
		return nil, err
	}

	return &instruments, nil
}

// DeadlettersCount returns the observable counter that tracks how many messages
// have been dropped to deadletters across the actor system.
//
// Use with Meter.RegisterCallback to observe the current value periodically.
func (x *ActorSystemMetric) DeadlettersCount() metric.Int64ObservableCounter {
	return x.deadlettersCount
}

// PIDsCount returns the observable counter that reports the total number of
// live PIDs (actors) currently active in the actor system.
//
// Use with Meter.RegisterCallback to observe the current value periodically.
func (x *ActorSystemMetric) PIDsCount() metric.Int64ObservableCounter {
	return x.pidsCount
}

// Uptime returns the histogram used to record the actor system uptime in
// seconds. Record deltas or absolute uptime as appropriate for your setup.
func (x *ActorSystemMetric) Uptime() metric.Int64ObservableCounter {
	return x.uptime
}

// PeersCount returns the observable counter for the number of connected peers
// (e.g., cluster members) in the actor system.
func (x *ActorSystemMetric) PeersCount() metric.Int64ObservableCounter {
	return x.peersCount
}
