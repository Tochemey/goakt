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

// ActorMetric defines the actor instrumentation
type ActorMetric struct {
	// Specifies the total number of child actors
	childrenCount metric.Int64ObservableCounter
	// Specifies the total number of stashed messages
	stashCount metric.Int64ObservableCounter
	// Specifies the total number of restarts
	restartCount metric.Int64ObservableCounter
	// Specifies the total number of instance created
	spawnCount metric.Int64ObservableCounter
	// Specifies the last message received processing duration
	// This is expressed in milliseconds
	lastReceivedDuration metric.Int64Histogram
	// Specifies the total number of messages processed
	processedCount metric.Int64ObservableCounter
}

// NewActorMetric creates an instance of ActorMetric
func NewActorMetric(meter metric.Meter) (*ActorMetric, error) {
	// create an instance of ActorMetric
	actorMetric := new(ActorMetric)
	var err error
	// set the child count instrument
	if actorMetric.childrenCount, err = meter.Int64ObservableCounter(
		"actor_child_count",
		metric.WithDescription("Total number of child actors"),
	); err != nil {
		return nil, fmt.Errorf("failed to create childrenCount instrument, %w", err)
	}
	// set the stashed messages count instrument
	if actorMetric.stashCount, err = meter.Int64ObservableCounter(
		"actor_stash_count",
		metric.WithDescription("Total number of messages stashed"),
	); err != nil {
		return nil, fmt.Errorf("failed to create stashCount instrument, %w", err)
	}

	// set the spawn messages count instrument
	if actorMetric.spawnCount, err = meter.Int64ObservableCounter(
		"actor_spawn_count",
		metric.WithDescription("Total number of instances created"),
	); err != nil {
		return nil, fmt.Errorf("failed to create spawnCount instrument, %w", err)
	}

	// set the spawn messages count instrument
	if actorMetric.restartCount, err = meter.Int64ObservableCounter(
		"actor_restart_count",
		metric.WithDescription("Total number of restart"),
	); err != nil {
		return nil, fmt.Errorf("failed to create restartCount instrument, %w", err)
	}

	// set the last received message duration instrument
	if actorMetric.lastReceivedDuration, err = meter.Int64Histogram(
		"actor_received_duration",
		metric.WithDescription("The latency of the last message processed in milliseconds"),
		metric.WithUnit("ms"),
	); err != nil {
		return nil, fmt.Errorf("failed to create lastReceivedDuration instrument, %w", err)
	}

	// set the processed count instrument
	if actorMetric.processedCount, err = meter.Int64ObservableCounter(
		"actor_processed_count",
		metric.WithDescription("Total number of messages processed"),
	); err != nil {
		return nil, fmt.Errorf("failed to create processedCount instrument, %w", err)
	}

	return actorMetric, nil
}

// ChildrenCount returns the total number of child actors
func (x *ActorMetric) ChildrenCount() metric.Int64ObservableCounter {
	return x.childrenCount
}

// StashCount returns the total number of stashed messages
func (x *ActorMetric) StashCount() metric.Int64ObservableCounter {
	return x.stashCount
}

// RestartCount returns the total number of restart
func (x *ActorMetric) RestartCount() metric.Int64ObservableCounter {
	return x.restartCount
}

// SpawnCount returns the total number of instances created for the given actor
func (x *ActorMetric) SpawnCount() metric.Int64ObservableCounter {
	return x.spawnCount
}

// LastReceivedDuration returns the last message received duration latency in milliseconds
func (x *ActorMetric) LastReceivedDuration() metric.Int64Histogram {
	return x.lastReceivedDuration
}

// ProcessedCount returns the total number of messages processed by the given actor
// at a given time in point
func (x *ActorMetric) ProcessedCount() metric.Int64ObservableCounter {
	return x.processedCount
}
