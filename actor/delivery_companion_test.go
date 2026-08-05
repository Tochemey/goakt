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
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/passivation"
)

// newCompanionTestSystem starts a cluster-disabled actor system for
// companion-resolution tests and stops it when the test finishes.
func newCompanionTestSystem(t *testing.T) (context.Context, *actorSystem) {
	t.Helper()

	ctx := context.TODO()
	system, err := NewActorSystem("companionTest", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, system.Start(ctx))

	t.Cleanup(func() {
		require.NoError(t, system.Stop(context.WithoutCancel(ctx)))
	})

	return ctx, system.(*actorSystem)
}

func TestReliableCompanionName(t *testing.T) {
	incarnationID := uuid.NewString()

	assert.Equal(t, reliableProducerControllerNamePrefix+incarnationID, reliableCompanionName(ReliableControllerRoleProducer, incarnationID))
	assert.Equal(t, reliableConsumerControllerNamePrefix+incarnationID, reliableCompanionName(ReliableControllerRoleConsumer, incarnationID))
	assert.Empty(t, reliableCompanionName(reliableControllerRoleUnknown, incarnationID))
	assert.True(t, isReliableDeliveryControllerName(reliableCompanionName(ReliableControllerRoleProducer, incarnationID)))
	assert.True(t, isReliableDeliveryControllerName(reliableCompanionName(ReliableControllerRoleConsumer, incarnationID)))
	assert.True(t, isSystemName(reliableCompanionName(ReliableControllerRoleProducer, incarnationID)))
}

func TestNewReliableCompanionSpec(t *testing.T) {
	t.Run("With valid inputs", func(t *testing.T) {
		spec, err := newReliableCompanionSpec(ReliableControllerRoleProducer, "endpoint", uuid.NewString())
		require.NoError(t, err)
		require.NotNil(t, spec)
		assert.Equal(t, ReliableControllerRoleProducer, spec.role)
		assert.Equal(t, "endpoint", spec.endpointName)
	})

	t.Run("With unsupported role", func(t *testing.T) {
		spec, err := newReliableCompanionSpec(reliableControllerRoleUnknown, "endpoint", uuid.NewString())
		require.Error(t, err)
		assert.Nil(t, spec)
	})

	t.Run("With blank endpoint name", func(t *testing.T) {
		spec, err := newReliableCompanionSpec(ReliableControllerRoleConsumer, "  ", uuid.NewString())
		require.Error(t, err)
		assert.Nil(t, spec)
	})

	t.Run("With invalid incarnation ID", func(t *testing.T) {
		spec, err := newReliableCompanionSpec(ReliableControllerRoleConsumer, "endpoint", "not-a-uuid")
		require.Error(t, err)
		assert.Nil(t, spec)
	})
}

func TestResolveReliableCompanion(t *testing.T) {
	t.Run("With cluster-disabled local resolution", func(t *testing.T) {
		ctx, system := newCompanionTestSystem(t)

		endpoint, err := system.Spawn(ctx, "endpoint", NewMockActor())
		require.NoError(t, err)

		spec, err := newReliableCompanionSpec(ReliableControllerRoleProducer, "endpoint", endpoint.IncarnationID())
		require.NoError(t, err)

		companionName := reliableCompanionName(ReliableControllerRoleProducer, endpoint.IncarnationID())
		companion, err := system.Spawn(ctx, companionName, NewMockActor(), asSystem(), asReliableCompanion(spec))
		require.NoError(t, err)

		resolved, err := system.resolveReliableCompanion("endpoint", ReliableControllerRoleProducer)
		require.NoError(t, err)
		assert.True(t, companion.Equals(resolved))
	})

	t.Run("With unsupported role", func(t *testing.T) {
		_, system := newCompanionTestSystem(t)

		resolved, err := system.resolveReliableCompanion("endpoint", reliableControllerRoleUnknown)
		require.Error(t, err)
		assert.Nil(t, resolved)
		assert.NotErrorIs(t, err, errReliableCompanionUnavailable)
	})

	t.Run("With missing endpoint", func(t *testing.T) {
		_, system := newCompanionTestSystem(t)

		resolved, err := system.resolveReliableCompanion("endpoint", ReliableControllerRoleProducer)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.Nil(t, resolved)
	})

	t.Run("With missing companion", func(t *testing.T) {
		ctx, system := newCompanionTestSystem(t)

		_, err := system.Spawn(ctx, "endpoint", NewMockActor())
		require.NoError(t, err)

		resolved, err := system.resolveReliableCompanion("endpoint", ReliableControllerRoleConsumer)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.Nil(t, resolved)
	})

	t.Run("With unmarked companion", func(t *testing.T) {
		ctx, system := newCompanionTestSystem(t)

		endpoint, err := system.Spawn(ctx, "endpoint", NewMockActor())
		require.NoError(t, err)

		companionName := reliableCompanionName(ReliableControllerRoleProducer, endpoint.IncarnationID())
		_, err = system.Spawn(ctx, companionName, NewMockActor(), asSystem())
		require.NoError(t, err)

		resolved, err := system.resolveReliableCompanion("endpoint", ReliableControllerRoleProducer)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.Nil(t, resolved)
	})

	t.Run("With mismatched role metadata", func(t *testing.T) {
		ctx, system := newCompanionTestSystem(t)

		endpoint, err := system.Spawn(ctx, "endpoint", NewMockActor())
		require.NoError(t, err)

		spec, err := newReliableCompanionSpec(ReliableControllerRoleConsumer, "endpoint", endpoint.IncarnationID())
		require.NoError(t, err)

		companionName := reliableCompanionName(ReliableControllerRoleProducer, endpoint.IncarnationID())
		_, err = system.Spawn(ctx, companionName, NewMockActor(), asSystem(), asReliableCompanion(spec))
		require.NoError(t, err)

		resolved, err := system.resolveReliableCompanion("endpoint", ReliableControllerRoleProducer)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.Nil(t, resolved)
	})

	t.Run("With foreign endpoint metadata", func(t *testing.T) {
		ctx, system := newCompanionTestSystem(t)

		endpoint, err := system.Spawn(ctx, "endpoint", NewMockActor())
		require.NoError(t, err)

		spec, err := newReliableCompanionSpec(ReliableControllerRoleProducer, "other", endpoint.IncarnationID())
		require.NoError(t, err)

		companionName := reliableCompanionName(ReliableControllerRoleProducer, endpoint.IncarnationID())
		_, err = system.Spawn(ctx, companionName, NewMockActor(), asSystem(), asReliableCompanion(spec))
		require.NoError(t, err)

		resolved, err := system.resolveReliableCompanion("endpoint", ReliableControllerRoleProducer)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.Nil(t, resolved)
	})

	t.Run("With stale incarnation metadata", func(t *testing.T) {
		ctx, system := newCompanionTestSystem(t)

		endpoint, err := system.Spawn(ctx, "endpoint", NewMockActor())
		require.NoError(t, err)

		spec, err := newReliableCompanionSpec(ReliableControllerRoleProducer, "endpoint", uuid.NewString())
		require.NoError(t, err)

		companionName := reliableCompanionName(ReliableControllerRoleProducer, endpoint.IncarnationID())
		_, err = system.Spawn(ctx, companionName, NewMockActor(), asSystem(), asReliableCompanion(spec))
		require.NoError(t, err)

		resolved, err := system.resolveReliableCompanion("endpoint", ReliableControllerRoleProducer)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.Nil(t, resolved)
	})

	t.Run("With stopped companion", func(t *testing.T) {
		ctx, system := newCompanionTestSystem(t)

		endpoint, err := system.Spawn(ctx, "endpoint", NewMockActor())
		require.NoError(t, err)

		spec, err := newReliableCompanionSpec(ReliableControllerRoleProducer, "endpoint", endpoint.IncarnationID())
		require.NoError(t, err)

		companion, err := system.Spawn(ctx, "companionStandIn", NewMockActor(), asReliableCompanion(spec))
		require.NoError(t, err)
		require.NoError(t, companion.Shutdown(ctx))

		err = validateReliableCompanion(endpoint, companion, ReliableControllerRoleProducer)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
	})
}

