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

package actor

import (
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/internal/metric"
)

// pidMaxSizeBytes is the size class the PID must stay in. The PID is the one
// object every actor allocates, so a field that pushes it over this boundary
// costs the whole class step on every actor in the process. Raise it only
// with a measurement from BenchmarkActorMemoryFootprint that justifies it.
//
// It is class 416. Embedding the default user mailbox added the 16 bytes of
// mailboxHead and mailboxTail, moving the PID from 376 bytes (class 384) to 392
// bytes (class 416). BenchmarkActorMemoryFootprint justifies the step: dropping
// the separate 144-byte UnboundedMailbox object nets a per-idle-actor decrease
// well past the 32 bytes of size class the PID takes on.
const pidMaxSizeBytes = 416

// TestPIDStaysInItsSizeClass guards the PID's size class.
func TestPIDStaysInItsSizeClass(t *testing.T) {
	require.LessOrEqual(t, unsafe.Sizeof(PID{}), uintptr(pidMaxSizeBytes), "PID grew past its size class; rarely set fields belong in pidCompanion")
}

// TestPIDCompanionAbsentByDefault verifies that a PID built without any of the
// rarely used spawn settings has no companion at all, which is what keeps
// those settings off the idle actor footprint, and that every accessor
// tolerates that.
func TestPIDCompanionAbsentByDefault(t *testing.T) {
	pid := &PID{}
	withSingletonSpec(nil)(pid)
	withRole("")(pid)
	withMetricProvider(nil)(pid)

	require.Nil(t, pid.companion)
	require.Nil(t, pid.reliableDelivery())
	require.Nil(t, pid.reliableCompanion())
	require.Nil(t, pid.durableQueue())
	require.Nil(t, pid.durableWorkQueue())
	require.Nil(t, pid.placementRole())
	require.Nil(t, pid.singletonSpec())
	require.Nil(t, pid.metricProvider())
	require.Nil(t, pid.observeOptions())
	require.Empty(t, pid.metricKind())
}

// TestPIDCompanionAccessors verifies that the spawn options attach the
// companion once, on first use, and that every accessor returns what was set.
func TestPIDCompanionAccessors(t *testing.T) {
	pid := &PID{}
	require.Nil(t, pid.companion)

	companion := pid.attachCompanion()
	require.NotNil(t, companion)
	require.Same(t, companion, pid.attachCompanion())

	config := producerDeliveryConfig("orders-consumer")
	spec := &reliableCompanionSpec{}
	queue := &mockDurableQueue{}
	workQueue := &mockDurableWorkQueue{}
	singleton := &singletonSpec{
		SpawnTimeout: time.Second,
		WaitInterval: 500 * time.Millisecond,
		MaxRetries:   3,
	}
	provider := metric.NewProvider()

	withReliableDelivery(config)(pid)
	withReliableCompanion(spec)(pid)
	withDurableQueue(queue)(pid)
	withDurableWorkQueue(workQueue)(pid)
	withSingletonSpec(singleton)(pid)
	withRole("payments")(pid)
	withMetricProvider(provider)(pid)

	require.Same(t, companion, pid.companion)
	require.Equal(t, config, pid.reliableDelivery())
	require.NotSame(t, config, pid.reliableDelivery())
	require.Same(t, spec, pid.reliableCompanion())
	require.Same(t, queue, pid.durableQueue())
	require.Same(t, workQueue, pid.durableWorkQueue())
	require.Same(t, singleton, pid.singletonSpec())
	require.Same(t, provider, pid.metricProvider())
	require.NotNil(t, pid.placementRole())
	require.Equal(t, "payments", *pid.placementRole())
	require.Equal(t, "payments", *pid.Role())
}
