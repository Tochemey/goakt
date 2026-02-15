// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package actor

import (
	"context"
	cryptotls "crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	consulcontainer "github.com/testcontainers/testcontainers-go/modules/consul"
	etcdContainer "github.com/testcontainers/testcontainers-go/modules/etcd"
	"github.com/travisjeffery/go-dynaport"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v3/address"
	"github.com/tochemey/goakt/v3/datacenter"
	"github.com/tochemey/goakt/v3/discovery"
	"github.com/tochemey/goakt/v3/discovery/consul"
	"github.com/tochemey/goakt/v3/discovery/etcd"
	"github.com/tochemey/goakt/v3/discovery/nats"
	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/eventstream"
	"github.com/tochemey/goakt/v3/extension"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/cluster"
	"github.com/tochemey/goakt/v3/internal/datacentercontroller"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/pause"
	"github.com/tochemey/goakt/v3/internal/registry"
	"github.com/tochemey/goakt/v3/internal/types"
	"github.com/tochemey/goakt/v3/internal/xsync"
	"github.com/tochemey/goakt/v3/log"
	mockcluster "github.com/tochemey/goakt/v3/mocks/cluster"
	mocksremote "github.com/tochemey/goakt/v3/mocks/remote"
	"github.com/tochemey/goakt/v3/passivation"
	"github.com/tochemey/goakt/v3/remote"
	"github.com/tochemey/goakt/v3/test/data/testpb"
	"github.com/tochemey/goakt/v3/tls"
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
		ctx.Logger().Info("MockActor started")
	case *testpb.TestSend:
	case *testpb.TestPanic:
		panic("Boom")
	case *testpb.TestReply:
		ctx.Response(&testpb.Reply{Content: "received message"})
	case *testpb.TestTimeout:
		// delay for a while before sending the reply
		wg := sync.WaitGroup{}
		wg.Go(func() {
			pause.For(receivingDelay)
		})
		// block until timer is up
		wg.Wait()
	default:
		ctx.Unhandled()
	}
}

func MockSupervisionPID(t *testing.T) *PID {
	t.Helper()
	return &PID{
		logger:                log.DiscardLogger,
		address:               address.New("child", "test-system", "127.0.0.1", 0),
		supervisionStopSignal: make(chan types.Unit, 1),
		eventsStream:          eventstream.New(),
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
		panic(gerrors.ErrUnhandled)
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
		panic(gerrors.ErrUnhandled)
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

type MockRoutee struct {
	counter int
	logger  log.Logger
}

var _ Actor = (*MockRoutee)(nil)

func (x *MockRoutee) PreStart(*Context) error {
	x.logger = log.DiscardLogger
	return nil
}

func (x *MockRoutee) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *testpb.TestLog:
		x.counter++
		x.logger.Infof("Got message: %s", msg.GetText())
	case *testpb.TestGetCount:
		x.counter++
		ctx.Response(&testpb.TestCount{Value: int32(x.counter)})
	case *testpb.TestSum:
		if msg.Delay != nil {
			wg := sync.WaitGroup{}
			wg.Go(func() {
				pause.For(msg.GetDelay().AsDuration())
			})
			wg.Wait()
		}

		sum := msg.GetA() + msg.GetB()
		ctx.Response(&testpb.TestSumResult{Result: sum})
	default:
		ctx.Unhandled()
	}
}

func (x *MockRoutee) PostStop(*Context) error {
	return nil
}

type BlockingRoutee struct{}

var _ Actor = (*BlockingRoutee)(nil)

func (b *BlockingRoutee) PreStart(*Context) error {
	return nil
}

func (b *BlockingRoutee) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSum:
		pause.For(10 * time.Second)
	default:
		ctx.Unhandled()
	}
}

func (b *BlockingRoutee) PostStop(*Context) error {
	return nil
}

type MockSum struct {
	sum          int64
	failureCount int32
}

var _ Actor = (*MockSum)(nil)

func NewMockSum() *MockSum {
	return &MockSum{}
}

func (m *MockSum) PostStop(*Context) error {
	return nil
}

func (m *MockSum) PreStart(*Context) error {
	return nil
}