func TestReliableCompanionHiddenFromPublicAPIs(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)

	endpoint, err := system.Spawn(ctx, "endpoint", NewMockActor())
	require.NoError(t, err)

	spec, err := newReliableCompanionSpec(ReliableControllerRoleConsumer, "endpoint", endpoint.IncarnationID())
	require.NoError(t, err)

	companionName := reliableCompanionName(ReliableControllerRoleConsumer, endpoint.IncarnationID())
	_, err = system.Spawn(ctx, companionName, NewMockActor(), asSystem(), asReliableCompanion(spec))
	require.NoError(t, err)

	_, err = system.ActorOf(ctx, companionName)
	assert.ErrorIs(t, err, gerrors.ErrActorNotFound)

	actors, err := system.Actors(ctx, time.Second)
	require.NoError(t, err)

	for _, pid := range actors {
		assert.NotEqual(t, companionName, pid.Name())
	}

	assert.ErrorIs(t, system.Kill(ctx, companionName), gerrors.ErrActorNotFound)

	_, err = system.ReSpawn(ctx, companionName)
	assert.ErrorIs(t, err, gerrors.ErrActorNotFound)
}

func TestReliableEndpointDefaults(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)

	config := producerDeliveryConfig("consumer")
	endpoint, err := system.Spawn(ctx, "endpoint", NewMockActor(), asReliableEndpoint(config))
	require.NoError(t, err)

	assert.IsType(t, new(passivation.LongLivedStrategy), endpoint.passivationStrategy)
	assert.Equal(t, config, endpoint.reliableDelivery)
	assert.NotSame(t, config, endpoint.reliableDelivery)

	spec, err := newReliableCompanionSpec(ReliableControllerRoleProducer, "endpoint", endpoint.IncarnationID())
	require.NoError(t, err)

	companionName := reliableCompanionName(ReliableControllerRoleProducer, endpoint.IncarnationID())
	companion, err := system.Spawn(ctx, companionName, NewMockActor(), asSystem(), asReliableCompanion(spec))
	require.NoError(t, err)

	assert.IsType(t, new(passivation.LongLivedStrategy), companion.passivationStrategy)
}

func TestToSerializeCarriesReliableDelivery(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)

	config := producerDeliveryConfig("consumer")
	endpoint, err := system.Spawn(ctx, "endpoint", NewMockActor(), asReliableEndpoint(config))
	require.NoError(t, err)

	// mutating the caller-owned configuration must not leak into the PID snapshot
	config.producer.consumerName = "changed"

	serialized, err := endpoint.toSerialize()
	require.NoError(t, err)

	assert.Equal(t, endpoint.IncarnationID(), serialized.GetIncarnationId())
	assert.Equal(t, "consumer", serialized.GetReliableDelivery().GetProducer().GetConsumerName())

	plain, err := system.Spawn(ctx, "plain", NewMockActor())
	require.NoError(t, err)

	plainSerialized, err := plain.toSerialize()
	require.NoError(t, err)
	assert.Nil(t, plainSerialized.GetReliableDelivery())
	assert.Equal(t, plain.IncarnationID(), plainSerialized.GetIncarnationId())
}
