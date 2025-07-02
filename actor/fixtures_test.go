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
	"github.com/tochemey/goakt/v3/internal/pause"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/remote"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

type MockSubscriber struct {
	counter *atomic.Int64
}

var _ Actor = &MockSubscriber{}

func NewMockSubscriber() *MockSubscriber {
	return &MockSubscriber{
		counter: atomic.NewInt64(0),
	}
}

func (x *MockSubscriber) PreStart(*Context) error {
	return nil
}

func (x *MockSubscriber) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.SubscribeAck:
		x.counter.Inc()
	case *testpb.TestCount:
		x.counter.Inc()
	case *goaktpb.UnsubscribeAck:
		x.counter.Dec()
	}
}

func (x *MockSubscriber) PostStop(*Context) error {
	return nil
}

// MockActor is an actor that helps run various test scenarios
type MockActor struct{}

// enforce compilation error
var _ Actor = (*MockActor)(nil)

// NewMockActor creates a actor
func NewMockActor() *MockActor {
	return &MockActor{}
}

// Init initialize the actor. This function can be used to set up some database connections
// or some sort of initialization before the actor init processing message
func (p *MockActor) PreStart(*Context) error {
	return nil
}

// Shutdown gracefully shuts down the given actor
func (p *MockActor) PostStop(*Context) error {
	return nil
}

// Receive processes any message dropped into the actor mailbox without a reply
func (p *MockActor) Receive(ctx *ReceiveContext) {
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
			pause.For(receivingDelay)
			wg.Done()
		}()
		// block until timer is up
		wg.Wait()
	default:
		ctx.Unhandled()
	}
}

// MockSupervisor is an actor that monitors another actor
// and reacts to its failure.
type MockSupervisor struct{}

// enforce compilation error
var _ Actor = (*MockSupervisor)(nil)

// NewMockSupervisor creates an instance of supervisorQA
func NewMockSupervisor() *MockSupervisor {
	return &MockSupervisor{}
}

func (p *MockSupervisor) PreStart(*Context) error {
	return nil
}

func (p *MockSupervisor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testpb.TestSend:
	case *goaktpb.Terminated:
		// pass
	default:
		panic(ErrUnhandled)
	}
}

func (p *MockSupervisor) PostStop(*Context) error {
	return nil
}

// MockSupervised is an actor that is monitored
type MockSupervised struct{}

// enforce compilation error
var _ Actor = (*MockSupervised)(nil)

// NewMockSupervised creates an instance of supervisedQA
func NewMockSupervised() *MockSupervised {
	return &MockSupervised{}
}

func (x *MockSupervised) PreStart(*Context) error {
	return nil
}

func (x *MockSupervised) Receive(ctx *ReceiveContext) {
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

func (x *MockSupervised) PostStop(*Context) error {
	return nil
}

// MockBehavior is used to test the actor behavior
type MockBehavior struct{}

// enforce compilation error
var _ Actor = &MockBehavior{}

func (x *MockBehavior) PreStart(*Context) error {
	return nil
}

func (x *MockBehavior) PostStop(*Context) error {
	return nil
}

func (x *MockBehavior) Receive(ctx *ReceiveContext) {
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
func (x *MockBehavior) Authenticated(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestReadiness:
		ctx.Response(new(testpb.TestReady))
		ctx.UnBecome()
	}
}

func (x *MockBehavior) CreditAccount(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.CreditAccount:
		ctx.Response(new(testpb.AccountCredited))
		ctx.BecomeStacked(x.DebitAccount)
	case *testpb.TestBye:
		_ = ctx.Self().Shutdown(ctx.Context())
	}
}

