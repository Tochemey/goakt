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

// Tester is an actor that helps run various test scenarios
type Tester struct{}

// enforce compilation error
var _ Actor = (*Tester)(nil)

// NewTester creates a Tester
func NewTester() *Tester {
	return &Tester{}
}

// Init initialize the actor. This function can be used to set up some database connections
// or some sort of initialization before the actor init processing public
func (p *Tester) PreStart(context.Context) error {
	return nil
}

// Stop gracefully shuts down the given actor
func (p *Tester) PostStop(context.Context) error {
	return nil
}

// Receive processes any message dropped into the actor mailbox without a reply
func (p *Tester) Receive(ctx ReceiveContext) {
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

// Monitor is an actor that monitors another actor
// and reacts to its failure.
type Monitor struct{}

// enforce compilation error
var _ Actor = (*Monitor)(nil)

// NewMonitor creates an instance of Monitor
func NewMonitor() *Monitor {
	return &Monitor{}
}

func (p *Monitor) PreStart(context.Context) error {
	return nil
}

func (p *Monitor) Receive(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.TestSend:
	default:
		panic(ErrUnhandled)
	}
}

func (p *Monitor) PostStop(context.Context) error {
	return nil
}

// Monitored is an actor that is monitored
type Monitored struct{}

// enforce compilation error
var _ Actor = (*Monitored)(nil)

// NewMonitored creates an instance of Monitored
func NewMonitored() *Monitored {
	return &Monitored{}
}

func (c *Monitored) PreStart(context.Context) error {
	return nil
}

func (c *Monitored) Receive(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.TestSend:
	case *testspb.TestPanic:
		panic("panicked")
	default:
		panic(ErrUnhandled)
	}
}

func (c *Monitored) PostStop(context.Context) error {
	return nil
}
