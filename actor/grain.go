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

	"github.com/tochemey/goakt/v3/extension"
)

const (
	identitySeparator = "/"
)

// Grain defines the contract for grains (virtual actors) in the actor system.
//
// A Grain is a lightweight, virtual actor that is automatically activated and deactivated by the actor system.
// Grains provide location transparency, meaning they are addressed by identity rather than by physical location.
// Each grain instance processes messages sequentially, ensuring single-threaded execution and avoiding concurrency issues.
//
// Key properties of grains:
//   - **Activation/Deactivation:** Grains are activated on demand and deactivated when idle to conserve resources.
//   - **Location Transparency:** Grains are addressed by unique identity, not by network address or process.
//   - **Single-threaded Execution:** Each grain processes one message at a time, simplifying state management.
//   - **Lifecycle Hooks:** Grains can initialize and clean up resources using activation and deactivation hooks.
//   - **Dependency Injection:** Grains can declare dependencies that are injected by the runtime.
//
// Implementation guidelines:
//   - Implement `OnActivate` to initialize state or resources when the grain is loaded.
//   - Implement `OnDeactivate` to persist state and clean up resources before removal.
//   - Implement `Receive` to process incoming messages; only one message is processed at a time.
//   - Always respect the provided context for cancellation and deadlines.
//   - Do not retain references to context or message instances beyond the method scope.
//   - Use the `Dependencies` method to declare external dependencies required by the grain.
//
// Example:
//
//	type MyGrain struct{}
//
//	func (g *MyGrain) OnActivate(ctx *GrainContext) error {
//	    // Load state or initialize resources
//	    return nil
//	}
//
//	func (g *MyGrain) OnDeactivate(ctx *GrainContext) error {
//	    // Persist state or clean up resources
//	    return nil
//	}
//
//	func (g *MyGrain) Dependencies() []extension.Dependency {
//	    // Return required dependencies
//	    return nil
//	}
//
//	func (g *MyGrain) Receive(request *GrainRequest, opts ...GrainRequestOption) (*GrainResponse, error) {
//	    // Handle the incoming message and return a response
//	    return NewGrainResponse("ok"), nil
//	}
type Grain interface {
	// OnActivate is called when the grain is loaded into memory.
	// Use this to load state or initialize resources.
	//
	// The provided GrainContext contains grain identity and system references.
	// Return an error to indicate activation failure.
	OnActivate(ctx *GrainContext) error

	// OnDeactivate is called before the grain is removed from memory.
	// Use this to persist state and release resources.
	//
	// The provided GrainContext contains grain identity and system references.
	// Return an error to indicate deactivation failure.
	OnDeactivate(ctx *GrainContext) error

	// Dependencies returns a slice of external dependencies required by the grain.
	// These dependencies are injected by the actor system at activation time.
	Dependencies() []extension.Dependency

	// Receive processes an incoming message for the grain.
	// Returns a response or error. Only one call is active at a time.
	//
	// The request contains the message and sender information.
	// Use opts for additional request-scoped options.
	Receive(message proto.Message, option *GrainReceiveOption) (proto.Message, error)
}
