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

// SchedulerMetric groups the OpenTelemetry instruments that describe the
// message scheduler's activity.
//
// Both totals only grow for the lifetime of the node, so they are observable
// counters fed by the scheduling API calls and read at scrape time.
//
// Instruments:
//   - scheduler.scheduled.count (Int64ObservableCounter)
//   - scheduler.cancelled.count (Int64ObservableCounter)
type SchedulerMetric struct {
	scheduledCount metric.Int64ObservableCounter
	cancelledCount metric.Int64ObservableCounter
}

// NewSchedulerMetric creates the scheduler instruments using the provided
// Meter. It returns an error if any instrument cannot be created so telemetry
// initialization failures are surfaced early.
func NewSchedulerMetric(meter metric.Meter) (*SchedulerMetric, error) {
	var instruments SchedulerMetric
	var err error

	if instruments.scheduledCount, err = meter.Int64ObservableCounter(
		"scheduler.scheduled.count",
		metric.WithDescription("Total number of messages successfully scheduled for future delivery"),
	); err != nil {
		return nil, err
	}

	if instruments.cancelledCount, err = meter.Int64ObservableCounter(
		"scheduler.cancelled.count",
		metric.WithDescription("Total number of scheduled messages successfully cancelled"),
	); err != nil {
		return nil, err
	}

	return &instruments, nil
}

// ScheduledCount returns the observable counter that tracks how many messages
// were successfully scheduled for future delivery.
//
// Use with Meter.RegisterCallback to observe the current value periodically.
func (x *SchedulerMetric) ScheduledCount() metric.Int64ObservableCounter {
	return x.scheduledCount
}

// CancelledCount returns the observable counter that tracks how many scheduled
// messages were successfully cancelled.
//
// Use with Meter.RegisterCallback to observe the current value periodically.
func (x *SchedulerMetric) CancelledCount() metric.Int64ObservableCounter {
	return x.cancelledCount
}
