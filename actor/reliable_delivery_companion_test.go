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
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/datacenter"
	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/cluster"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	dynaport "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/remoteclient"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
	mockscluster "github.com/tochemey/goakt/v4/mocks/cluster"
	mocksremote "github.com/tochemey/goakt/v4/mocks/remoteclient"
	"github.com/tochemey/goakt/v4/passivation"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
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

func TestReliableCompanionSpecWireRoundTrip(t *testing.T) {
	t.Run("With a valid spec", func(t *testing.T) {
		spec, err := newReliableCompanionSpec(ReliableControllerRoleProducer, "endpoint", uuid.NewString())
		require.NoError(t, err)

		restored, err := reliableCompanionSpecFromProto(spec.toProto())
		require.NoError(t, err)
		assert.Equal(t, spec, restored)
	})

	t.Run("With a nil spec", func(t *testing.T) {
		var spec *reliableCompanionSpec
		assert.Nil(t, spec.toProto())

		restored, err := reliableCompanionSpecFromProto(nil)
		require.Error(t, err)
		assert.Nil(t, restored)
	})

	t.Run("With an unspecified role", func(t *testing.T) {
		spec, err := newReliableCompanionSpec(ReliableControllerRoleConsumer, "endpoint", uuid.NewString())
		require.NoError(t, err)

		tampered := spec.toProto()
		tampered.Role = 0

		restored, err := reliableCompanionSpecFromProto(tampered)
		require.Error(t, err)
		assert.Nil(t, restored)
	})

	t.Run("With a tampered incarnation", func(t *testing.T) {
		spec, err := newReliableCompanionSpec(ReliableControllerRoleConsumer, "endpoint", uuid.NewString())
		require.NoError(t, err)

		tampered := spec.toProto()
		tampered.EndpointIncarnationId = "not-a-uuid"

		restored, err := reliableCompanionSpecFromProto(tampered)
		require.Error(t, err)
		assert.Nil(t, restored)
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

		resolved, err := system.resolveReliableCompanion(ctx, "endpoint", ReliableControllerRoleProducer, nil)
		require.NoError(t, err)
		assert.True(t, companion.Equals(resolved))
	})

	t.Run("With unsupported role", func(t *testing.T) {
		ctx, system := newCompanionTestSystem(t)

		resolved, err := system.resolveReliableCompanion(ctx, "endpoint", reliableControllerRoleUnknown, nil)
		require.Error(t, err)
		assert.Nil(t, resolved)
		assert.NotErrorIs(t, err, errReliableCompanionUnavailable)
	})

	t.Run("With missing endpoint", func(t *testing.T) {
		ctx, system := newCompanionTestSystem(t)

		resolved, err := system.resolveReliableCompanion(ctx, "endpoint", ReliableControllerRoleProducer, nil)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.Nil(t, resolved)
	})

	t.Run("With missing companion", func(t *testing.T) {
		ctx, system := newCompanionTestSystem(t)

		_, err := system.Spawn(ctx, "endpoint", NewMockActor())
		require.NoError(t, err)

		resolved, err := system.resolveReliableCompanion(ctx, "endpoint", ReliableControllerRoleConsumer, nil)
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

		resolved, err := system.resolveReliableCompanion(ctx, "endpoint", ReliableControllerRoleProducer, nil)
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

		resolved, err := system.resolveReliableCompanion(ctx, "endpoint", ReliableControllerRoleProducer, nil)
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

		resolved, err := system.resolveReliableCompanion(ctx, "endpoint", ReliableControllerRoleProducer, nil)
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

		resolved, err := system.resolveReliableCompanion(ctx, "endpoint", ReliableControllerRoleProducer, nil)
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

	t.Run("With stopped endpoint", func(t *testing.T) {
		ctx, system := newCompanionTestSystem(t)

		endpoint, err := system.Spawn(ctx, "endpoint", NewMockActor())
		require.NoError(t, err)

		spec, err := newReliableCompanionSpec(ReliableControllerRoleProducer, "endpoint", endpoint.IncarnationID())
		require.NoError(t, err)

		companionName := reliableCompanionName(ReliableControllerRoleProducer, endpoint.IncarnationID())
		_, err = system.Spawn(ctx, companionName, NewMockActor(), asSystem(), asReliableCompanion(spec))
		require.NoError(t, err)

		endpoint.setState(runningState, false)

		resolved, err := system.resolveReliableCompanion(ctx, "endpoint", ReliableControllerRoleProducer, nil)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.ErrorContains(t, err, "is not running")
		assert.Nil(t, resolved)
	})
}

func TestResolveRemoteReliableCompanion(t *testing.T) {
	incarnationID := uuid.NewString()
	companionName := reliableCompanionName(ReliableControllerRoleProducer, incarnationID)
	remoteHostPort := "10.0.0.2:9000"
	localHostPort := "127.0.0.1:8080"

	validSpec, err := newReliableCompanionSpec(ReliableControllerRoleProducer, "endpoint", incarnationID)
	require.NoError(t, err)

	endpointRecord := func(hostPort string) *internalpb.Actor {
		return &internalpb.Actor{
			Address:       "goakt://test-replication@" + hostPort + "/endpoint",
			IncarnationId: incarnationID,
		}
	}

	companionRecord := func(hostPort string, spec *internalpb.ReliableCompanionSpec) *internalpb.Actor {
		return &internalpb.Actor{
			Address:           "goakt://test-replication@" + hostPort + "/" + companionName,
			IncarnationId:     incarnationID,
			ReliableCompanion: spec,
		}
	}

	t.Run("With no registry endpoint", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		clusterMock.EXPECT().GetActor(mock.Anything, "endpoint").Return(nil, cluster.ErrActorNotFound)

		resolved, err := system.resolveRemoteReliableCompanion(context.Background(), "endpoint", ReliableControllerRoleProducer, nil)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.ErrorContains(t, err, "no registry record")
		assert.Nil(t, resolved)
	})

	t.Run("With no published companion", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		clusterMock.EXPECT().GetActor(mock.Anything, "endpoint").Return(endpointRecord(remoteHostPort), nil)
		clusterMock.EXPECT().GetActor(mock.Anything, companionName).Return(nil, cluster.ErrActorNotFound)

		resolved, err := system.resolveRemoteReliableCompanion(context.Background(), "endpoint", ReliableControllerRoleProducer, nil)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.ErrorContains(t, err, "no published")
		assert.Nil(t, resolved)
	})

	t.Run("With missing companion spec", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		clusterMock.EXPECT().GetActor(mock.Anything, "endpoint").Return(endpointRecord(remoteHostPort), nil)
		clusterMock.EXPECT().GetActor(mock.Anything, companionName).Return(companionRecord(remoteHostPort, nil), nil)

		resolved, err := system.resolveRemoteReliableCompanion(context.Background(), "endpoint", ReliableControllerRoleProducer, nil)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.ErrorContains(t, err, "is not a runtime companion")
		assert.Nil(t, resolved)
	})

	t.Run("With wrong companion role", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		wrongSpec, err := newReliableCompanionSpec(ReliableControllerRoleConsumer, "endpoint", incarnationID)
		require.NoError(t, err)

		clusterMock.EXPECT().GetActor(mock.Anything, "endpoint").Return(endpointRecord(remoteHostPort), nil)
		clusterMock.EXPECT().GetActor(mock.Anything, companionName).Return(companionRecord(remoteHostPort, wrongSpec.toProto()), nil)

		resolved, err := system.resolveRemoteReliableCompanion(context.Background(), "endpoint", ReliableControllerRoleProducer, nil)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.ErrorContains(t, err, "runs role=")
		assert.Nil(t, resolved)
	})

	t.Run("With wrong owner endpoint", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		wrongOwner, err := newReliableCompanionSpec(ReliableControllerRoleProducer, "other", incarnationID)
		require.NoError(t, err)

		clusterMock.EXPECT().GetActor(mock.Anything, "endpoint").Return(endpointRecord(remoteHostPort), nil)
		clusterMock.EXPECT().GetActor(mock.Anything, companionName).Return(companionRecord(remoteHostPort, wrongOwner.toProto()), nil)

		resolved, err := system.resolveRemoteReliableCompanion(context.Background(), "endpoint", ReliableControllerRoleProducer, nil)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.ErrorContains(t, err, "is owned by endpoint=")
		assert.Nil(t, resolved)
	})

	t.Run("With incarnation mismatch", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		staleSpec, err := newReliableCompanionSpec(ReliableControllerRoleProducer, "endpoint", uuid.NewString())
		require.NoError(t, err)

		clusterMock.EXPECT().GetActor(mock.Anything, "endpoint").Return(endpointRecord(remoteHostPort), nil)
		clusterMock.EXPECT().GetActor(mock.Anything, companionName).Return(companionRecord(remoteHostPort, staleSpec.toProto()), nil)

		resolved, err := system.resolveRemoteReliableCompanion(context.Background(), "endpoint", ReliableControllerRoleProducer, nil)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.ErrorContains(t, err, "is bound to incarnation=")
		assert.Nil(t, resolved)
	})

	t.Run("With invalid registry address", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		clusterMock.EXPECT().GetActor(mock.Anything, "endpoint").Return(&internalpb.Actor{
			Address:       "not-an-address",
			IncarnationId: incarnationID,
		}, nil)
		clusterMock.EXPECT().GetActor(mock.Anything, companionName).Return(companionRecord(remoteHostPort, validSpec.toProto()), nil)

		resolved, err := system.resolveRemoteReliableCompanion(context.Background(), "endpoint", ReliableControllerRoleProducer, nil)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.ErrorContains(t, err, "invalid address")
		assert.Nil(t, resolved)
	})

	t.Run("With split nodes", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		clusterMock.EXPECT().GetActor(mock.Anything, "endpoint").Return(endpointRecord("10.0.0.1:9000"), nil)
		clusterMock.EXPECT().GetActor(mock.Anything, companionName).Return(companionRecord(remoteHostPort, validSpec.toProto()), nil)

		resolved, err := system.resolveRemoteReliableCompanion(context.Background(), "endpoint", ReliableControllerRoleProducer, nil)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.ErrorContains(t, err, "live on different nodes")
		assert.Nil(t, resolved)
	})

	t.Run("With self-reference", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		clusterMock.EXPECT().GetActor(mock.Anything, "endpoint").Return(endpointRecord(localHostPort), nil)
		clusterMock.EXPECT().GetActor(mock.Anything, companionName).Return(companionRecord(localHostPort, validSpec.toProto()), nil)

		resolved, err := system.resolveRemoteReliableCompanion(context.Background(), "endpoint", ReliableControllerRoleProducer, nil)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.ErrorContains(t, err, "point at this node")
		assert.Nil(t, resolved)
	})

	t.Run("With happy remote path", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		clusterMock.EXPECT().GetActor(mock.Anything, "endpoint").Return(endpointRecord(remoteHostPort), nil)
		clusterMock.EXPECT().GetActor(mock.Anything, companionName).Return(companionRecord(remoteHostPort, validSpec.toProto()), nil)

		resolved, err := system.resolveRemoteReliableCompanion(context.Background(), "endpoint", ReliableControllerRoleProducer, nil)
		require.NoError(t, err)
		require.NotNil(t, resolved)
		assert.True(t, resolved.IsRemote())
		assert.Equal(t, companionName, resolved.Name())
	})
}

