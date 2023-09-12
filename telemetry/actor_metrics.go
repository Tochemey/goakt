package telemetry

import (
	"fmt"

	"go.opentelemetry.io/otel/metric"
)

// ActorMetrics define the type of metrics we are collecting
// from an actor
type ActorMetrics struct {
	// captures the number of times a given actor has panic
	PanicCount metric.Int64ObservableCounter
	// captures the actor mailbox size
	MailboxSize metric.Int64ObservableGauge
	// captures the number of time the actor has started
	StartCount metric.Int64ObservableCounter
	// captures the count of messages received by the actor
	ReceivedCount metric.Int64ObservableCounter
	// captures the duration of message received and processed
	ReceivedDurationHistogram metric.Float64Histogram
}

// NewMetrics creates an instance of ActorMetrics
func NewMetrics(meter metric.Meter) (*ActorMetrics, error) {
	// create an instance of metrics
	metrics := new(ActorMetrics)
	var err error

	// set the various counters
	if metrics.PanicCount, err = meter.Int64ObservableCounter(
		failureCounterName,
		metric.WithDescription("The total number of failures(panic) by the actor"),
	); err != nil {
		return nil, fmt.Errorf("failed to create failure count instrument, %v", err)
	}

	if metrics.MailboxSize, err = meter.Int64ObservableGauge(
		mailboxGaugeName,
		metric.WithDescription("The number of messages in point in time by the actor"),
	); err != nil {
		return nil, fmt.Errorf("failed to create mailbix count instrument, %v", err)
	}

	if metrics.StartCount, err = meter.Int64ObservableCounter(
		startedCounterName,
		metric.WithDescription("The total number of start")); err != nil {
		return nil, fmt.Errorf("failed to create start count instrument, %v", err)
	}

	if metrics.ReceivedCount, err = meter.Int64ObservableCounter(
		receivedCounterName, metric.WithDescription("The total number of messages received")); err != nil {
		return nil, fmt.Errorf("failed to create received count instrument, %v", err)
	}

	if metrics.ReceivedDurationHistogram, err = meter.Float64Histogram(
		receivedDurationHistogramName,
		metric.WithDescription("The latency of received message in milliseconds"),
		metric.WithUnit("ms"),
	); err != nil {
		return nil, fmt.Errorf("failed to create latency instrument, %v", err)
	}

	return metrics, nil
}
