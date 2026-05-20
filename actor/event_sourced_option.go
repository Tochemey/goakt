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

func (c *eventSourcedConfig) ID() string { return eventSourcedConfigID }

// MarshalBinary encodes the criteria as a proto-marshaled [internalpb.SnapshotSpec].
// A nil criteria encodes to an empty byte slice.
func (c *eventSourcedConfig) MarshalBinary() ([]byte, error) {
	spec := codec.EncodeSnapshotCriteria(c.criteria)
	if spec == nil {
		return []byte{}, nil
	}
	return proto.Marshal(spec)
}

// UnmarshalBinary decodes data written by MarshalBinary.
func (c *eventSourcedConfig) UnmarshalBinary(data []byte) error {
	if len(data) == 0 {
		c.criteria = nil
		return nil
	}
	var spec internalpb.SnapshotSpec
	if err := proto.Unmarshal(data, &spec); err != nil {
		return err
	}
	c.criteria = codec.DecodeSnapshotCriteria(&spec)
	return nil
}

// EventSourcingOption configures the event-sourcing wiring installed by
// [WithEventSourcing].
type EventSourcingOption func(*eventSourcingConfig)

// eventSourcingConfig accumulates the optional knobs for [WithEventSourcing].
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

// WithEventSourcedBehavior declares an additional [EventSourcedBehavior] to
// register with the event-sourcing system at startup. It can be supplied to
// [WithEventSourcing] alongside (or instead of) the positional behaviors slice
// and is the recommended form when registering behaviors one-by-one.
//
// Multiple uses accumulate; the same effect can also be achieved at runtime
// via [ActorSystem.RegisterEventSourcedBehavior].
func WithEventSourcedBehavior(behavior EventSourcedBehavior) EventSourcingOption {
	return func(c *eventSourcingConfig) {
		if behavior != nil {
			c.behaviors = append(c.behaviors, behavior)
		}
	}
}

// WithEventSourcing wires the actor system for event-sourced actors:
//
//   - registers the [persistence.EventsStore] as an extension;
//   - registers the optional [persistence.SnapshotStore] (via [WithSnapshotStore])
//     as an extension;
//   - registers the internal event-sourced actor and config types so spawn
//     requests can be reconstructed during cluster relocation;
//   - registers every behavior in behaviors (and any added via
//     [WithEventSourcedBehavior]) so its concrete type can be instantiated
//     from a dependency on this node, whether the actor was spawned here or
//     relocated here from another node.
//
// Call this option on every node in the cluster at startup with the same set
// of behaviors. Behaviors can be added later at runtime via
// [ActorSystem.RegisterEventSourcedBehavior]. A behavior that has not been
// declared here or registered at runtime cannot be spawned on, or relocated
// to, this node.
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
		for _, b := range behaviors {
			if b == nil {
				continue
			}
			s.registry.Register(b)
		}

		for _, b := range config.behaviors {
			s.registry.Register(b)
		}
	})
}
