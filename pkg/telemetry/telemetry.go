package telemetry

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const (
	instrumentationName           = "github.com/Tochemey/goakt"
	failureCounterName            = "actor_failure_count"
	receivedCounterName           = "actor_received_count"
	mailboxGaugeName              = "actor_mailbox_gauge"
	restartedCounterName          = "actor_restarted_count"
	receivedDurationHistogramName = "actor_received_duration"
	actorSystemActorsCounterName  = "actor_system_actors_count"
)

// Telemetry encapsulates some settings for an actor
type Telemetry struct {
	TracerProvider trace.TracerProvider
	Tracer         trace.Tracer

	MeterProvider metric.MeterProvider
	Meter         metric.Meter

	Metrics *ActorMetrics
}

// New creates an instance of Telemetry
func New(options ...Option) *Telemetry {
	// create a config instance
	telemetry := &Telemetry{
		TracerProvider: otel.GetTracerProvider(),
		MeterProvider:  otel.GetMeterProvider(),
	}

	// apply the various options
	for _, opt := range options {
		opt.Apply(telemetry)
	}

	// set the tracer
	telemetry.Tracer = telemetry.TracerProvider.Tracer(
		instrumentationName,
		trace.WithInstrumentationVersion(Version()),
	)

	// set the meter
	telemetry.Meter = telemetry.MeterProvider.Meter(
		instrumentationName,
		metric.WithInstrumentationVersion(Version()),
	)

	// set the metrics
	var err error
	if telemetry.Metrics, err = NewMetrics(telemetry.Meter); err != nil {
		otel.Handle(err)
	}
	return telemetry
}