func (m *MockSum) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *testpb.TestSumResult:
		m.sum = msg.GetResult()
	case *testpb.TestGetSumResult:
		ctx.Response(&testpb.TestSumResult{Result: m.sum})
	case *goaktpb.StatusFailure:
		m.failureCount++
	case *testpb.TestGetCount:
		ctx.Response(&testpb.TestCount{Value: m.failureCount})
	}
}

type TailChopProbe struct {
	sum           atomic.Int64
	failures      atomic.Int32
	failureNotifs chan struct{}
}

var _ Actor = (*TailChopProbe)(nil)

func NewTailChopProbe() *TailChopProbe {
	return &TailChopProbe{
		failureNotifs: make(chan struct{}, 1),
	}
}

func (x *TailChopProbe) PreStart(*Context) error {
	return nil
}

func (x *TailChopProbe) PostStop(*Context) error {
	return nil
}

func (x *TailChopProbe) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *testpb.TestSumResult:
		x.sum.Store(msg.GetResult())
	case *goaktpb.StatusFailure:
		x.failures.Inc()
		select {
		case x.failureNotifs <- struct{}{}:
		default:
		}
	case *testpb.TestGetSumResult:
		ctx.Response(&testpb.TestSumResult{Result: x.sum.Load()})
	case *testpb.TestGetCount:
		ctx.Response(&testpb.TestCount{Value: x.failures.Load()})
	}
}

func (x *TailChopProbe) WaitForFailure(timeout time.Duration) bool {
	select {
	case <-x.failureNotifs:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (x *TailChopProbe) FailureCount() int32 {
	return x.failures.Load()
}

func (x *TailChopProbe) Sum() int64 {
	return x.sum.Load()
}

type MockFaultyRoutee struct{}

var _ Actor = (*MockFaultyRoutee)(nil)

func (x *MockFaultyRoutee) PostStop(*Context) error {
	return nil
}

func (x *MockFaultyRoutee) PreStart(*Context) error {
	return nil
}

func (x *MockFaultyRoutee) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
		// pass
	default:
		ctx.Err(errors.New("routee failure"))
	}
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
	case *goaktpb.PanicSignal:
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
	case *goaktpb.PanicSignal:
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
		wg.Go(func() {
			pause.For(time.Minute)
		})
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

type MockPanickingActivateDeactivateGrain struct {
	activatePanicValue any
	panicValue         any
}

func (x *MockPanickingActivateDeactivateGrain) OnActivate(context.Context, *GrainProps) error {
	if x.activatePanicValue != nil {
		panic(x.activatePanicValue)
	}
	return nil
}

func (x *MockPanickingActivateDeactivateGrain) OnDeactivate(context.Context, *GrainProps) error {
	if x.panicValue != nil {
		panic(x.panicValue)
	}
	panic("deactivate panic")
}

func (x *MockPanickingActivateDeactivateGrain) OnReceive(*GrainContext) {}

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
		panic(gerrors.NewInternalError(errors.New("test panic")))
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
		panic(gerrors.NewPanicError(errors.New("case2 panic error")))
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

type MockFakePassivationStrategy struct{}

func (x *MockFakePassivationStrategy) Name() string {
	return "MockFakePassivationStrategy"
}

var _ passivation.Strategy = (*MockFakePassivationStrategy)(nil)

func (x *MockFakePassivationStrategy) String() string {
	return "MockFakePassivationStrategy"
}

func MockPassivationPID(t *testing.T, name string, strategy passivation.Strategy) *PID {
	t.Helper()
	pid := &PID{
		address:             address.New(name, "test-system", "127.0.0.1", 0),
		passivationStrategy: strategy,
		logger:              log.DiscardLogger,
	}
	return pid
}

type MockPassivationParticipant struct {
	id        string
	last      time.Time
	passivate func(string) bool
}

func (s *MockPassivationParticipant) passivationID() string {
	return s.id
}

func (s *MockPassivationParticipant) passivationLatestActivity() time.Time {
	return s.last
}

func (s *MockPassivationParticipant) passivationTry(reason string) bool {
	if s.passivate != nil {
		return s.passivate(reason)
	}
	return true
}

type MockErrorMailbox struct{}

var _ Mailbox = (*MockErrorMailbox)(nil)

func NewMockErrorMailbox() *MockErrorMailbox {
	return &MockErrorMailbox{}
}

// Dequeue implements Mailbox.
func (m *MockErrorMailbox) Dequeue() (msg *ReceiveContext) {
	return nil
}

// Dispose implements Mailbox.
func (m *MockErrorMailbox) Dispose() {}

// Enqueue implements Mailbox.
func (m *MockErrorMailbox) Enqueue(_ *ReceiveContext) error {
	return fmt.Errorf("mock error mailbox: failed to enqueue message")
}

// IsEmpty implements Mailbox.
func (m *MockErrorMailbox) IsEmpty() bool {
	return true
}

// Len implements Mailbox.
func (m *MockErrorMailbox) Len() int64 {
	return 0
}

type MockFIFO struct {
	expected int
	seen     []int32
	done     chan struct{}
	once     sync.Once
}

func NewMockFIFO(expected int) *MockFIFO {
	return &MockFIFO{
		expected: expected,
		done:     make(chan struct{}),
	}
}

func (a *MockFIFO) PreStart(*Context) error { return nil }

func (a *MockFIFO) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *testpb.TestCount:
		a.seen = append(a.seen, msg.GetValue())
		if len(a.seen) == a.expected {
			a.once.Do(func() { close(a.done) })
		}
	default:
		ctx.Unhandled()
	}
}

