package actors

import (
	"context"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/discovery"
	mocks "github.com/tochemey/goakt/goaktmocks/discovery"
	"github.com/tochemey/goakt/log"
	testpb "github.com/tochemey/goakt/test/data/pb/v1"
	"github.com/travisjeffery/go-dynaport"
	"google.golang.org/protobuf/proto"
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
	t.Run("With Spawn an actor when not started", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		actor := NewTestActor()
		actorRef := sys.Spawn(ctx, "Test", actor)
		assert.Nil(t, actorRef)
	})
	t.Run("With Spawn an actor when started", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actor := NewTestActor()
		actorRef := sys.Spawn(ctx, "Test", actor)
		assert.NotNil(t, actorRef)

		// stop the actor after some time
		time.Sleep(time.Second)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With Spawn an actor already exist", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("test", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actor := NewTestActor()
		ref1 := sys.Spawn(ctx, "Test", actor)
		assert.NotNil(t, ref1)

		ref2 := sys.Spawn(ctx, "Test", actor)
		assert.NotNil(t, ref2)

		// point to the same memory address
		assert.True(t, ref1 == ref2)

		// stop the actor after some time
		time.Sleep(time.Second)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With clustering enabled:single node", func(t *testing.T) {
		ctx := context.TODO()
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		logger := log.New(log.DebugLevel, os.Stdout)

		podName := "pod"
		host := "127.0.0.1"

		// set the environments
		t.Setenv("GOSSIP_PORT", strconv.Itoa(gossipPort))
		t.Setenv("CLUSTER_PORT", strconv.Itoa(clusterPort))
		t.Setenv("REMOTING_PORT", strconv.Itoa(remotingPort))
		t.Setenv("NODE_NAME", podName)
		t.Setenv("NODE_IP", host)
		t.Setenv("GRPC_GO_LOG_VERBOSITY_LEVEL", "99")
		t.Setenv("GRPC_GO_LOG_SEVERITY_LEVEL", "info")

		// define discovered addresses
		addrs := []string{
			net.JoinHostPort(host, strconv.Itoa(gossipPort)),
		}

		// mock the discovery provider
		provider := new(mocks.Provider)
		config := discovery.NewConfig()
		sd := discovery.NewServiceDiscovery(provider, config)
		newActorSystem, err := NewActorSystem(
			"test",
			WithPassivationDisabled(),
			WithLogger(logger),
			WithReplyTimeout(time.Minute),
			WithClustering(sd, 9))
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

		// wait for the cluster to start
		time.Sleep(time.Second)

		// create an actor
		actorName := uuid.NewString()
		actor := NewTestActor()
		actorRef := newActorSystem.Spawn(ctx, actorName, actor)
		assert.NotNil(t, actorRef)

		// wait for a while for replication to take effect
		// otherwise the subsequent test will return actor not found
		time.Sleep(time.Second)

		// get the actor
		addr, pid, err := newActorSystem.ActorOf(ctx, actorName)
		require.NoError(t, err)
		require.NotNil(t, addr)
		require.Nil(t, pid)

		reply, err := RemoteAsk(ctx, addr, new(testpb.TestReply))
		require.NoError(t, err)
		require.NotNil(t, reply)

		// get the actor partition
		partition := newActorSystem.GetPartition(ctx, actorName)
		assert.GreaterOrEqual(t, partition, uint64(0))

		// stop the actor after some time
		time.Sleep(time.Second)

		t.Cleanup(func() {
			err = newActorSystem.Stop(ctx)
			assert.NoError(t, err)
			provider.AssertExpectations(t)
		})
	})
	t.Run("With remoting enabled", func(t *testing.T) {
		ctx := context.TODO()
		remotingPort := dynaport.Get(1)[0]

		logger := log.New(log.DebugLevel, os.Stdout)
		host := "127.0.0.1"

		newActorSystem, err := NewActorSystem(
			"test",
			WithPassivationDisabled(),
			WithLogger(logger),
			WithReplyTimeout(time.Minute),
			WithRemoting(host, int32(remotingPort)))
		require.NoError(t, err)

		// start the actor system
		err = newActorSystem.Start(ctx)
		require.NoError(t, err)

		// wait for the cluster to fully start
		time.Sleep(time.Second)

		// create an actor
		actorName := uuid.NewString()
		actor := NewTestActor()
		actorRef := newActorSystem.Spawn(ctx, actorName, actor)
		assert.NotNil(t, actorRef)

		path := NewPath(actorName, &Address{
			host:     host,
			port:     remotingPort,
			system:   newActorSystem.Name(),
			protocol: protocol,
		})
		addr := path.RemoteAddress()

		reply, err := RemoteAsk(ctx, addr, new(testpb.TestReply))
		require.NoError(t, err)
		require.NotNil(t, reply)

		actual := new(testpb.Reply)
		require.NoError(t, reply.UnmarshalTo(actual))

		expected := &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expected, actual))

		addr, pid, err := newActorSystem.ActorOf(ctx, actorName)
		require.Error(t, err)
		require.EqualError(t, err, ErrMethodCallNotAllowed.Error())
		require.Nil(t, addr)
		require.Nil(t, pid)

		// stop the actor after some time
		// stop the actor after some time
		time.Sleep(time.Second)

		t.Cleanup(func() {
			err = newActorSystem.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With ActorOf:remoting not enabled", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actorName := "testActor"
		actor := NewTestActor()
		actorRef := sys.Spawn(ctx, actorName, actor)
		assert.NotNil(t, actorRef)

		addr, pid, err := sys.ActorOf(ctx, actorName)
		require.NoError(t, err)
		require.NotNil(t, pid)
		require.Nil(t, addr)

		// stop the actor after some time
		time.Sleep(time.Second)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With ActorOf: not found", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actorName := "notFound"
		addr, pid, err := sys.ActorOf(ctx, actorName)
		require.Error(t, err)
		require.EqualError(t, err, ErrActorNotFound.Error())
		require.Nil(t, pid)
		require.Nil(t, addr)

		// stop the actor after some time
		time.Sleep(time.Second)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
}