func (x *MockBehavior) DebitAccount(ctx *ReceiveContext) {
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

type MockStash struct{}

func (x *MockStash) PreStart(*Context) error {
	return nil
}

func (x *MockStash) Receive(ctx *ReceiveContext) {
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

func (x *MockStash) Ready(ctx *ReceiveContext) {
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

func (x *MockStash) PostStop(*Context) error {
	return nil
}

var _ Actor = &MockStash{}

type MockPreStart struct{}

func (x *MockPreStart) PreStart(*Context) error {
	return errors.New("failed")
}

func (x *MockPreStart) Receive(*ReceiveContext) {}

func (x *MockPreStart) PostStop(*Context) error {
	return nil
}

var _ Actor = &MockPreStart{}

type MockPostStop struct{}

func (x *MockPostStop) PreStart(*Context) error {
	return nil
}

func (x *MockPostStop) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testpb.TestSend:
	case *testpb.TestPanic:
		panic("panicked")
	}
}

func (x *MockPostStop) PostStop(*Context) error {
	return errors.New("failed")
}

var _ Actor = &MockPostStop{}

type MockRestart struct {
	counter *atomic.Int64
}

func NewMockRestart() *MockRestart {
	return &MockRestart{counter: atomic.NewInt64(0)}
}

func (x *MockRestart) PreStart(*Context) error {
	// increment counter
	x.counter.Inc()
	// error when counter is greater than 1
	if x.counter.Load() > 1 {
		return errors.New("cannot restart")
	}
	return nil
}

func (x *MockRestart) Receive(*ReceiveContext) {
}

func (x *MockRestart) PostStop(*Context) error {
	return nil
}

var _ Actor = &MockRestart{}

type MockForward struct {
	actorRef  *PID
	remoteRef *PID
}

func (x *MockForward) PreStart(*Context) error {
	return nil
}

func (x *MockForward) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testpb.TestBye:
		ctx.Forward(x.actorRef)
	case *testpb.TestRemoteForward:
		ctx.RemoteForward(x.remoteRef.Address())
	}
}

func (x *MockForward) PostStop(*Context) error {
	return nil
}

type MockRemote struct {
}

func (x *MockRemote) PreStart(*Context) error {
	return nil
}

func (x *MockRemote) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestRemoteForward:
		ctx.Shutdown()
	}
}

func (x *MockRemote) PostStop(*Context) error {
	return nil
}

type MockUnhandled struct{}

var _ Actor = &MockUnhandled{}

func (d *MockUnhandled) PreStart(*Context) error {
	return nil
}

func (d *MockUnhandled) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
		// pass
	default:
		ctx.Unhandled()
	}
}

