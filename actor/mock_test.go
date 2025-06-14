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

package actor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/kapetan-io/tackle/autotls"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v3/discovery"
	"github.com/tochemey/goakt/v3/discovery/nats"
	"github.com/tochemey/goakt/v3/extension"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/util"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/remote"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

type mockSubscriber struct {
	counter *atomic.Int64
}

var _ Actor = &mockSubscriber{}

func newMockSubscriber() *mockSubscriber {
	return &mockSubscriber{
		counter: atomic.NewInt64(0),
	}
}

func (x *mockSubscriber) PreStart(*Context) error {
	return nil
}

func (x *mockSubscriber) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.SubscribeAck:
		x.counter.Inc()
	case *testpb.TestCount:
		x.counter.Inc()
	case *goaktpb.UnsubscribeAck:
		x.counter.Dec()
	}
}

func (x *mockSubscriber) PostStop(*Context) error {
	return nil
}

// mockActor is an actor that helps run various test scenarios
type mockActor struct{}

// enforce compilation error
var _ Actor = (*mockActor)(nil)

// newMockActor creates a actorQA
func newMockActor() *mockActor {
	return &mockActor{}
}

// Init initialize the actor. This function can be used to set up some database connections
// or some sort of initialization before the actor init processing message
func (p *mockActor) PreStart(*Context) error {
	return nil
}

// Shutdown gracefully shuts down the given actor
func (p *mockActor) PostStop(*Context) error {
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
			util.Pause(receivingDelay)
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

func (p *mockSupervisorActor) PreStart(*Context) error {
	return nil
}

func (p *mockSupervisorActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testpb.TestSend:
	case *goaktpb.Terminated:
		// pass
	default:
		panic(ErrUnhandled)
	}
}

func (p *mockSupervisorActor) PostStop(*Context) error {
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

func (x *mockSupervisedActor) PreStart(*Context) error {
	return nil
}

func (x *mockSupervisedActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testpb.TestSend:
	case *testpb.TestReply:
		ctx.Response(new(testpb.Reply))
	case *testpb.TestPanic:
		panic("panicked")
	case *testpb.TestPanicError:
		panic(errors.New("panicked"))
	default:
		panic(ErrUnhandled)
	}
}

func (x *mockSupervisedActor) PostStop(*Context) error {
	return nil
}

// mockBehaviorActor is used to test the actor behavior
type mockBehaviorActor struct{}

// enforce compilation error
var _ Actor = &mockBehaviorActor{}

func (x *mockBehaviorActor) PreStart(*Context) error {
	return nil
}

func (x *mockBehaviorActor) PostStop(*Context) error {
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

// Authenticated behavior is executed when the actor receive the TestAuth message
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
	id string
}

func (e *exchanger) PreStart(*Context) error {
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
		ctx.Shutdown()
	}
}

func (e *exchanger) PostStop(*Context) error {
	return nil
}

var _ Actor = &exchanger{}

type mockStashActor struct{}

func (x *mockStashActor) PreStart(*Context) error {
	return nil
}

func (x *mockStashActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testpb.TestStash:
		ctx.Become(x.Ready)
		ctx.Stash()
	case *testpb.TestLogin:
	case *testpb.TestBye:
		ctx.Shutdown()
	}
}

