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

// actorQA is an actor that helps run various test scenarios
type actorQA struct {
}

// enforce compilation error
var _ Actor = (*actorQA)(nil)

// newActor creates a actorQA
func newActor() *actorQA {
	return &actorQA{}
}

// Init initialize the actor. This function can be used to set up some database connections
// or some sort of initialization before the actor init processing message
func (p *actorQA) PreStart(context.Context) error {
	return nil
}

// Shutdown gracefully shuts down the given actor
func (p *actorQA) PostStop(context.Context) error {
	return nil
}

// Receive processes any message dropped into the actor mailbox without a reply
func (p *actorQA) Receive(ctx *ReceiveContext) {
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

// supervisorQA is an actor that monitors another actor
// and reacts to its failure.
type supervisorQA struct{}

// enforce compilation error
var _ Actor = (*supervisorQA)(nil)

// newTestSupervisor creates an instance of supervisorQA
func newTestSupervisor() *supervisorQA {
	return &supervisorQA{}
}

func (p *supervisorQA) PreStart(context.Context) error {
	return nil
}

func (p *supervisorQA) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testspb.TestSend:
	case *goaktpb.Terminated:
		// pass
	default:
		panic(ErrUnhandled)
	}
}

func (p *supervisorQA) PostStop(context.Context) error {
	return nil
}

// supervisedQA is an actor that is monitored
type supervisedQA struct{}

// enforce compilation error
var _ Actor = (*supervisedQA)(nil)

// newTestSupervised creates an instance of supervisedQA
func newTestSupervised() *supervisedQA {
	return &supervisedQA{}
}

func (x *supervisedQA) PreStart(context.Context) error {
	return nil
}

func (x *supervisedQA) Receive(ctx *ReceiveContext) {
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

func (x *supervisedQA) PostStop(context.Context) error {
	return nil
}

// behaviorQA is used to test the actor behavior
type behaviorQA struct{}

// enforce compilation error
var _ Actor = &behaviorQA{}

func (x *behaviorQA) PreStart(_ context.Context) error {
	return nil
}

func (x *behaviorQA) PostStop(_ context.Context) error {
	return nil
}

func (x *behaviorQA) Receive(ctx *ReceiveContext) {
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
func (x *behaviorQA) Authenticated(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.TestReadiness:
		ctx.Response(new(testspb.TestReady))
		ctx.UnBecome()
	}
}

func (x *behaviorQA) CreditAccount(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.CreditAccount:
		ctx.Response(new(testspb.AccountCredited))
		ctx.BecomeStacked(x.DebitAccount)
	case *testspb.TestBye:
		_ = ctx.Self().Shutdown(ctx.Context())
	}
}

func (x *behaviorQA) DebitAccount(ctx *ReceiveContext) {
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

type stashQA struct{}

func (x *stashQA) PreStart(context.Context) error {
	return nil
}

func (x *stashQA) Receive(ctx *ReceiveContext) {
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

func (x *stashQA) Ready(ctx *ReceiveContext) {
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

func (x *stashQA) PostStop(context.Context) error {
	return nil
}

var _ Actor = &stashQA{}

type preStartQA struct{}

func (x *preStartQA) PreStart(context.Context) error {
	return errors.New("failed")
}

func (x *preStartQA) Receive(*ReceiveContext) {}

func (x *preStartQA) PostStop(context.Context) error {
	return nil
}

var _ Actor = &preStartQA{}

type postStopQA struct{}

func (x *postStopQA) PreStart(context.Context) error {
	return nil
}

func (x *postStopQA) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testspb.TestSend:
	case *testspb.TestPanic:
		panic("panicked")
	}
}

func (x *postStopQA) PostStop(context.Context) error {
	return errors.New("failed")
}

var _ Actor = &postStopQA{}

type restartQA struct {
	counter *atomic.Int64
}

func newRestart() *restartQA {
	return &restartQA{counter: atomic.NewInt64(0)}
}

func (x *restartQA) PreStart(context.Context) error {
	// increment counter
	x.counter.Inc()
	// error when counter is greater than 1
	if x.counter.Load() > 1 {
		return errors.New("cannot restart")
	}
	return nil
}

func (x *restartQA) Receive(*ReceiveContext) {
}

func (x *restartQA) PostStop(context.Context) error {
	return nil
}

var _ Actor = &restartQA{}

type forwardQA struct {
	actorRef  *PID
	remoteRef *PID
}

func (x *forwardQA) PreStart(context.Context) error {
	return nil
}

func (x *forwardQA) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testspb.TestBye:
		ctx.Forward(x.actorRef)
	case *testspb.RemoteForward:
		ctx.RemoteForward(x.remoteRef.Address())
	}
}

func (x *forwardQA) PostStop(context.Context) error {
	return nil
}

type remoteQA struct {
}

func (x *remoteQA) PreStart(context.Context) error {
	return nil
}

func (x *remoteQA) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.RemoteForward:
		ctx.Shutdown()
	}
}

func (x *remoteQA) PostStop(context.Context) error {
	return nil
}

type unhandledQA struct{}

var _ Actor = &unhandledQA{}

func (d *unhandledQA) PreStart(context.Context) error {
	return nil
}

func (d *unhandledQA) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
		// pass
	default:
		ctx.Unhandled()
	}
}

func (d *unhandledQA) PostStop(context.Context) error {
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
		WithJanitorInterval(time.Minute),
		WithPeerStateLoopInterval(500*time.Millisecond),
		WithCluster(
			NewClusterConfig().
				WithKinds(new(actorQA)).
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

type routerQA struct {
	counter int
	logger  log.Logger
}

var _ Actor = (*routerQA)(nil)

func (x *routerQA) PreStart(context.Context) error {
	x.logger = log.DiscardLogger
	return nil
}

func (x *routerQA) Receive(ctx *ReceiveContext) {
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

func (x *routerQA) PostStop(context.Context) error {
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