func (d *MockUnhandled) PostStop(*Context) error {
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
	actorSystemName := "accountsSystem"
	natsSubject := "some-subject"
	// create the config
	config := nats.Config{
		NatsServer:    fmt.Sprintf("nats://%s", serverAddr),
		NatsSubject:   natsSubject,
		Host:          host,
		DiscoveryPort: discoveryPort,
	}

	// create the instance of provider
	provider := nats.NewDiscovery(&config, nats.WithLogger(log.DiscardLogger))

	// create the actor system options
	options := []Option{
		WithLogger(logger),
		WithRemote(remote.NewConfig(host, remotingPort)),
		WithCluster(
			NewClusterConfig().
				WithKinds(
					new(MockActor),
					new(MockEntity),
					new(MockGrainActor),
				).
				WithGrains(new(MockGrain)).
				WithPartitionCount(7).
				WithReplicaCount(1).
				WithPeersPort(clusterPort).
				WithMinimumPeersQuorum(1).
				WithDiscoveryPort(discoveryPort).
				WithBootstrapTimeout(time.Second).
				WithClusterStateSyncInterval(300 * time.Millisecond).
				WithPeersStateSyncInterval(500 * time.Millisecond).
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
	pause.For(2 * time.Second)

	// return the cluster startNode
	return system, provider
}

type MockRouter struct {
	counter int
	logger  log.Logger
}

var _ Actor = (*MockRouter)(nil)

func (x *MockRouter) PreStart(*Context) error {
	x.logger = log.DiscardLogger
	return nil
}

func (x *MockRouter) Receive(ctx *ReceiveContext) {
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

func (x *MockRouter) PostStop(*Context) error {
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

type MockStateStore interface {
	extension.Extension
	WriteState(persistenceID string, state *testpb.Account) error
	GetLatestState(persistenceID string) (*testpb.Account, error)
}

type MockExtension struct {
	db *sync.Map
}

var _ MockStateStore = (*MockExtension)(nil)

func NewMockExtension() *MockExtension {
	return &MockExtension{
		db: &sync.Map{},
	}
}

func (m *MockExtension) ID() string {
	return "MockStateStore"
}

// GetLatestState implements MockStateStore.
func (m *MockExtension) GetLatestState(persistenceID string) (*testpb.Account, error) {
	value, ok := m.db.Load(persistenceID)
	if !ok {
		return new(testpb.Account), nil
	}
	return value.(*testpb.Account), nil
}

// WriteState implements MockStateStore.
func (m *MockExtension) WriteState(persistenceID string, state *testpb.Account) error {
	m.db.Store(persistenceID, state)
	return nil
}

type MockEntity struct {
	persistenceID string
	currentState  *atomic.Pointer[testpb.Account]
	stateStore    MockStateStore
}

var _ Actor = (*MockEntity)(nil)

func NewMockEntity() *MockEntity {
	return &MockEntity{}
}

// PreStart implements Actor.
func (m *MockEntity) PreStart(ctx *Context) error {
	m.currentState = atomic.NewPointer(new(testpb.Account))
	m.stateStore = ctx.Extension("MockStateStore").(MockStateStore)
	m.persistenceID = ctx.ActorName()
	return m.recoverFromStore()
}

// PostStop implements Actor.
func (m *MockEntity) PostStop(*Context) error {
	return m.stateStore.WriteState(m.persistenceID, m.currentState.Load())
}

// Receive implements Actor.
func (m *MockEntity) Receive(ctx *ReceiveContext) {
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

func (m *MockEntity) recoverFromStore() error {
	latestState, err := m.stateStore.GetLatestState(m.persistenceID)
	if err != nil {
		return fmt.Errorf("failed to get the latest state: %w", err)
	}

	if latestState != nil {
		m.currentState.Store(latestState)
	}

	return nil
}

type MockDependency struct {
	id       string
	Username string
	Email    string
}

var _ extension.Dependency = (*MockDependency)(nil)

func NewMockDependency(id, userName, email string) *MockDependency {
	return &MockDependency{
		id:       id,
		Username: userName,
		Email:    email,
	}
}

func (x *MockDependency) MarshalBinary() (data []byte, err error) {
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

func (x *MockDependency) UnmarshalBinary(data []byte) error {
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

func (x *MockDependency) ID() string {
	return x.id
}

type MockEscalation struct{}

var _ Actor = (*MockEscalation)(nil)

func NewMockEscalation() *MockEscalation {
	return &MockEscalation{}
}

// PreStart implements Actor.
func (e *MockEscalation) PreStart(*Context) error {
	return nil
}

// Receive implements Actor.
func (e *MockEscalation) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *goaktpb.Mayday:
		ctx.Stop(ctx.Sender())
	default:
		ctx.Unhandled()
	}
}

// PostStop implements Actor.
func (e *MockEscalation) PostStop(*Context) error {
	return nil
}

type MockReinstate struct{}

var _ Actor = (*MockReinstate)(nil)

func NewMockReinstate() *MockReinstate {
	return &MockReinstate{}
}

// PostStop implements Actor.
func (r *MockReinstate) PostStop(*Context) error {
	return nil
}

// PreStart implements Actor.
func (r *MockReinstate) PreStart(*Context) error {
	return nil
}

// Receive implements Actor.
func (r *MockReinstate) Receive(ctx *ReceiveContext) {
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

type MockPostStart struct{}

// enforce compilation error
var _ Actor = (*MockPostStart)(nil)

// NewMockActor creates a actor
func NewMockPostStart() *MockPostStart {
	return &MockPostStart{}
}

// Init initialize the actor. This function can be used to set up some database connections
// or some sort of initialization before the actor init processing message
func (p *MockPostStart) PreStart(*Context) error {
	return nil
}

// Shutdown gracefully shuts down the given actor
func (p *MockPostStart) PostStop(*Context) error {
	return nil
}

// Receive processes any message dropped into the actor mailbox without a reply
func (p *MockPostStart) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
		postStarCount.Inc()
	default:
		ctx.Unhandled()
	}
}

type MockGrain struct {
	name string
}

var _ Grain = (*MockGrain)(nil)

func NewMockGrain() *MockGrain {
	return &MockGrain{}
}

// OnActivate implements Grain.
// nolint
func (m *MockGrain) OnActivate(ctx context.Context, props *GrainProps) error {
	m.name = props.Identity().Name()
	return nil
}

// OnDeactivate implements Grain.
// nolint
func (m *MockGrain) OnDeactivate(ctx context.Context, props *GrainProps) error {
	return nil
}

// OnReceive implements Grain.
func (m *MockGrain) OnReceive(ctx *GrainContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
		ctx.ActorSystem().Logger().Infof("%s received TestSend message in MockGrain", ctx.Self().Name())
		ctx.NoErr()
	case *testpb.TestPing:
		actorName := "Actor20"
		response, err := ctx.AskActor(actorName, ctx.Message(), time.Second)
		if err != nil {
			ctx.Err(err)
			return
		}
		ctx.Response(response)
	case *testpb.TestBye:
		actorName := "Actor20"
		err := ctx.TellActor(actorName, ctx.Message())
		if err != nil {
			ctx.Err(err)
			return
		}
		ctx.NoErr()
	case *testpb.TestMessage:
		identity, err := ctx.GrainIdentity("Grain2", func(_ context.Context) (Grain, error) {
			return NewMockGrain(), nil
		})
		if err != nil {
			ctx.Err(err)
			return
		}

		// send a message to the grain
		resp, err := ctx.AskGrain(identity, new(testpb.TestReply), time.Second)
		if err != nil {
			ctx.Err(err)
			return
		}
		ctx.Response(resp)

	case *testpb.TestReady:
		identity, err := ctx.GrainIdentity("Grain2", func(_ context.Context) (Grain, error) {
			return NewMockGrain(), nil
		})
		if err != nil {
			ctx.Err(err)
			return
		}

		if err := ctx.TellGrain(identity, new(testpb.TestSend)); err != nil {
			ctx.Err(err)
			return
		}
		ctx.NoErr()

	case *testpb.TestReply:
		ctx.Response(&testpb.Reply{Content: "received message"})
	case *testpb.TestTimeout:
		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			pause.For(time.Minute)
			wg.Done()
		}()
		wg.Wait()
		ctx.NoErr()
	default:
		ctx.Unhandled()
	}
}

type MockGrainActivationFailure struct{}

var _ Grain = (*MockGrainActivationFailure)(nil)

func NewMockGrainActivationFailure() *MockGrainActivationFailure {
	return &MockGrainActivationFailure{}
}

// OnActivate implements Grain.
// nolint
func (m *MockGrainActivationFailure) OnActivate(ctx context.Context, props *GrainProps) error {
	return errors.New("failed to activate grain")
}

// OnDeactivate implements Grain.
// nolint
func (m *MockGrainActivationFailure) OnDeactivate(ctx context.Context, props *GrainProps) error {
	return nil
}

// OnReceive implements Grain.
func (m *MockGrainActivationFailure) OnReceive(ctx *GrainContext) {
	ctx.NoErr()
}

type MockGrainDeactivationFailure struct{}

// OnActivate implements Grain.
// nolint
func (m *MockGrainDeactivationFailure) OnActivate(ctx context.Context, props *GrainProps) error {
	return nil
}

// OnDeactivate implements Grain.
// nolint
func (m *MockGrainDeactivationFailure) OnDeactivate(ctx context.Context, props *GrainProps) error {
	return errors.New("failed to deactivate grain")
}

// OnReceive implements Grain.
func (m *MockGrainDeactivationFailure) OnReceive(ctx *GrainContext) {
	ctx.NoErr()
}

var _ Grain = (*MockGrainDeactivationFailure)(nil)

func NewMockGrainDeactivationFailure() *MockGrainDeactivationFailure {
	return &MockGrainDeactivationFailure{}
}

type MockGrainReceiveFailure struct{}

var _ Grain = (*MockGrainReceiveFailure)(nil)

func NewMockGrainReceiveFailure() *MockGrainReceiveFailure {
	return &MockGrainReceiveFailure{}
}

// OnActivate implements Grain.
// nolint
func (m *MockGrainReceiveFailure) OnActivate(ctx context.Context, props *GrainProps) error {
	return nil
}

// OnDeactivate implements Grain.
// nolint
func (m *MockGrainReceiveFailure) OnDeactivate(ctx context.Context, props *GrainProps) error {
	return nil
}

// OnReceive implements Grain.
func (m *MockGrainReceiveFailure) OnReceive(ctx *GrainContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
		ctx.Err(errors.New("failed to process message"))
	default:
		ctx.Unhandled()
	}
}

type MockPersistenceGrain struct {
	persistenceID string
	currentState  *atomic.Pointer[testpb.Account]
	stateStore    MockStateStore
}

var _ Grain = (*MockPersistenceGrain)(nil)

func NewMockPersistenceGrain() *MockPersistenceGrain {
	return &MockPersistenceGrain{}
}

// OnActivate implements Grain.
// nolint
func (m *MockPersistenceGrain) OnActivate(ctx context.Context, props *GrainProps) error {
	m.currentState = atomic.NewPointer(new(testpb.Account))
	m.stateStore = props.ActorSystem().Extension("MockStateStore").(MockStateStore)
	m.persistenceID = props.Identity().Name()
	return m.recoverFromStore()
}

// OnDeactivate implements Grain.
// nolint
func (m *MockPersistenceGrain) OnDeactivate(ctx context.Context, props *GrainProps) error {
	return m.stateStore.WriteState(m.persistenceID, m.currentState.Load())
}

// OnReceive implements Grain.
func (m *MockPersistenceGrain) OnReceive(ctx *GrainContext) {
	switch received := ctx.Message().(type) {
	case *testpb.CreateAccount:
		balance := received.GetAccountBalance()
		newBalance := m.currentState.Load().GetAccountBalance() + balance
		m.currentState.Store(&testpb.Account{
			AccountId:      m.persistenceID,
			AccountBalance: newBalance,
		})

		// persist the Grain state
		if err := m.stateStore.WriteState(m.persistenceID, m.currentState.Load()); err != nil {
			ctx.Err(err)
			return
		}

		ctx.NoErr()
	case *testpb.CreditAccount:
		// TODO: in production extra validation will be needed.
		balance := received.GetBalance()
		newBalance := m.currentState.Load().GetAccountBalance() + balance
		m.currentState.Store(&testpb.Account{
			AccountId:      m.persistenceID,
			AccountBalance: newBalance,
		})

		// persist the Grain state
		if err := m.stateStore.WriteState(m.persistenceID, m.currentState.Load()); err != nil {
			ctx.Err(err)
			return
		}

		ctx.Response(m.currentState.Load())
	case *testpb.GetAccount:
		ctx.Response(m.currentState.Load())
	default:
		ctx.Unhandled()
	}
}

func (m *MockPersistenceGrain) recoverFromStore() error {
	latestState, err := m.stateStore.GetLatestState(m.persistenceID)
	if err != nil {
		return fmt.Errorf("failed to get the latest state: %w", err)
	}

	if latestState != nil {
		m.currentState.Store(latestState)
	}

	return nil
}

type MockPanickingGrain struct{}

func NewMockPanickingGrain() *MockPanickingGrain {
	return &MockPanickingGrain{}
}

// OnActivate implements Grain.
// nolint
func (m *MockPanickingGrain) OnActivate(ctx context.Context, props *GrainProps) error {
	return nil
}

// OnDeactivate implements Grain.
// nolint
func (m *MockPanickingGrain) OnDeactivate(ctx context.Context, props *GrainProps) error {
	return nil
}

// OnReceive implements Grain.
// nolint
func (m *MockPanickingGrain) OnReceive(ctx *GrainContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
		panic("test panic")
	case *testpb.TestReply:
		panic(NewInternalError(errors.New("test panic")))
	}
}