func TestEnsureReliableCompanionEdges(t *testing.T) {
	t.Run("With terminating companion", func(t *testing.T) {
		ctx, system := newCompanionTestSystem(t)

		endpoint, err := system.Spawn(ctx, "orders", &reliableProducerMock{}, AsReliableProducer("orders-consumer"))
		require.NoError(t, err)

		companion, err := system.resolveReliableCompanion(ctx, "orders", ReliableControllerRoleProducer, nil)
		require.NoError(t, err)
		companion.setState(runningState, false)

		err = system.ensureReliableCompanion(ctx, endpoint)
		require.ErrorContains(t, err, "is still terminating")
	})

	t.Run("With invalid endpoint incarnation", func(t *testing.T) {
		ctx, system := newCompanionTestSystem(t)

		addr := address.NewReference("orders", system.Name(), "127.0.0.1", 0)
		endpoint := &PID{
			address:          addr,
			path:             newPath(addr),
			actorSystem:      system,
			reliableDelivery: producerDeliveryConfig("orders-consumer"),
		}

		err := system.ensureReliableCompanion(ctx, endpoint)
		require.Error(t, err)
	})
}

func TestRollbackReliableSpawnClusterCleanup(t *testing.T) {
	newStoppedEndpoint := func(system *actorSystem) *PID {
		addr := address.New("orders", system.name, "127.0.0.1", 8080)
		endpoint := &PID{
			address:          addr,
			path:             newPath(addr),
			actorSystem:      system,
			logger:           log.DiscardLogger,
			reliableDelivery: producerDeliveryConfig("orders-consumer"),
		}
		endpoint.setState(runningState, false)
		return endpoint
	}

	t.Run("With cluster disabled", func(t *testing.T) {
		ctx, system := newCompanionTestSystem(t)
		endpoint := newStoppedEndpoint(system)
		system.rollbackReliableSpawn(ctx, endpoint)
	})

	t.Run("With cluster remove failure", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		endpoint := newStoppedEndpoint(system)

		companionName := reliableCompanionName(ReliableControllerRoleProducer, endpoint.IncarnationID())
		clusterMock.EXPECT().RemoveActor(mock.Anything, companionName).Return(assert.AnError).Once()
		clusterMock.EXPECT().GetActor(mock.Anything, "orders").Return(nil, cluster.ErrActorNotFound).Maybe()

		system.rollbackReliableSpawn(context.Background(), endpoint)
	})

	t.Run("With cluster remove success", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		endpoint := newStoppedEndpoint(system)

		companionName := reliableCompanionName(ReliableControllerRoleProducer, endpoint.IncarnationID())
		clusterMock.EXPECT().RemoveActor(mock.Anything, companionName).Return(nil).Once()
		clusterMock.EXPECT().GetActor(mock.Anything, "orders").Return(nil, cluster.ErrActorNotFound).Maybe()

		system.rollbackReliableSpawn(context.Background(), endpoint)
	})
}

