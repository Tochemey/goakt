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

package metric

import "go.opentelemetry.io/otel/metric"

// ActorSystemMetric groups OpenTelemetry instruments that describe
// actor‑system health and capacity at a coarse (system) level.
//
// The live actor count, connected peer count, and uptime are point-in-time
// readings that can rise and fall, so they are observable gauges. The
// deadletter total only grows, so it stays an observable counter.
//
// Instruments:
//   - actorsystem.deadletters.count  (Int64ObservableCounter)
//   - actorsystem.actors.count       (Int64ObservableGauge)
//   - actorsystem.peers.count        (Int64ObservableGauge) — optional; see PeersCount
//   - actorsystem.uptime             (Int64ObservableGauge, unit: seconds)
type ActorSystemMetric struct {
	deadlettersCount metric.Int64ObservableCounter
	pidsCount        metric.Int64ObservableGauge
	peersCount       metric.Int64ObservableGauge
	uptime           metric.Int64ObservableGauge
}

// NewActorSystemMetric creates the system‑level instruments using the provided
// Meter. It initializes:
//   - actorsystem.deadletters.count (Int64ObservableCounter)
//   - actorsystem.actors.count      (Int64ObservableGauge)
//   - actorsystem.uptime            (Int64ObservableGauge, unit "s")
//   - actorsystem.peers.count       (Int64ObservableGauge)
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

	if instruments.pidsCount, err = meter.Int64ObservableGauge(
		"actorsystem.actors.count",
		metric.WithDescription("Current number of live PIDs in the actor system"),
	); err != nil {
		return nil, err
	}

	if instruments.uptime, err = meter.Int64ObservableGauge(
		"actorsystem.uptime",
		metric.WithDescription("Uptime of the actor system in seconds"),
		metric.WithUnit("s"),
	); err != nil {
		return nil, err
	}

	if instruments.peersCount, err = meter.Int64ObservableGauge(
		"actorsystem.peers.count",
		metric.WithDescription("Current number of connected peers in the actor system"),
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

// PIDsCount returns the observable gauge that reports the number of live PIDs
// (actors) currently active in the actor system.
//
// Use with Meter.RegisterCallback to observe the current value periodically.
func (x *ActorSystemMetric) PIDsCount() metric.Int64ObservableGauge {
	return x.pidsCount
}

// Uptime returns the observable gauge for the actor system uptime in seconds.
// Use with Meter.RegisterCallback to observe the current value periodically.
func (x *ActorSystemMetric) Uptime() metric.Int64ObservableGauge {
	return x.uptime
}

// PeersCount returns the observable gauge for the number of connected peers
// (e.g., cluster members) in the actor system.
func (x *ActorSystemMetric) PeersCount() metric.Int64ObservableGauge {
	return x.peersCount
}