var _ Grain = (*MockPanickingGrain)(nil)

type MockGrainActor struct{}

// enforce compilation error
var _ Actor = (*MockGrainActor)(nil)

// NewMockActor creates a actor
func NewMockGrainActor() *MockGrainActor {
	return &MockGrainActor{}
}

// Init initialize the actor. This function can be used to set up some database connections
// or some sort of initialization before the actor init processing message
func (p *MockGrainActor) PreStart(*Context) error {
	return nil
}

// Shutdown gracefully shuts down the given actor
func (p *MockGrainActor) PostStop(*Context) error {
	return nil
}

// Receive processes any message dropped into the actor mailbox without a reply
func (p *MockGrainActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
	case *testpb.TestSend:
	case *testpb.TestPing:
		ctx.Response(new(testpb.TestPong))
	case *testpb.TestBye:
		ctx.Shutdown()

	default:
		ctx.Unhandled()
	}
}

type MockShutdownHook struct {
	strategy       RecoveryStrategy
	executionCount *atomic.Int32
	maxRetries     int
}

var _ ShutdownHook = (*MockShutdownHook)(nil)

// Execute implements ShutdownHook.
func (m *MockShutdownHook) Execute(context.Context, ActorSystem) error {
	defer func() {
		m.executionCount.Inc()
	}()

	switch m.strategy {
	case ShouldFail:
		return fmt.Errorf("mock shutdown hook failed")
	case ShouldRetryAndFail:
		return fmt.Errorf("mock shutdown hook failed after retrying")
	case ShouldSkip:
		return fmt.Errorf("mock shutdown hook skipped")
	case ShouldRetryAndSkip:
		return fmt.Errorf("mock shutdown hook skipped after retrying")
	default:
		return nil
	}
}

