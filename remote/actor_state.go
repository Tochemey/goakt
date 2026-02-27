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

package remote

// ActorState represents a queryable aspect of an actor's lifecycle or configuration
// on a remote node. It mirrors the internalpb.State proto enum and is used as a
// parameter to RemoteState to specify which state to check.
//
// Each value corresponds to a predicate that can be evaluated against a remote actor:
// the server returns true if the actor satisfies that predicate, false otherwise.
// Use ActorStateRunning to check if an actor is active and processing messages.
//
// Wire format: Values align with the proto State enum (0-5) for direct mapping
// when sending RemoteStateRequest over the wire.
//
// Example:
//
//	state, err := client.RemoteState(ctx, host, port, "my-actor", remote.ActorStateRunning)
//	if err != nil {
//	    return err
//	}
//	if state {
//	    // actor is running
//	}
type ActorState uint32

const (
	// ActorStateUnknown is the zero value and indicates an unspecified or unrecognized
	// state. When used in RemoteState, the server typically returns false.
	// Prefer one of the concrete states for explicit checks.
	ActorStateUnknown ActorState = 0

	// ActorStateRunning indicates the actor is active and processing messages.
	// The server returns true if pid.IsRunning(), false otherwise.
	// Use this to verify an actor is alive before sending messages or performing
	// operations that require an active actor.
	ActorStateRunning ActorState = 1

	// ActorStateSuspended indicates the actor has been suspended (e.g., via
	// passivation or explicit suspend). The server returns true if pid.IsSuspended().
	// A suspended actor does not process messages until reinstated.
	ActorStateSuspended ActorState = 2

	// ActorStateStopping indicates the actor is in the process of shutting down.
	// The server returns true if pid.IsStopping(). Use this to detect actors
	// that are being terminated and may not accept new work.
	ActorStateStopping ActorState = 3

	// ActorStateRelocatable indicates the actor is configured for relocation.
	// The server returns true if pid.IsRelocatable(). Relocatable actors can
	// be moved to another node on failure or for load balancing.
	ActorStateRelocatable ActorState = 4

	// ActorStateSingleton indicates the actor is a cluster singleton.
	// The server returns true if pid.IsSingleton(). Only one instance of a
	// singleton actor exists across the cluster at any time.
	ActorStateSingleton ActorState = 5
)

// String returns a human-readable representation of the state for logging and debugging.
func (s ActorState) String() string {
	switch s {
	case ActorStateUnknown:
		return "unknown"
	case ActorStateRunning:
		return "running"
	case ActorStateSuspended:
		return "suspended"
	case ActorStateStopping:
		return "stopping"
	case ActorStateRelocatable:
		return "relocatable"
	case ActorStateSingleton:
		return "singleton"
	default:
		return "unknown"
	}
}
