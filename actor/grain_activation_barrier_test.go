package actor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/internal/cluster"
	mockscluster "github.com/tochemey/goakt/v3/mocks/cluster"
)

func TestGrainActivationBarrier_WaitReady(t *testing.T) {
	barrier := newGrainActivationBarrier(2, 50*time.Millisecond)
	barrier.open()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	require.NoError(t, barrier.wait(ctx))
}

func TestGrainActivationBarrier_WaitTimeout(t *testing.T) {
	barrier := newGrainActivationBarrier(2, 20*time.Millisecond)
	err := barrier.wait(context.Background())
	require.ErrorIs(t, err, gerrors.ErrGrainActivationBarrierTimeout)
}

func TestGrainActivationBarrier_WaitContextCancel(t *testing.T) {
	barrier := newGrainActivationBarrier(2, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := barrier.wait(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestActorSystemWaitForGrainActivationBarrier_NoBarrier(t *testing.T) {
	sys := &actorSystem{}
	sys.clusterEnabled.Store(true)

	require.NoError(t, sys.waitForGrainActivationBarrier(context.Background()))
}

func TestActorSystemSetupGrainActivationBarrier_ImmediateOpen(t *testing.T) {
	config := NewClusterConfig().
		WithMinimumPeersQuorum(1).
		WithGrainActivationBarrier(10 * time.Millisecond)

	sys := &actorSystem{clusterConfig: config}
	sys.clusterEnabled.Store(true)

	sys.setupGrainActivationBarrier(context.Background())
	require.NotNil(t, sys.grainBarrier)

	select {
	case <-sys.grainBarrier.ready:
	default:
		t.Fatal("expected activation barrier to be open")
	}
}

func TestActorSystemTryOpenGrainActivationBarrier(t *testing.T) {
	cl := mockscluster.NewCluster(t)
	config := NewClusterConfig().
		WithMinimumPeersQuorum(2).
		WithGrainActivationBarrier(0)

	sys := &actorSystem{cluster: cl, clusterConfig: config}
	sys.clusterEnabled.Store(true)
	sys.grainBarrier = newGrainActivationBarrier(2, 0)

	peers := []*cluster.Peer{{Host: "127.0.0.1"}, {Host: "127.0.0.2"}}
	cl.EXPECT().Members(mock.Anything).Return(peers, nil).Once()

	sys.tryOpenGrainActivationBarrier(context.Background())

	select {
	case <-sys.grainBarrier.ready:
	default:
		t.Fatal("expected activation barrier to be open")
	}
}
