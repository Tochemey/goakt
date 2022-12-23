package actors

import (
	"context"
	"sync"
	"time"

	"github.com/tochemey/goakt/log"
	testspb "github.com/tochemey/goakt/test/data/pb/v1"
)

type BenchActor struct {
	Wg sync.WaitGroup
}

func (p *BenchActor) PreStart(context.Context) error {
	return nil
}

func (p *BenchActor) Receive(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.TestSend:
		p.Wg.Done()
	case *testspb.TestReply:
		ctx.Response(&testspb.Reply{Content: "received message"})
		p.Wg.Done()
	}
}

func (p *BenchActor) PostStop(context.Context) error {
	return nil
}

type TestActor struct {
}

var _ Actor = (*TestActor)(nil)

// NewTestActor creates a TestActor
func NewTestActor() *TestActor {
	return &TestActor{}
}

// Init initialize the actor. This function can be used to set up some database connections
// or some sort of initialization before the actor init processing messages
func (p *TestActor) PreStart(context.Context) error {
	return nil
}

// Stop gracefully shuts down the given actor
func (p *TestActor) PostStop(context.Context) error {
	return nil
}

// Receive processes any message dropped into the actor mailbox without a reply
func (p *TestActor) Receive(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.TestSend:
	case *testspb.TestPanic:
		log.Panic("Boom")
	case *testspb.TestReply:
		log.Info("received request/response")
		ctx.Response(&testspb.Reply{Content: "received message"})
	case *testspb.TestTimeout:
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
		log.Panic(ErrUnhandled)
	}
}

type ParentActor struct{}

var _ Actor = (*ParentActor)(nil)

func NewParentActor() *ParentActor {
	return &ParentActor{}
}

func (p *ParentActor) PreStart(context.Context) error {
	return nil
}

func (p *ParentActor) Receive(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.TestSend:
	default:
		log.Panic(ErrUnhandled)
	}
}

func (p *ParentActor) PostStop(context.Context) error {
	return nil
}

type ChildActor struct{}

var _ Actor = (*ChildActor)(nil)

func NewChildActor() *ChildActor {
	return &ChildActor{}
}

func (c *ChildActor) PreStart(context.Context) error {
	return nil
}

func (c *ChildActor) Receive(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.TestSend:
	case *testspb.TestPanic:
		log.Panic("panicked")
	default:
		log.Panic(ErrUnhandled)
	}
}

func (c *ChildActor) PostStop(context.Context) error {
	return nil
}