func TestReleaseDepartedReliableCompanion(t *testing.T) {
	t.Run("With producer role and release error", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)

		incarnationID := uuid.NewString()
		companionName := reliableCompanionName(ReliableControllerRoleProducer, incarnationID)
		clusterMock.EXPECT().GetActor(mock.Anything, companionName).Return(nil, assert.AnError).Once()

		props := &internalpb.Actor{
			IncarnationId: incarnationID,
			ReliableDelivery: &internalpb.ReliableDeliveryConfig{
				Endpoint: &internalpb.ReliableDeliveryConfig_Producer{
					Producer: &internalpb.ReliableProducerConfig{ConsumerName: "consumer"},
				},
			},
		}

		system.releaseDepartedReliableCompanion(context.Background(), props, "10.0.0.1:9000")
	})

	t.Run("With consumer role", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)

		incarnationID := uuid.NewString()
		companionName := reliableCompanionName(ReliableControllerRoleConsumer, incarnationID)
		clusterMock.EXPECT().GetActor(mock.Anything, companionName).Return(nil, cluster.ErrActorNotFound).Once()

		props := &internalpb.Actor{
			IncarnationId: incarnationID,
			ReliableDelivery: &internalpb.ReliableDeliveryConfig{
				Endpoint: &internalpb.ReliableDeliveryConfig_Consumer{
					Consumer: &internalpb.ReliableConsumerConfig{ProducerName: "producer"},
				},
			},
		}

		system.releaseDepartedReliableCompanion(context.Background(), props, "10.0.0.1:9000")
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

	// the spawn transaction created the controller companion automatically
	companion, err := system.resolveReliableCompanion(ctx, "endpoint", ReliableControllerRoleProducer, nil)
	require.NoError(t, err)

	assert.IsType(t, new(passivation.LongLivedStrategy), companion.passivationStrategy)

	// the endpoint keeps the normal relocation default; the controller never
	// relocates on its own because the relocated endpoint rebuilds a fresh one
	assert.True(t, endpoint.IsRelocatable())
	assert.False(t, companion.IsRelocatable())
}

// produceSubmission commands the reliable producer mock to submit one
// application message through its controller.
type produceSubmission struct {
	messageID string
	payload   any
}

// askSubmission is a produce submission sent with Ask. The producer answers
// it from local knowledge only, before any storage or delivery work happens.
type askSubmission struct {
	messageID string
	payload   any
}

// submissionAccepted is the producer's reply to askSubmission: the message
// was accepted into its buffer. It deliberately cannot say anything about
// storage or delivery.
type submissionAccepted struct {
	queued int
}

// reliableProducerMock is a producer endpoint that answers the controller
// handshake the way a real application producer would: it queues submissions,
// spends one RequestNext grant per submission, idempotently resends the same
// Produced when a grant is retried, and acknowledges Stored. All state lives
// in the actor and is only touched inside its own mailbox turns.
type reliableProducerMock struct {
	controller   *PID
	request      *RequestNext
	pending      []*produceSubmission
	lastToken    string
	lastProduced *Produced
}

func (x *reliableProducerMock) PreStart(*Context) error { return nil }
func (x *reliableProducerMock) PostStop(*Context) error { return nil }

func (x *reliableProducerMock) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *PostStart:
	case *RequestNext:
		if !msg.IsAuthorizedFor(ctx.Self(), ctx.Sender()) {
			return
		}

		x.controller = ctx.Sender()

		if msg.Token() == x.lastToken && x.lastProduced != nil {
			ctx.Tell(x.controller, x.lastProduced)
			return
		}

		x.request = msg
		x.flush(ctx)
	case *Stored:
		ack, err := NewStoredAck(msg)
		if err != nil {
			ctx.Err(err)
			return
		}

		ctx.Tell(ctx.Sender(), ack)
	case *produceSubmission:
		x.pending = append(x.pending, msg)
		x.flush(ctx)
	case *askSubmission:
		x.pending = append(x.pending, &produceSubmission{messageID: msg.messageID, payload: msg.payload})
		ctx.Response(&submissionAccepted{queued: len(x.pending)})
		x.flush(ctx)
	default:
		ctx.Unhandled()
	}
}

// flush spends the held grant on the oldest queued submission.
func (x *reliableProducerMock) flush(ctx *ReceiveContext) {
	if x.request == nil || len(x.pending) == 0 {
		return
	}

	submission := x.pending[0]
	produced, err := NewProduced(x.request, submission.messageID, submission.payload)
	if err != nil {
		ctx.Err(err)
		return
	}

	x.pending = x.pending[1:]
	x.lastToken = x.request.Token()
	x.lastProduced = produced
	x.request = nil
	ctx.Tell(x.controller, produced)
}

