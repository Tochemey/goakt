package telemetry

import (
	"fmt"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/instrument"
	"go.opentelemetry.io/otel/metric/unit"
)

// SystemMetrics define the type of metrics we are collecting
// from the actor system
type SystemMetrics struct {
	// captures the number of actors in the actor system
	ActorSystemActorsCount instrument.Int64ObservableCounter
}

// NewSystemMetrics creates an instance of ActorMetrics
func NewSystemMetrics(meter metric.Meter) (*SystemMetrics, error) {
	// create an instance of metrics
	metrics := new(SystemMetrics)
	var err error

	if metrics.ActorSystemActorsCount, err = meter.Int64ObservableCounter(
		actorSystemActorsCounterName, instrument.WithDescription("The total number of actors in the Actor System"),
		instrument.WithUnit(unit.Dimensionless)); err != nil {
		return nil, fmt.Errorf("failed to create received count instrument, %v", err)
	}

	return metrics, nil
}
