/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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

// mockActor is an actor that helps run various test scenarios
type mockActor struct {
}

// enforce compilation error
var _ Actor = (*mockActor)(nil)

// newMockActor creates a actorQA
func newMockActor() *mockActor {
	return &mockActor{}
}

// Init initialize the actor. This function can be used to set up some database connections
// or some sort of initialization before the actor init processing message
func (p *mockActor) PreStart(context.Context) error {
	return nil
}

// Shutdown gracefully shuts down the given actor
func (p *mockActor) PostStop(context.Context) error {
	return nil
}

// Receive processes any message dropped into the actor mailbox without a reply
func (p *mockActor) Receive(ctx *ReceiveContext) {
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

// mockSupervisorActor is an actor that monitors another actor
// and reacts to its failure.
type mockSupervisorActor struct{}

// enforce compilation error
var _ Actor = (*mockSupervisorActor)(nil)

// newMockSupervisorActor creates an instance of supervisorQA
func newMockSupervisorActor() *mockSupervisorActor {
	return &mockSupervisorActor{}
}

func (p *mockSupervisorActor) PreStart(context.Context) error {
	return nil
}

func (p *mockSupervisorActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testspb.TestSend:
	case *goaktpb.Terminated:
		// pass
	default:
		panic(ErrUnhandled)
	}
}

func (p *mockSupervisorActor) PostStop(context.Context) error {
	return nil
}

// mockSupervisedActor is an actor that is monitored
type mockSupervisedActor struct{}

// enforce compilation error
var _ Actor = (*mockSupervisedActor)(nil)

// newMockSupervisedActor creates an instance of supervisedQA
func newMockSupervisedActor() *mockSupervisedActor {
	return &mockSupervisedActor{}
}

func (x *mockSupervisedActor) PreStart(context.Context) error {
	return nil
}

func (x *mockSupervisedActor) Receive(ctx *ReceiveContext) {
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

func (x *mockSupervisedActor) PostStop(context.Context) error {
	return nil
}

// mockBehaviorActor is used to test the actor behavior
type mockBehaviorActor struct{}

// enforce compilation error
var _ Actor = &mockBehaviorActor{}

func (x *mockBehaviorActor) PreStart(_ context.Context) error {
	return nil
}

func (x *mockBehaviorActor) PostStop(_ context.Context) error {
	return nil
}

func (x *mockBehaviorActor) Receive(ctx *ReceiveContext) {
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
func (x *mockBehaviorActor) Authenticated(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.TestReadiness:
		ctx.Response(new(testspb.TestReady))
		ctx.UnBecome()
	}
}

func (x *mockBehaviorActor) CreditAccount(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.CreditAccount:
		ctx.Response(new(testspb.AccountCredited))
		ctx.BecomeStacked(x.DebitAccount)
	case *testspb.TestBye:
		_ = ctx.Self().Shutdown(ctx.Context())
	}
}

func (x *mockBehaviorActor) DebitAccount(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.DebitAccount:
		ctx.Response(new(testspb.AccountDebited))
		ctx.UnBecomeStacked()
	}
}

type exchanger struct {
	id string
}

func (e *exchanger) PreStart(context.Context) error {
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
		ctx.Shutdown()
	}
}

func (e *exchanger) PostStop(context.Context) error {
	return nil
}

var _ Actor = &exchanger{}

type mockStashActor struct{}

func (x *mockStashActor) PreStart(context.Context) error {
	return nil
}

func (x *mockStashActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testspb.TestStash:
		ctx.Become(x.Ready)
		ctx.Stash()
	case *testspb.TestLogin:
	case *testspb.TestBye:
		ctx.Shutdown()
	}
}

func (x *mockStashActor) Ready(ctx *ReceiveContext) {
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

func (x *mockStashActor) PostStop(context.Context) error {
	return nil
}

var _ Actor = &mockStashActor{}

type mockPreStartActor struct{}

func (x *mockPreStartActor) PreStart(context.Context) error {
	return errors.New("failed")
}

func (x *mockPreStartActor) Receive(*ReceiveContext) {}

func (x *mockPreStartActor) PostStop(context.Context) error {
	return nil
}

var _ Actor = &mockPreStartActor{}

type mockPostStopActor struct{}

func (x *mockPostStopActor) PreStart(context.Context) error {
	return nil
}

func (x *mockPostStopActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testspb.TestSend:
	case *testspb.TestPanic:
		panic("panicked")
	}
}

func (x *mockPostStopActor) PostStop(context.Context) error {
	return errors.New("failed")
}

var _ Actor = &mockPostStopActor{}

type mockRestartActor struct {
	counter *atomic.Int64
}

func newRestart() *mockRestartActor {
	return &mockRestartActor{counter: atomic.NewInt64(0)}
}

func (x *mockRestartActor) PreStart(context.Context) error {
	// increment counter
	x.counter.Inc()
	// error when counter is greater than 1
	if x.counter.Load() > 1 {
		return errors.New("cannot restart")
	}
	return nil
}

func (x *mockRestartActor) Receive(*ReceiveContext) {
}

func (x *mockRestartActor) PostStop(context.Context) error {
	return nil
}

var _ Actor = &mockRestartActor{}

type mockForwardActor struct {
	actorRef  *PID
	remoteRef *PID
}

func (x *mockForwardActor) PreStart(context.Context) error {
	return nil
}

func (x *mockForwardActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testspb.TestBye:
		ctx.Forward(x.actorRef)
	case *testspb.TestRemoteForward:
		ctx.RemoteForward(x.remoteRef.Address())
	}
}

func (x *mockForwardActor) PostStop(context.Context) error {
	return nil
}

type mockRemoteActor struct {
}

func (x *mockRemoteActor) PreStart(context.Context) error {
	return nil
}

func (x *mockRemoteActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.TestRemoteForward:
		ctx.Shutdown()
	}
}

func (x *mockRemoteActor) PostStop(context.Context) error {
	return nil
}

type mockUnhandledMessageActor struct{}

var _ Actor = &mockUnhandledMessageActor{}

func (d *mockUnhandledMessageActor) PreStart(context.Context) error {
	return nil
}

func (d *mockUnhandledMessageActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
		// pass
	default:
		ctx.Unhandled()
	}
}

func (d *mockUnhandledMessageActor) PostStop(context.Context) error {
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
		WithShutdownTimeout(time.Minute),
		WithCluster(
			NewClusterConfig().
				WithKinds(new(mockActor)).
				WithPeersPort(clusterPort).
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

type mockRouterActor struct {
	counter int
	logger  log.Logger
}

var _ Actor = (*mockRouterActor)(nil)

func (x *mockRouterActor) PreStart(context.Context) error {
	x.logger = log.DiscardLogger
	return nil
}

func (x *mockRouterActor) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *testpb.TestLog:
		x.counter++
		x.logger.Infof("Got message: %s", msg.GetText())
	case *testpb.TestGetCount:
		x.counter++
		ctx.Response(&testpb.TestCount{Value: int32(x.counter)})
	default:
		ctx.Unhandled()
	}
}

func (x *mockRouterActor) PostStop(context.Context) error {
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
