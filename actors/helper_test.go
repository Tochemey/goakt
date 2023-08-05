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
	logger log.Logger
}

var _ Actor = (*TestActor)(nil)

// NewTestActor creates a TestActor
func NewTestActor() *TestActor {
	return &TestActor{
		logger: log.DefaultLogger,
	}
}

// Init initialize the actor. This function can be used to set up some database connections
// or some sort of initialization before the actor init processing public
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
		p.logger.Panic("Boom")
	case *testspb.TestReply:
		p.logger.Info("received request/response")
		ctx.Response(&testspb.Reply{Content: "received message"})
	case *testspb.TestTimeout:
		// delay for a while before sending the reply
		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			time.Sleep(receivingDelay)
			wg.Done()
		}()
		// block until timer is up
		wg.Wait()
	default:
		p.logger.Panic(ErrUnhandled)
	}
}

type ParentActor struct {
	logger log.Logger
}

var _ Actor = (*ParentActor)(nil)

func NewParentActor() *ParentActor {
	return &ParentActor{
		logger: log.DefaultLogger,
	}
}

func (p *ParentActor) PreStart(context.Context) error {
	return nil
}

func (p *ParentActor) Receive(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.TestSend:
	default:
		p.logger.Panic(ErrUnhandled)
	}
}

func (p *ParentActor) PostStop(context.Context) error {
	return nil
}

type ChildActor struct {
	logger log.Logger
}

var _ Actor = (*ChildActor)(nil)

func NewChildActor() *ChildActor {
	return &ChildActor{
		logger: log.DefaultLogger,
	}
}

func (c *ChildActor) PreStart(context.Context) error {
	return nil
}

func (c *ChildActor) Receive(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.TestSend:
	case *testspb.TestPanic:
		c.logger.Panic("panicked")
	default:
		c.logger.Panic(ErrUnhandled)
	}
}

func (c *ChildActor) PostStop(context.Context) error {
	return nil
}