// awaitDeliveries polls the consumer mock until it has recorded at least
// count deliveries and returns them collapsed to their first occurrence per
// sequence, since a slow confirmation legitimately allows a redelivery.
func awaitDeliveries(t *testing.T, ctx context.Context, consumer *PID, count int) []*Delivery {
	t.Helper()

	var distinct []*Delivery

	require.Eventually(t, func() bool {
		response, err := Ask(ctx, consumer, &getDeliveries{}, time.Second)
		if err != nil {
			return false
		}

		recorded, _ := response.([]*Delivery)
		seen := make(map[int64]bool, len(recorded))
		distinct = distinct[:0]

		for _, delivery := range recorded {
			if seen[delivery.Seq()] {
				continue
			}

			seen[delivery.Seq()] = true
			distinct = append(distinct, delivery)
		}

		return len(distinct) >= count
	}, 20*time.Second, 20*time.Millisecond)

	return distinct
}

func TestReliableDeliveryEndToEnd(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)

	producer, err := system.Spawn(ctx, "orders-producer", &reliableProducerMock{}, AsReliableProducer("orders-consumer"))
	require.NoError(t, err)

	consumer, err := system.Spawn(ctx, "orders-consumer", &reliableConsumerMock{autoConfirm: true}, AsReliableConsumer("orders-producer", WithReliableResendInterval(200*time.Millisecond)))
	require.NoError(t, err)

	// both controller companions were created by the spawn transaction
	_, err = system.resolveReliableCompanion(ctx, "orders-producer", ReliableControllerRoleProducer, nil)
	require.NoError(t, err)
	_, err = system.resolveReliableCompanion(ctx, "orders-consumer", ReliableControllerRoleConsumer, nil)
	require.NoError(t, err)

	// ingress stays plain Tell: a message that never becomes Produced is not
	// part of the reliable flow and must not reach the consumer
	require.NoError(t, Tell(ctx, producer, new(testpb.TestSend)))

	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("m-%d", i)
		require.NoError(t, Tell(ctx, producer, &produceSubmission{messageID: id, payload: &testpb.Reply{Content: id}}))
	}

	deliveries := awaitDeliveries(t, ctx, consumer, 3)
	require.Len(t, deliveries, 3)

	for i, delivery := range deliveries {
		id := fmt.Sprintf("m-%d", i+1)
		assert.Equal(t, id, delivery.MessageID())
		assert.Equal(t, int64(i+1), delivery.Seq())

		reply, ok := delivery.Payload().(*testpb.Reply)
		require.True(t, ok)
		assert.Equal(t, id, reply.GetContent())
	}
}

func TestReliableDeliveryEndToEndDurable(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)

	queue := &mockDurableQueue{}
	producer, err := system.Spawn(ctx, "orders-producer", &reliableProducerMock{}, AsReliableProducer("orders-consumer", WithReliableDurableQueue(queue)))
	require.NoError(t, err)

	consumer, err := system.Spawn(ctx, "orders-consumer", &reliableConsumerMock{autoConfirm: true}, AsReliableConsumer("orders-producer", WithReliableResendInterval(200*time.Millisecond)))
	require.NoError(t, err)

	for i := 1; i <= 2; i++ {
		id := fmt.Sprintf("m-%d", i)
		require.NoError(t, Tell(ctx, producer, &produceSubmission{messageID: id, payload: &testpb.Reply{Content: id}}))
	}

	deliveries := awaitDeliveries(t, ctx, consumer, 2)
	require.Len(t, deliveries, 2)

	// every message went through the durable store-accept handshake; confirm
	// writes may also appear as confirmation watermarks catch up
	require.Eventually(t, func() bool {
		_, operations, _ := queue.snapshot()
		return containsAllOperations(operations, "store:m-1", "accept:m-1", "store:m-2", "accept:m-2")
	}, 10*time.Second, 20*time.Millisecond)
}

// containsAllOperations reports whether every wanted operation appears in order.
func containsAllOperations(operations []string, wanted ...string) bool {
	index := 0
	for _, operation := range operations {
		if index < len(wanted) && operation == wanted[index] {
			index++
		}
	}
	return index == len(wanted)
}

func TestReliableEndpointShutdownStopsCompanion(t *testing.T) {
	t.Run("With producer endpoint", func(t *testing.T) {
		ctx, system := newCompanionTestSystem(t)

		producer, err := system.Spawn(ctx, "orders-producer", &reliableProducerMock{}, AsReliableProducer("orders-consumer"))
		require.NoError(t, err)

		companion, err := system.resolveReliableCompanion(ctx, "orders-producer", ReliableControllerRoleProducer, nil)
		require.NoError(t, err)

		require.NoError(t, producer.Shutdown(ctx))
		assert.False(t, companion.IsRunning())

		require.Eventually(t, func() bool {
			_, ok := system.actors.nodeByName(companion.Name())
			return !ok
		}, 3*time.Second, 10*time.Millisecond)
	})

	t.Run("With consumer endpoint", func(t *testing.T) {
		ctx, system := newCompanionTestSystem(t)

		consumer, err := system.Spawn(ctx, "orders-consumer", &reliableConsumerMock{autoConfirm: true}, AsReliableConsumer("orders-producer"))
		require.NoError(t, err)

		companion, err := system.resolveReliableCompanion(ctx, "orders-consumer", ReliableControllerRoleConsumer, nil)
		require.NoError(t, err)

		require.NoError(t, consumer.Shutdown(ctx))
		assert.False(t, companion.IsRunning())

		require.Eventually(t, func() bool {
			_, ok := system.actors.nodeByName(companion.Name())
			return !ok
		}, 3*time.Second, 10*time.Millisecond)
	})
}

func TestReliableEndpointReSpawnRecreatesCompanion(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)

	producer, err := system.Spawn(ctx, "orders-producer", &reliableProducerMock{}, AsReliableProducer("orders-consumer"))
	require.NoError(t, err)

	companion, err := system.resolveReliableCompanion(ctx, "orders-producer", ReliableControllerRoleProducer, nil)
	require.NoError(t, err)

	// simulate the controller's terminal self-stop, which the private stop
	// path permits while the system keeps running
	require.NoError(t, companion.Shutdown(ctx))

	require.Eventually(t, func() bool {
		_, ok := system.actors.nodeByName(companion.Name())
		return !ok
	}, 3*time.Second, 10*time.Millisecond)

	respawned, err := system.ReSpawn(ctx, "orders-producer")
	require.NoError(t, err)
	require.True(t, respawned.Equals(producer))

	recreated, err := system.resolveReliableCompanion(ctx, "orders-producer", ReliableControllerRoleProducer, nil)
	require.NoError(t, err)
	assert.True(t, recreated.IsRunning())
	assert.Equal(t, companion.Name(), recreated.Name())
	assert.NotSame(t, companion, recreated)

	// a live companion is restarted with the endpoint subtree, never duplicated
	respawned, err = system.ReSpawn(ctx, "orders-producer")
	require.NoError(t, err)

	companions := 0

	for _, child := range system.tree().children(respawned) {
		if child.reliableCompanion != nil {
			companions++
		}
	}

	assert.Equal(t, 1, companions)
}

