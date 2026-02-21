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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/extension"
)

func TestNewGrainPropsStoresFields(t *testing.T) {
	t.Parallel()

	identity := newGrainIdentity(NewMockGrain(), "grain-props")
	sys := &actorSystem{name: "grain-system"}
	deps := []extension.Dependency{
		NewMockDependency("dep-1", "user-1", "email-1"),
		NewMockDependency("dep-2", "user-2", "email-2"),
	}

	props := newGrainProps(identity, sys, deps)
	require.Equal(t, identity, props.Identity())
	require.Equal(t, sys, props.ActorSystem())
	require.Equal(t, deps, props.Dependencies())
}

func TestGrainPropsDependenciesNilSafe(t *testing.T) {
	t.Parallel()

	identity := newGrainIdentity(NewMockGrain(), "nil-deps")
	sys := &actorSystem{name: "grain-system"}

	props := newGrainProps(identity, sys, nil)
	require.Equal(t, identity, props.Identity())
	require.Equal(t, sys, props.ActorSystem())
	require.Nil(t, props.Dependencies())
}
