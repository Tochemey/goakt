package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/actors"
	"github.com/tochemey/goakt/log"
	pb "github.com/tochemey/goakt/pb/goakt/v1"
	testpb "github.com/tochemey/goakt/test/data/pb/v1"
	"go.uber.org/goleak"
	"google.golang.org/protobuf/proto"
)

func TestPersistentActor(t *testing.T) {
	t.Run("with state reply", func(t *testing.T) {
		defer goleak.VerifyNone(t)
		ctx := context.TODO()
		// create an actor config
		actorConfig, err := actors.NewConfig("TestActorSystem", "127.0.0.1:0",
			actors.WithPassivationDisabled(),
			actors.WithLogger(log.DiscardLogger),
			actors.WithActorInitMaxRetries(3))

		require.NoError(t, err)
		assert.NotNil(t, actorConfig)

		// create an actor system
		actorSystem, err := actors.NewActorSystem(actorConfig)
		require.NoError(t, err)
		assert.NotNil(t, actorSystem)

		// start the actor system
		err = actorSystem.Start(ctx)
		require.NoError(t, err)

		// create the event store
		eventStore := NewMemoryEventStore()
		// create a persistence id
		persistenceID := uuid.NewString()
		// create the persistence behavior
		behavior := newTestAccountBehavior(persistenceID)

		// create the persistence actor using the behavior previously created
		persistentActor := NewEventSourcedActor[*testpb.Account](behavior, eventStore)
		// spawn the actor
		pid := actorSystem.Spawn(ctx, behavior.Kind(), behavior.PersistenceID(), persistentActor)
		require.NotNil(t, pid)

		var command proto.Message

		command = &testpb.CreateAccount{AccountBalance: 500.00}
		// send the command to the actor
		reply, err := actors.SendSync(ctx, pid, command, time.Second)
		require.NoError(t, err)
		require.NotNil(t, reply)
		require.IsType(t, new(pb.CommandReply), reply)

		commandReply := reply.(*pb.CommandReply)
		require.IsType(t, new(pb.CommandReply_StateReply), commandReply.GetReply())

		state := commandReply.GetReply().(*pb.CommandReply_StateReply)
		assert.EqualValues(t, 1, state.StateReply.GetSequenceNumber())

		// marshal the resulting state
		resultingState := new(testpb.Account)
		err = state.StateReply.GetState().UnmarshalTo(resultingState)
		require.NoError(t, err)

		expected := &testpb.Account{
			AccountId:      persistenceID,
			AccountBalance: 500.00,
		}
		assert.True(t, proto.Equal(expected, resultingState))

		// send another command to credit the balance
		command = &testpb.CreditAccount{
			AccountId: persistenceID,
			Balance:   250,
		}
		reply, err = actors.SendSync(ctx, pid, command, time.Second)
		require.NoError(t, err)
		require.NotNil(t, reply)
		require.IsType(t, new(pb.CommandReply), reply)

		commandReply = reply.(*pb.CommandReply)
		require.IsType(t, new(pb.CommandReply_StateReply), commandReply.GetReply())

		state = commandReply.GetReply().(*pb.CommandReply_StateReply)
		assert.EqualValues(t, 2, state.StateReply.GetSequenceNumber())

		// marshal the resulting state
		resultingState = new(testpb.Account)
		err = state.StateReply.GetState().UnmarshalTo(resultingState)
		require.NoError(t, err)

		expected = &testpb.Account{
			AccountId:      persistenceID,
			AccountBalance: 750.00,
		}
		assert.True(t, proto.Equal(expected, resultingState))

		// stop the actor system
		err = actorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("with error reply", func(t *testing.T) {
		defer goleak.VerifyNone(t)
		ctx := context.TODO()
		// create an actor config
		actorConfig, err := actors.NewConfig("TestActorSystem", "127.0.0.1:0",
			actors.WithPassivationDisabled(),
			actors.WithLogger(log.DiscardLogger),
			actors.WithActorInitMaxRetries(3))

		require.NoError(t, err)
		assert.NotNil(t, actorConfig)

		// create an actor system
		actorSystem, err := actors.NewActorSystem(actorConfig)
		require.NoError(t, err)
		assert.NotNil(t, actorSystem)

		// start the actor system
		err = actorSystem.Start(ctx)
		require.NoError(t, err)

		// create the event store
		eventStore := NewMemoryEventStore()
		// create a persistence id
		persistenceID := uuid.NewString()
		// create the persistence behavior
		behavior := newTestAccountBehavior(persistenceID)

		// create the persistence actor using the behavior previously created
		persistentActor := NewEventSourcedActor[*testpb.Account](behavior, eventStore)
		// spawn the actor
		pid := actorSystem.Spawn(ctx, behavior.Kind(), behavior.PersistenceID(), persistentActor)
		require.NotNil(t, pid)

		var command proto.Message

		command = &testpb.CreateAccount{AccountBalance: 500.00}
		// send the command to the actor
		reply, err := actors.SendSync(ctx, pid, command, time.Second)
		require.NoError(t, err)
		require.NotNil(t, reply)
		require.IsType(t, new(pb.CommandReply), reply)

		commandReply := reply.(*pb.CommandReply)
		require.IsType(t, new(pb.CommandReply_StateReply), commandReply.GetReply())

		state := commandReply.GetReply().(*pb.CommandReply_StateReply)
		assert.EqualValues(t, 1, state.StateReply.GetSequenceNumber())

		// marshal the resulting state
		resultingState := new(testpb.Account)
		err = state.StateReply.GetState().UnmarshalTo(resultingState)
		require.NoError(t, err)

		expected := &testpb.Account{
			AccountId:      persistenceID,
			AccountBalance: 500.00,
		}
		assert.True(t, proto.Equal(expected, resultingState))

		// send another command to credit the balance
		command = &testpb.CreditAccount{
			AccountId: "different-id",
			Balance:   250,
		}
		reply, err = actors.SendSync(ctx, pid, command, time.Second)
		require.NoError(t, err)
		require.NotNil(t, reply)
		require.IsType(t, new(pb.CommandReply), reply)

		commandReply = reply.(*pb.CommandReply)
		require.IsType(t, new(pb.CommandReply_ErrorReply), commandReply.GetReply())

		errorReply := commandReply.GetReply().(*pb.CommandReply_ErrorReply)
		assert.Equal(t, "command sent to the wrong entity", errorReply.ErrorReply.GetMessage())

		// stop the actor system
		err = actorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("with unhandled command", func(t *testing.T) {
		defer goleak.VerifyNone(t)
		ctx := context.TODO()
		// create an actor config
		actorConfig, err := actors.NewConfig("TestActorSystem", "127.0.0.1:0",
			actors.WithPassivationDisabled(),
			actors.WithLogger(log.DiscardLogger),
			actors.WithActorInitMaxRetries(3))

		require.NoError(t, err)
		assert.NotNil(t, actorConfig)

		// create an actor system
		actorSystem, err := actors.NewActorSystem(actorConfig)
		require.NoError(t, err)
		assert.NotNil(t, actorSystem)

		// start the actor system
		err = actorSystem.Start(ctx)
		require.NoError(t, err)

		// create the event store
		eventStore := NewMemoryEventStore()
		// create a persistence id
		persistenceID := uuid.NewString()
		// create the persistence behavior
		behavior := newTestAccountBehavior(persistenceID)

		// create the persistence actor using the behavior previously created
		persistentActor := NewEventSourcedActor[*testpb.Account](behavior, eventStore)
		// spawn the actor
		pid := actorSystem.Spawn(ctx, behavior.Kind(), behavior.PersistenceID(), persistentActor)
		require.NotNil(t, pid)

		command := &testpb.TestSend{}
		// send the command to the actor
		reply, err := actors.SendSync(ctx, pid, command, time.Second)
		require.NoError(t, err)
		require.NotNil(t, reply)
		require.IsType(t, new(pb.CommandReply), reply)

		commandReply := reply.(*pb.CommandReply)
		require.IsType(t, new(pb.CommandReply_ErrorReply), commandReply.GetReply())

		errorReply := commandReply.GetReply().(*pb.CommandReply_ErrorReply)
		assert.Equal(t, "unhandled command", errorReply.ErrorReply.GetMessage())
		// stop the actor system
		err = actorSystem.Stop(ctx)
		assert.NoError(t, err)
	})
	//t.Run("with state recovery from event store", func(t *testing.T) {
	//	defer goleak.VerifyNone(t)
	//	ctx := context.TODO()
	//	// create an actor config
	//	actorConfig, err := actors.NewConfig("TestActorSystem", "127.0.0.1:0",
	//		actors.WithPassivationDisabled(),
	//		actors.WithLogger(log.DiscardLogger),
	//		actors.WithActorInitMaxRetries(3))
	//
	//	require.NoError(t, err)
	//	assert.NotNil(t, actorConfig)
	//
	//	// create an actor system
	//	actorSystem, err := actors.NewActorSystem(actorConfig)
	//	require.NoError(t, err)
	//	assert.NotNil(t, actorSystem)
	//
	//	// start the actor system
	//	err = actorSystem.Start(ctx)
	//	require.NoError(t, err)
	//
	//	// create the event store
	//	eventStore := NewMemoryEventStore()
	//	// create a persistence id
	//	persistenceID := uuid.NewString()
	//	// create the persistence behavior
	//	behavior := newTestAccountBehavior(persistenceID)
	//
	//	// create the persistence actor using the behavior previously created
	//	persistentActor := NewEventSourcedActor[*testpb.Account](behavior, eventStore)
	//	// spawn the actor
	//	pid := actorSystem.Spawn(ctx, behavior.Kind(), behavior.PersistenceID(), persistentActor)
	//	require.NotNil(t, pid)
	//
	//	var command proto.Message
	//
	//	command = &testpb.CreateAccount{AccountBalance: 500.00}
	//	// send the command to the actor
	//	reply, err := actors.SendSync(ctx, pid, command, time.Second)
	//	require.NoError(t, err)
	//	require.NotNil(t, reply)
	//	require.IsType(t, new(pb.CommandReply), reply)
	//
	//	commandReply := reply.(*pb.CommandReply)
	//	require.IsType(t, new(pb.CommandReply_StateReply), commandReply.GetReply())
	//
	//	state := commandReply.GetReply().(*pb.CommandReply_StateReply)
	//	assert.EqualValues(t, 1, state.StateReply.GetSequenceNumber())
	//
	//	// marshal the resulting state
	//	resultingState := new(testpb.Account)
	//	err = state.StateReply.GetState().UnmarshalTo(resultingState)
	//	require.NoError(t, err)
	//
	//	expected := &testpb.Account{
	//		AccountId:      persistenceID,
	//		AccountBalance: 500.00,
	//	}
	//	assert.True(t, proto.Equal(expected, resultingState))
	//
	//	// send another command to credit the balance
	//	command = &testpb.CreditAccount{
	//		AccountId: persistenceID,
	//		Balance:   250,
	//	}
	//	reply, err = actors.SendSync(ctx, pid, command, time.Second)
	//	require.NoError(t, err)
	//	require.NotNil(t, reply)
	//	require.IsType(t, new(pb.CommandReply), reply)
	//
	//	commandReply = reply.(*pb.CommandReply)
	//	require.IsType(t, new(pb.CommandReply_StateReply), commandReply.GetReply())
	//
	//	state = commandReply.GetReply().(*pb.CommandReply_StateReply)
	//	assert.EqualValues(t, 2, state.StateReply.GetSequenceNumber())
	//
	//	// marshal the resulting state
	//	resultingState = new(testpb.Account)
	//	err = state.StateReply.GetState().UnmarshalTo(resultingState)
	//	require.NoError(t, err)
	//
	//	expected = &testpb.Account{
	//		AccountId:      persistenceID,
	//		AccountBalance: 750.00,
	//	}
	//	assert.True(t, proto.Equal(expected, resultingState))
	//
	//	// shutdown the persistent actor
	//
	//	// stop the actor system
	//	err = actorSystem.Stop(ctx)
	//	assert.NoError(t, err)
	//})
}

// testAccountBehavior implements persistence.Behavior
type testAccountBehavior struct {
	id string
}

// make sure that testAccountBehavior is a true persistence behavior
var _ EventSourcedBehavior[*testpb.Account] = &testAccountBehavior{}

// newTestAccountBehavior creates an instance of testAccountBehavior
func newTestAccountBehavior(id string) *testAccountBehavior {
	return &testAccountBehavior{id: id}
}

// Kind return the kind of behavior
func (a *testAccountBehavior) Kind() string {
	return "Account"
}

// PersistenceID returns the persistence ID
func (a *testAccountBehavior) PersistenceID() string {
	return a.id
}

// InitialState returns the initial state
func (a *testAccountBehavior) InitialState() *testpb.Account {
	return new(testpb.Account)
}

// HandleCommand handles every command that is sent to the persistent behavior
func (a *testAccountBehavior) HandleCommand(ctx context.Context, command Command, priorState *testpb.Account) (event Event, err error) {
	switch cmd := command.(type) {
	case *testpb.CreateAccount:
		// TODO in production grid app validate the command using the prior state
		return &testpb.AccountCreated{
			AccountId:      a.id,
			AccountBalance: cmd.GetAccountBalance(),
		}, nil

	case *testpb.CreditAccount:
		if cmd.GetAccountId() == a.id {
			return &testpb.AccountCredited{
				AccountId:      cmd.GetAccountId(),
				AccountBalance: cmd.GetBalance(),
			}, nil
		}

		return nil, errors.New("command sent to the wrong entity")

	default:
		return nil, errors.New("unhandled command")
	}
}

// HandleEvent handles every event emitted
func (a *testAccountBehavior) HandleEvent(ctx context.Context, event Event, priorState *testpb.Account) (state *testpb.Account, err error) {
	switch evt := event.(type) {
	case *testpb.AccountCreated:
		return &testpb.Account{
			AccountId:      evt.GetAccountId(),
			AccountBalance: evt.GetAccountBalance(),
		}, nil

	case *testpb.AccountCredited:
		bal := priorState.GetAccountBalance() + evt.GetAccountBalance()
		return &testpb.Account{
			AccountId:      evt.GetAccountId(),
			AccountBalance: bal,
		}, nil

	default:
		return nil, errors.New("unhandled event")
	}
}
