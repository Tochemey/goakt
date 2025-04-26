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

// Actor defines the interface that all actors in the system must implement.
//
// Actors are lightweight, concurrent, and isolated units of computation that
// encapsulate both state and behavior. In this system, actors can be supervised,
// restarted on failure, or run in a distributed (remote) cluster-aware environment.
//
// Any struct implementing this interface must be immutable—i.e., all fields should
// be private (unexported) to ensure thread safety. Initialization should occur in the
// PreStart hook, not via exported fields or constructors.
//
// Actors follow a lifecycle composed of:
//   - PreStart: initialization before message processing begins.
//   - Receive: core behavior and message handling.
//   - PostStop: cleanup when the actor shuts down.
//
// **Supervision and Fault Tolerance**
// If an actor fails during message processing, its supervisor can decide how to handle the failure
// (e.g., restart, stop, escalate). If state recovery is needed (e.g., for persistent actors), it must
// be explicitly handled inside the PreStart hook, typically in response to a PostStart system message.
//
// **Clustering and Remoting**
// In a distributed deployment, actors can be remotely spawned, communicated with across nodes.
// Ensure that any resources initialized in PreStart are safe for clustered environments (e.g., stateless,
// replicated, or retryable).
type Actor interface {
	// PreStart is called once before the actor starts processing messages.
	//
	// Use this method to initialize dependencies such as database clients,
	// caches, or external service connections and persistent state recovery. If PreStart returns an error,
	// the actor will not be started, and the failure will be handled by its supervisor.
	PreStart(ctx *Context) error

	// Receive handles all messages sent to the actor's mailbox.
	//
	// This is the heart of the actor's behavior. Messages can include user-defined
	// commands/events as well as internal system messages such as PostStart or lifecycle signals.
	//
	// Actors can reply to messages using async messaging patterns or configure replies inline
	// where supported. Avoid heavy synchronous workflows as they may degrade throughput in high-load scenarios.
	//
	// Tip: Use pattern matching or typed message handlers to organize complex message workflows.
	Receive(ctx *ReceiveContext)

	// PostStop is called when the actor is about to shut down.
	//
	// This lifecycle hook is invoked after the actor has finished processing all messages
	// in its mailbox and is guaranteed to run before the actor is fully terminated.
	//
	// Use this method to perform final cleanup actions such as:
	//   - Releasing resources (e.g., database connections, goroutines, open files)
	//   - Flushing logs or metrics
	//   - Notifying other systems of termination (e.g., via events or pub/sub)
	//
	// This method is especially important passivated actors, as it is also
	// called during passivation when an idle actor is stopped to free up resources.
	//
	// Note: If PostStop returns an error, the error is logged but does not prevent the actor
	// from being stopped. Keep PostStop logic fast and resilient to avoid delaying system shutdowns.
	PostStop(ctx *Context) error
}
