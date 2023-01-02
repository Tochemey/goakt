package telemetry

import (
	"fmt"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/instrument"
	"go.opentelemetry.io/otel/metric/instrument/asyncint64"
	"go.opentelemetry.io/otel/metric/instrument/syncfloat64"
	"go.opentelemetry.io/otel/metric/unit"
)

const (
	failureCounterName            = "actor_failure_count"
	receivedCounterName           = "actor_received_count"
	mailboxGaugeName              = "actor_mailbox_gauge"
	restartedCounterName          = "actor_restarted_count"
	receivedDurationHistogramName = "actor_received_duration"
	actorSystemActorsCounterName  = "actor_system_actors_count"
)

// ActorMetrics define the type of metrics we are collecting
// from an actor
type ActorMetrics struct {
	// captures the number of times a given actor has panic
	PanicCount asyncint64.Counter
	// captures the actor mailbox size
	MailboxSize asyncint64.Gauge
	// captures the number of time the actor has restarted
	RestartedCount asyncint64.Counter
	// captures the count of messages received by the actor
	ReceivedCount asyncint64.Counter
	// captures the duration of message received and processed
	ReceivedDurationHistogram syncfloat64.Histogram
}

// NewMetrics creates an instance of ActorMetrics
func NewMetrics(meter metric.Meter) (*ActorMetrics, error) {
	// create an instance of metrics
	metrics := new(ActorMetrics)
	var err error

	// set the various counters
	if metrics.PanicCount, err = meter.AsyncInt64().Counter(
		failureCounterName,
		instrument.WithDescription("The total number of failures(panic) by the actor"),
		instrument.WithUnit(unit.Dimensionless),
	); err != nil {
		return nil, fmt.Errorf("failed to create failure count instrument, %v", err)
	}

	if metrics.MailboxSize, err = meter.AsyncInt64().Gauge(
		mailboxGaugeName,
		instrument.WithDescription("The number of messages in point in time by the actor"),
	); err != nil {
		return nil, fmt.Errorf("failed to create mailbix count instrument, %v", err)
	}

	if metrics.RestartedCount, err = meter.AsyncInt64().Counter(
		restartedCounterName,
		instrument.WithDescription("The total number of restart"),
		instrument.WithUnit(unit.Dimensionless)); err != nil {
		return nil, fmt.Errorf("failed to create restart count instrument, %v", err)
	}

	if metrics.ReceivedCount, err = meter.AsyncInt64().Counter(
		receivedCounterName, instrument.WithDescription("The total number of messages received"),
		instrument.WithUnit(unit.Dimensionless)); err != nil {
		return nil, fmt.Errorf("failed to create received count instrument, %v", err)
	}

	if metrics.ReceivedDurationHistogram, err = meter.SyncFloat64().Histogram(
		receivedDurationHistogramName,
		instrument.WithDescription("The latency of received message in milliseconds"),
		instrument.WithUnit(unit.Milliseconds),
	); err != nil {
		return nil, fmt.Errorf("failed to create latency instrument, %v", err)
	}

	return metrics, nil
}
