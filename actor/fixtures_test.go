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
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/discovery"
	"github.com/tochemey/goakt/v3/discovery/nats"
	"github.com/tochemey/goakt/v3/extension"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/util"
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
			util.Pause(receivingDelay)
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
					new(MockActor),
					new(MockEntity),
				).
				WithGrains(new(MockGrain)).
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
	return "mockStateStore"
}

// GetLatestState implements mockStateStore.
func (m *MockExtension) GetLatestState(persistenceID string) (*testpb.Account, error) {
	value, ok := m.db.Load(persistenceID)
	if !ok {
		return new(testpb.Account), nil
	}
	return value.(*testpb.Account), nil
}

// WriteState implements mockStateStore.
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
	m.stateStore = ctx.Extension("mockStateStore").(MockStateStore)
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

type MockRestartPostStart struct{}

// enforce compilation error
var _ Actor = (*MockRestartPostStart)(nil)

func NewMockRestartPostStart() *MockRestartPostStart {
	return &MockRestartPostStart{}
}

// Init initialize the actor. This function can be used to set up some database connections
// or some sort of initialization before the actor init processing message
func (p *MockRestartPostStart) PreStart(*Context) error {
	return nil
}

// Shutdown gracefully shuts down the given actor
func (p *MockRestartPostStart) PostStop(*Context) error {
	return nil
}

// Receive processes any message dropped into the actor mailbox without a reply
func (p *MockRestartPostStart) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
		postStarCount.Inc()
	default:
		ctx.Unhandled()
	}
}

type MockGrain struct {
	id          *Identity
	actorSystem ActorSystem
}

var _ Grain = (*MockGrain)(nil)

// NewMockGrain creates a new instance of MockGrain.
func NewMockGrain() *MockGrain {
	return &MockGrain{}
}

// HandleRequest implements Grain.
// nolint
func (m *MockGrain) ReceiveSync(ctx context.Context, message proto.Message) (proto.Message, error) {
	switch msg := message.(type) {
	case *testpb.TestSend:
		return &testpb.Reply{Content: "received message"}, nil
	default:
		return nil, fmt.Errorf("unhandled message type %T", msg)
	}
}

// ReceiveAsync implements Grain.
// nolint
func (m *MockGrain) ReceiveAsync(ctx context.Context, message proto.Message) error {
	switch msg := message.(type) {
	case *testpb.TestSend:
		return nil
	default:
		return fmt.Errorf("unhandled message type %T", msg)
	}
}

// OnActivate implements Grain.
// nolint
func (m *MockGrain) OnActivate(ctx *GrainContext) error {
	m.id = ctx.Self()
	m.actorSystem = ctx.ActorSystem()
	return nil
}

// Dependencies implements Grain.
func (m *MockGrain) Dependencies() []extension.Dependency {
	return nil
}

// OnDeactivate implements Grain.
// nolint
func (m *MockGrain) OnDeactivate(ctx *GrainContext) error {
	return nil
}

type MockGrainActivation struct{}

func NewMockGrainActivation() *MockGrainActivation {
	return &MockGrainActivation{}
}

// Dependencies implements Grain.
func (m *MockGrainActivation) Dependencies() []extension.Dependency {
	return nil
}

// OnActivate implements Grain.
func (m *MockGrainActivation) OnActivate(ctx *GrainContext) error {
	return errors.New("failed to activate grain")
}

// OnDeactivate implements Grain.
func (m *MockGrainActivation) OnDeactivate(*GrainContext) error {
	return nil
}

// ReceiveAsync implements Grain.
// nolint
func (m *MockGrainActivation) ReceiveAsync(ctx context.Context, message proto.Message) error {
	return nil
}

// ReceiveSync implements Grain.
// nolint
func (m *MockGrainActivation) ReceiveSync(ctx context.Context, message proto.Message) (proto.Message, error) {
	return nil, nil
}

type MockGrainDeactivation struct{}

func NewMockGrainDeactivation() *MockGrainDeactivation {
	return &MockGrainDeactivation{}
}

// Dependencies implements Grain.
func (m *MockGrainDeactivation) Dependencies() []extension.Dependency {
	return nil
}

// OnActivate implements Grain.
func (m *MockGrainDeactivation) OnActivate(*GrainContext) error {
	return nil
}

// OnDeactivate implements Grain.
func (m *MockGrainDeactivation) OnDeactivate(*GrainContext) error {
	return errors.New("failed to deactivate grain")
}

// ReceiveAsync implements Grain.
// nolint
func (m *MockGrainDeactivation) ReceiveAsync(ctx context.Context, message proto.Message) error {
	switch msg := message.(type) {
	case *testpb.TestSend:
		return nil
	default:
		return fmt.Errorf("unhandled message type %T", msg)
	}
}

// ReceiveSync implements Grain.
// nolint
func (m *MockGrainDeactivation) ReceiveSync(ctx context.Context, message proto.Message) (proto.Message, error) {
	return nil, nil
}

type MockGrainError struct{}

func NewMockGrainError() *MockGrainError {
	return &MockGrainError{}
}

// Dependencies implements Grain.
func (m *MockGrainError) Dependencies() []extension.Dependency {
	return nil
}

// OnActivate implements Grain.
func (m *MockGrainError) OnActivate(*GrainContext) error {
	return nil
}

// OnDeactivate implements Grain.
func (m *MockGrainError) OnDeactivate(*GrainContext) error {
	return nil
}

// ReceiveAsync implements Grain.
// nolint
func (m *MockGrainError) ReceiveAsync(ctx context.Context, message proto.Message) error {
	switch msg := message.(type) {
	case *testpb.TestSend:
		return errors.New("failed to process message")
	default:
		return fmt.Errorf("unhandled message type %T", msg)
	}
}

// ReceiveSync implements Grain.
// nolint
func (m *MockGrainError) ReceiveSync(ctx context.Context, message proto.Message) (proto.Message, error) {
	switch msg := message.(type) {
	case *testpb.TestSend:
		return nil, errors.New("failed to process message")
	default:
		return nil, fmt.Errorf("unhandled message type %T", msg)
	}
}
