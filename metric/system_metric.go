/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package metric

import (
	"fmt"

	"go.opentelemetry.io/otel/metric"
)

// ActorSystemMetric defines the actor system metrics
type ActorSystemMetric struct {
	actorsCount     metric.Int64ObservableCounter
	deadletterCount metric.Int64ObservableCounter
}

// NewActorSystemMetric creates an instance of ActorSystemMetric
func NewActorSystemMetric(meter metric.Meter) (*ActorSystemMetric, error) {
	// create an instance of ActorMetric
	systemMetric := new(ActorSystemMetric)
	var err error
	// set the child count instrument
	if systemMetric.actorsCount, err = meter.Int64ObservableCounter(
		"actors_count",
		metric.WithDescription("Total number of actors"),
	); err != nil {
		return nil, fmt.Errorf("failed to create actorsCount instrument, %v", err)
	}
	// set the stashed messages count instrument
	if systemMetric.deadletterCount, err = meter.Int64ObservableCounter(
		"actor_system_deadletter_count",
		metric.WithDescription("Total number of deadletter messages"),
	); err != nil {
		return nil, fmt.Errorf("failed to create deadletterCount instrument, %v", err)
	}
	return systemMetric, nil
}

// ActorsCount returns the total number of actors
func (x *ActorSystemMetric) ActorsCount() metric.Int64ObservableCounter {
	return x.actorsCount
}

// DeadletterCount returns the total number of deadletter
func (x *ActorSystemMetric) DeadletterCount() metric.Int64ObservableCounter {
	return x.deadletterCount
}
