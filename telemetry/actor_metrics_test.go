package telemetry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
)

func TestNewMetrics(t *testing.T) {
	metrics, err := NewMetrics(noop.NewMeterProvider().Meter("test"))
	require.NoError(t, err)
	assert.NotNil(t, metrics)
	assert.NotNil(t, metrics.ReceivedDurationHistogram)
	assert.NotNil(t, metrics.ReceivedCount)
	assert.NotNil(t, metrics.RestartedCount)
	assert.NotNil(t, metrics.MailboxSize)
	assert.NotNil(t, metrics.PanicCount)
}
