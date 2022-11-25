package actors

import (
	"context"
	"sync"
	"time"

	actorsv1 "github.com/tochemey/goakt/gen/actors/v1/tests"
	"github.com/tochemey/goakt/log"
)

type BenchActor struct {
	Wg sync.WaitGroup
}

func (p *BenchActor) ID() string {
	return "benchActor"
}

func (p *BenchActor) Init(ctx context.Context) error {
	return nil
}

func (p *BenchActor) Receive(message Message) error {
	switch message.Payload().(type) {
	case *actorsv1.TestSend:
		p.Wg.Done()
	case *actorsv1.TestReply:
		message.SetResponse(&actorsv1.Reply{Content: "received message"})
		p.Wg.Done()
	}
	return nil
}

func (p *BenchActor) Stop(ctx context.Context) {
}

type TestActor struct {
	id string
}

var _ Actor = (*TestActor)(nil)

// NewTestActor creates a TestActor
func NewTestActor(id string) *TestActor {
	return &TestActor{
		id: id,
	}
}

func (p *TestActor) ID() string {
	return p.id
}

// Init initialize the actor. This function can be used to set up some database connections
// or some sort of initialization before the actor init processing messages
func (p *TestActor) Init(ctx context.Context) error {
	return nil
}

// Stop gracefully shuts down the given actor
func (p *TestActor) Stop(ctx context.Context) {
}

// Receive processes any message dropped into the actor mailbox without a reply
func (p *TestActor) Receive(message Message) error {
	switch message.Payload().(type) {
	case *actorsv1.TestSend:
		// pass
	case *actorsv1.TestPanic:
		log.Panic("Boom")
	case *actorsv1.TestReply:
		message.SetResponse(&actorsv1.Reply{Content: "received message"})
	case *actorsv1.TestTimeout:
		// delay for a while before sending the reply
		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			time.Sleep(recvDelay)
			wg.Done()
		}()
		// block until timer is up
		wg.Wait()
	default:
		return ErrUnhandled
	}
	return nil
}
