package telemetry

import (
	"fmt"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/instrument"
	"go.opentelemetry.io/otel/metric/unit"
)

// ActorMetrics define the type of metrics we are collecting
// from an actor
type ActorMetrics struct {
	// captures the number of times a given actor has panic
	PanicCount instrument.Int64ObservableCounter
	// captures the actor mailbox size
	MailboxSize instrument.Int64ObservableGauge
	// captures the number of time the actor has restarted
	RestartedCount instrument.Int64ObservableCounter
	// captures the count of messages received by the actor
	ReceivedCount instrument.Int64ObservableCounter
	// captures the duration of message received and processed
	ReceivedDurationHistogram instrument.Float64Histogram
}

// NewMetrics creates an instance of ActorMetrics
func NewMetrics(meter metric.Meter) (*ActorMetrics, error) {
	// create an instance of metrics
	metrics := new(ActorMetrics)
	var err error

	// set the various counters
	if metrics.PanicCount, err = meter.Int64ObservableCounter(
		failureCounterName,
		instrument.WithDescription("The total number of failures(panic) by the actor"),
		instrument.WithUnit(unit.Dimensionless),
	); err != nil {
		return nil, fmt.Errorf("failed to create failure count instrument, %v", err)
	}

	if metrics.MailboxSize, err = meter.Int64ObservableCounter(
		mailboxGaugeName,
		instrument.WithDescription("The number of messages in point in time by the actor"),
	); err != nil {
		return nil, fmt.Errorf("failed to create mailbix count instrument, %v", err)
	}

	if metrics.RestartedCount, err = meter.Int64ObservableCounter(
		restartedCounterName,
		instrument.WithDescription("The total number of restart"),
		instrument.WithUnit(unit.Dimensionless)); err != nil {
		return nil, fmt.Errorf("failed to create restart count instrument, %v", err)
	}

	if metrics.ReceivedCount, err = meter.Int64ObservableCounter(
		receivedCounterName, instrument.WithDescription("The total number of messages received"),
		instrument.WithUnit(unit.Dimensionless)); err != nil {
		return nil, fmt.Errorf("failed to create received count instrument, %v", err)
	}

	if metrics.ReceivedDurationHistogram, err = meter.Float64Histogram(
		receivedDurationHistogramName,
		instrument.WithDescription("The latency of received message in milliseconds"),
		instrument.WithUnit(unit.Milliseconds),
	); err != nil {
		return nil, fmt.Errorf("failed to create latency instrument, %v", err)
	}

	return metrics, nil
}
