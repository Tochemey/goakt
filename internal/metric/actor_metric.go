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

import (
	"fmt"

	"go.opentelemetry.io/otel/metric"
)

// ActorMetric groups OpenTelemetry instruments that describe the health,
// throughput, and lifecycle of a single PID (actor).
//
// Included instruments:
//   - pid.children.count             (Int64ObservableCounter)
//   - pid.stash.size                 (Int64ObservableCounter)
//   - pid.deadletters.count          (Int64ObservableCounter)
//   - pid.restart.count              (Int64ObservableCounter)
//   - pid.last.received.duration     (Int64ObservableCounter, unit: ms)
//   - pid.processed.count            (Int64ObservableCounter)
//   - pid.uptime                     (Int64ObservableCounter, unit: s)
type ActorMetric struct {
	deadlettersCount     metric.Int64ObservableCounter
	childrenCount        metric.Int64ObservableCounter
	restartCount         metric.Int64ObservableCounter
	lastReceivedDuration metric.Int64ObservableCounter
	processedCount       metric.Int64ObservableCounter
	stashSize            metric.Int64ObservableCounter
	uptime               metric.Int64ObservableCounter
}

// NewActorMetric constructs all actor-level instruments with the provided Meter.
// It initializes observable counters for counts (children, stash size, deadletters,
// restarts, processed) and histograms for durations (last received in ms, uptime in s).
// Returns an error if any instrument fails to be created so telemetry setup issues
// can be surfaced early.
func NewActorMetric(meter metric.Meter) (*ActorMetric, error) {
	var instruments ActorMetric
	var err error

	if instruments.childrenCount, err = meter.Int64ObservableCounter(
		"actor.children.count",
		metric.WithDescription("Total number of child actors"),
	); err != nil {
		return nil, fmt.Errorf("failed to create childrenCount instrument, %v", err)
	}

	// set the stashed messages count instrument
	if instruments.stashSize, err = meter.Int64ObservableCounter(
		"actor.stash.size",
		metric.WithDescription("Total number of messages stashed"),
	); err != nil {
		return nil, fmt.Errorf("failed to create stashSize instrument, %v", err)
	}

	// set the deadletters count instrument
	if instruments.deadlettersCount, err = meter.Int64ObservableCounter(
		"actor.deadletters.count",
		metric.WithDescription("Total number of deadletters"),
	); err != nil {
		return nil, fmt.Errorf("failed to create deadlettersCount instrument, %v", err)
	}

	// set the restart count instrument
	if instruments.restartCount, err = meter.Int64ObservableCounter(
		"actor.restart.count",
		metric.WithDescription("Total number of restarts"),
	); err != nil {
		return nil, fmt.Errorf("failed to create restartCount instrument, %v", err)
	}

	// set the last received duration instrument
	if instruments.lastReceivedDuration, err = meter.Int64ObservableCounter(
		"actor.last.received.duration",
		metric.WithDescription("Duration since last message received in milliseconds"),
		metric.WithUnit("ms"),
	); err != nil {
		return nil, fmt.Errorf("failed to create lastReceivedDuration instrument, %v", err)
	}

	// set the processed count instrument
	if instruments.processedCount, err = meter.Int64ObservableCounter(
		"actor.processed.count",
		metric.WithDescription("Total number of messages processed"),
	); err != nil {
		return nil, fmt.Errorf("failed to create processedCount instrument, %v", err)
	}

	// set the uptime instrument
	if instruments.uptime, err = meter.Int64ObservableCounter(
		"actor.uptime",
		metric.WithDescription("Uptime of the PID in seconds"),
		metric.WithUnit("s"),
	); err != nil {
		return nil, fmt.Errorf("failed to create uptime instrument, %v", err)
	}

	return &instruments, nil
}

// ChildrenCount returns an observable counter for the number of child actors
// owned by the PID. Observe this via Meter.RegisterCallback.
func (x *ActorMetric) ChildrenCount() metric.Int64ObservableCounter {
	return x.childrenCount
}

// StashSize returns an observable counter for the number of messages currently
// stashed by the PID. Observe this via Meter.RegisterCallback.
func (x *ActorMetric) StashSize() metric.Int64ObservableCounter {
	return x.stashSize
}

// DeadlettersCount returns an observable counter for messages dropped to
// deadletters by the PID. Observe this via Meter.RegisterCallback.
func (x *ActorMetric) DeadlettersCount() metric.Int64ObservableCounter {
	return x.deadlettersCount
}

// RestartCount returns an observable counter for how many times the PID
// has been restarted. Observe this via Meter.RegisterCallback.
func (x *ActorMetric) RestartCount() metric.Int64ObservableCounter {
	return x.restartCount
}

// LastReceivedDuration returns a histogram (unit: milliseconds) used to record
// time since the PID last processed a message.
//
// Example:
//
//	inst.LastReceivedDuration().Record(ctx, float64(msSinceLast), metric.WithAttributes(...))
func (x *ActorMetric) LastReceivedDuration() metric.Int64ObservableCounter {
	return x.lastReceivedDuration
}

// ProcessedCount returns an observable counter for the total number of messages
// processed by the PID. Observe this via Meter.RegisterCallback.
func (x *ActorMetric) ProcessedCount() metric.Int64ObservableCounter {
	return x.processedCount
}

// Uptime returns a histogram (unit: seconds) used to record the PID’s uptime.
//
// Example:
//
//	inst.Uptime().Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(...))
func (x *ActorMetric) Uptime() metric.Int64ObservableCounter {
	return x.uptime
}
