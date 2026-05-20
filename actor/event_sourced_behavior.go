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

	"github.com/tochemey/goakt/v4/extension"
)

// EventSourcedBehavior defines the contract for an event-sourced actor.
//
// All mutable state lives in the value returned by InitialState and is evolved
// by successive HandleEvent calls. HandleEvent runs during live processing and
// during recovery replay, so it must be a pure function — identical inputs must
// produce identical outputs.
//
// EventSourcedBehavior embeds [extension.Dependency] so the framework can ship
// the behavior across nodes during cluster relocation: the behavior travels in
// the spawn request as a dependency and is reconstructed on the target node via
// its BinaryUnmarshaler. Implementations whose fields are entirely zero-valued
// can return nil/empty from MarshalBinary and ignore UnmarshalBinary; behaviors
// that carry per-actor configuration must serialize that configuration.
type EventSourcedBehavior interface {
	extension.Dependency

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
