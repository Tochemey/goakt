/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package actors

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"go.uber.org/atomic"
	"go.uber.org/goleak"

	"github.com/tochemey/goakt/v2/discovery"
	"github.com/tochemey/goakt/v2/discovery/nats"
	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/log"
	testspb "github.com/tochemey/goakt/v2/test/data/testpb"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m, goleak.IgnoreTopFunction("github.com/golang/glog.(*loggingT).flushDaemon"),
		goleak.IgnoreTopFunction("github.com/go-redis/redis/v8/internal/pool.(*ConnPool).reaper"),
		goleak.IgnoreTopFunction("golang.org/x/net/http2.(*serverConn).serve"),
		goleak.IgnoreTopFunction("github.com/nats-io/nats%2ego.(*Conn).doReconnect"),
		goleak.IgnoreTopFunction("sync.runtime_notifyListWait"),
		goleak.IgnoreTopFunction("internal/poll.runtime_pollWait"))
}

// testActor is an actor that helps run various test scenarios
type testActor struct {
	counter *atomic.Int64
}

// enforce compilation error
var _ Actor = (*testActor)(nil)

// newTestActor creates a testActor
func newTestActor() *testActor {
	return &testActor{
		counter: atomic.NewInt64(0),
	}
}

// Init initialize the actor. This function can be used to set up some database connections
// or some sort of initialization before the actor init processing public
func (p *testActor) PreStart(context.Context) error {
	return nil
}

// Shutdown gracefully shuts down the given actor
func (p *testActor) PostStop(context.Context) error {
	p.counter.Store(0)
	return nil
}

// Receive processes any message dropped into the actor mailbox without a reply
func (p *testActor) Receive(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testspb.TestSend:
		p.counter.Inc()
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
		ctx.Unhandled()
	}
}

// supervisor is an actor that monitors another actor
// and reacts to its failure.
type supervisor struct{}

// enforce compilation error
var _ Actor = (*supervisor)(nil)

// newSupervisor creates an instance of supervisor
func newSupervisor() *supervisor {
	return &supervisor{}
}

func (p *supervisor) PreStart(context.Context) error {
	return nil
}

func (p *supervisor) Receive(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testspb.TestSend:
	default:
		panic(ErrUnhandled)
	}
}

func (p *supervisor) PostStop(context.Context) error {
	return nil
}

// supervised is an actor that is monitored
type supervised struct{}

// enforce compilation error
var _ Actor = (*supervised)(nil)

// newSupervised creates an instance of supervised
func newSupervised() *supervised {
	return &supervised{}
}

func (c *supervised) PreStart(context.Context) error {
	return nil
}

func (c *supervised) Receive(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testspb.TestSend:
	case *testspb.TestPanic:
		panic("panicked")
	default:
		panic(ErrUnhandled)
	}
}

func (c *supervised) PostStop(context.Context) error {
	return nil
}

// userActor is used to test the actor behavior
type userActor struct{}

// enforce compilation error
var _ Actor = &userActor{}

func (x *userActor) PreStart(_ context.Context) error {
	return nil
}

func (x *userActor) PostStop(_ context.Context) error {
	return nil
}

func (x *userActor) Receive(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testspb.TestLogin:
		ctx.Response(new(testspb.TestLoginSuccess))
		ctx.Become(x.Authenticated)
	case *testspb.CreateAccount:
		ctx.Response(new(testspb.AccountCreated))
		ctx.BecomeStacked(x.CreditAccount)
	}
}

// Authenticated behavior is executed when the actor receive the TestAuth message
func (x *userActor) Authenticated(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testspb.TestReadiness:
		ctx.Response(new(testspb.TestReady))
		ctx.UnBecome()
	}
}

func (x *userActor) CreditAccount(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testspb.CreditAccount:
		ctx.Response(new(testspb.AccountCredited))
		ctx.BecomeStacked(x.DebitAccount)
	case *testspb.TestBye:
		_ = ctx.Self().Shutdown(ctx.Context())
	}
}

func (x *userActor) DebitAccount(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testspb.DebitAccount:
		ctx.Response(new(testspb.AccountDebited))
		ctx.UnBecomeStacked()
	}
}

type exchanger struct{}

func (e *exchanger) PreStart(context.Context) error {
	return nil
}

func (e *exchanger) Receive(ctx ReceiveContext) {
	message := ctx.Message()
	switch message.(type) {
	case *goaktpb.PostStart:
	case *testspb.TestSend:
		_ = ctx.Self().Tell(ctx.Context(), ctx.Sender(), new(testspb.TestSend))
	case *testspb.TestReply:
		ctx.Response(new(testspb.Reply))
	case *testspb.TestRemoteSend:
		_ = ctx.Self().RemoteTell(context.Background(), ctx.RemoteSender(), new(testspb.TestBye))
	case *testspb.TestBye:
		_ = ctx.Self().Shutdown(ctx.Context())
	}
}