func (a *MockFIFO) PostStop(*Context) error { return nil }

func (a *MockFIFO) Seen() []int32 {
	copySeen := make([]int32, len(a.seen))
	copy(copySeen, a.seen)
	return copySeen
}

func (a *MockFIFO) Done() <-chan struct{} {
	return a.done
}

type MockFailingDependency struct {
	err error
}

func (d *MockFailingDependency) ID() string {
	return "failing-dependency"
}

func (d *MockFailingDependency) MarshalBinary() ([]byte, error) {
	return nil, d.err
}

func (d *MockFailingDependency) UnmarshalBinary(_ []byte) error {
	return nil
}

func MockReplicationTestSystem(clusterMock *mockcluster.Cluster) *actorSystem {
	topic := &PID{}
	topic.setState(runningState, false)
	noSender := &PID{}
	noSender.setState(runningState, true)

	sys := &actorSystem{
		name:         "test-replication",
		logger:       log.DiscardLogger,
		actors:       newTree(),
		actorsQueue:  make(chan *internalpb.Actor, 4),
		grainsQueue:  make(chan *internalpb.Grain, 4),
		remoteConfig: remote.NewConfig("127.0.0.1", 8080),
		clusterNode:  &discovery.Node{Host: "127.0.0.1", PeersPort: 9000},
		cluster:      clusterMock,
		topicActor:   topic,
		noSender:     noSender,
	}

	sys.started.Store(true)
	sys.starting.Store(false)
	sys.shuttingDown.Store(false)
	sys.startedAt.Store(time.Now().Unix())
	sys.actorsCounter.Store(0)
	sys.deadlettersCounter.Store(0)

	sys.clusterEnabled.Store(true)
	sys.relocationEnabled.Store(false) // callers toggle when needed
	sys.noSender.actorSystem = sys
	sys.topicActor.actorSystem = sys

	return sys
}

func MockSimpleClusterReadyActorSystem(rem remote.Remoting, cl cluster.Cluster, node *discovery.Node, opts ...remote.Option) *actorSystem {
	sys := &actorSystem{
		logger:      log.DiscardLogger,
		cluster:     cl,
		remoting:    rem,
		clusterNode: node,
		remoteConfig: remote.NewConfig(
			node.Host,
			node.RemotingPort,
			opts...,
		),
	}

	sys.started.Store(true)
	sys.clusterEnabled.Store(true)
	sys.shuttingDown.Store(false)
	sys.grains = xsync.NewMap[string, *grainPID]()
	sys.registry = registry.NewRegistry()
	sys.reflection = newReflection(sys.registry)
	sys.grainsQueue = make(chan *internalpb.Grain, 1)

	// nolint
	go func() {
		for range sys.grainsQueue {
			// drop test grains
		}
	}()

	return sys
}

