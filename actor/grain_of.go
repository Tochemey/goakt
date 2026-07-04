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
	"reflect"

	gerrors "github.com/tochemey/goakt/v4/errors"
)

// GrainOf retrieves or activates the Grain (virtual actor) of kind T identified by the given name.
//
// T must be a pointer to a struct implementing the Grain interface, for example
// GrainOf[*UserGrain]. The grain kind is derived from T via reflection, so no factory
// and no grain instance are required to resolve the identity. The kind is automatically
// registered in the local registry, which makes remote activation, recreation and
// relocation of the grain possible on this node without a prior RegisterGrainKind call.
//
// When the grain is not yet active anywhere, it is instantiated as a zero value of T and
// activated. Initialization belongs in the grain's OnActivate hook; external resources are
// supplied through WithGrainDependencies and retrieved from the GrainProps passed to
// OnActivate. This is the same construction contract the cluster already applies when it
// recreates or relocates grains on other nodes.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control.
//   - system: The actor system on which the grain is resolved or activated.
//   - name: The unique name identifying the Grain instance within its kind.
//   - opts: Optional configuration options for the Grain (e.g., activation timeout, retries).
//
// Returns:
//   - *GrainIdentity: The identity object representing the located or newly activated Grain.
//   - error: ErrInvalidGrainKind when T is not a pointer to a struct, ErrActorSystemNotStarted
//     when the system is nil or not running, or any activation error.
//
// Example:
//
//	identity, err := actor.GrainOf[*UserGrain](ctx, system, "user-123")
//	if err != nil {
//	    return err
//	}
//
//	response, err := system.AskGrain(ctx, identity, request, time.Second)
func GrainOf[T Grain](ctx context.Context, system ActorSystem, name string, opts ...GrainOption) (*GrainIdentity, error) {
	if system == nil {
		return nil, gerrors.ErrActorSystemNotStarted
	}

	rtype := reflect.TypeFor[T]()
	if rtype.Kind() != reflect.Pointer || rtype.Elem().Kind() != reflect.Struct {
		return nil, gerrors.ErrInvalidGrainKind
	}

	var grainType T
	return system.grainOf(ctx, grainType, name, opts...)
}