func (x *mockStashActor) Ready(ctx *ReceiveContext) {
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

func (x *mockStashActor) PostStop(*Context) error {
	return nil
}

var _ Actor = &mockStashActor{}

type mockPreStartActor struct{}

func (x *mockPreStartActor) PreStart(*Context) error {
	return errors.New("failed")
}

func (x *mockPreStartActor) Receive(*ReceiveContext) {}

func (x *mockPreStartActor) PostStop(*Context) error {
	return nil
}

var _ Actor = &mockPreStartActor{}

type mockPostStopActor struct{}

func (x *mockPostStopActor) PreStart(*Context) error {
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

func (x *mockPostStopActor) PostStop(*Context) error {
	return errors.New("failed")
}

var _ Actor = &mockPostStopActor{}

type mockRestartActor struct {
	counter *atomic.Int64
}

func newRestart() *mockRestartActor {
	return &mockRestartActor{counter: atomic.NewInt64(0)}
}

func (x *mockRestartActor) PreStart(*Context) error {
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

func (x *mockRestartActor) PostStop(*Context) error {
	return nil
}

var _ Actor = &mockRestartActor{}

type mockForwardActor struct {
	actorRef  *PID
	remoteRef *PID
}

func (x *mockForwardActor) PreStart(*Context) error {
	return nil
}

func (x *mockForwardActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testpb.TestBye:
		ctx.Forward(x.actorRef)
	case *testpb.TestRemoteForward:
		ctx.RemoteForward(x.remoteRef.Address())
	}
}

func (x *mockForwardActor) PostStop(*Context) error {
	return nil
}

type mockRemoteActor struct {
}

func (x *mockRemoteActor) PreStart(*Context) error {
	return nil
}

func (x *mockRemoteActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestRemoteForward:
		ctx.Shutdown()
	}
}

func (x *mockRemoteActor) PostStop(*Context) error {
	return nil
}

type mockUnhandledMessageActor struct{}

var _ Actor = &mockUnhandledMessageActor{}

func (d *mockUnhandledMessageActor) PreStart(*Context) error {
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

func (d *mockUnhandledMessageActor) PostStop(*Context) error {
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

type testClusterConfig struct {
	tlsEnabled        bool
	conf              autotls.Config
	pubsubEnabled     bool
	relocationEnabled bool
	extension         extension.Extension
	dependency        extension.Dependency
}

type testClusterOption func(*testClusterConfig)

func withTestTSL(conf autotls.Config) testClusterOption {
	return func(tc *testClusterConfig) {
		tc.tlsEnabled = true
		tc.conf = conf
	}
}

func withTestPubSub() testClusterOption {
	return func(tc *testClusterConfig) {
		tc.pubsubEnabled = true
	}
}

func withoutTestRelocation() testClusterOption {
	return func(tc *testClusterConfig) {
		tc.relocationEnabled = false
	}
}

func withMockExtension(ext extension.Extension) testClusterOption {
	return func(tcc *testClusterConfig) {
		tcc.extension = ext
	}
}

func testCluster(t *testing.T, serverAddr string, opts ...testClusterOption) (ActorSystem, discovery.Provider) {
	ctx := context.TODO()
	logger := log.DiscardLogger

	// generate the ports for the single startNode
	nodePorts := dynaport.Get(3)
	discoveryPort := nodePorts[0]
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
		DiscoveryPort:   discoveryPort,
	}

	// create the instance of provider
	provider := nats.NewDiscovery(&config, nats.WithLogger(log.DiscardLogger))

	// create the actor system options
	options := []Option{
		WithLogger(logger),
		WithRemote(remote.NewConfig(host, remotingPort)),
		WithPeerStateLoopInterval(500 * time.Millisecond),
		WithCluster(
			NewClusterConfig().
				WithKinds(
					new(mockActor),
					new(mockEntity),
				).
				WithPartitionCount(7).
				WithReplicaCount(1).
				WithPeersPort(clusterPort).
				WithMinimumPeersQuorum(1).
				WithDiscoveryPort(discoveryPort).
				WithDiscovery(provider)),
	}

	cfg := &testClusterConfig{
		tlsEnabled:        false,
		pubsubEnabled:     false,
		relocationEnabled: true,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	if cfg.pubsubEnabled {
		options = append(options, WithPubSub())
	}

	if cfg.tlsEnabled {
		options = append(options, WithTLS(&TLSInfo{
			ClientTLS: cfg.conf.ClientTLS,
			ServerTLS: cfg.conf.ServerTLS,
		}))
	}

	if !cfg.relocationEnabled {
		options = append(options, WithoutRelocation())
	}

	if cfg.extension != nil {
		options = append(options, WithExtensions(cfg.extension))
	}

	// create the actor system
	system, err := NewActorSystem(actorSystemName, options...)

	require.NotNil(t, system)
	require.NoError(t, err)

	if cfg.dependency != nil {
		require.NoError(t, system.Inject(cfg.dependency))
	}

	// start the node
	require.NoError(t, system.Start(ctx))
	util.Pause(2 * time.Second)

	// return the cluster startNode
	return system, provider
}

type mockRouterActor struct {
	counter int
	logger  log.Logger
}

var _ Actor = (*mockRouterActor)(nil)

func (x *mockRouterActor) PreStart(*Context) error {
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

func (x *mockRouterActor) PostStop(*Context) error {
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

type mockStateStore interface {
	extension.Extension
	WriteState(persistenceID string, state *testpb.Account) error
	GetLatestState(persistenceID string) (*testpb.Account, error)
}

type mockExtension struct {
	db *sync.Map
}

var _ mockStateStore = (*mockExtension)(nil)

func newMockExtension() *mockExtension {
	return &mockExtension{
		db: &sync.Map{},
	}
}

func (m *mockExtension) ID() string {
	return "mockStateStore"
}

// GetLatestState implements mockStateStore.
func (m *mockExtension) GetLatestState(persistenceID string) (*testpb.Account, error) {
	value, ok := m.db.Load(persistenceID)
	if !ok {
		return new(testpb.Account), nil
	}
	return value.(*testpb.Account), nil
}

// WriteState implements mockStateStore.
func (m *mockExtension) WriteState(persistenceID string, state *testpb.Account) error {
	m.db.Store(persistenceID, state)
	return nil
}

type mockEntity struct {
	persistenceID string
	currentState  *atomic.Pointer[testpb.Account]
	stateStore    mockStateStore
}

var _ Actor = (*mockEntity)(nil)

func newMockEntity() *mockEntity {
	return &mockEntity{}
}

// PreStart implements Actor.
func (m *mockEntity) PreStart(ctx *Context) error {
	m.currentState = atomic.NewPointer(new(testpb.Account))
	m.stateStore = ctx.Extension("mockStateStore").(mockStateStore)
	m.persistenceID = ctx.ActorName()
	return m.recoverFromStore()
}

// PostStop implements Actor.
func (m *mockEntity) PostStop(*Context) error {
	return m.stateStore.WriteState(m.persistenceID, m.currentState.Load())
}

// Receive implements Actor.
func (m *mockEntity) Receive(ctx *ReceiveContext) {
	switch received := ctx.Message().(type) {
	case *goaktpb.PostStart:
		// pass
	case *testpb.CreateAccount:
		// TODO: in production extra validation will be needed.
		balance := received.GetAccountBalance()
		newBalance := m.currentState.Load().GetAccountBalance() + balance
		m.currentState.Store(&testpb.Account{
			AccountId:      m.persistenceID,
			AccountBalance: newBalance,
		})
		// persist the actor state
		if err := m.stateStore.WriteState(m.persistenceID, m.currentState.Load()); err != nil {
			ctx.Err(err)
			return
		}
		// here we are dealing with Ask which is the appropriate pattern for external applications that need
		// response immediately
		ctx.Response(m.currentState.Load())
	case *testpb.CreditAccount:
		// TODO: in production extra validation will be needed.
		balance := received.GetBalance()
		newBalance := m.currentState.Load().GetAccountBalance() + balance
		m.currentState.Store(&testpb.Account{
			AccountId:      m.persistenceID,
			AccountBalance: newBalance,
		})
		// persist the actor state
		if err := m.stateStore.WriteState(m.persistenceID, m.currentState.Load()); err != nil {
			ctx.Err(err)
			return
		}
		// here we are dealing with Ask which is the appropriate pattern for external applications that need
		// response immediately
		ctx.Response(m.currentState.Load())
	case *testpb.GetAccount:
		ctx.Response(m.currentState.Load())
	default:
		ctx.Unhandled()
	}
}

func (m *mockEntity) recoverFromStore() error {
	latestState, err := m.stateStore.GetLatestState(m.persistenceID)
	if err != nil {
		return fmt.Errorf("failed to get the latest state: %w", err)
	}

	if latestState != nil {
		m.currentState.Store(latestState)
	}

	return nil
}

type mockDependency struct {
	id       string
	Username string
	Email    string
}

var _ extension.Dependency = (*mockDependency)(nil)

func dependencyMock(id, userName, email string) *mockDependency {
	return &mockDependency{
		id:       id,
		Username: userName,
		Email:    email,
	}
}

func (x *mockDependency) MarshalBinary() (data []byte, err error) {
	// create a serializable struct that includes all fields
	serializable := struct {
		ID       string `json:"id"`
		Username string `json:"Username"`
		Email    string `json:"Email"`
	}{
		ID:       x.id,
		Username: x.Username,
		Email:    x.Email,
	}

	return json.Marshal(serializable)
}

func (x *mockDependency) UnmarshalBinary(data []byte) error {
	serializable := struct {
		ID       string `json:"id"`
		Username string `json:"Username"`
		Email    string `json:"Email"`
	}{}

	if err := json.Unmarshal(data, &serializable); err != nil {
		return err
	}

	// Update the dependency fields
	x.id = serializable.ID
	x.Username = serializable.Username
	x.Email = serializable.Email

	return nil
}

func (x *mockDependency) ID() string {
	return x.id
}

type escalationSupervisor struct{}

var _ Actor = (*escalationSupervisor)(nil)

func newEscalationSupervisor() *escalationSupervisor {
	return &escalationSupervisor{}
}

// PreStart implements Actor.
func (e *escalationSupervisor) PreStart(*Context) error {
	return nil
}

// Receive implements Actor.
func (e *escalationSupervisor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *goaktpb.Mayday:
		ctx.Stop(ctx.Sender())
	default:
		ctx.Unhandled()
	}
}

// PostStop implements Actor.
func (e *escalationSupervisor) PostStop(*Context) error {
	return nil
}

type reinstateSupervisor struct{}

var _ Actor = (*reinstateSupervisor)(nil)

func newReinstateSupervisor() *reinstateSupervisor {
	return &reinstateSupervisor{}
}

// PostStop implements Actor.
func (r *reinstateSupervisor) PostStop(*Context) error {
	return nil
}

// PreStart implements Actor.
func (r *reinstateSupervisor) PreStart(*Context) error {
	return nil
}

// Receive implements Actor.
func (r *reinstateSupervisor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *goaktpb.Mayday:
		actorName := ctx.Sender().Name()

		if actorName == "reinstate" {
			ctx.Reinstate(ctx.Sender())
			return
		}

		if actorName == "reinstateNamed" {
			ctx.ReinstateNamed(actorName)
			return
		}

		ctx.Stop(ctx.Sender())
	default:
		ctx.Unhandled()
	}
}

var postStarCount = atomic.NewInt32(0)

type postStartActor struct{}

// enforce compilation error
var _ Actor = (*postStartActor)(nil)

// newMockActor creates a actorQA
func newPostActor() *postStartActor {
	return &postStartActor{}
}

// Init initialize the actor. This function can be used to set up some database connections
// or some sort of initialization before the actor init processing message
func (p *postStartActor) PreStart(*Context) error {
	return nil
}

// Shutdown gracefully shuts down the given actor
func (p *postStartActor) PostStop(*Context) error {
	return nil
}

// Receive processes any message dropped into the actor mailbox without a reply
func (p *postStartActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
		postStarCount.Inc()
	default:
		ctx.Unhandled()
	}
}
