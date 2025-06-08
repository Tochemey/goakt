/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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

package actor

import (
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/persistence"
)

// VirtualActor defines the contract that all virtual actors must implement to participate in the actor system.
//
// This interface specifies the lifecycle hooks and message handling logic required for building robust, stateful, and distributed actors.
// By implementing VirtualActor, you enable your actor to be managed, activated, deactivated, and messaged by the actor runtime.
//
// # Key Concepts
//
//   - **Single-threaded execution:** The actor runtime guarantees that only one method (OnActivate, OnDeactivate, or HandleMessage)
//     is called at a time for each actor instance. This eliminates the need for explicit locking or synchronization within your actor logic.
//   - **State persistence:** Actors can persist their state across activations and system restarts using the provided persistence.State.
//   - **Automatic lifecycle management:** The runtime activates actors on demand, deactivates them when idle, and manages resource cleanup.
//   - **Location transparency:** Actors can be addressed and messaged without knowledge of their physical location.
//
// # Implementation Guidelines
//
//  1. **State Management:** Define your actor's state by implementing the persistence.State interface, or use NoState for stateless actors.
//  2. **Activation:** Use OnActivate to load state, initialize resources, or set up subscriptions. This method is called when the actor is loaded into memory.
//  3. **Deactivation:** Use OnDeactivate to persist state and clean up resources. This method is called before the actor is removed from memory.
//  4. **Message Handling:** Implement HandleMessage to process incoming messages. All messages are delivered sequentially.
//
// # Example
//
//	type MyActor struct {
//	    persistence.State // Implements persistence.State for state management
//	}
//
//	func (a *MyActor) OnActivate(ctx *actor.VirtualContext, initialState persistence.State) error {
//	    // Load or initialize state, set up resources
//	    return nil
//	}
//
//	func (a *MyActor) OnDeactivate(ctx *actor.VirtualContext) error {
//	    // Persist state, clean up resources
//	    return nil
//	}
//
//	func (a *MyActor) HandleMessage(ctx *actor.VirtualContext, msg proto.Message) (proto.Message, error) {
//	    // Process message and optionally return a response
//	    return nil, nil
//	}
//
// # Notes
//
//   - Always respect the provided context for cancellation and deadlines.
//   - Avoid retaining references to VirtualContext or message objects beyond the scope of each method.
//   - Use the actor's logger (ctx.Logger()) for structured logging.
//
// See package documentation for more details and best practices.
type VirtualActor interface {
	// OnActivate is called when the actor is loaded into memory.
	//
	// This method is invoked by the actor runtime when:
	//   - The actor receives its first message after being idle
	//   - The actor is explicitly activated by the system
	//   - The actor is restored after a system restart
	//
	// Use this method to:
	//   - Load persistent state from storage
	//   - Initialize transient resources (connections, timers, etc.)
	//   - Set up subscriptions or external integrations
	//
	// If OnActivate returns an error, the actor activation fails and
	// subsequent messages will trigger retry attempts.
	//
	// Parameters:
	//   - ctx: The VirtualContext for this activation, providing access to state, logger, and cancellation.
	//   - initialState: The actor's persisted state loaded from storage, or the zero value of the actor state
	//     if no state was found (for example, on first activation). Use this to initialize your actor's state.
	//
	// Returns:
	//   - error: If non-nil, activation fails and will be retried by the runtime.
	OnActivate(ctx *VirtualContext, initialState persistence.State) error

	// OnDeactivate is called when the actor is being removed from memory.
	//
	// This method is invoked by the actor runtime when:
	//   - The actor has been idle beyond its configured timeout
	//   - The system is shutting down gracefully
	//   - Memory pressure requires evicting idle actors
	//
	// Use this method to:
	//   - Persist any cached state changes
	//   - Clean up transient resources (close connections, cancel timers)
	//   - Unsubscribe from external events
	//
	// The actor runtime waits for OnDeactivate to complete before
	// removing the actor from memory. Long-running cleanup should
	// respect the provided context for cancellation.
	//
	// Parameters:
	//   - ctx: The VirtualContext for this deactivation, providing access to state, logger, and cancellation.
	//
	// Returns:
	//   - error: If non-nil, the error will be logged but the actor will still be removed from memory.
	OnDeactivate(ctx *VirtualContext) error

	// HandleMessage handles an incoming message for this actor.
	//
	// Messages are processed sequentially - the actor runtime guarantees
	// that only one HandleMessage call is active at a time for each
	// actor instance. This eliminates the need for synchronization
	// within the actor's message handling logic.
	//
	// The message parameter contains the message payload.
	// It can be any protobuf message type, allowing for flexible
	// message structures defined by the application.
	//
	// The method should return a response message or an error. In case of fire-and-forget
	// semantics, the response can be nil. If an error occurs, it will be logged
	// by the actor runtime, and the actor will remain active for further messages.
	//
	// Parameters:
	//   - ctx: The VirtualContext for this message, providing access to state, logger, and cancellation.
	//   - message: The incoming message to be handled.
	//
	// Returns:
	//   - proto.Message: An optional response message (nil for fire-and-forget).
	//   - error: Any error encountered during message processing.
	HandleMessage(ctx *VirtualContext, message proto.Message) (proto.Message, error)
}