// Recovery implements ShutdownHook.
func (m *MockShutdownHook) Recovery() *ShutdownHookRecovery {
	return NewShutdownHookRecovery(
		WithShutdownHookRecoveryStrategy(m.strategy),
		WithShutdownHookRetry(m.maxRetries, 100*time.Millisecond),
	)
}

type MockPanickingShutdownHook struct {
	executionCount *atomic.Int32
	testCase       string
}

func (m *MockPanickingShutdownHook) Execute(context.Context, ActorSystem) error {
	defer func() {
		m.executionCount.Inc()
	}()

	switch m.testCase {
	case "case1":
		panic(errors.New("case1 panic error"))
	case "case2":
		panic(NewPanicError(errors.New("case2 panic error")))
	default:
		panic("implement me")
	}
}

func (m *MockPanickingShutdownHook) Recovery() *ShutdownHookRecovery {
	return nil
}

type MockShutdownHookWithoutRecovery struct {
	executionCount *atomic.Int32
}

func (m *MockShutdownHookWithoutRecovery) Execute(context.Context, ActorSystem) error {
	m.executionCount.Inc()
	return errors.New("mock shutdown hook without recovery")
}

func (m *MockShutdownHookWithoutRecovery) Recovery() *ShutdownHookRecovery {
	return nil
}
