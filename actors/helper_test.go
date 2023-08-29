package actors

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	testspb "github.com/tochemey/goakt/test/data/pb/v1"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m, goleak.IgnoreTopFunction("github.com/golang/glog.(*loggingT).flushDaemon"),
		goleak.IgnoreTopFunction("github.com/go-redis/redis/v8/internal/pool.(*ConnPool).reaper"),
		goleak.IgnoreTopFunction("golang.org/x/net/http2.(*serverConn).serve"),
		goleak.IgnoreTopFunction("internal/poll.runtime_pollWait"))
}

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

type TestActor struct{}

var _ Actor = (*TestActor)(nil)

// NewTestActor creates a TestActor
func NewTestActor() *TestActor {
	return &TestActor{}
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
		panic("Boom")
	case *testspb.TestReply:
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
		panic(ErrUnhandled)
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
		panic(ErrUnhandled)
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
		panic("panicked")
	default:
		panic(ErrUnhandled)
	}
}

func (c *ChildActor) PostStop(context.Context) error {
	return nil
}
