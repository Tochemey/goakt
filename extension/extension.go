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

package extension

// Extension defines a pluggable interface that can be injected into the ActorSystem
// to extend its core functionality with custom or domain-specific behavior.
//
// Extensions provide a powerful mechanism to augment the actor system with capabilities
// not available out-of-the-box—such as support for event sourcing, metrics, distributed tracing,
// or dependency injection.
//
// Once registered, an Extension becomes accessible from any actor's message context,
// making it easy to share cross-cutting concerns across the system.
//
// Example use case:
//   - Injecting an event sourcing engine
//   - Registering a metrics recorder
//   - Adding a service registry client
//
// Extensions must implement the ID method to uniquely identify themselves within the ActorSystem.
type Extension interface {
	// ID returns the unique identifier for the extension.
	//
	// The identifier must:
	//   - Be no more than 255 characters long.
	//   - Start with an alphanumeric character [a-zA-Z0-9].
	//   - Contain only alphanumeric characters, hyphens (-), or underscores (_) thereafter.
	//
	// Identifiers that do not meet these constraints are considered invalid.
	ID() string
}
