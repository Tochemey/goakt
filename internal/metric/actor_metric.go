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

import (
	"fmt"

	"go.opentelemetry.io/otel/metric"
)

// ActorMetric groups OpenTelemetry instruments that describe the health,
// throughput, and lifecycle of a single PID (actor).
//
// Point-in-time readings that can rise and fall (children, stash size, last
// received duration, uptime) are observable gauges. Cumulative totals that only
// grow within an actor incarnation (deadletters, restarts, processed, failures,
// reinstatements) are observable counters.
//
// Included instruments:
//   - actor.children.count           (Int64ObservableGauge)
//   - actor.stash.size               (Int64ObservableGauge)
//   - actor.deadletters.count        (Int64ObservableCounter)
//   - actor.restart.count            (Int64ObservableCounter)
//   - actor.last.received.duration   (Int64ObservableGauge, unit: ms)
//   - actor.processed.count          (Int64ObservableCounter)
//   - actor.uptime                   (Int64ObservableGauge, unit: s)
//   - actor.failure.count            (Int64ObservableCounter)
//   - actor.reinstate.count          (Int64ObservableCounter)
//   - actor.unhandled.count          (Int64ObservableCounter)
//   - actor.mailbox.size             (Int64ObservableGauge)
type ActorMetric struct {
	deadlettersCount     metric.Int64ObservableCounter
	childrenCount        metric.Int64ObservableGauge
	restartCount         metric.Int64ObservableCounter
	lastReceivedDuration metric.Int64ObservableGauge
	processedCount       metric.Int64ObservableCounter
	stashSize            metric.Int64ObservableGauge
	uptime               metric.Int64ObservableGauge
	failureCount         metric.Int64ObservableCounter
	reinstateCount       metric.Int64ObservableCounter
	unhandledCount       metric.Int64ObservableCounter
	mailboxSize          metric.Int64ObservableGauge
}

// NewActorMetric constructs all actor-level instruments with the provided Meter.
// It initializes observable gauges for point-in-time readings that can rise and
// fall (children, stash size, last received duration, uptime) and observable
// counters for cumulative totals (deadletters, restarts, processed, failures,
// reinstatements). Returns an error if any instrument fails to be created so
// telemetry setup issues can be surfaced early.
func NewActorMetric(meter metric.Meter) (*ActorMetric, error) {
	var instruments ActorMetric
	var err error

	if instruments.childrenCount, err = meter.Int64ObservableGauge(
		"actor.children.count",
		metric.WithDescription("Current number of child actors"),
	); err != nil {
		return nil, fmt.Errorf("failed to create childrenCount instrument, %v", err)
	}

	// set the stashed messages count instrument
	if instruments.stashSize, err = meter.Int64ObservableGauge(
		"actor.stash.size",
		metric.WithDescription("Current number of messages stashed"),
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
	if instruments.lastReceivedDuration, err = meter.Int64ObservableGauge(
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
	if instruments.uptime, err = meter.Int64ObservableGauge(
		"actor.uptime",
		metric.WithDescription("Uptime of the PID in seconds"),
		metric.WithUnit("s"),
	); err != nil {
		return nil, fmt.Errorf("failed to create uptime instrument, %v", err)
	}

	// set the failure count instrument
	if instruments.failureCount, err = meter.Int64ObservableCounter(
		"actor.failure.count",
		metric.WithDescription("Total number of failures observed"),
	); err != nil {
		return nil, fmt.Errorf("failed to create failureCount instrument, %v", err)
	}

	// set the reinstate count instrument
	if instruments.reinstateCount, err = meter.Int64ObservableCounter(
		"actor.reinstate.count",
		metric.WithDescription("Total number of reinstatements (suspended -> resumed)"),
	); err != nil {
		return nil, fmt.Errorf("failed to create reinstateCount instrument, %v", err)
	}

	// set the unhandled count instrument
	if instruments.unhandledCount, err = meter.Int64ObservableCounter(
		"actor.unhandled.count",
		metric.WithDescription("Total number of messages the actor marked as unhandled"),
	); err != nil {
		return nil, fmt.Errorf("failed to create unhandledCount instrument, %v", err)
	}

	// set the mailbox size instrument
	if instruments.mailboxSize, err = meter.Int64ObservableGauge(
		"actor.mailbox.size",
		metric.WithDescription("Current number of messages waiting in the mailbox"),
	); err != nil {
		return nil, fmt.Errorf("failed to create mailboxSize instrument, %v", err)
	}

	return &instruments, nil
}

// ChildrenCount returns an observable gauge for the number of child actors
// owned by the PID. Observe this via Meter.RegisterCallback.
func (x *ActorMetric) ChildrenCount() metric.Int64ObservableGauge {
	return x.childrenCount
}

// StashSize returns an observable gauge for the number of messages currently
// stashed by the PID. Observe this via Meter.RegisterCallback.
func (x *ActorMetric) StashSize() metric.Int64ObservableGauge {
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

// LastReceivedDuration returns an observable gauge (unit: milliseconds) for the
// time since the PID last processed a message. Observe this via
// Meter.RegisterCallback.
func (x *ActorMetric) LastReceivedDuration() metric.Int64ObservableGauge {
	return x.lastReceivedDuration
}

// ProcessedCount returns an observable counter for the total number of messages
// processed by the PID. Observe this via Meter.RegisterCallback.
func (x *ActorMetric) ProcessedCount() metric.Int64ObservableCounter {
	return x.processedCount
}

// Uptime returns an observable gauge (unit: seconds) for the PID's uptime.
// Observe this via Meter.RegisterCallback.
func (x *ActorMetric) Uptime() metric.Int64ObservableGauge {
	return x.uptime
}

// FailureCount returns an observable counter for the total number of failures
// observed by the PID. Observe this via Meter.RegisterCallback.
func (x *ActorMetric) FailureCount() metric.Int64ObservableCounter {
	return x.failureCount
}

// ReinstateCount returns an observable counter for the total number of
// reinstatements (suspended -> resumed transitions) of the PID.
// Observe this via Meter.RegisterCallback.
func (x *ActorMetric) ReinstateCount() metric.Int64ObservableCounter {
	return x.reinstateCount
}

// UnhandledCount returns an observable counter for the total number of messages
// the PID explicitly marked as unhandled through ReceiveContext.Unhandled.
// Observe this via Meter.RegisterCallback.
func (x *ActorMetric) UnhandledCount() metric.Int64ObservableCounter {
	return x.unhandledCount
}

// MailboxSize returns an observable gauge for the number of messages waiting in
// the PID's mailbox, enqueued and not yet dispatched.
// Observe this via Meter.RegisterCallback.
func (x *ActorMetric) MailboxSize() metric.Int64ObservableGauge {
	return x.mailboxSize
}
