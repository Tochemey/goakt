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
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/discovery"
	"github.com/tochemey/goakt/v2/discovery/nats"
	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/internal/lib"
	"github.com/tochemey/goakt/v2/log"
	"github.com/tochemey/goakt/v2/persistence"
	"github.com/tochemey/goakt/v2/test/data/testpb"
)

// mockActor is an actor that helps run various test scenarios
type mockActor struct {
}

// enforce compilation error
var _ Actor = (*mockActor)(nil)

// newMockActor creates a testActor
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
	case *testpb.TestSend:
	case *testpb.TestPanic:
		panic("Boom")
	case *testpb.TestReply:
		ctx.Response(&testpb.Reply{Content: "received message"})
	case *testpb.TestTimeout:
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

// mockSupervisor is an actor that monitors another actor
// and reacts to its failure.
type mockSupervisor struct{}

// enforce compilation error
var _ Actor = (*mockSupervisor)(nil)

// newMockSupervisor creates an instance of mockSupervisor
func newMockSupervisor() *mockSupervisor {
	return &mockSupervisor{}
}

func (p *mockSupervisor) PreStart(context.Context) error {
	return nil
}

func (p *mockSupervisor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testpb.TestSend:
	case *goaktpb.Terminated:
		// pass
	default:
		panic(ErrUnhandled)
	}
}

func (p *mockSupervisor) PostStop(context.Context) error {
	return nil
}

// mockSupervisee is an actor that is monitored
type mockSupervisee struct{}

// enforce compilation error
var _ Actor = (*mockSupervisee)(nil)

// newMockSupervisee creates an instance of mockSupervisee
func newMockSupervisee() *mockSupervisee {
	return &mockSupervisee{}
}

func (x *mockSupervisee) PreStart(context.Context) error {
	return nil
}

func (x *mockSupervisee) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testpb.TestSend:
	case *testpb.TestReply:
		ctx.Response(new(testpb.Reply))
	case *testpb.TestPanic:
		panic("panicked")
	default:
		panic(ErrUnhandled)
	}
}

func (x *mockSupervisee) PostStop(context.Context) error {
	return nil
}

// mockBehaviorActor is used to test the actor commandHandler
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
	case *testpb.TestLogin:
		ctx.Response(new(testpb.TestLoginSuccess))
		ctx.Become(x.Authenticated)
	case *testpb.CreateAccount:
		ctx.Response(new(testpb.AccountCreated))
		ctx.BecomeStacked(x.CreditAccount)
	}
}

// Authenticated commandHandler is executed when the actor receive the TestAuth message
func (x *mockBehaviorActor) Authenticated(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestReadiness:
		ctx.Response(new(testpb.TestReady))
		ctx.UnBecome()
	}
}

func (x *mockBehaviorActor) CreditAccount(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.CreditAccount:
		ctx.Response(new(testpb.AccountCredited))
		ctx.BecomeStacked(x.DebitAccount)
	case *testpb.TestBye:
		_ = ctx.Self().Shutdown(ctx.Context())
	}
}

func (x *mockBehaviorActor) DebitAccount(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.DebitAccount:
		ctx.Response(new(testpb.AccountDebited))
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
	case *testpb.TestSend:
		ctx.Tell(ctx.Sender(), new(testpb.TestSend))
	case *testpb.TaskComplete:
	case *testpb.TestReply:
		ctx.Response(new(testpb.Reply))
	case *testpb.TestRemoteSend:
		ctx.RemoteTell(ctx.RemoteSender(), new(testpb.TestBye))
	case *testpb.TestBye:
		_ = ctx.Self().Shutdown(ctx.Context())
	}
}

func (e *exchanger) PostStop(context.Context) error {
	return nil
}

var _ Actor = &exchanger{}

type mockStasher struct{}

func (x *mockStasher) PreStart(context.Context) error {
	return nil
}

func (x *mockStasher) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testpb.TestStash:
		ctx.Become(x.Ready)
		ctx.Stash()
	case *testpb.TestLogin:
	case *testpb.TestBye:
		_ = ctx.Self().Shutdown(ctx.Context())
	}
}

