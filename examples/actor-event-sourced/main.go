package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/pkg/errors"
	goakt "github.com/tochemey/goakt/actors"
	samplepb "github.com/tochemey/goakt/examples/protos/pb/v1"
	"github.com/tochemey/goakt/log"
	pb "github.com/tochemey/goakt/pb/goakt/v1"
	"github.com/tochemey/goakt/persistence"
	"google.golang.org/protobuf/proto"
)

func main() {
	ctx := context.Background()

	// use the goakt default logger. real-life implement the logger interface`
	logger := log.DefaultLogger

	// create the actor system configuration. kindly in real-life application handle the error
	config, _ := goakt.NewConfig("SampleActorSystem", "127.0.0.1:0",
		goakt.WithPassivationDisabled(),
		goakt.WithLogger(logger),
		goakt.WithActorInitMaxRetries(3))

	// create the actor system. kindly in real-life application handle the error
	actorSystem, _ := goakt.NewActorSystem(config)

	// start the actor system
	_ = actorSystem.Start(ctx)

	// create the event store
	eventStore := persistence.NewMemoryEventStore()

	// create a persistence id
	persistenceID := uuid.NewString()

	// create the persistence behavior
	behavior := NewAccountBehavior(persistenceID)

	// create the persistence actor using the behavior previously created
	persistentActor := persistence.NewEventSourcedActor[*samplepb.Account](behavior, eventStore)
	// spawn the actor
	pid := actorSystem.Spawn(ctx, behavior.Kind(), behavior.PersistenceID(), persistentActor)

	// send some commands to the pid
	var command proto.Message
	// create an account
	command = &samplepb.CreateAccount{
		AccountId:      persistenceID,
		AccountBalance: 500.00,
	}
	// send the command to the actor. Please don't ignore the error in production grid code
	reply, _ := goakt.SendSync(ctx, pid, command, time.Second)
	// cast the reply to a command reply because we know the persistence actor will always send a command reply
	commandReply := reply.(*pb.CommandReply)
	state := commandReply.GetReply().(*pb.CommandReply_StateReply)
	logger.Infof("resulting sequence number: %d", state.StateReply.GetSequenceNumber())

	account := new(samplepb.Account)
	_ = state.StateReply.GetState().UnmarshalTo(account)

	logger.Infof("current balance: %v", account.GetAccountBalance())

	// send another command to credit the balance
	command = &samplepb.CreditAccount{
		AccountId: persistenceID,
		Balance:   250,
	}
	reply, _ = goakt.SendSync(ctx, pid, command, time.Second)
	commandReply = reply.(*pb.CommandReply)
	state = commandReply.GetReply().(*pb.CommandReply_StateReply)
	logger.Infof("resulting sequence number: %d", state.StateReply.GetSequenceNumber())

	account = new(samplepb.Account)
	_ = state.StateReply.GetState().UnmarshalTo(account)

	logger.Infof("current balance: %v", account.GetAccountBalance())

	// capture ctrl+c
	interruptSignal := make(chan os.Signal, 1)
	signal.Notify(interruptSignal, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-interruptSignal

	// stop the actor system
	_ = actorSystem.Stop(ctx)
	os.Exit(0)
}

// AccountBehavior implements persistence.Behavior
type AccountBehavior struct {
	id string
}

// make sure that AccountBehavior is a true persistence behavior
var _ persistence.EventSourcedBehavior[*samplepb.Account] = &AccountBehavior{}

// NewAccountBehavior creates an instance of AccountBehavior
func NewAccountBehavior(id string) *AccountBehavior {
	return &AccountBehavior{id: id}
}

// Kind return the kind of behavior
func (a *AccountBehavior) Kind() string {
	return "Account"
}

// PersistenceID returns the persistence ID
func (a *AccountBehavior) PersistenceID() string {
	return a.id
}

// InitialState returns the initial state
func (a *AccountBehavior) InitialState() *samplepb.Account {
	return new(samplepb.Account)
}

// HandleCommand handles every command that is sent to the persistent behavior
func (a *AccountBehavior) HandleCommand(ctx context.Context, command persistence.Command, priorState *samplepb.Account) (event persistence.Event, err error) {
	switch cmd := command.(type) {
	case *samplepb.CreateAccount:
		// TODO in production grid app validate the command using the prior state
		return &samplepb.AccountCreated{
			AccountId:      cmd.GetAccountId(),
			AccountBalance: cmd.GetAccountBalance(),
		}, nil

	case *samplepb.CreditAccount:
		// TODO in production grid app validate the command using the prior state
		return &samplepb.AccountCredited{
			AccountId:      cmd.GetAccountId(),
			AccountBalance: cmd.GetBalance(),
		}, nil

	default:
		return nil, errors.New("unhandled command")
	}
}

// HandleEvent handles every event emitted
func (a *AccountBehavior) HandleEvent(ctx context.Context, event persistence.Event, priorState *samplepb.Account) (state *samplepb.Account, err error) {
	switch evt := event.(type) {
	case *samplepb.AccountCreated:
		return &samplepb.Account{
			AccountId:      evt.GetAccountId(),
			AccountBalance: evt.GetAccountBalance(),
		}, nil

	case *samplepb.AccountCredited:
		bal := priorState.GetAccountBalance() + evt.GetAccountBalance()
		return &samplepb.Account{
			AccountId:      evt.GetAccountId(),
			AccountBalance: bal,
		}, nil

	default:
		return nil, errors.New("unhandled event")
	}
}
