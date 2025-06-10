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
	identitySeparator = "/"
)

// Grain defines the contract for grains (virtual actors) in the actor system.
//
// A Grain is a virtual actor with automatic activation, deactivation, and location transparency.
// Grains are managed by the runtime, which handles their lifecycle and message dispatch.
// Each grain instance processes messages sequentially and is guaranteed single-threaded execution.
//
// Key properties of grains:
//   - Activated on demand and deactivated when idle.
//   - Messaged by identity, not by physical location (location transparent).
//   - Must always respond to messages or return an error.
//
// Implementation notes:
//   - Use OnActivate to initialize state or resources when the grain is loaded.
//   - Use OnDeactivate to persist state and clean up resources before removal.
//   - Implement HandleRequest to process incoming messages; messages are delivered one at a time.
//   - Always respect the provided context for cancellation and deadlines.
//   - Do not retain references to context or message instances beyond the method scope.
type Grain interface {
	// OnActivate is called when the grain is loaded into memory.
	// Use this to load state or initialize resources.
	OnActivate(ctx *GrainContext, opts ...GrainOption) error

	// OnDeactivate is called before the grain is removed from memory.
	// Use this to persist state and release resources.
	OnDeactivate(ctx *GrainContext) error

	// HandleRequest processes an incoming message for the grain.
	// Returns a response or error. Only one call is active at a time.
	HandleRequest(request *GrainRequest) (*GrainResponse, error)
}
