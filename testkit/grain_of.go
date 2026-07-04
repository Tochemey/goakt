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

package testkit

import (
	"context"

	"github.com/stretchr/testify/require"

	goakt "github.com/tochemey/goakt/v4/actor"
)

// GrainOf retrieves or activates the Grain of kind T on the test kit's actor system
// and fails the test on error.
//
// T must be a pointer to a struct implementing the Grain interface, for example
// GrainOf[*UserGrain](ctx, kit, "user-123"). See actor.GrainOf for details.
func GrainOf[T goakt.Grain](ctx context.Context, kit *TestKit, name string, opts ...goakt.GrainOption) *goakt.GrainIdentity {
	require.True(kit.testingT, kit.started.Load(), "require testkit to be started")
	identity, err := goakt.GrainOf[T](ctx, kit.actorSystem, name, opts...)
	require.NoError(kit.testingT, err)
	require.NotNil(kit.testingT, identity)
	return identity
}

// NodeGrainOf retrieves or activates the Grain of kind T on the given cluster test node
// and fails the test on error.
//
// T must be a pointer to a struct implementing the Grain interface, for example
// NodeGrainOf[*UserGrain](ctx, node, "user-123"). See actor.GrainOf for details.
func NodeGrainOf[T goakt.Grain](ctx context.Context, node *TestNode, name string, opts ...goakt.GrainOption) *goakt.GrainIdentity {
	require.True(node.testingT, node.created.Load(), "cannot create a grain identity before the test node is created")
	identity, err := goakt.GrainOf[T](ctx, node.actorSystem, name, opts...)
	require.NoError(node.testingT, err)
	require.NotNil(node.testingT, identity)
	return identity
}