func TestReliableEndpointSpawnRollback(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)

	queue := &mockDurableQueue{loadErr: errors.New("backing store is unreachable")}
	pid, err := system.Spawn(ctx, "orders-producer", &reliableProducerMock{},
		AsReliableProducer("orders-consumer", WithReliableDurableQueue(queue), WithReliableQueueRetry(1, time.Millisecond)))
	require.Error(t, err)
	require.Nil(t, pid)

	// a failed spawn leaves nothing behind: the endpoint record disappears
	// and the same name spawns cleanly afterwards
	require.Eventually(t, func() bool {
		_, ok := system.actors.nodeByName("orders-producer")
		return !ok
	}, 3*time.Second, 10*time.Millisecond)

	fresh, err := system.Spawn(ctx, "orders-producer", &reliableProducerMock{}, AsReliableProducer("orders-consumer"))
	require.NoError(t, err)
	assert.True(t, fresh.IsRunning())
}

func TestReliableEndpointDataCenterRejected(t *testing.T) {
	// a cross-datacenter endpoint could never resolve its controller pair in
	// the local cluster registry, so the placement is rejected up front
	ctx, system := newCompanionTestSystem(t)

	pid, err := system.SpawnOn(ctx, "orders-producer", &reliableProducerMock{},
		AsReliableProducer("orders-consumer"), WithDataCenter(&datacenter.DataCenter{Name: "dc-west", Region: "us", Zone: "a"}))
	require.ErrorContains(t, err, "data center")
	assert.Nil(t, pid)
}

func TestReliableEndpointRemoteChildSpawnRejected(t *testing.T) {
	// the remote child spawn request cannot carry reliable-delivery settings,
	// so the options are rejected instead of silently dropped
	remoteParent := newRemotePID(address.New("parent", "remote-system", "127.0.0.1", 8080), nil)

	pid, err := remoteParent.SpawnChild(context.TODO(), "orders-producer", &reliableProducerMock{}, AsReliableProducer("orders-consumer"))
	require.ErrorContains(t, err, "remote children")
	assert.Nil(t, pid)
}

func TestReliableEndpointRemotingOnlyRemotePlacementRejected(t *testing.T) {
	// remoting without clustering cannot resolve peer controllers, so remote
	// placement of a reliable endpoint must fail fast instead of spawning a
	// flow that never connects
	ctx := context.TODO()
	ports := dynaport.Get(1)
	host := "127.0.0.1"

	system, err := NewActorSystem("remoting-only",
		WithLogger(log.DiscardLogger),
		WithRemote(remote.NewConfig(host, ports[0])))
	require.NoError(t, err)
	require.NoError(t, system.Start(ctx))
	t.Cleanup(func() {
		assert.NoError(t, system.Stop(ctx))
	})

	pid, err := system.Spawn(ctx, "orders-producer", &reliableProducerMock{},
		AsReliableProducer("orders-consumer"),
		WithHostAndPort(host, ports[0]))
	require.ErrorIs(t, err, gerrors.ErrReliableClusterRequired)
	assert.Nil(t, pid)

	// single-node local placement remains valid without a cluster
	local, err := system.Spawn(ctx, "orders-producer", &reliableProducerMock{},
		AsReliableProducer("orders-consumer"))
	require.NoError(t, err)
	assert.NotNil(t, local)
}

func TestSpawnConfigRejectReliableRemotePlacement(t *testing.T) {
	// placement requires cluster resolution: without it the endpoint would
	// never connect, and clustering wins the precedence when both rules fail
	config := newSpawnConfig(AsReliableProducer("orders-consumer"), WithHostAndPort("127.0.0.1", 8080))
	require.ErrorIs(t, config.rejectReliableRemotePlacement(false), gerrors.ErrReliableClusterRequired)
	require.NoError(t, config.rejectReliableRemotePlacement(true))

	// the placement wire never carries a peer address, so a placement route
	// must reject it instead of silently dropping the setting
	producer := newSpawnConfig(AsReliableProducer("orders-consumer", WithReliableRemoteConsumer("127.0.0.1", 2280)))
	require.ErrorIs(t, producer.rejectReliableRemotePlacement(true), gerrors.ErrReliablePeerClusterConflict)
	require.ErrorIs(t, producer.rejectReliableRemotePlacement(false), gerrors.ErrReliableClusterRequired)

	consumer := newSpawnConfig(AsReliableConsumer("orders-producer", WithReliableRemoteProducer("127.0.0.1", 2280)))
	require.ErrorIs(t, consumer.rejectReliableRemotePlacement(true), gerrors.ErrReliablePeerClusterConflict)

	// a spawn without reliable settings is never the guard's business
	require.NoError(t, newSpawnConfig(WithHostAndPort("127.0.0.1", 8080)).rejectReliableRemotePlacement(false))
	require.NoError(t, newSpawnConfig(WithHostAndPort("127.0.0.1", 8080)).rejectReliableRemotePlacement(true))
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
	assert.True(t, serialized.GetRelocatable())

	// the companion record carries the ownership spec cluster resolution
	// validates and is pinned to its node
	companion, err := system.resolveReliableCompanion(ctx, "endpoint", ReliableControllerRoleProducer, nil)
	require.NoError(t, err)

	companionSerialized, err := companion.toSerialize()
	require.NoError(t, err)

	spec := companionSerialized.GetReliableCompanion()
	require.NotNil(t, spec)
	assert.Equal(t, internalpb.ReliableControllerRole_RELIABLE_CONTROLLER_ROLE_PRODUCER, spec.GetRole())
	assert.Equal(t, "endpoint", spec.GetEndpointName())
	assert.Equal(t, endpoint.IncarnationID(), spec.GetEndpointIncarnationId())
	assert.False(t, companionSerialized.GetRelocatable())

	plain, err := system.Spawn(ctx, "plain", NewMockActor())
	require.NoError(t, err)

	plainSerialized, err := plain.toSerialize()
	require.NoError(t, err)
	assert.Nil(t, plainSerialized.GetReliableDelivery())
	assert.Nil(t, plainSerialized.GetReliableCompanion())
	assert.Equal(t, plain.IncarnationID(), plainSerialized.GetIncarnationId())
}

