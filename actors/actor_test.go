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
	"strconv"
	"sync"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/discovery"
	"github.com/tochemey/goakt/discovery/nats"
	"github.com/tochemey/goakt/log"
	testspb "github.com/tochemey/goakt/test/data/pb/v1"
	"github.com/travisjeffery/go-dynaport"
	"go.uber.org/atomic"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m, goleak.IgnoreTopFunction("github.com/golang/glog.(*loggingT).flushDaemon"),
		goleak.IgnoreTopFunction("github.com/go-redis/redis/v8/internal/pool.(*ConnPool).reaper"),
		goleak.IgnoreTopFunction("golang.org/x/net/http2.(*serverConn).serve"),
		goleak.IgnoreTopFunction("github.com/nats-io/nats%2ego.(*Conn).doReconnect"),
		goleak.IgnoreTopFunction("sync.runtime_notifyListWait"),
		goleak.IgnoreTopFunction("internal/poll.runtime_pollWait"))
}

// Tester is an actor that helps run various test scenarios
type Tester struct {
	counter *atomic.Int64
}

// enforce compilation error
var _ Actor = (*Tester)(nil)

// NewTester creates a Tester
func NewTester() *Tester {
	return &Tester{
		counter: atomic.NewInt64(0),
	}
}

// Init initialize the actor. This function can be used to set up some database connections
// or some sort of initialization before the actor init processing public
func (p *Tester) PreStart(context.Context) error {
	return nil
}

// Shutdown gracefully shuts down the given actor
func (p *Tester) PostStop(context.Context) error {
	p.counter.Store(0)
	return nil
}

// Receive processes any message dropped into the actor mailbox without a reply
func (p *Tester) Receive(ctx ReceiveContext) {
	switch ctx.Message().(type) {
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

// UserActor is used to test the actor behavior
type UserActor struct{}

// enforce compilation error
var _ Actor = &UserActor{}

func (x *UserActor) PreStart(_ context.Context) error {
	return nil
}

func (x *UserActor) PostStop(_ context.Context) error {
	return nil
}

func (x *UserActor) Receive(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.TestLogin:
		ctx.Response(new(testspb.TestLoginSuccess))
		ctx.Become(x.Authenticated)
	case *testspb.CreateAccount:
		ctx.Response(new(testspb.AccountCreated))
		ctx.BecomeStacked(x.CreditAccount)
	}
}

// Authenticated behavior is executed when the actor receive the TestAuth message
func (x *UserActor) Authenticated(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.TestReadiness:
		ctx.Response(new(testspb.TestReady))
		ctx.UnBecome()
	}
}

func (x *UserActor) CreditAccount(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.CreditAccount:
		ctx.Response(new(testspb.AccountCredited))
		ctx.BecomeStacked(x.DebitAccount)
	case *testspb.TestBye:
		_ = ctx.Self().Shutdown(ctx.Context())
	}
}

func (x *UserActor) DebitAccount(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.DebitAccount:
		ctx.Response(new(testspb.AccountDebited))
		ctx.UnBecomeStacked()
	}
}

type Exchanger struct{}

func (e *Exchanger) PreStart(context.Context) error {
	return nil
}

