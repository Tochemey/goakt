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
	"context"
	"fmt"
	"reflect"

	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/types"
)

// eventSourcedBehaviorDependencyID is the dependency ID of eventSourcedDependency.
const eventSourcedBehaviorDependencyID = "goakt-es-behavior"

// EventSourcedBehavior is the contract an event-sourced actor's command and
// event handling must satisfy.
//
// InitialState returns the actor's starting state when no snapshot is found.
// HandleCommand validates a command against the prior state and returns the
// events to persist. HandleEvent applies a single event to the prior state
// and returns the next state.
//
// HandleEvent runs during both live processing and recovery replay and must
// be a pure function: identical inputs must produce identical outputs.
//
// Behaviors travel between nodes by Go type name only. Each node must register
// every behavior it expects to spawn or receive via [WithEventSourcing] or
// [ActorSystem.RegisterEventSourcedBehavior]; per-actor state is rebuilt from
// the event log on recovery.
//
// An event-sourced actor cannot have user-spawned children. Calling
// [PID.SpawnChild] on its PID returns [errors.ErrEventSourcedChildrenNotAllowed].
type EventSourcedBehavior interface {
	// InitialState returns the zero value of the actor's state.
	// It is used when no snapshot is found during recovery.
	InitialState() any

	// HandleCommand validates command against priorState and returns the events
	// to persist. Return a nil or empty slice for a read-only no-op.
	// Return a non-nil error to reject the command — no events will be persisted.
	HandleCommand(ctx context.Context, command any, priorState any) (events []any, err error)

	// HandleEvent applies a single event to priorState and returns the next state.
	// Must be a pure function; it is called during both live processing and replay.
	HandleEvent(ctx context.Context, event any, priorState any) (state any, err error)
}

// eventSourcedDependency wraps an [EventSourcedBehavior] so it can be carried
// as an [extension.Dependency] alongside spawn requests. It is unexported so
// only [actorSystem.SpawnEventSourced] installs it; values passed via
// [WithDependencies] cannot satisfy this type.
//
// MarshalBinary records the inner behavior's Go type name. UnmarshalBinary
// looks the name up in [types.GlobalRegistry] and instantiates a zero-valued
// behavior; per-actor state is rebuilt from the event log on recovery.
type eventSourcedDependency struct {
	inner EventSourcedBehavior
}

var _ extension.Dependency = (*eventSourcedDependency)(nil)

// ID returns the reserved dependency ID.
func (x *eventSourcedDependency) ID() string { return eventSourcedBehaviorDependencyID }

// MarshalBinary records the inner behavior's Go type name. It returns an
// error when the inner behavior is nil.
func (x *eventSourcedDependency) MarshalBinary() ([]byte, error) {
	if x.inner == nil {
		return nil, fmt.Errorf("eventSourcedDependency: inner behavior is nil")
	}
	return proto.Marshal(&internalpb.EventSourcedBehaviorEnvelope{
		TypeName: types.Name(x.inner),
	})
}

// UnmarshalBinary decodes the type name written by MarshalBinary and
// instantiates a zero-valued behavior of that type via [types.GlobalRegistry].
// Register behaviors there via [WithEventSourcing] or
// [ActorSystem.RegisterEventSourcedBehavior].
func (x *eventSourcedDependency) UnmarshalBinary(data []byte) error {
	var env internalpb.EventSourcedBehaviorEnvelope
	if err := proto.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("eventSourcedDependency: unmarshal envelope: %w", err)
	}

	typeName := env.GetTypeName()
	if typeName == "" {
		return fmt.Errorf("eventSourcedDependency: envelope has empty type name")
	}

	rtype, ok := types.GlobalRegistry.TypeOf(typeName)
	if !ok {
		return fmt.Errorf("eventSourcedDependency: behavior type %q not registered on this node", typeName)
	}

	inner, ok := reflect.New(rtype).Interface().(EventSourcedBehavior)
	if !ok {
		return fmt.Errorf("eventSourcedDependency: type %q does not implement EventSourcedBehavior", typeName)
	}

	x.inner = inner
	return nil
}
