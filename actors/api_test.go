package actors

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/log"
	testpb "github.com/tochemey/goakt/test/data/pb/v1"
	"github.com/travisjeffery/go-dynaport"
	"google.golang.org/protobuf/proto"
)

func TestAsk(t *testing.T) {
	// create the context
	ctx := context.TODO()
	// define the logger to use
	logger := log.New(log.DebugLevel, os.Stdout)
	// create the actor system
	sys, err := NewActorSystem("test",
		WithLogger(logger),
		WithPassivationDisabled())
	// assert there are no error
	require.NoError(t, err)

	// start the actor system
	err = sys.Start(ctx)
	assert.NoError(t, err)

	// create a test actor
	actorName := "test"
	actor := NewTester()
	actorRef := sys.Spawn(ctx, actorName, actor)
	assert.NotNil(t, actorRef)

	// create a message to send to the test actor
	message := new(testpb.TestReply)
	// send the message to the actor
	reply, err := Ask(ctx, actorRef, message, receivingTimeout)
	// perform some assertions
	require.NoError(t, err)
	assert.NotNil(t, reply)
	expected := &testpb.Reply{Content: "received message"}
	assert.True(t, proto.Equal(expected, reply))

	// stop the actor after some time
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	err = sys.Stop(ctx)
}

func TestTell(t *testing.T) {
	// create the context
	ctx := context.TODO()
	// define the logger to use
	logger := log.New(log.DebugLevel, os.Stdout)
	// create the actor system
	sys, err := NewActorSystem("test",
		WithLogger(logger),
		WithPassivationDisabled())
	// assert there are no error
	require.NoError(t, err)

	// start the actor system
	err = sys.Start(ctx)
	assert.NoError(t, err)

	// create a test actor
	actorName := "test"
	actor := NewTester()
	actorRef := sys.Spawn(ctx, actorName, actor)
	assert.NotNil(t, actorRef)

	// create a message to send to the test actor
	message := new(testpb.TestSend)
	// send the message to the actor
	err = Tell(ctx, actorRef, message)
	// perform some assertions
	require.NoError(t, err)
	count := 1
	assert.EqualValues(t, count, actorRef.ReceivedCount(ctx))

	// stop the actor after some time
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	err = sys.Stop(ctx)
}

func TestRemoteMessaging(t *testing.T) {
	// create the context
	ctx := context.TODO()
	// define the logger to use
	logger := log.New(log.DebugLevel, os.Stdout)
	// generate the remoting port
	nodePorts := dynaport.Get(1)
	remotingPort := nodePorts[0]
	host := "127.0.0.1"

	// create the actor system
	sys, err := NewActorSystem("test",
		WithLogger(logger),
		WithPassivationDisabled(),
		WithRemoting(host, int32(remotingPort)),
	)
	// assert there are no error
	require.NoError(t, err)

	// start the actor system
	err = sys.Start(ctx)
	assert.NoError(t, err)

	// create a test actor
	actorName := "test"
	actor := NewTester()
	actorRef := sys.Spawn(ctx, actorName, actor)
	assert.NotNil(t, actorRef)

	// get the address of the actor
	addr, err := RemoteLookup(ctx, host, remotingPort, actorName)
	require.NoError(t, err)

	var message proto.Message
	// create a message to send to the test actor
	message = new(testpb.TestReply)
	// send the message to the actor
	reply, err := RemoteAsk(ctx, addr, message)
	// perform some assertions
	require.NoError(t, err)
	require.NotNil(t, reply)
	require.True(t, reply.MessageIs(new(testpb.Reply)))

	actual := new(testpb.Reply)
	err = reply.UnmarshalTo(actual)
	require.NoError(t, err)

	expected := &testpb.Reply{Content: "received message"}
	assert.True(t, proto.Equal(expected, actual))

	// create a message to send to the test actor
	message = new(testpb.TestSend)
	// send the message to the actor
	for i := 0; i < 10; i++ {
		err = RemoteTell(ctx, addr, message)
		// perform some assertions
		require.NoError(t, err)
	}

	count := 11
	assert.EqualValues(t, count, actorRef.ReceivedCount(ctx))

	// stop the actor after some time
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	err = sys.Stop(ctx)
}
