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
	otelmetric "go.opentelemetry.io/otel/metric"

	"github.com/tochemey/goakt/v4/internal/metric"
)

// pidCompanion holds the spawn-time settings most actors never set: the
// reliable-delivery endpoint configuration and its durable queues, the
// metrics identity, the singleton specification and the placement role. It
// is the PID's own companion object and is unrelated to the reliable-delivery
// companion controller.
//
// It lives off the PID so an ordinary actor does not pay for it. The PID
// carries one pointer that stays nil until a spawn option attaches a
// companion, and every accessor tolerates the nil. Nothing here is read on
// the message path, and nothing is written once the actor is published, so
// the struct needs no locking of its own.
type pidCompanion struct {
	// reliableDelivery contains the endpoint's reliable-delivery settings.
	reliableDelivery *reliableDeliveryConfig

	// reliableCompanion marks an endpoint-owned reliable-delivery controller
	// and pins it to its endpoint incarnation. It is nil for ordinary actors.
	reliableCompanion *reliableCompanionSpec

	// durableQueue is the point-to-point producer endpoint's durable queue
	// instance, retained so ReSpawn can recreate a terminally stopped producer
	// controller with its storage. It is nil for consumers, work-pulling
	// producers, and volatile point-to-point producers.
	durableQueue DurableProducerQueue

	// durableWorkQueue is the work-pulling producer endpoint's durable work
	// queue instance, retained for the same recovery path as durableQueue.
	durableWorkQueue DurableWorkQueue

	// role is the cluster placement role the actor was spawned with, if any.
	role *string

	// singletonSpec is set only when the actor is a cluster singleton.
	singletonSpec *singletonSpec

	// metricProvider is the provider the actor system wired in when metrics
	// are enabled.
	metricProvider *metric.Provider

	// observeOptions caches the OTel attribute set identifying this actor in
	// observations made by the actor system's metrics callback. Built once at
	// construction when metrics are enabled; nil otherwise. It stays nil in the
	// low cardinality mode, where the callback reports one series per actor kind
	// and no actor is named individually.
	observeOptions []otelmetric.ObserveOption

	// metricKind caches the actor kind reported by the metrics callback, so a
	// scrape never pays for the reflection that resolves it. Set once at
	// construction when metrics are enabled, in both metric modes; empty
	// otherwise.
	metricKind string
}

// attachCompanion returns the PID's companion, attaching a fresh one when the
// PID has none yet. Spawn options call it before the PID is published, so it
// needs no synchronization.
func (x *PID) attachCompanion() *pidCompanion {
	if x.companion == nil {
		x.companion = new(pidCompanion)
	}

	return x.companion
}

// reliableDelivery returns the endpoint's reliable-delivery settings, or nil
// for an ordinary actor.
func (x *PID) reliableDelivery() *reliableDeliveryConfig {
	if x.companion == nil {
		return nil
	}

	return x.companion.reliableDelivery
}

// reliableCompanion returns the specification pinning this actor as an
// endpoint-owned reliable-delivery controller, or nil for an ordinary actor.
func (x *PID) reliableCompanion() *reliableCompanionSpec {
	if x.companion == nil {
		return nil
	}

	return x.companion.reliableCompanion
}

// durableQueue returns the durable queue retained for a point-to-point
// producer endpoint, or nil.
func (x *PID) durableQueue() DurableProducerQueue {
	if x.companion == nil {
		return nil
	}

	return x.companion.durableQueue
}

// durableWorkQueue returns the durable work queue retained for a work-pulling
// producer endpoint, or nil.
func (x *PID) durableWorkQueue() DurableWorkQueue {
	if x.companion == nil {
		return nil
	}

	return x.companion.durableWorkQueue
}

// placementRole returns the cluster placement role the actor was spawned
// with, or nil when none was set.
func (x *PID) placementRole() *string {
	if x.companion == nil {
		return nil
	}

	return x.companion.role
}

// singletonSpec returns the cluster singleton specification, or nil when the
// actor is not a singleton.
func (x *PID) singletonSpec() *singletonSpec {
	if x.companion == nil {
		return nil
	}

	return x.companion.singletonSpec
}

// metricProvider returns the metric provider wired to this actor, or nil when
// metrics are disabled.
func (x *PID) metricProvider() *metric.Provider {
	if x.companion == nil {
		return nil
	}

	return x.companion.metricProvider
}

// observeOptions returns the cached attribute set identifying this actor in
// metric observations, or nil when metrics are disabled or reported per kind.
func (x *PID) observeOptions() []otelmetric.ObserveOption {
	if x.companion == nil {
		return nil
	}

	return x.companion.observeOptions
}

// metricKind returns the cached actor kind reported by the metrics callback,
// or the empty string when metrics are disabled.
func (x *PID) metricKind() string {
	if x.companion == nil {
		return ""
	}

	return x.companion.metricKind
}
