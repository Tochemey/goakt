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
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
)

func TestActorSystemInstrument(t *testing.T) {
	meter := noop.NewMeterProvider().Meter("test")
	instruments, err := NewActorSystemMetric(meter)
	require.NoError(t, err)
	require.NotNil(t, instruments)

	require.NotNil(t, instruments.DeadlettersCount())
	require.NotNil(t, instruments.PIDsCount())
	require.NotNil(t, instruments.Uptime())
	require.NotNil(t, instruments.PeersCount())
}

func TestActorSystemInstrumentErrors(t *testing.T) {
	t.Parallel()

	errBoom := errors.New("boom")
	baseMeter := noop.NewMeterProvider().Meter("test")

	testCases := []struct {
		name    string
		failKey string
	}{
		{name: "deadletters counter", failKey: "actorsystem.deadletters.count"},
		{name: "pids counter", failKey: "actorsystem.actors.count"},
		{name: "uptime histogram", failKey: "actorsystem.uptime"},
		{name: "peers counter", failKey: "actorsystem.peers.count"},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			meter := instrumentFailingMeter{
				Meter: baseMeter,
				failures: map[string]error{
					tt.failKey: errBoom,
				},
			}

			instruments, err := NewActorSystemMetric(meter)
			require.Error(t, err)
			require.Nil(t, instruments)
		})
	}
}