func TestReliableEndpointRemoteSpawn(t *testing.T) {
	// remote placement of a reliable endpoint resolves peer controllers
	// through the cluster registry, so a remoting-only host rejects the spawn
	// before creating an endpoint that could never connect: remoting-only
	// flows spawn each endpoint locally on its own node with explicit peer
	// addressing instead
	ctx := context.TODO()
	host := "127.0.0.1"
	ports := dynaport.Get(1)

	sys, err := NewActorSystem("remote-reliable",
		WithLogger(log.DiscardLogger),
		WithRemote(remote.NewConfig(host, ports[0])))
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))

	t.Cleanup(func() {
		assert.NoError(t, sys.Stop(context.WithoutCancel(ctx)))
	})

	pause.For(time.Second)

	require.NoError(t, sys.Register(ctx, &reliableProducerMock{}))
	require.NoError(t, sys.Inject(&mockDurableQueue{}))

	queue := &mockDurableQueue{}
	remoting := remoteclient.NewClient()

	t.Cleanup(remoting.Close)

	_, err = remoting.RemoteSpawn(ctx, host, ports[0], &remote.SpawnRequest{
		Name:         "orders-producer",
		Kind:         types.Name(&reliableProducerMock{}),
		Relocatable:  true,
		Dependencies: []extension.Dependency{queue},
		ReliableDelivery: &remote.ReliableDeliverySpec{
			Producer: &remote.ReliableProducerSpec{
				ConsumerName:             "orders-consumer",
				DurableQueueID:           queue.ID(),
				QueueRetryMaxAttempts:    DefaultReliableQueueRetryAttempts,
				QueueRetryInitialBackoff: DefaultReliableQueueRetryBackoff,
				LocalRetryInterval:       DefaultReliableProducerRetryInterval,
			},
		},
	})
	require.Error(t, err)
	assert.ErrorContains(t, err, gerrors.ErrReliableClusterRequired.Error())

	system := sys.(*actorSystem)
	_, ok := system.actors.nodeByName("orders-producer")
	assert.False(t, ok)
}

// getDeliverySenders asks senderRecordingConsumerMock for the sender of each
// recorded delivery.
type getDeliverySenders struct{}

// senderRecordingConsumerMock confirms every delivery and records which PID
// delivered it, so tests can assert that deliveries come only from the
// consumer's own controller.
type senderRecordingConsumerMock struct {
	deliveries []*Delivery
	senders    []*PID
}

func (x *senderRecordingConsumerMock) PreStart(*Context) error { return nil }
func (x *senderRecordingConsumerMock) PostStop(*Context) error { return nil }

func (x *senderRecordingConsumerMock) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *PostStart:
	case *Delivery:
		x.deliveries = append(x.deliveries, msg)
		x.senders = append(x.senders, ctx.Sender())

		confirmed, err := NewConfirmed(msg)
		if err != nil {
			ctx.Err(err)
			return
		}

		ctx.Tell(ctx.Sender(), confirmed)
	case *getDeliveries:
		ctx.Response(append([]*Delivery(nil), x.deliveries...))
	case *getDeliverySenders:
		ctx.Response(append([]*PID(nil), x.senders...))
	default:
		ctx.Unhandled()
	}
}

// startCheckout commands the checkout actor to hand one finished order to the
// reliable flow.
type startCheckout struct {
	orderID string
}

// processedNotice is the consumer's business-level notification to the
// checkout actor, sent through ordinary messaging.
type processedNotice struct {
	orderID string
}

// getNotices asks the checkout actor which orders were confirmed processed.
type getNotices struct{}

// checkoutMock is an ordinary actor that feeds the reliable flow the same way
// it would message any other actor: the producer PID is plain constructor
// state and the handoff is a plain Tell from its own Receive.
type checkoutMock struct {
	producer *PID
	notices  []string
}

func (x *checkoutMock) PreStart(*Context) error { return nil }
func (x *checkoutMock) PostStop(*Context) error { return nil }

func (x *checkoutMock) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *PostStart:
	case *startCheckout:
		// the payload carries the reply-to actor name because Delivery's
		// sender is the controller, never the business origin
		ctx.Tell(x.producer, &produceSubmission{
			messageID: msg.orderID,
			payload:   &testpb.Reply{Content: ctx.Self().Name()},
		})
	case *processedNotice:
		x.notices = append(x.notices, msg.orderID)
	case *getNotices:
		ctx.Response(append([]string(nil), x.notices...))
	default:
		ctx.Unhandled()
	}
}

// replyingConsumerMock processes deliveries idempotently and notifies the
// origin actor named in the payload through ordinary messaging before
// confirming.
type replyingConsumerMock struct {
	seen map[string]bool
}

func (x *replyingConsumerMock) PreStart(*Context) error {
	x.seen = make(map[string]bool)
	return nil
}

func (x *replyingConsumerMock) PostStop(*Context) error { return nil }

func (x *replyingConsumerMock) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *PostStart:
	case *Delivery:
		if !x.seen[msg.MessageID()] {
			reply, ok := msg.Payload().(*testpb.Reply)
			if !ok {
				ctx.Err(fmt.Errorf("unexpected payload type %T", msg.Payload()))
				return
			}

			origin, err := ctx.ActorSystem().ActorOf(ctx.Context(), reply.GetContent())
			if err != nil {
				ctx.Err(err)
				return
			}

			x.seen[msg.MessageID()] = true
			ctx.Tell(origin, &processedNotice{orderID: msg.MessageID()})
		}

		confirmed, err := NewConfirmed(msg)
		if err != nil {
			ctx.Err(err)
			return
		}

		ctx.Tell(ctx.Sender(), confirmed)
	default:
		ctx.Unhandled()
	}
}

