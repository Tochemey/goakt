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

const (
	grainKeySeparator = ":"
)

// Grain defines the contract that all grains (virtual actors) must implement to participate in the actor system.
// This interface specifies the lifecycle hooks and message handling logic required
// for building robust, stateful, and distributed grains. By implementing Grain,
// your actor becomes manageable by the runtime, which handles its activation,
// deactivation, and message dispatch.
//
// Key Concepts
//
//   - Single-threaded execution: The actor runtime guarantees that only one method
//     (`OnActivate`, `OnDeactivate`, or `Receive`) is invoked at a time for each
//     grain instance. This removes the need for explicit locking or synchronization within
//     grain logic.
//   - State snapshot persistence: Grains can persist state across activations and deactivations
//     by implementing the `persistence.Snapshot` interface.
//   - Automatic lifecycle management: Grains are activated on demand, deactivated when idle,
//     and automatically cleaned up by the runtime.
//   - Location transparency: Grains are messaged by identity, not by physical location,
//     supporting scalability and fault-tolerance.
//
// Implementation Guidelines
//
//  1. State Management: Define your grain's state by implementing `persistence.Snapshot`.
//     Stateless grains can embed `persistence.NoState`.
//  2. Activation: Use `OnActivate` to load state, initialize resources, or set up subscriptions.
//     This is called when the grain is first loaded into memory.
//  3. Deactivation: Use `OnDeactivate` to persist state and clean up resources.
//     This is called before the grain is removed from memory.
//  4. Message Handling: Implement `Receive` to process messages sent to the grain.
//     Messages are delivered sequentially and handled in isolation.
//
// Example
//
//	type MyGrain struct {
//	    persistence.Snapshot // Implements state snapshot persistence
//	}
//
//	func (g *MyGrain) OnActivate(ctx *actor.Context) error {
//	    // Initialize or restore grain state
//	    return nil
//	}
//
//	func (g *MyGrain) OnDeactivate(ctx *actor.Context) error {
//	    // Persist state and clean up
//	    return nil
//	}
//
//	func (g *MyGrain) Receive(ctx context.Context, msg proto.Message) (proto.Message, error) {
//	    // Handle incoming message
//	    return nil, nil
//	}
//
// Notes
//
//   - Always respect the provided `Context` for cancellation and deadlines.
//   - Do not retain references to `Context` or `proto.Message` instances beyond the method scope.
//   - Use `ctx.Logger()` for structured logging.
//   - See package documentation for additional design guidance.
type Grain interface {
	// OnActivate is called when the grain is loaded into memory.
	//
	// This method is invoked by the actor runtime in the following situations:
	//   - The grain receives its first message after being idle
	//   - The grain is explicitly activated by the system
	//   - The grain is restored after a system restart
	//
	// Use this method to:
	//   - Load persistent state from storage
	//   - Initialize transient resources (e.g., network connections, timers)
	//   - Subscribe to external events or systems
	//
	// If an error is returned, activation fails, and the runtime will retry
	// activation on subsequent messages.
	OnActivate(ctx *GrainContext, opts ...GrainOption) error

	// OnDeactivate is called before the grain is removed from memory.
	//
	// This method is invoked by the actor runtime when:
	//   - The grain remains idle beyond its configured timeout
	//   - The system is shutting down gracefully
	//   - The runtime is reclaiming memory under pressure
	//
	// Use this method to:
	//   - Persist any in-memory state changes
	//   - Release transient resources (e.g., close connections, cancel timers)
	//   - Unsubscribe from external systems
	OnDeactivate(ctx *GrainContext) error

	// HandleRequest processes an incoming message for the grain.
	//
	// The runtime ensures single-threaded message processing — only one call
	// to `Receive` is active at any time per grain instance. This eliminates
	// the need for synchronization in grain code.
	//
	// This method can:
	//   - Modify the grain's state
	//   - Send back a response (or return nil for fire-and-forget)
	//   - Trigger side effects or follow-up actions
	HandleRequest(request *GrainRequest) (*GrainResponse, error)
}