func MockPID(system ActorSystem, name string, port int) *PID {
	return &PID{
		address:     address.New(name, system.Name(), "host", port),
		actorSystem: system,
	}
}

type MockReinstateRaceActor struct {
	postStopCount *atomic.Int32
}

func (a *MockReinstateRaceActor) PreStart(*Context) error { return nil }

func (a *MockReinstateRaceActor) Receive(*ReceiveContext) {}

func (a *MockReinstateRaceActor) PostStop(*Context) error {
	if a.postStopCount != nil {
		a.postStopCount.Inc()
	}
	return nil
}

// MockUnimplementedActor deliberately does not implement the Actor interface to exercise reflection error paths.
type MockUnimplementedActor struct{}

type MockContextReleasingGrain struct {
	done chan *GrainContext
}

var _ Grain = (*MockContextReleasingGrain)(nil)

// nolint
func (g *MockContextReleasingGrain) OnActivate(ctx context.Context, props *GrainProps) error {
	return nil
}

func (g *MockContextReleasingGrain) OnReceive(ctx *GrainContext) {
	g.done <- ctx
	ctx.NoErr()
}

// nolint
func (g *MockContextReleasingGrain) OnDeactivate(ctx context.Context, props *GrainProps) error {
	return nil
}

type MockNopMailbox struct{}

func (MockNopMailbox) Enqueue(*ReceiveContext) error { return nil }
func (MockNopMailbox) Dequeue() *ReceiveContext      { return nil }
func (MockNopMailbox) IsEmpty() bool                 { return true }
func (MockNopMailbox) Len() int64                    { return 0 }
func (MockNopMailbox) Dispose()                      {}

func MockSingletonClusterReadyActorSystem(t *testing.T) *actorSystem {
	t.Helper()
	ports := dynaport.Get(3)

	sys, err := NewActorSystem("spawn-test", WithLogger(log.DiscardLogger))
	require.NoError(t, err)

	clusterNode := &discovery.Node{
		Name:          "spawn-test-node",
		Host:          "127.0.0.1",
		DiscoveryPort: ports[0],
		PeersPort:     ports[1],
		RemotingPort:  ports[2],
	}

	actorSys := sys.(*actorSystem)
	actorSys.started.Store(true)
	actorSys.clusterEnabled.Store(true)
	actorSys.remotingEnabled.Store(true)
	actorSys.clusterNode = clusterNode

	return actorSys
}

// //////////////////////////////////////// CLUSTER PROVIDERS MOCKS //////////////////////////////////////
type providerFactory func(t *testing.T, host string, discoveryPort int) discovery.Provider

type headerPropagator struct {
	headerKey string
	ctxKey    any
}

func (x *headerPropagator) Inject(ctx context.Context, headers http.Header) error {
	if val := ctx.Value(x.ctxKey); val != nil {
		headers.Set(x.headerKey, fmt.Sprint(val))
	}
	return nil
}

func (x *headerPropagator) Extract(ctx context.Context, headers http.Header) (context.Context, error) {
	if val := headers.Get(x.headerKey); val != "" {
		ctx = context.WithValue(ctx, x.ctxKey, val)
	}
	return ctx, nil
}

type contextEchoGrain struct {
	key  any
	mu   sync.RWMutex
	seen any
}

func (*contextEchoGrain) OnActivate(context.Context, *GrainProps) error   { return nil }
func (*contextEchoGrain) OnDeactivate(context.Context, *GrainProps) error { return nil }

func (g *contextEchoGrain) OnReceive(ctx *GrainContext) {
	g.setSeen(ctx.Context().Value(g.key))
	if _, ok := ctx.Message().(*testpb.TestReply); ok {
		ctx.Response(&testpb.Reply{Content: fmt.Sprint(g.Seen())})
		return
	}
	ctx.NoErr()
}

func (g *contextEchoGrain) setSeen(val any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.seen = val
}

func (g *contextEchoGrain) Seen() any {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.seen
}

