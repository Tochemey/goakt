package actor

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/extension"
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
