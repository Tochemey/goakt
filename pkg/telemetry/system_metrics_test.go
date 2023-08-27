package telemetry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
)

func TestNewSystemMetrics(t *testing.T) {
	metrics, err := NewSystemMetrics(noop.NewMeterProvider().Meter("test"))
	require.NoError(t, err)
	assert.NotNil(t, metrics)
	assert.NotNil(t, metrics.ActorSystemActorsCount)
}