// TestReliableDeliveryAskAnswersFromLocalKnowledge verifies the documented
// Ask boundary: a caller may Ask the producer, but the answer reflects only
// acceptance into the producer's buffer, never delivery, and every Delivery
// reaches the consumer from its own controller rather than from the
// submitter, so the flow offers no reply path to the business origin.
func TestReliableDeliveryAskAnswersFromLocalKnowledge(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)

	producer, err := system.Spawn(ctx, "orders-producer", &reliableProducerMock{}, AsReliableProducer("orders-consumer"))
	require.NoError(t, err)

	// the consumer endpoint does not exist yet, so delivery is impossible;
	// the Ask still answers immediately because the producer reports local
	// acceptance only
	response, err := Ask(ctx, producer, &askSubmission{messageID: "m-1", payload: &testpb.Reply{Content: "m-1"}}, time.Second)
	require.NoError(t, err)

	accepted, ok := response.(*submissionAccepted)
	require.True(t, ok)
	assert.Equal(t, 1, accepted.queued)

	// once the consumer exists, the flow completes the delivery the Ask
	// could not speak for
	consumer, err := system.Spawn(ctx, "orders-consumer", &senderRecordingConsumerMock{}, AsReliableConsumer("orders-producer", WithReliableResendInterval(200*time.Millisecond)))
	require.NoError(t, err)

	deliveries := awaitDeliveries(t, ctx, consumer, 1)
	require.Len(t, deliveries, 1)
	assert.Equal(t, "m-1", deliveries[0].MessageID())

	consumerController, err := system.resolveReliableCompanion(ctx, "orders-consumer", ReliableControllerRoleConsumer, nil)
	require.NoError(t, err)

	response, err = Ask(ctx, consumer, &getDeliverySenders{}, time.Second)
	require.NoError(t, err)

	senders, ok := response.([]*PID)
	require.True(t, ok)
	require.NotEmpty(t, senders)

	for _, sender := range senders {
		assert.True(t, sender.Equals(consumerController))
		assert.False(t, sender.Equals(producer))
	}
}

// TestReliableDeliveryFedByOrdinaryActor verifies that the producer is an
// ordinary actor other actors can message from their own Receive, and that
// the consumer answers the business origin through ordinary messaging using
// correlation carried in the payload.
func TestReliableDeliveryFedByOrdinaryActor(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)

	producer, err := system.Spawn(ctx, "orders-producer", &reliableProducerMock{}, AsReliableProducer("orders-consumer"))
	require.NoError(t, err)

	_, err = system.Spawn(ctx, "orders-consumer", &replyingConsumerMock{}, AsReliableConsumer("orders-producer", WithReliableResendInterval(200*time.Millisecond)))
	require.NoError(t, err)

	checkout, err := system.Spawn(ctx, "checkout", &checkoutMock{producer: producer})
	require.NoError(t, err)

	require.NoError(t, Tell(ctx, checkout, &startCheckout{orderID: "ord-1"}))

	require.Eventually(t, func() bool {
		response, err := Ask(ctx, checkout, &getNotices{}, time.Second)
		if err != nil {
			return false
		}

		notices, _ := response.([]string)
		return len(notices) == 1 && notices[0] == "ord-1"
	}, 20*time.Second, 20*time.Millisecond)
}

func TestAuthenticateWorkPullingWorkerLocalEdges(t *testing.T) {
	ctx, system := newCompanionTestSystem(t)

	t.Run("With no sender", func(t *testing.T) {
		_, _, err := system.authenticateWorkPullingWorker(ctx, nil, "jobs-producer")
		require.ErrorContains(t, err, "registration sender is required")
	})

	t.Run("With a blank producer name", func(t *testing.T) {
		sender, err := system.Spawn(ctx, "any-sender", NewMockActor())
		require.NoError(t, err)

		_, _, err = system.authenticateWorkPullingWorker(ctx, sender, "  ")
		require.ErrorContains(t, err, "producer endpoint name is required")
	})

	t.Run("With a companion whose endpoint has no local record", func(t *testing.T) {
		spec, err := newReliableCompanionSpec(ReliableControllerRoleConsumer, "ghost-worker", uuid.NewString())
		require.NoError(t, err)

		orphan, err := system.Spawn(ctx, reliableCompanionName(ReliableControllerRoleConsumer, spec.endpointIncarnationID), &deliveryRecorder{}, asSystem(), asReliableCompanion(spec))
		require.NoError(t, err)

		_, _, err = system.authenticateWorkPullingWorker(ctx, orphan, "jobs-producer")
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.ErrorContains(t, err, "has no local record")
	})

	t.Run("With a companion bound to a stale endpoint incarnation", func(t *testing.T) {
		_, err := system.Spawn(ctx, "edge-worker", NewMockActor())
		require.NoError(t, err)

		spec, err := newReliableCompanionSpec(ReliableControllerRoleConsumer, "edge-worker", uuid.NewString())
		require.NoError(t, err)

		stale, err := system.Spawn(ctx, reliableCompanionName(ReliableControllerRoleConsumer, spec.endpointIncarnationID), &deliveryRecorder{}, asSystem(), asReliableCompanion(spec))
		require.NoError(t, err)

		_, _, err = system.authenticateWorkPullingWorker(ctx, stale, "jobs-producer")
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
	})

	t.Run("With an unknown endpoint on local resolution", func(t *testing.T) {
		_, err := system.resolveLocalReliableCompanion("no-such-endpoint", ReliableControllerRoleProducer)
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.ErrorContains(t, err, "has no local record")
	})
}