func (e *Exchanger) Receive(ctx ReceiveContext) {
	message := ctx.Message()
	switch message.(type) {
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

func (e *Exchanger) PostStop(context.Context) error {
	return nil
}

var _ Actor = &Exchanger{}

type Stasher struct{}

func (x *Stasher) PreStart(context.Context) error {
	return nil
}

func (x *Stasher) Receive(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.TestStash:
		ctx.Become(x.Ready)
		ctx.Stash()
	case *testspb.TestLogin:
	case *testspb.TestBye:
		_ = ctx.Self().Shutdown(ctx.Context())
	}
}

func (x *Stasher) Ready(ctx ReceiveContext) {
	switch ctx.Message().(type) {
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

func (x *Stasher) PostStop(context.Context) error {
	return nil
}

var _ Actor = &Stasher{}

type PreStartBreaker struct{}

func (x *PreStartBreaker) PreStart(context.Context) error {
	return errors.New("failed")
}

func (x *PreStartBreaker) Receive(ReceiveContext) {}

func (x *PreStartBreaker) PostStop(context.Context) error {
	return nil
}

var _ Actor = &PreStartBreaker{}

type PostStopBreaker struct{}

func (x *PostStopBreaker) PreStart(context.Context) error {
	return nil
}

func (x *PostStopBreaker) Receive(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.TestSend:
	case *testspb.TestPanic:
		panic("panicked")
	}
}

func (x *PostStopBreaker) PostStop(context.Context) error {
	return errors.New("failed")
}

var _ Actor = &PostStopBreaker{}

type RestartBreaker struct {
	counter *atomic.Int64
}

func NewRestartBreaker() *RestartBreaker {
	return &RestartBreaker{counter: atomic.NewInt64(0)}
}

func (x *RestartBreaker) PreStart(context.Context) error {
	// increment counter
	x.counter.Inc()
	// error when counter is greater than 1
	if x.counter.Load() > 1 {
		return errors.New("cannot restart")
	}
	return nil
}

func (x *RestartBreaker) Receive(ReceiveContext) {
}

func (x *RestartBreaker) PostStop(context.Context) error {
	return nil
}

var _ Actor = &RestartBreaker{}

type Forwarder struct {
	actorRef PID
}

func (x *Forwarder) PreStart(context.Context) error {
	return nil
}

func (x *Forwarder) Receive(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.TestBye:
		ctx.Forward(x.actorRef)
	}
}

func (x *Forwarder) PostStop(context.Context) error {
	return nil
}

var _ Actor = &Forwarder{}

type Discarder struct{}

var _ Actor = &Discarder{}

func (d *Discarder) PreStart(context.Context) error {
	return nil
}

func (d *Discarder) Receive(ctx ReceiveContext) {
	switch ctx.Message().(type) {
	default:
		ctx.Unhandled()
	}
}

func (d *Discarder) PostStop(context.Context) error {
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
	// set the environments
	require.NoError(t, os.Setenv("GOSSIP_PORT", strconv.Itoa(gossipPort)))
	require.NoError(t, os.Setenv("CLUSTER_PORT", strconv.Itoa(clusterPort)))
	require.NoError(t, os.Setenv("REMOTING_PORT", strconv.Itoa(remotingPort)))
	require.NoError(t, os.Setenv("NODE_NAME", nodeName))
	require.NoError(t, os.Setenv("NODE_IP", host))

	// create the various config option
	applicationName := "accounts"
	actorSystemName := "testSystem"
	natsSubject := "some-subject"
	// create the instance of provider
	provider := nats.NewDiscovery()

	// create the config
	config := discovery.Config{
		nats.ApplicationName: applicationName,
		nats.ActorSystemName: actorSystemName,
		nats.NatsServer:      serverAddr,
		nats.NatsSubject:     natsSubject,
	}

	// create the sd
	sd := discovery.NewServiceDiscovery(provider, config)

	// create the actor system
	system, err := NewActorSystem(
		nodeName,
		WithPassivationDisabled(),
		WithLogger(logger),
		WithReplyTimeout(time.Minute),
		WithClustering(sd, 10))

	require.NotNil(t, system)
	require.NoError(t, err)

	// start the node
	require.NoError(t, system.Start(ctx))

	// clear the env var
	require.NoError(t, os.Unsetenv("GOSSIP_PORT"))
	require.NoError(t, os.Unsetenv("CLUSTER_PORT"))
	require.NoError(t, os.Unsetenv("REMOTING_PORT"))
	require.NoError(t, os.Unsetenv("NODE_NAME"))
	require.NoError(t, os.Unsetenv("NODE_IP"))

	time.Sleep(2 * time.Second)

	// return the cluster startNode
	return system, provider
}
