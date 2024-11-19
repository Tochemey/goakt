/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v2/discovery"
	"github.com/tochemey/goakt/v2/discovery/nats"
	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/internal/lib"
	"github.com/tochemey/goakt/v2/log"
	"github.com/tochemey/goakt/v2/test/data/testpb"
	testspb "github.com/tochemey/goakt/v2/test/data/testpb"
)

// testActor is an actor that helps run various test scenarios
type testActor struct {
}

// enforce compilation error
var _ Actor = (*testActor)(nil)

// newTestActor creates a testActor
func newTestActor() *testActor {
	return &testActor{}
}

// Init initialize the actor. This function can be used to set up some database connections
// or some sort of initialization before the actor init processing public
func (p *testActor) PreStart(context.Context) error {
	return nil
}

// Shutdown gracefully shuts down the given actor
func (p *testActor) PostStop(context.Context) error {
	return nil
}

// Receive processes any message dropped into the actor mailbox without a reply
func (p *testActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
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
			lib.Pause(receivingDelay)
			wg.Done()
		}()
		// block until timer is up
		wg.Wait()
	default:
		ctx.Unhandled()
	}
}

// testSupervisor is an actor that monitors another actor
// and reacts to its failure.
type testSupervisor struct{}

// enforce compilation error
var _ Actor = (*testSupervisor)(nil)

// newTestSupervisor creates an instance of testSupervisor
func newTestSupervisor() *testSupervisor {
	return &testSupervisor{}
}

func (p *testSupervisor) PreStart(context.Context) error {
	return nil
}

func (p *testSupervisor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testspb.TestSend:
	default:
		panic(ErrUnhandled)
	}
}

func (p *testSupervisor) PostStop(context.Context) error {
	return nil
}

// testSupervised is an actor that is monitored
type testSupervised struct{}

// enforce compilation error
var _ Actor = (*testSupervised)(nil)

// newTestSupervised creates an instance of testSupervised
func newTestSupervised() *testSupervised {
	return &testSupervised{}
}

func (x *testSupervised) PreStart(context.Context) error {
	return nil
}

func (x *testSupervised) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testspb.TestSend:
	case *testspb.TestReply:
		ctx.Response(new(testspb.Reply))
	case *testspb.TestPanic:
		panic("panicked")
	default:
		panic(ErrUnhandled)
	}
}

func (x *testSupervised) PostStop(context.Context) error {
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

func (x *userActor) Receive(ctx *ReceiveContext) {
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
func (x *userActor) Authenticated(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.TestReadiness:
		ctx.Response(new(testspb.TestReady))
		ctx.UnBecome()
	}
}

func (x *userActor) CreditAccount(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.CreditAccount:
		ctx.Response(new(testspb.AccountCredited))
		ctx.BecomeStacked(x.DebitAccount)
	case *testspb.TestBye:
		_ = ctx.Self().Shutdown(ctx.Context())
	}
}

func (x *userActor) DebitAccount(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.DebitAccount:
		ctx.Response(new(testspb.AccountDebited))
		ctx.UnBecomeStacked()
	}
}

type exchanger struct {
	logger log.Logger
	id     string
}

func (e *exchanger) PreStart(context.Context) error {
	e.logger = log.DiscardLogger
	return nil
}

func (e *exchanger) Receive(ctx *ReceiveContext) {
	message := ctx.Message()
	switch message.(type) {
	case *goaktpb.PostStart:
		e.id = ctx.Self().ID()
	case *testspb.TestSend:
		ctx.Tell(ctx.Sender(), new(testspb.TestSend))
	case *testspb.TaskComplete:
	case *testspb.TestReply:
		ctx.Response(new(testspb.Reply))
	case *testspb.TestRemoteSend:
		ctx.RemoteTell(ctx.RemoteSender(), new(testspb.TestBye))
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

func (x *stasher) Receive(ctx *ReceiveContext) {
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

func (x *stasher) Ready(ctx *ReceiveContext) {
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

func (x *testPreStart) Receive(*ReceiveContext) {}

func (x *testPreStart) PostStop(context.Context) error {
	return nil
}

var _ Actor = &testPreStart{}

type testPostStop struct{}

func (x *testPostStop) PreStart(context.Context) error {
	return nil
}

func (x *testPostStop) Receive(ctx *ReceiveContext) {
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

func (x *testRestart) Receive(*ReceiveContext) {
}

func (x *testRestart) PostStop(context.Context) error {
	return nil
}

var _ Actor = &testRestart{}

type forwarder struct {
	actorRef *PID
}

func (x *forwarder) PreStart(context.Context) error {
	return nil
}

func (x *forwarder) Receive(ctx *ReceiveContext) {
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

func (d *discarder) Receive(ctx *ReceiveContext) {
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

func startClusterSystem(t *testing.T, serverAddr string) (ActorSystem, discovery.Provider) {
	ctx := context.TODO()
	logger := log.DiscardLogger

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
		NatsServer:      fmt.Sprintf("nats://%s", serverAddr),
		NatsSubject:     natsSubject,
		Host:            host,
		DiscoveryPort:   gossipPort,
	}

	// create the instance of provider
	provider := nats.NewDiscovery(&config, nats.WithLogger(log.DiscardLogger))

	// create the actor system
	system, err := NewActorSystem(
		actorSystemName,
		WithPassivationDisabled(),
		WithLogger(logger),
		WithRemoting(host, int32(remotingPort)),
		WithPeerStateLoopInterval(500*time.Millisecond),
		WithCluster(
			NewClusterConfig().
				WithKinds(new(testActor)).
				WithPartitionCount(10).
				WithReplicaCount(1).
				WithPeersPort(clusterPort).
				WithMinimumPeersQuorum(1).
				WithDiscoveryPort(gossipPort).
				WithDiscovery(provider)),
	)

	require.NotNil(t, system)
	require.NoError(t, err)

	// start the node
	require.NoError(t, system.Start(ctx))
	lib.Pause(2 * time.Second)

	// return the cluster startNode
	return system, provider
}

type unhandledSupervisorDirective struct{}

func (x unhandledSupervisorDirective) isSupervisorDirective() {}

type worker struct {
	counter int
	logger  log.Logger
}

var _ Actor = (*worker)(nil)

func newWorker() *worker {
	return &worker{}
}

func (x *worker) PreStart(context.Context) error {
	x.logger = log.DiscardLogger
	return nil
}

func (x *worker) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *testpb.DoLog:
		x.counter++
		x.logger.Infof("Got message: %s", msg.GetText())
	case *testpb.GetCount:
		x.counter++
		ctx.Response(&testpb.Count{Value: int32(x.counter)})
	default:
		ctx.Unhandled()
	}
}

func (x *worker) PostStop(context.Context) error {
	return nil
}

func extractMessage(bytes []byte) (string, error) {
	// a map container to decode the JSON structure into
	c := make(map[string]json.RawMessage)

	// unmarshal JSON
	if err := json.Unmarshal(bytes, &c); err != nil {
		return "", err
	}
	for k, v := range c {
		if k == "msg" {
			return strconv.Unquote(string(v))
		}
	}

	return "", nil
}

func extractLevel(bytes []byte) (string, error) {
	// a map container to decode the JSON structure into
	c := make(map[string]json.RawMessage)

	// unmarshal JSON
	if err := json.Unmarshal(bytes, &c); err != nil {
		return "", err
	}
	for k, v := range c {
		if k == "level" {
			return strconv.Unquote(string(v))
		}
	}

	return "", nil
}