func TestAuthenticateRemoteWorkPullingWorker(t *testing.T) {
	incarnationID := uuid.NewString()
	companionName := reliableCompanionName(ReliableControllerRoleConsumer, incarnationID)
	remoteHostPort := "10.0.0.2:9000"

	spec, err := newReliableCompanionSpec(ReliableControllerRoleConsumer, "remote-worker", incarnationID)
	require.NoError(t, err)

	sender := newRemotePID(address.New(companionName, "test-replication", "10.0.0.2", 9000), nil)

	companionRecord := func(addr string, companion *internalpb.ReliableCompanionSpec) *internalpb.Actor {
		return &internalpb.Actor{Address: addr, IncarnationId: incarnationID, ReliableCompanion: companion}
	}

	endpointRecord := func(hostPort, incarnation string, delivery *internalpb.ReliableDeliveryConfig) *internalpb.Actor {
		return &internalpb.Actor{Address: "goakt://test-replication@" + hostPort + "/remote-worker", IncarnationId: incarnation, ReliableDelivery: delivery}
	}

	validCompanion := companionRecord("goakt://test-replication@"+remoteHostPort+"/"+companionName, spec.toProto())

	t.Run("With cluster mode disabled", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		system.clusterEnabled.Store(false)

		_, _, err := system.authenticateWorkPullingWorker(context.Background(), sender, "jobs-producer")
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.ErrorContains(t, err, "requires cluster mode")
	})

	t.Run("With no companion registry record", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		clusterMock.EXPECT().GetActor(mock.Anything, companionName).Return(nil, cluster.ErrActorNotFound)

		_, _, err := system.authenticateWorkPullingWorker(context.Background(), sender, "jobs-producer")
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.ErrorContains(t, err, "has no registry record")
	})

	t.Run("With a record that is not a runtime companion", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		clusterMock.EXPECT().GetActor(mock.Anything, companionName).Return(companionRecord("goakt://test-replication@"+remoteHostPort+"/"+companionName, nil), nil)

		_, _, err := system.authenticateWorkPullingWorker(context.Background(), sender, "jobs-producer")
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.ErrorContains(t, err, "is not a runtime companion")
	})

	t.Run("With a producer-role companion", func(t *testing.T) {
		wrongRole, err := newReliableCompanionSpec(ReliableControllerRoleProducer, "remote-worker", incarnationID)
		require.NoError(t, err)

		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		clusterMock.EXPECT().GetActor(mock.Anything, companionName).Return(companionRecord("goakt://test-replication@"+remoteHostPort+"/"+companionName, wrongRole.toProto()), nil)

		_, _, err = system.authenticateWorkPullingWorker(context.Background(), sender, "jobs-producer")
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.ErrorContains(t, err, "runs role=")
	})

	t.Run("With an invalid companion address", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		clusterMock.EXPECT().GetActor(mock.Anything, companionName).Return(companionRecord("not-an-address", spec.toProto()), nil)

		_, _, err := system.authenticateWorkPullingWorker(context.Background(), sender, "jobs-producer")
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.ErrorContains(t, err, "carries an invalid address")
	})

	t.Run("With a companion address not matching the sender", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		clusterMock.EXPECT().GetActor(mock.Anything, companionName).Return(companionRecord("goakt://test-replication@10.0.0.9:9000/"+companionName, spec.toProto()), nil)

		_, _, err := system.authenticateWorkPullingWorker(context.Background(), sender, "jobs-producer")
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.ErrorContains(t, err, "does not match the registration sender")
	})

	t.Run("With no endpoint registry record", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		clusterMock.EXPECT().GetActor(mock.Anything, companionName).Return(validCompanion, nil)
		clusterMock.EXPECT().GetActor(mock.Anything, "remote-worker").Return(nil, cluster.ErrActorNotFound)

		_, _, err := system.authenticateWorkPullingWorker(context.Background(), sender, "jobs-producer")
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.ErrorContains(t, err, "has no registry record")
	})

	t.Run("With an invalid endpoint address", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		clusterMock.EXPECT().GetActor(mock.Anything, companionName).Return(validCompanion, nil)
		clusterMock.EXPECT().GetActor(mock.Anything, "remote-worker").Return(&internalpb.Actor{Address: "not-an-address", IncarnationId: incarnationID}, nil)

		_, _, err := system.authenticateWorkPullingWorker(context.Background(), sender, "jobs-producer")
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.ErrorContains(t, err, "carries an invalid address")
	})

	t.Run("With the endpoint on another node", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		clusterMock.EXPECT().GetActor(mock.Anything, companionName).Return(validCompanion, nil)
		clusterMock.EXPECT().GetActor(mock.Anything, "remote-worker").Return(endpointRecord("10.0.0.9:9000", incarnationID, nil), nil)

		_, _, err := system.authenticateWorkPullingWorker(context.Background(), sender, "jobs-producer")
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.ErrorContains(t, err, "live on different nodes")
	})

	t.Run("With a stale endpoint incarnation", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		clusterMock.EXPECT().GetActor(mock.Anything, companionName).Return(validCompanion, nil)
		clusterMock.EXPECT().GetActor(mock.Anything, "remote-worker").Return(endpointRecord(remoteHostPort, uuid.NewString(), nil), nil)

		_, _, err := system.authenticateWorkPullingWorker(context.Background(), sender, "jobs-producer")
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.ErrorContains(t, err, "is bound to incarnation=")
	})

	t.Run("With no consumer configuration", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		clusterMock.EXPECT().GetActor(mock.Anything, companionName).Return(validCompanion, nil)
		clusterMock.EXPECT().GetActor(mock.Anything, "remote-worker").Return(endpointRecord(remoteHostPort, incarnationID, nil), nil)

		_, _, err := system.authenticateWorkPullingWorker(context.Background(), sender, "jobs-producer")
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.ErrorContains(t, err, "has no consumer configuration")
	})

	t.Run("With a worker naming another producer", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		clusterMock.EXPECT().GetActor(mock.Anything, companionName).Return(validCompanion, nil)
		clusterMock.EXPECT().GetActor(mock.Anything, "remote-worker").Return(endpointRecord(remoteHostPort, incarnationID, consumerDeliveryConfig("other-producer").toProto()), nil)

		_, _, err := system.authenticateWorkPullingWorker(context.Background(), sender, "jobs-producer")
		require.ErrorIs(t, err, errReliableCompanionUnavailable)
		assert.ErrorContains(t, err, "does not name producer")
	})

	t.Run("With a fully verified remote worker", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		system := MockReplicationTestSystem(clusterMock)
		clusterMock.EXPECT().GetActor(mock.Anything, companionName).Return(validCompanion, nil)
		clusterMock.EXPECT().GetActor(mock.Anything, "remote-worker").Return(endpointRecord(remoteHostPort, incarnationID, consumerDeliveryConfig("jobs-producer").toProto()), nil)

		verified, endpointName, err := system.authenticateWorkPullingWorker(context.Background(), sender, "jobs-producer")
		require.NoError(t, err)
		assert.True(t, verified.Equals(sender))
		assert.Equal(t, "remote-worker", endpointName)
	})
}

func TestResolvePeerReliableCompanionNoLivePair(t *testing.T) {
	clusterMock := mockscluster.NewCluster(t)
	system := MockReplicationTestSystem(clusterMock)
	remotingMock := mocksremote.NewClient(t)
	system.remoting = remotingMock

	// the peer answers but reports no live endpoint-companion pair, which is
	// the transient unavailable condition the caller's tick retries
	remotingMock.EXPECT().GetReliableCompanion(mock.Anything, "10.0.0.7", 9000, "endpoint", mock.Anything).Return(address.NoSender(), nil).Once()

	peer := &reliablePeerAddress{host: "10.0.0.7", port: 9000}
	resolved, err := system.resolveRemoteReliableCompanion(context.Background(), "endpoint", ReliableControllerRoleProducer, peer)
	require.ErrorIs(t, err, errReliableCompanionUnavailable)
	assert.ErrorContains(t, err, "has no live")
	assert.Nil(t, resolved)
}