type contextEchoActor struct {
	key  any
	mu   sync.RWMutex
	seen any
}

func (*contextEchoActor) PreStart(*Context) error { return nil }
func (*contextEchoActor) PostStop(*Context) error { return nil }

func (a *contextEchoActor) Receive(ctx *ReceiveContext) {
	a.setSeen(ctx.Context().Value(a.key))

	switch ctx.Message().(type) {
	case *testpb.TestReply:
		ctx.Response(&testpb.Reply{Content: fmt.Sprint(a.Seen())})
	case *testpb.TestSend:
		// no-op
	default:
	}
}

func (a *contextEchoActor) setSeen(val any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seen = val
}

func (a *contextEchoActor) Seen() any {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.seen
}

type MockFailingContextPropagator struct {
	err error
}

func (f *MockFailingContextPropagator) Inject(_ context.Context, _ http.Header) error {
	return nil
}

func (f *MockFailingContextPropagator) Extract(ctx context.Context, _ http.Header) (context.Context, error) {
	return ctx, f.err
}

type MockPanicContextPropagator struct{}

func (MockPanicContextPropagator) Inject(_ context.Context, _ http.Header) error {
	return nil
}

func (MockPanicContextPropagator) Extract(context.Context, http.Header) (context.Context, error) {
	panic("context propagation panic")
}

func MockClusterEnsureGrainSystem(t *testing.T, grain Grain, name string) (*actorSystem, *mockcluster.Cluster, *GrainIdentity) {
	t.Helper()

	clusterMock := mockcluster.NewCluster(t)
	remotingMock := mocksremote.NewRemoting(t)
	node := &discovery.Node{
		Host:          "127.0.0.1",
		PeersPort:     14000,
		RemotingPort:  15000,
		DiscoveryPort: 0,
	}
	sys := MockSimpleClusterReadyActorSystem(remotingMock, clusterMock, node)
	sys.registry.Register(grain)

	return sys, clusterMock, newGrainIdentity(grain, name)
}

func seedInactiveGrainPID(sys *actorSystem, id *GrainIdentity, grain Grain, config *grainConfig) *grainPID {
	if config == nil {
		config = newGrainConfig()
	}
	pid := newGrainPID(id, grain, sys, config)
	sys.grains.Set(id.String(), pid)
	return pid
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

func startConsulAgent(t *testing.T) *consulcontainer.ConsulContainer {
	t.Helper()
	container, err := consulcontainer.Run(t.Context(), "hashicorp/consul:1.15")
	require.NoError(t, err)
	pause.For(1 * time.Second)
	t.Cleanup(func() {
		err := container.Terminate(context.Background())
		require.NoError(t, err)
	})
	return container
}

func startEtcdCluster(t *testing.T) *etcdContainer.EtcdContainer {
	t.Helper()
	etcdContainer, err := etcdContainer.Run(
		t.Context(),
		"gcr.io/etcd-development/etcd:v3.5.14",
		etcdContainer.WithNodes("etcd-1", "etcd-2", "etcd-3"),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		err := testcontainers.TerminateContainer(etcdContainer)
		require.NoError(t, err)
	})
	return etcdContainer
}

type testClusterConfig struct {
	tlsEnabled        bool
	serverTLS         *cryptotls.Config
	clientTLS         *cryptotls.Config
	pubsubEnabled     bool
	relocationEnabled bool
	extension         extension.Extension
	dependency        extension.Dependency
	compression       remote.Compression
	roles             []string
}

type testClusterOption func(*testClusterConfig)

