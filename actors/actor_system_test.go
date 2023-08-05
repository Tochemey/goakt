package actors

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/discovery"
	"github.com/tochemey/goakt/log"
	mocks "github.com/tochemey/goakt/mocks/discovery"
	"github.com/travisjeffery/go-dynaport"
)

func TestActorSystem(t *testing.T) {
	t.Run("New instance with Defaults", func(t *testing.T) {
		actorSys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSys)
		var iface any = actorSys
		_, ok := iface.(ActorSystem)
		assert.True(t, ok)
		assert.Equal(t, "testSys", actorSys.Name())
		assert.Empty(t, actorSys.Actors())
	})
	t.Run("New instance with Missing Name", func(t *testing.T) {
		sys, err := NewActorSystem("")
		assert.Error(t, err)
		assert.Nil(t, sys)
		assert.EqualError(t, err, ErrNameRequired.Error())
	})
	t.Run("With StartActor an actor when not started", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		actor := NewTestActor()
		actorRef := sys.StartActor(ctx, "Test", actor)
		assert.Nil(t, actorRef)
	})
	t.Run("With StartActor an actor when started", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actor := NewTestActor()
		actorRef := sys.StartActor(ctx, "Test", actor)
		assert.NotNil(t, actorRef)

		// stop the actor after some time
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With StartActor an actor already exist", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("test", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actor := NewTestActor()
		ref1 := sys.StartActor(ctx, "Test", actor)
		assert.NotNil(t, ref1)

		ref2 := sys.StartActor(ctx, "Test", actor)
		assert.NotNil(t, ref2)

		// point to the same memory address
		assert.True(t, ref1 == ref2)

		// stop the actor after some time
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("Start and Stop with clustering enabled", func(t *testing.T) {
		ctx := context.TODO()
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		podName := "pod"
		host := "127.0.0.1"

		// set the environments
		t.Setenv("GOSSIP_PORT", strconv.Itoa(gossipPort))
		t.Setenv("CLUSTER_PORT", strconv.Itoa(clusterPort))
		t.Setenv("REMOTING_PORT", strconv.Itoa(remotingPort))
		t.Setenv("POD_NAME", podName)
		t.Setenv("POD_IP", host)

		// define discovered addresses
		addrs := []string{
			fmt.Sprintf("%s:%d", host, gossipPort),
		}

		// mock the discovery provider
		provider := new(mocks.Provider)
		config := discovery.NewConfig()
		sd := discovery.NewServiceDiscovery(provider, config)
		newActorSystem, err := NewActorSystem(
			"test",
			WithLogger(log.DefaultLogger),
			WithClustering(sd, 20))
		require.NoError(t, err)

		provider.EXPECT().ID().Return("testDisco")
		provider.EXPECT().Initialize().Return(nil)
		provider.EXPECT().Register().Return(nil)
		provider.EXPECT().Deregister().Return(nil)
		provider.EXPECT().SetConfig(config).Return(nil)
		provider.EXPECT().DiscoverPeers().Return(addrs, nil)

		// start the actor system
		err = newActorSystem.Start(ctx)
		require.NoError(t, err)

		// stop the actor after some time
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()

		err = newActorSystem.Stop(ctx)
		require.NoError(t, err)

		provider.AssertExpectations(t)
	})
}
