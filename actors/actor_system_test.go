package actors

import (
	"context"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	eventspb "github.com/tochemey/goakt/pb/events/v1"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/discovery"
	"github.com/tochemey/goakt/log"
	addresspb "github.com/tochemey/goakt/pb/address/v1"
	testpb "github.com/tochemey/goakt/test/data/pb/v1"
	testkit "github.com/tochemey/goakt/testkit/discovery"
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
	t.Run("With invalid actor system Name", func(t *testing.T) {
		sys, err := NewActorSystem("$omeN@me")
		assert.Error(t, err)
		assert.Nil(t, sys)
		assert.EqualError(t, err, ErrInvalidActorSystemName.Error())
	})
	t.Run("With Spawn an actor when not System started", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		actor := NewTester()
		actorRef, err := sys.Spawn(ctx, "Test", actor)
		assert.Error(t, err)
		assert.EqualError(t, err, ErrActorSystemNotStarted.Error())
		assert.Nil(t, actorRef)
	})
	t.Run("With Spawn an actor when started", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actor := NewTester()
		actorRef, err := sys.Spawn(ctx, "Test", actor)
		assert.NoError(t, err)
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

		actor := NewTester()
		ref1, err := sys.Spawn(ctx, "Test", actor)
		assert.NoError(t, err)
		assert.NotNil(t, ref1)

		ref2, err := sys.Spawn(ctx, "Test", actor)
		assert.NotNil(t, ref2)
		assert.NoError(t, err)

		// point to the same memory address
		assert.True(t, ref1 == ref2)

		// stop the actor after some time
		time.Sleep(time.Second)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With clustering enabled", func(t *testing.T) {
		ctx := context.TODO()
		nodePorts := dynaport.Get(3)
		gossipPort := nodePorts[0]
		clusterPort := nodePorts[1]
		remotingPort := nodePorts[2]

		logger := log.New(log.DebugLevel, os.Stdout)

		podName := "pod"
		host := "localhost"

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
		provider := new(testkit.Provider)
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
		actor := NewTester()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// wait for a while for replication to take effect
		// otherwise the subsequent test will return actor not found
		time.Sleep(time.Second)

		// get the actor
		addr, pid, err := newActorSystem.ActorOf(ctx, actorName)
		require.NoError(t, err)
		require.NotNil(t, addr)
		require.Nil(t, pid)

		// use RemoteActor method and compare the results
		remoteAddr, err := newActorSystem.RemoteActor(ctx, actorName)
		require.NoError(t, err)
		require.NotNil(t, remoteAddr)
		require.True(t, proto.Equal(remoteAddr, addr))

		reply, err := RemoteAsk(ctx, addr, new(testpb.TestReply))
		require.NoError(t, err)
		require.NotNil(t, reply)

		// get the actor partition
		partition := newActorSystem.GetPartition(actorName)
		assert.GreaterOrEqual(t, partition, uint64(0))

		// assert actor not found
		actorName = "some-actor"
		addr, pid, err = newActorSystem.ActorOf(ctx, actorName)
		require.Error(t, err)
		require.EqualError(t, err, ErrActorNotFound(actorName).Error())
		require.Nil(t, addr)

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
		host := "localhost"

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
		actor := NewTester()
		actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
		assert.NoError(t, err)
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
		time.Sleep(time.Second)
		err = newActorSystem.Kill(ctx, actorName)
		require.NoError(t, err)

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
		actor := NewTester()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		assert.NoError(t, err)
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
		require.EqualError(t, err, ErrActorNotFound(actorName).Error())
		require.Nil(t, pid)
		require.Nil(t, addr)

		// stop the actor after some time
		time.Sleep(time.Second)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With ActorOf actor system started", func(t *testing.T) {
		ctx := context.TODO()
		remotingPort := dynaport.Get(1)[0]

		logger := log.New(log.DebugLevel, os.Stdout)
		host := "localhost"

		newActorSystem, err := NewActorSystem(
			"test",
			WithPassivationDisabled(),
			WithLogger(logger),
			WithReplyTimeout(time.Minute),
			WithRemoting(host, int32(remotingPort)))
		require.NoError(t, err)

		// create an actor
		actorName := uuid.NewString()

		addr, pid, err := newActorSystem.ActorOf(ctx, actorName)
		require.Error(t, err)
		require.EqualError(t, err, ErrActorSystemNotStarted.Error())
		require.Nil(t, addr)
		require.Nil(t, pid)
	})
	t.Run("With ReSpawn", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actorName := "Exchanger"
		actorRef, err := sys.Spawn(ctx, actorName, &Exchanger{})
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// send a message to the actor
		reply, err := Ask(ctx, actorRef, new(testpb.TestReply), receivingTimeout)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expected := new(testpb.Reply)
		require.True(t, proto.Equal(expected, reply))
		require.True(t, actorRef.IsRunning())

		// wait for a while for the system to stop
		time.Sleep(time.Second)
		// restart the actor
		_, err = sys.ReSpawn(ctx, actorName)
		require.NoError(t, err)

		// wait for the actor to complete start
		// TODO we can add a callback for complete start
		time.Sleep(time.Second)
		require.True(t, actorRef.IsRunning())

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With ReSpawn with PreStart failure", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys",
			WithLogger(log.DiscardLogger),
			WithExpireActorAfter(time.Minute))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actorName := "actor"
		actorRef, err := sys.Spawn(ctx, actorName, NewRestartBreaker())
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		require.True(t, actorRef.IsRunning())

		// wait for a while for the system to stop
		time.Sleep(time.Second)
		// restart the actor
		pid, err := sys.ReSpawn(ctx, actorName)
		require.Error(t, err)
		require.Nil(t, pid)

		require.False(t, actorRef.IsRunning())

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With ReSpawn: actor not found", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actorName := "Exchanger"
		actorRef, err := sys.Spawn(ctx, actorName, &Exchanger{})
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// send a message to the actor
		reply, err := Ask(ctx, actorRef, new(testpb.TestReply), receivingTimeout)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expected := new(testpb.Reply)
		require.True(t, proto.Equal(expected, reply))
		require.True(t, actorRef.IsRunning())
		// stop the actor after some time
		time.Sleep(time.Second)

		err = sys.Kill(ctx, actorName)
		require.NoError(t, err)

		// wait for a while for the system to stop
		time.Sleep(time.Second)
		// restart the actor
		_, err = sys.ReSpawn(ctx, actorName)
		require.Error(t, err)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With ReSpawn an actor when not System started", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		_, err := sys.ReSpawn(ctx, "some-actor")
		assert.Error(t, err)
		assert.EqualError(t, err, "actor system has not started yet")
	})
	t.Run("ReSpawn with remoting enabled", func(t *testing.T) {
		ctx := context.TODO()
		remotingPort := dynaport.Get(1)[0]

		logger := log.New(log.DebugLevel, os.Stdout)
		host := "localhost"

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

		actorName := "Exchanger"
		actorRef, err := newActorSystem.Spawn(ctx, actorName, &Exchanger{})
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// send a message to the actor
		reply, err := Ask(ctx, actorRef, new(testpb.TestReply), receivingTimeout)
		require.NoError(t, err)
		require.NotNil(t, reply)
		expected := new(testpb.Reply)
		require.True(t, proto.Equal(expected, reply))
		require.True(t, actorRef.IsRunning())
		// stop the actor after some time
		time.Sleep(time.Second)

		// restart the actor
		_, err = newActorSystem.ReSpawn(ctx, actorName)
		require.NoError(t, err)

		// wait for the actor to complete start
		// TODO we can add a callback for complete start
		time.Sleep(time.Second)
		require.True(t, actorRef.IsRunning())

		t.Cleanup(func() {
			err = newActorSystem.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With NumActors", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actorName := "Exchanger"
		actorRef, err := sys.Spawn(ctx, actorName, &Exchanger{})
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// wait for the start of the actor to be complete
		time.Sleep(time.Second)

		assert.EqualValues(t, 1, sys.NumActors())

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With remoting enabled: Actor not found", func(t *testing.T) {
		ctx := context.TODO()
		remotingPort := dynaport.Get(1)[0]

		logger := log.New(log.DebugLevel, os.Stdout)
		host := "localhost"

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

		actorName := "some-actor"
		addr, err := RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)
		require.Nil(t, addr)

		// attempt to send a message will fail
		reply, err := RemoteAsk(ctx, &addresspb.Address{
			Host: host,
			Port: int32(remotingPort),
			Name: actorName,
			Id:   "",
		}, new(testpb.TestReply))
		require.Error(t, err)
		require.Nil(t, reply)

		// stop the actor after some time
		time.Sleep(time.Second)

		t.Cleanup(func() {
			err = newActorSystem.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With RemoteActor failure when system not started", func(t *testing.T) {
		ctx := context.TODO()
		remotingPort := dynaport.Get(1)[0]

		logger := log.New(log.DebugLevel, os.Stdout)
		host := "localhost"

		newActorSystem, err := NewActorSystem(
			"test",
			WithPassivationDisabled(),
			WithLogger(logger),
			WithReplyTimeout(time.Minute),
			WithRemoting(host, int32(remotingPort)))
		require.NoError(t, err)

		actorName := "some-actor"
		remoteAddr, err := newActorSystem.RemoteActor(ctx, actorName)
		require.Error(t, err)
		require.EqualError(t, err, ErrActorSystemNotStarted.Error())
		require.Nil(t, remoteAddr)
	})
	t.Run("With RemoteActor failure when system not started", func(t *testing.T) {
		ctx := context.TODO()
		remotingPort := dynaport.Get(1)[0]

		logger := log.New(log.DebugLevel, os.Stdout)
		host := "localhost"

		newActorSystem, err := NewActorSystem(
			"test",
			WithPassivationDisabled(),
			WithLogger(logger),
			WithReplyTimeout(time.Minute),
			WithRemoting(host, int32(remotingPort)))
		require.NoError(t, err)

		err = newActorSystem.Stop(ctx)
		require.Error(t, err)
		require.EqualError(t, err, ErrActorSystemNotStarted.Error())
	})
	t.Run("With RemoteActor failure when cluster is not enabled", func(t *testing.T) {
		ctx := context.TODO()
		remotingPort := dynaport.Get(1)[0]

		logger := log.New(log.DebugLevel, os.Stdout)
		host := "localhost"

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

		// wait for the system to properly start
		actorName := "some-actor"
		remoteAddr, err := newActorSystem.RemoteActor(ctx, actorName)
		require.Error(t, err)
		require.EqualError(t, err, ErrClusterDisabled.Error())
		require.Nil(t, remoteAddr)

		// stop the actor after some time
		time.Sleep(time.Second)

		t.Cleanup(func() {
			err = newActorSystem.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With LocalActor", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		// create an actor
		actorName := "Exchanger"
		ref, err := sys.Spawn(ctx, actorName, &Exchanger{})
		assert.NoError(t, err)
		require.NotNil(t, ref)

		// locate the actor
		local, err := sys.LocalActor(actorName)
		require.NoError(t, err)
		require.NotNil(t, local)

		require.Equal(t, ref.ActorPath().String(), local.ActorPath().String())

		// stop the actor after some time
		time.Sleep(time.Second)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With LocalActor: Actor not found", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		// locate the actor
		ref, err := sys.LocalActor("some-name")
		require.Error(t, err)
		require.Nil(t, ref)
		require.EqualError(t, err, ErrActorNotFound("some-name").Error())

		// stop the actor after some time
		time.Sleep(time.Second)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With LocalActor when system not started", func(t *testing.T) {
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// create an actor
		actorName := "Exchanger"

		// locate the actor
		local, err := sys.LocalActor(actorName)
		require.Error(t, err)
		require.EqualError(t, err, ErrActorSystemNotStarted.Error())
		require.Nil(t, local)
	})
	t.Run("With Kill an actor when not System started", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Kill(ctx, "Test")
		assert.Error(t, err)
		assert.EqualError(t, err, "actor system has not started yet")
	})
	t.Run("With Kill an actor when actor not found", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)
		err = sys.Kill(ctx, "Test")
		assert.Error(t, err)
		assert.EqualError(t, err, "actor=goakt://testSys@/Test not found")
		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With housekeeping", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("housekeeperSys",
			WithLogger(log.DefaultLogger),
			WithExpireActorAfter(passivateAfter))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		// wait for the system to properly start
		time.Sleep(time.Second)

		actorName := "HousekeeperActor"
		actorHandler := NewTester()
		actorRef, err := sys.Spawn(ctx, actorName, actorHandler)
		assert.NoError(t, err)
		require.NotNil(t, actorRef)

		// wait for the actor to properly start
		time.Sleep(time.Second)

		// locate the actor
		ref, err := sys.LocalActor(actorName)
		require.Error(t, err)
		require.Nil(t, ref)
		require.EqualError(t, err, ErrActorNotFound(actorName).Error())

		// stop the actor after some time
		time.Sleep(time.Second)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With GetPartition returning zero in non cluster env", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("housekeeperSys",
			WithLogger(log.DefaultLogger),
			WithExpireActorAfter(passivateAfter))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		// wait for the system to properly start
		time.Sleep(time.Second)

		partition := sys.GetPartition("some-actor")
		assert.Zero(t, partition)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With error Stop", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actor := &PostStopBreaker{}
		actorRef, err := sys.Spawn(ctx, "Test", actor)
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// stop the actor after some time
		time.Sleep(time.Second)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.Error(t, err)
		})
	})
	t.Run("With deadletters subscription ", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		// wait for complete start
		time.Sleep(time.Second)

		// create a deadletter subscriber
		consumer, err := sys.Subscribe(eventspb.Event_DEAD_LETTER)
		require.NoError(t, err)
		require.NotNil(t, consumer)

		// create the blackhole actor
		actor := &BlackHole{}
		actorRef, err := sys.Spawn(ctx, "BlackHole ", actor)
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// wait a while
		time.Sleep(time.Second)

		// every message sent to the actor will result in deadletters
		for i := 0; i < 5; i++ {
			require.NoError(t, Tell(ctx, actorRef, new(testpb.TestSend)))
		}

		time.Sleep(time.Second)

		var items []*eventspb.DeadletterEvent
		for message := range consumer.Iterator() {
			payload := message.Payload()
			deadletter := payload.(*eventspb.DeadletterEvent)
			items = append(items, deadletter)
		}

		require.Len(t, items, 5)

		// unsubscribe the consumer
		err = sys.Unsubscribe(eventspb.Event_DEAD_LETTER, consumer)
		require.NoError(t, err)

		t.Cleanup(func() {
			err = sys.Stop(ctx)
			assert.NoError(t, err)
		})
	})
	t.Run("With deadletters subscription when not started", func(t *testing.T) {
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// create a deadletter subscriber
		consumer, err := sys.Subscribe(eventspb.Event_DEAD_LETTER)
		require.Error(t, err)
		require.Nil(t, consumer)
	})
	t.Run("With deadletters unsubscription when not started", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		consumer, err := sys.Subscribe(eventspb.Event_DEAD_LETTER)
		require.NoError(t, err)
		require.NotNil(t, consumer)

		// stop the actor system
		assert.NoError(t, sys.Stop(ctx))

		time.Sleep(time.Second)

		// create a deadletter subscriber
		err = sys.Unsubscribe(eventspb.Event_DEAD_LETTER, consumer)
		require.Error(t, err)
	})
}