func withTestTLS(serverTLS, clientTLS *cryptotls.Config) testClusterOption {
	return func(tc *testClusterConfig) {
		tc.tlsEnabled = true
		tc.serverTLS = serverTLS
		tc.clientTLS = clientTLS
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

func withMockCompression(c remote.Compression) testClusterOption {
	return func(tcc *testClusterConfig) {
		tcc.compression = c
	}
}

func withMockRoles(roles ...string) testClusterOption {
	return func(tcc *testClusterConfig) {
		tcc.roles = roles
	}
}

func createNATsProvider(serverAddr string) providerFactory {
	return func(_ *testing.T, host string, discoveryPort int) discovery.Provider {
		natsSubject := "some-subject"
		config := nats.Config{
			NatsServer:    fmt.Sprintf("nats://%s", serverAddr),
			NatsSubject:   natsSubject,
			Host:          host,
			DiscoveryPort: discoveryPort,
		}
		return nats.NewDiscovery(&config, nats.WithLogger(log.DiscardLogger))
	}
}

func createConsulProvider(agentEndpoint string) providerFactory {
	return func(t *testing.T, host string, discoveryPort int) discovery.Provider {
		config := &consul.Config{
			Address:         agentEndpoint,
			Timeout:         10 * time.Second,
			ActorSystemName: "accountsSystem",
			Host:            host,
			DiscoveryPort:   discoveryPort,
			Context:         t.Context(),
			HealthCheck: &consul.HealthCheck{
				Interval: 10 * time.Second,
				Timeout:  5 * time.Second,
			},
			QueryOptions: &consul.QueryOptions{
				OnlyPassing: false,
				AllowStale:  false,
				WaitTime:    5 * time.Second,
			},
		}
		return consul.NewDiscovery(config)
	}
}

func createEtcdProvider(serverAddr string) providerFactory {
	return func(t *testing.T, host string, discoveryPort int) discovery.Provider {
		config := &etcd.Config{
			Endpoints:       []string{serverAddr},
			Timeout:         10 * time.Second,
			ActorSystemName: "accountsSystem",
			Host:            host,
			DiscoveryPort:   discoveryPort,
			Context:         t.Context(),
			TTL:             60,
			DialTimeout:     5 * time.Second,
		}
		return etcd.NewDiscovery(config)
	}
}

func testSystem(t *testing.T, providerFactory providerFactory, opts ...testClusterOption) (ActorSystem, discovery.Provider) {
	ctx := context.TODO()
	logger := log.DiscardLogger

	// dynamic ports
	ports := dynaport.Get(3)
	discoveryPort := ports[0]
	remotingPort := ports[1]
	peersPort := ports[2]

	host := "127.0.0.1"
	actorSystemName := "accountsSystem"

	// provider
	provider := providerFactory(t, host, discoveryPort)

	cfg := &testClusterConfig{relocationEnabled: true}
	for _, opt := range opts {
		opt(cfg)
	}

	// base options
	options := []Option{
		WithLogger(logger),
		WithShutdownTimeout(3 * time.Minute),
		WithCluster(
			NewClusterConfig().
				WithKinds(new(MockActor), new(MockEntity), new(MockGrainActor)).
				WithGrains(new(MockGrain)).
				WithPartitionCount(7).
				WithReplicaCount(1).
				WithPeersPort(peersPort).
				WithMinimumPeersQuorum(1).
				WithDiscoveryPort(discoveryPort).
				WithBootstrapTimeout(time.Second).
				WithClusterStateSyncInterval(300 * time.Millisecond).
				WithClusterBalancerInterval(100 * time.Millisecond).
				WithRoles(cfg.roles...).
				WithDiscovery(provider)),
	}

	if cfg.pubsubEnabled {
		options = append(options, WithPubSub())
	}

	if cfg.tlsEnabled {
		options = append(options, WithTLS(&tls.Info{
			ClientConfig: cfg.clientTLS,
			ServerConfig: cfg.serverTLS,
		}))
	}

	if !cfg.relocationEnabled {
		options = append(options, WithoutRelocation())
	}

	if cfg.extension != nil {
		options = append(options, WithExtensions(cfg.extension))
	}

	options = append(options, WithRemote(remote.NewConfig(host, remotingPort, remote.WithCompression(cfg.compression))))

	system, err := NewActorSystem(actorSystemName, options...)
	require.NotNil(t, system)
	require.NoError(t, err)

	if cfg.dependency != nil {
		require.NoError(t, system.Inject(cfg.dependency))
	}

	require.NoError(t, system.Start(ctx))
	return system, provider
}

func testNATs(t *testing.T, serverAddr string, opts ...testClusterOption) (ActorSystem, discovery.Provider) {
	return testSystem(t, createNATsProvider(serverAddr), opts...)
}

func testConsul(t *testing.T, agentEndpoint string, opts ...testClusterOption) (ActorSystem, discovery.Provider) {
	return testSystem(t, createConsulProvider(agentEndpoint), opts...)
}

func testEtcd(t *testing.T, serverAddr string, opts ...testClusterOption) (ActorSystem, discovery.Provider) {
	return testSystem(t, createEtcdProvider(serverAddr), opts...)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type MockMeterProvider struct {
	otelmetric.MeterProvider
	meter otelmetric.Meter
}

func (x *MockMeterProvider) Meter(name string, opts ...otelmetric.MeterOption) otelmetric.Meter {
	if x.meter != nil {
		return x.meter
	}
	return x.MeterProvider.Meter(name, opts...)
}

type recordingPeerStateStore struct {
	err      error
	called   bool
	lastPeer *internalpb.PeerState
}

func (s *recordingPeerStateStore) PersistPeerState(_ context.Context, peer *internalpb.PeerState) error {
	s.called = true
	s.lastPeer = peer
	return s.err
}

func (s *recordingPeerStateStore) GetPeerState(_ context.Context, _ string) (*internalpb.PeerState, bool) {
	return nil, false
}

func (s *recordingPeerStateStore) DeletePeerState(_ context.Context, _ string) error {
	return nil
}

func (s *recordingPeerStateStore) Close() error {
	return nil
}

// MockControlPlane implements datacenter.ControlPlane for spawnOnDatacenter tests.
type MockControlPlane struct {
	listActive func(context.Context) ([]datacenter.DataCenterRecord, error)
}

func (*MockControlPlane) Register(_ context.Context, record datacenter.DataCenterRecord) (string, uint64, error) {
	return record.ID, 1, nil
}

func (*MockControlPlane) Heartbeat(_ context.Context, _ string, version uint64) (uint64, time.Time, error) {
	return version + 1, time.Now().Add(time.Hour), nil
}

func (*MockControlPlane) SetState(_ context.Context, _ string, _ datacenter.DataCenterState, version uint64) (uint64, error) {
	return version + 1, nil
}

func (x *MockControlPlane) ListActive(ctx context.Context) ([]datacenter.DataCenterRecord, error) {
	if x.listActive != nil {
		return x.listActive(ctx)
	}
	return nil, nil
}

func (*MockControlPlane) Watch(_ context.Context) (<-chan datacenter.ControlPlaneEvent, error) {
	return nil, gerrors.ErrWatchNotSupported
}

func (*MockControlPlane) Deregister(_ context.Context, _ string) error {
	return nil
}

// MockDatacenterSystem returns an *actorSystem configured for datacenter tests.
// The controller is started with the fake control plane; listActive is used to drive ActiveRecords().
func MockDatacenterSystem(t *testing.T, listActive func(_ context.Context) ([]datacenter.DataCenterRecord, error), remoting *mocksremote.Remoting) *actorSystem {
	t.Helper()
	dcConfig := datacenter.NewConfig()
	dcConfig.ControlPlane = &MockControlPlane{listActive: listActive}
	dcConfig.DataCenter = datacenter.DataCenter{Name: "local", Region: "r", Zone: "z"}
	endpoints := []string{"127.0.0.1:8080"}
	dcConfig.MaxCacheStaleness = 50 * time.Millisecond
	dcConfig.CacheRefreshInterval = 10 * time.Millisecond

	controller, err := datacentercontroller.NewController(dcConfig, endpoints)
	require.NoError(t, err)
	startCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	err = controller.Start(startCtx)
	cancel()
	require.NoError(t, err)
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
		_ = controller.Stop(stopCtx)
		stopCancel()
	})

	// Allow first cache refresh so ActiveRecords() returns non-stale data
	pause.For(25 * time.Millisecond)

	clusterMock := mockcluster.NewCluster(t)
	sys := MockReplicationTestSystem(clusterMock)
	sys.remoting = remoting
	sys.remotingEnabled.Store(true)
	sys.clusterConfig = NewClusterConfig().WithDataCenter(dcConfig)
	sys.dataCenterController = controller

	return sys
}
