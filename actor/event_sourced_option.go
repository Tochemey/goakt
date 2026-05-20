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
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/internal/codec"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/persistence"
)

// eventSourcedConfigID is the stable dependency ID for eventSourcedConfig.
const eventSourcedConfigID = "goakt-es-config"

// eventSourcedConfig carries the snapshot criteria for an event-sourced actor.
// It implements [extension.Dependency] so it is serialized alongside the behavior
// when the actor is relocated to another node.
type eventSourcedConfig struct {
	criteria *persistence.SnapshotCriteria
}

var _ extension.Dependency = (*eventSourcedConfig)(nil)

func (x *eventSourcedConfig) ID() string { return eventSourcedConfigID }

// MarshalBinary encodes the criteria as a proto-marshaled [internalpb.SnapshotSpec].
// A nil criteria encodes to an empty byte slice.
func (x *eventSourcedConfig) MarshalBinary() ([]byte, error) {
	spec := codec.EncodeSnapshotCriteria(x.criteria)
	if spec == nil {
		return []byte{}, nil
	}
	return proto.Marshal(spec)
}

// UnmarshalBinary decodes data written by MarshalBinary.
func (x *eventSourcedConfig) UnmarshalBinary(data []byte) error {
	if len(data) == 0 {
		x.criteria = nil
		return nil
	}

	var spec internalpb.SnapshotSpec
	if err := proto.Unmarshal(data, &spec); err != nil {
		return err
	}

	x.criteria = codec.DecodeSnapshotCriteria(&spec)
	return nil
}

// EventSourcingOption configures the event-sourcing wiring installed by
// [WithEventSourcing].
type EventSourcingOption func(*eventSourcingConfig)

// eventSourcingConfig holds the optional settings for [WithEventSourcing].
type eventSourcingConfig struct {
	snapshotStore persistence.SnapshotStore
	behaviors     []EventSourcedBehavior
}

// WithSnapshotStore installs an optional [persistence.SnapshotStore] alongside
// the events store wired by [WithEventSourcing]. When present, event-sourced
// actors will write a final snapshot on shutdown and intermediate snapshots
// when [WithSnapshotCriteria] is passed to [actorSystem.SpawnEventSourced].
func WithSnapshotStore(store persistence.SnapshotStore) EventSourcingOption {
	return func(c *eventSourcingConfig) { c.snapshotStore = store }
}

// WithEventSourcedBehavior declares a behavior to register at startup. It
// accumulates on each call and is equivalent to appending to the behaviors
// slice passed to [WithEventSourcing]. To register a behavior after the
// system has started, use [ActorSystem.RegisterEventSourcedBehavior].
func WithEventSourcedBehavior(behavior EventSourcedBehavior) EventSourcingOption {
	return func(c *eventSourcingConfig) {
		if behavior != nil {
			c.behaviors = append(c.behaviors, behavior)
		}
	}
}

// WithEventSourcing wires the actor system for event-sourced actors. It
// registers the events store as an extension, registers the optional snapshot
// store (via [WithSnapshotStore]), registers the internal actor and dependency
// types used for relocation, and records every behavior in behaviors and any
// added via [WithEventSourcedBehavior].
//
// Apply this option on every node in the cluster with the same behaviors.
// Behaviors can also be added at runtime via
// [ActorSystem.RegisterEventSourcedBehavior]. A behavior not registered on a
// node cannot be spawned there or relocated to it.
func WithEventSourcing(eventsStore persistence.EventsStore, behaviors []EventSourcedBehavior, opts ...EventSourcingOption) Option {
	config := &eventSourcingConfig{}
	for _, opt := range opts {
		opt(config)
	}

	return OptionFunc(func(s *actorSystem) {
		s.extensions.Set(persistence.EventsStoreExtensionID, persistence.NewEventsStoreExtension(eventsStore))
		if config.snapshotStore != nil {
			s.extensions.Set(persistence.SnapshotStoreExtensionID, persistence.NewSnapshotStoreExtension(config.snapshotStore))
		}

		s.registry.Register(&eventSourcedActor{})
		s.registry.Register(&eventSourcedConfig{})
		s.registry.Register(&eventSourcedDependency{})

		for _, b := range behaviors {
			registerBehavior(s, b)
		}

		for _, b := range config.behaviors {
			registerBehavior(s, b)
		}
	})
}

// registerBehavior records behavior in the actor system's registry and in
// [types.GlobalRegistry] so that [eventSourcedDependency.UnmarshalBinary] can
// instantiate it during deserialization.
func registerBehavior(s *actorSystem, behavior EventSourcedBehavior) {
	if behavior == nil {
		return
	}
	s.registry.Register(behavior)
	types.GlobalRegistry.Register(behavior)
}