func (x *mockStasher) Ready(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testpb.TestStash:
	case *testpb.TestLogin:
		ctx.Stash()
	case *testpb.TestSend:
		// do nothing
	case *testpb.TestUnstashAll:
		ctx.UnBecome()
		ctx.UnstashAll()
	case *testpb.TestUnstash:
		ctx.Unstash()
	}
}

func (x *mockStasher) PostStop(context.Context) error {
	return nil
}

var _ Actor = &mockStasher{}

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
	case *testpb.TestSend:
	case *testpb.TestPanic:
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
	case *testpb.TestBye:
		ctx.Forward(x.actorRef)
	case *testpb.RemoteForward:
		ctx.RemoteForward(x.remoteRef.Address())
	case *testpb.TestState:
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
	case *testpb.RemoteForward:
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
		WithPeerStateLoopInterval(500*time.Millisecond),
		WithCluster(
			NewClusterConfig().
				WithKinds(new(mockActor)).
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

type unsupportedSupervisorDirective struct{}

func (x unsupportedSupervisorDirective) isSupervisorDirective() {}

type mockRouter struct {
	counter int
	logger  log.Logger
}

var _ Actor = (*mockRouter)(nil)

func (x *mockRouter) PreStart(context.Context) error {
	x.logger = log.DiscardLogger
	return nil
}

func (x *mockRouter) Receive(ctx *ReceiveContext) {
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

func (x *mockRouter) PostStop(context.Context) error {
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

type mockStateStore struct {
	mu    *sync.RWMutex
	cache map[string]*persistence.State
}

var _ persistence.Store = (*mockStateStore)(nil)

func newMockStateStore() *mockStateStore {
	return &mockStateStore{
		mu:    &sync.RWMutex{},
		cache: make(map[string]*persistence.State),
	}
}

func (t *mockStateStore) Ping(context.Context) error {
	return nil
}

func (t *mockStateStore) PersistState(_ context.Context, durableState *persistence.State) error {
	t.mu.Lock()
	t.cache[durableState.ActorID] = durableState
	t.mu.Unlock()
	return nil
}

func (t *mockStateStore) GetState(_ context.Context, actorID string) (*persistence.State, error) {
	t.mu.RLock()
	state := t.cache[actorID]
	t.mu.RUnlock()
	return state, nil
}

type mockPersistentActor struct {
}

var _ PersistentActor = (*mockPersistentActor)(nil)

func newMockPersistentActor() *mockPersistentActor {
	return &mockPersistentActor{}
}

func (m *mockPersistentActor) EmptyState() proto.Message {
	return new(testpb.TestState)
}

func (m *mockPersistentActor) PreStart(context.Context) error {
	return nil
}

func (m *mockPersistentActor) PostStop(context.Context) error {
	return nil
}

func (m *mockPersistentActor) Receive(ctx *PersistentContext) *PersistentResponse {
	switch cmd := ctx.Command().(type) {
	case *testpb.TestStateCommandResponse:
		newVersion := ctx.CurrentState().Version() + 1
		actorState := new(testpb.TestState)
		return NewPersistentResponse().
			WithState(NewPersistentState(actorState, newVersion))

	case *testpb.TestStateCommandResponseWithForward:
		newVersion := ctx.CurrentState().Version() + 1
		actorState := new(testpb.TestState)
		return NewPersistentResponse().
			WithState(NewPersistentState(actorState, newVersion)).
			WithForwardTo(cmd.GetRecipient())

	case *testpb.TestNoStateCommandResponse:
		return NewPersistentResponse()

	case *testpb.TestForwardCommandResponse:
		return NewPersistentResponse().WithForwardTo(cmd.GetRecipient())

	case *testpb.TestErrorCommandResponse:
		return NewPersistentResponse().WithError(errors.New(cmd.GetErrorMessage()))

	case *testpb.TestStateCommandResponseWithInvalidVersion:
		newVersion := ctx.CurrentState().Version()
		actorState := new(testpb.TestState)
		return NewPersistentResponse().
			WithState(NewPersistentState(actorState, newVersion))
	default:
		return NewPersistentResponse()
	}
}