func (e *exchanger) PostStop(context.Context) error {
	return nil
}

var _ Actor = &exchanger{}

type stasher struct{}

func (x *stasher) PreStart(context.Context) error {
	return nil
}

func (x *stasher) Receive(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testspb.TestStash:
		ctx.Become(x.Ready)
		ctx.Stash()
	case *testspb.TestLogin:
	case *testspb.TestBye:
		_ = ctx.Self().Shutdown(ctx.Context())
	}
}

func (x *stasher) Ready(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testspb.TestStash:
	case *testspb.TestLogin:
		ctx.Stash()
	case *testspb.TestSend:
		// do nothing
	case *testspb.TestUnstashAll:
		ctx.UnBecome()
		ctx.UnstashAll()
	case *testspb.TestUnstash:
		ctx.Unstash()
	}
}

func (x *stasher) PostStop(context.Context) error {
	return nil
}

var _ Actor = &stasher{}

type testPreStart struct{}

func (x *testPreStart) PreStart(context.Context) error {
	return errors.New("failed")
}

func (x *testPreStart) Receive(ReceiveContext) {}

func (x *testPreStart) PostStop(context.Context) error {
	return nil
}

var _ Actor = &testPreStart{}

type testPostStop struct{}

func (x *testPostStop) PreStart(context.Context) error {
	return nil
}

func (x *testPostStop) Receive(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testspb.TestSend:
	case *testspb.TestPanic:
		panic("panicked")
	}
}

func (x *testPostStop) PostStop(context.Context) error {
	return errors.New("failed")
}

var _ Actor = &testPostStop{}

type testRestart struct {
	counter *atomic.Int64
}

func newTestRestart() *testRestart {
	return &testRestart{counter: atomic.NewInt64(0)}
}

func (x *testRestart) PreStart(context.Context) error {
	// increment counter
	x.counter.Inc()
	// error when counter is greater than 1
	if x.counter.Load() > 1 {
		return errors.New("cannot restart")
	}
	return nil
}

func (x *testRestart) Receive(ReceiveContext) {
}

func (x *testRestart) PostStop(context.Context) error {
	return nil
}

var _ Actor = &testRestart{}

type forwarder struct {
	actorRef PID
}

func (x *forwarder) PreStart(context.Context) error {
	return nil
}

func (x *forwarder) Receive(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testspb.TestBye:
		ctx.Forward(x.actorRef)
	}
}

func (x *forwarder) PostStop(context.Context) error {
	return nil
}

var _ Actor = &forwarder{}

type discarder struct{}

var _ Actor = &discarder{}

func (d *discarder) PreStart(context.Context) error {
	return nil
}

func (d *discarder) Receive(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
		// pass
	default:
		ctx.Unhandled()
	}
}

func (d *discarder) PostStop(context.Context) error {
	return nil
}

func startNatsServer(t *testing.T) *natsserver.Server {
	t.Helper()
	serv, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1",
		Port: -1,
	})

	require.NoError(t, err)

	ready := make(chan bool)
	go func() {
		ready <- true
		serv.Start()
	}()
	<-ready

	if !serv.ReadyForConnections(2 * time.Second) {
		t.Fatalf("nats-io server failed to start")
	}

	return serv
}

func startClusterSystem(t *testing.T, nodeName, serverAddr string) (ActorSystem, discovery.Provider) {
	ctx := context.TODO()
	logger := log.New(log.DebugLevel, os.Stdout)

	// generate the ports for the single startNode
	nodePorts := dynaport.Get(3)
	gossipPort := nodePorts[0]
	clusterPort := nodePorts[1]
	remotingPort := nodePorts[2]

	// create a Cluster startNode
	host := "127.0.0.1"
	// create the various config option
	applicationName := "accounts"
	actorSystemName := "testSystem"
	natsSubject := "some-subject"
	// create the config
	config := nats.Config{
		ApplicationName: applicationName,
		ActorSystemName: actorSystemName,
		NatsServer:      serverAddr,
		NatsSubject:     natsSubject,
	}

	hostNode := discovery.Node{
		Name:         host,
		Host:         host,
		GossipPort:   gossipPort,
		PeersPort:    clusterPort,
		RemotingPort: remotingPort,
	}

	// create the instance of provider
	provider := nats.NewDiscovery(&config, &hostNode)

	// create the actor system
	system, err := NewActorSystem(
		nodeName,
		WithPassivationDisabled(),
		WithLogger(logger),
		WithReplyTimeout(time.Minute),
		WithRemoting(host, int32(remotingPort)),
		WithClustering(provider, 10, 1, gossipPort, clusterPort))

	require.NotNil(t, system)
	require.NoError(t, err)

	// start the node
	require.NoError(t, system.Start(ctx))
	time.Sleep(2 * time.Second)

	// return the cluster startNode
	return system, provider
}
