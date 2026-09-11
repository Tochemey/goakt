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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	syncatomic "sync/atomic"
	"time"

	"github.com/tochemey/goakt/v4/datacenter"
	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/cluster"
	"github.com/tochemey/goakt/v4/internal/commands"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/remoteclient"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/passivation"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
	noopmetric "go.opentelemetry.io/otel/metric/noop"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/types/known/structpb"
)

var (
	_ Actor                = (*MockActor)(nil)
	_ Actor                = (*MockSupervisor)(nil)
	_ Actor                = (*MockSupervised)(nil)
	_ Actor                = (*MockBehaviorActor)(nil)
	_ Actor                = (*MockExchanger)(nil)
	_ Actor                = (*MockStashingActor)(nil)
	_ Actor                = (*MockStashAskActor)(nil)
	_ Actor                = (*MockStashAskAllActor)(nil)
	_ Actor                = (*MockPreStartFailingActor)(nil)
	_ Actor                = (*MockPostStopFailingActor)(nil)
	_ Actor                = (*MockRestartFailingActor)(nil)
	_ Actor                = (*MockPostStartCountingActor)(nil)
	_ Actor                = (*MockUnhandledActor)(nil)
	_ Actor                = (*MockPersistentActor)(nil)
	_ Actor                = (*MockStopOnPanicSupervisor)(nil)
	_ Actor                = (*MockReinstatingSupervisor)(nil)
	_ Actor                = (*MockPingActor)(nil)
	_ Actor                = (*MockSubscriber)(nil)
	_ Actor                = (*MockInstanceCountingActor)(nil)
	_ Actor                = (*MockMismatchedReplyActor)(nil)
	_ Actor                = (*MockRouter)(nil)
	_ Actor                = (*MockRoutee)(nil)
	_ Actor                = (*MockBlockingRoutee)(nil)
	_ Actor                = (*MockSummingRoutee)(nil)
	_ Actor                = (*MockTailChopRoutee)(nil)
	_ Actor                = (*MockFaultyRoutee)(nil)
	_ Grain                = (*MockGrain)(nil)
	_ Grain                = (*MockActivationFailingGrain)(nil)
	_ Grain                = (*MockDeactivationFailingGrain)(nil)
	_ Grain                = (*MockReceiveFailingGrain)(nil)
	_ Grain                = (*MockPanickingGrain)(nil)
	_ Grain                = (*MockPersistentGrain)(nil)
	_ Grain                = (*MockContextReleasingGrain)(nil)
	_ ShutdownHook         = (*MockShutdownHook)(nil)
	_ MockStateStore       = (*MockExtension)(nil)
	_ extension.Dependency = (*MockDependency)(nil)
	_ Mailbox              = (*MockFailingMailbox)(nil)
	_ passivation.Strategy = (*MockPassivationStrategy)(nil)
	_ Actor                = (*MockTerminatedProbeActor)(nil)
	_ Grain                = (*MockScriptedGrain)(nil)
	_ Grain                = (*MockEnvelopeReplyingGrain)(nil)
	_ Grain                = (*MockEnvelopeDeferringGrain)(nil)
	_ Grain                = (*MockReactivationGrain)(nil)
	_ Grain                = (*MockReentrantRecordingGrain)(nil)
	_ Grain                = (*MockDeactivationCountingGrain)(nil)
	_ Grain                = (*MockTimerProbeGrain)(nil)
	_ grainTimerSink       = (*MockTimerSink)(nil)
	_ Actor                = (*MockRestartMarkerActor)(nil)
	_ Actor                = (*MockMailboxBlockingActor)(nil)
)

// postStartCount counts the PostStart messages observed by MockPostStartCountingActor.
var postStartCount = atomic.NewInt32(0)

// activationProbePtr holds the probe MockActivationProbeGrain reports its activations to.
var activationProbePtr syncatomic.Pointer[activationProbe]

// MockNoopActor provides no-op PreStart and PostStop hooks for mocks that only define Receive.
type MockNoopActor struct{}

// PreStart does nothing.
func (MockNoopActor) PreStart(*Context) error {
	return nil
}

// PostStop does nothing.
func (MockNoopActor) PostStop(*Context) error {
	return nil
}

// MockNoopGrain provides no-op OnActivate and OnDeactivate hooks for mocks that only define OnReceive.
type MockNoopGrain struct{}

// OnActivate does nothing.
func (MockNoopGrain) OnActivate(context.Context, *GrainProps) error {
	return nil
}

// OnDeactivate does nothing.
func (MockNoopGrain) OnDeactivate(context.Context, *GrainProps) error {
	return nil
}

// MockActor is an actor that exercises the common message flows: reply, panic, timeout and unhandled.
type MockActor struct {
	MockNoopActor
}

// NewMockActor returns a MockActor.
func NewMockActor() *MockActor {
	return &MockActor{}
}

// Receive replies to TestReply, panics on TestPanic and stalls on TestTimeout.
func (x *MockActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *PostStart:
		ctx.Logger().Info("MockActor started")
	case *testpb.TestSend:
	case *testpb.TestPanic:
		panic("Boom")
	case *testpb.TestReply:
		ctx.Response(testpb.Reply_builder{Content: "received message"}.Build())
	case *testpb.TestTimeout:
		wg := sync.WaitGroup{}
		wg.Go(func() {
			pause.For(receivingDelay)
		})
		wg.Wait()
	default:
		ctx.Unhandled()
	}
}

// MockSupervisor is an actor that watches another actor and reacts to its termination.
type MockSupervisor struct {
	MockNoopActor
}

// NewMockSupervisor returns a MockSupervisor.
func NewMockSupervisor() *MockSupervisor {
	return &MockSupervisor{}
}

// Receive accepts PostStart, TestSend and Terminated, and panics on anything else.
func (x *MockSupervisor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *PostStart:
	case *testpb.TestSend:
	case *Terminated:
	default:
		panic(gerrors.ErrUnhandled)
	}
}

// MockSupervised is an actor that is supervised and panics on demand.
type MockSupervised struct {
	MockNoopActor
}

// NewMockSupervised returns a MockSupervised.
func NewMockSupervised() *MockSupervised {
	return &MockSupervised{}
}

// Receive replies to TestReply, panics with a string on TestPanic and with an error on TestPanicError.
func (x *MockSupervised) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *PostStart:
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

// MockBehaviorActor is an actor that swaps behaviors as it moves through login and account messages.
type MockBehaviorActor struct {
	MockNoopActor
}

// Receive becomes Authenticated on TestLogin and stacks CreditAccount on CreateAccount.
func (x *MockBehaviorActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *PostStart:
	case *testpb.TestLogin:
		ctx.Response(new(testpb.TestLoginSuccess))
		ctx.Become(x.Authenticated)
	case *testpb.CreateAccount:
		ctx.Response(new(testpb.AccountCreated))
		ctx.BecomeStacked(x.CreditAccount)
	}
}

// Authenticated replies to TestReadiness and reverts to the previous behavior.
func (x *MockBehaviorActor) Authenticated(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestReadiness:
		ctx.Response(new(testpb.TestReady))
		ctx.UnBecome()
	}
}

// CreditAccount replies to CreditAccount, stacks DebitAccount and shuts down on TestBye.
func (x *MockBehaviorActor) CreditAccount(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.CreditAccount:
		ctx.Response(new(testpb.AccountCredited))
		ctx.BecomeStacked(x.DebitAccount)
	case *testpb.TestBye:
		_ = ctx.Self().Shutdown(ctx.Context())
	}
}

// DebitAccount replies to DebitAccount and pops itself off the behavior stack.
func (x *MockBehaviorActor) DebitAccount(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.DebitAccount:
		ctx.Response(new(testpb.AccountDebited))
		ctx.UnBecomeStacked()
	}
}

// MockExchanger is an actor that echoes messages back to their sender.
type MockExchanger struct {
	MockNoopActor

	// id is the actor identity captured at PostStart.
	id string
}

// NewMockExchanger returns a MockExchanger.
func NewMockExchanger() *MockExchanger {
	return &MockExchanger{}
}

// Receive echoes TestSend and TestRemoteSend to the sender, replies to TestReply and shuts down on TestBye.
func (x *MockExchanger) Receive(ctx *ReceiveContext) {
	message := ctx.Message()
	switch message.(type) {
	case *PostStart:
		x.id = ctx.Self().ID()
	case *testpb.TestSend:
		ctx.Tell(ctx.Sender(), new(testpb.TestSend))
	case *testpb.TaskComplete:
	case *testpb.TestReply:
		ctx.Response(new(testpb.Reply))
	case *testpb.TestRemoteSend:
		ctx.Tell(ctx.Sender(), new(testpb.TestBye))
	case *testpb.TestBye:
		ctx.Shutdown()
	}
}

// MockStashingActor is an actor that buffers messages until it is told to release them.
type MockStashingActor struct {
	MockNoopActor
}

// Receive stashes TestStash and switches to Ready, and shuts down on TestBye.
func (x *MockStashingActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *PostStart:
	case *testpb.TestStash:
		ctx.Become(x.Ready)
		ctx.Stash()
	case *testpb.TestLogin:
	case *testpb.TestBye:
		ctx.Shutdown()
	}
}

// Ready stashes TestLogin and drains the buffer on TestUnstashAll or TestUnstash.
func (x *MockStashingActor) Ready(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *PostStart:
	case *testpb.TestStash:
	case *testpb.TestLogin:
		ctx.Stash()
	case *testpb.TestSend:
	case *testpb.TestUnstashAll:
		ctx.UnBecome()
		ctx.UnstashAll()
	case *testpb.TestUnstash:
		ctx.Unstash()
	}
}

// MockStashAskActor is an actor that stashes every Ask once and answers it after Unstash releases it.
// Each stash is paired with a self-sent TestUnstash that grants one reply slot.
type MockStashAskActor struct {
	MockNoopActor

	// repliesOwed is the number of released commands still waiting for a reply.
	repliesOwed int
}

// Receive answers TestCount while a reply slot is owed, and otherwise stashes it and self-sends TestUnstash.
func (x *MockStashAskActor) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *testpb.TestCount:
		if x.repliesOwed > 0 {
			x.repliesOwed--
			ctx.Response(testpb.TestCount_builder{Value: msg.GetValue()}.Build())
			return
		}

		ctx.Stash()
		ctx.Tell(ctx.Self(), new(testpb.TestUnstash))

	case *testpb.TestUnstash:
		x.repliesOwed++
		ctx.Unstash()
	}
}

// MockStashAskAllActor is an actor that stashes every Ask and releases the whole buffer on a self-sent TestUnstashAll.
// The release grants one reply slot per stashed command, so every Ask is answered exactly once.
type MockStashAskAllActor struct {
	MockNoopActor

	// repliesOwed is the number of released commands still waiting for a reply.
	repliesOwed int
}

// Receive answers TestCount while a reply slot is owed, and otherwise stashes it and self-sends TestUnstashAll.
func (x *MockStashAskAllActor) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *testpb.TestCount:
		if x.repliesOwed > 0 {
			x.repliesOwed--
			ctx.Response(testpb.TestCount_builder{Value: msg.GetValue()}.Build())
			return
		}

		ctx.Stash()
		ctx.Tell(ctx.Self(), new(testpb.TestUnstashAll))

	case *testpb.TestUnstashAll:
		x.repliesOwed += int(ctx.Self().StashSize())
		ctx.UnstashAll()
	}
}

// MockPreStartFailingActor is an actor that never starts.
type MockPreStartFailingActor struct {
	MockNoopActor
}

// PreStart fails on every call.
func (x *MockPreStartFailingActor) PreStart(*Context) error {
	return errors.New("failed")
}

// Receive ignores every message.
func (x *MockPreStartFailingActor) Receive(*ReceiveContext) {}

// MockPostStopFailingActor is an actor that fails while stopping.
type MockPostStopFailingActor struct {
	MockNoopActor
}

// Receive panics on TestPanic and ignores every other message.
func (x *MockPostStopFailingActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *PostStart:
	case *testpb.TestSend:
	case *testpb.TestPanic:
		panic("panicked")
	}
}

// PostStop fails on every call.
func (x *MockPostStopFailingActor) PostStop(*Context) error {
	return errors.New("failed")
}

// MockRestartFailingActor is an actor that starts once and fails every restart.
type MockRestartFailingActor struct {
	MockNoopActor

	// starts counts the PreStart calls made on this instance.
	starts *atomic.Int64
}

// NewMockRestartFailingActor returns a MockRestartFailingActor with a zero start count.
func NewMockRestartFailingActor() *MockRestartFailingActor {
	return &MockRestartFailingActor{starts: atomic.NewInt64(0)}
}

// PreStart succeeds on the first call and fails on every later one.
func (x *MockRestartFailingActor) PreStart(*Context) error {
	x.starts.Inc()

	if x.starts.Load() > 1 {
		return errors.New("cannot restart")
	}

	return nil
}

// Receive ignores every message.
func (x *MockRestartFailingActor) Receive(*ReceiveContext) {}

// MockPostStartCountingActor is an actor that records how often PostStart is delivered.
type MockPostStartCountingActor struct {
	MockNoopActor
}

// NewMockPostStartCountingActor returns a MockPostStartCountingActor.
func NewMockPostStartCountingActor() *MockPostStartCountingActor {
	return &MockPostStartCountingActor{}
}

// Receive increments postStartCount on PostStart and marks anything else unhandled.
func (x *MockPostStartCountingActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *PostStart:
		postStartCount.Inc()
	default:
		ctx.Unhandled()
	}
}

// MockForwardingActor is an actor that forwards messages to a local or a remote target.
type MockForwardingActor struct {
	MockNoopActor

	// actorRef is the local forwarding target.
	actorRef *PID
	// remoteRef is the remote forwarding target.
	remoteRef *PID
}

// Receive forwards TestBye to actorRef and TestRemoteForward to remoteRef.
func (x *MockForwardingActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *PostStart:
	case *testpb.TestBye:
		ctx.Forward(x.actorRef)
	case *testpb.TestRemoteForward:
		ctx.Forward(x.remoteRef)
	}
}

// MockForwardTargetActor is an actor that stops as soon as a forwarded message reaches it.
type MockForwardTargetActor struct {
	MockNoopActor
}

// Receive shuts the actor down on TestRemoteForward.
func (x *MockForwardTargetActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestRemoteForward:
		ctx.Shutdown()
	}
}

// MockUnhandledActor is an actor that routes everything but PostStart to the deadletters.
type MockUnhandledActor struct {
	MockNoopActor
}

// Receive marks every message other than PostStart as unhandled.
func (x *MockUnhandledActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *PostStart:
	default:
		ctx.Unhandled()
	}
}

// MockPersistentActor is an actor that keeps its account state in the MockStateStore extension across restarts.
type MockPersistentActor struct {
	// persistenceID is the key the account state is stored under.
	persistenceID string
	// currentState holds the state recovered at start and updated by every command.
	currentState *atomic.Pointer[testpb.Account]
	// stateStore is the extension the state is read from and written to.
	stateStore MockStateStore
}

// NewMockPersistentActor returns a MockPersistentActor.
func NewMockPersistentActor() *MockPersistentActor {
	return &MockPersistentActor{}
}

// PreStart resolves the state store extension and recovers the account state.
func (x *MockPersistentActor) PreStart(ctx *Context) error {
	x.currentState = atomic.NewPointer(new(testpb.Account))
	x.stateStore = ctx.Extension("MockStateStore").(MockStateStore)
	x.persistenceID = ctx.ActorName()
	return x.recoverFromStore()
}

// PostStop writes the current account state back to the store.
func (x *MockPersistentActor) PostStop(*Context) error {
	return x.stateStore.WriteState(x.persistenceID, x.currentState.Load())
}

// Receive applies CreateAccount and CreditAccount to the balance, persists the result and replies with the new state.
func (x *MockPersistentActor) Receive(ctx *ReceiveContext) {
	switch received := ctx.Message().(type) {
	case *PostStart:
	case *testpb.CreateAccount:
		balance := received.GetAccountBalance()
		newBalance := x.currentState.Load().GetAccountBalance() + balance
		x.currentState.Store(testpb.Account_builder{
			AccountId:      x.persistenceID,
			AccountBalance: newBalance,
		}.Build())

		if err := x.stateStore.WriteState(x.persistenceID, x.currentState.Load()); err != nil {
			ctx.Err(err)
			return
		}

		ctx.Response(x.currentState.Load())
	case *testpb.CreditAccount:
		balance := received.GetBalance()
		newBalance := x.currentState.Load().GetAccountBalance() + balance
		x.currentState.Store(testpb.Account_builder{
			AccountId:      x.persistenceID,
			AccountBalance: newBalance,
		}.Build())

		if err := x.stateStore.WriteState(x.persistenceID, x.currentState.Load()); err != nil {
			ctx.Err(err)
			return
		}

		ctx.Response(x.currentState.Load())
	case *testpb.GetAccount:
		ctx.Response(x.currentState.Load())
	default:
		ctx.Unhandled()
	}
}

// recoverFromStore loads the last persisted account state, when there is one.
func (x *MockPersistentActor) recoverFromStore() error {
	latestState, err := x.stateStore.GetLatestState(x.persistenceID)
	if err != nil {
		return fmt.Errorf("failed to get the latest state: %w", err)
	}

	if latestState != nil {
		x.currentState.Store(latestState)
	}

	return nil
}

// MockStopOnPanicSupervisor is a supervisor that stops any child reporting a panic.
type MockStopOnPanicSupervisor struct {
	MockNoopActor
}

// NewMockStopOnPanicSupervisor returns a MockStopOnPanicSupervisor.
func NewMockStopOnPanicSupervisor() *MockStopOnPanicSupervisor {
	return &MockStopOnPanicSupervisor{}
}

// Receive stops the sender on PanicSignal and marks anything else unhandled.
func (x *MockStopOnPanicSupervisor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *PostStart:
	case *PanicSignal:
		ctx.Stop(ctx.Sender())
	default:
		ctx.Unhandled()
	}
}

// MockReinstatingSupervisor is a supervisor that reinstates children named reinstate or reinstateNamed.
type MockReinstatingSupervisor struct {
	MockNoopActor
}

// NewMockReinstatingSupervisor returns a MockReinstatingSupervisor.
func NewMockReinstatingSupervisor() *MockReinstatingSupervisor {
	return &MockReinstatingSupervisor{}
}

// Receive reinstates the sender by reference or by name according to its name, and stops it otherwise.
func (x *MockReinstatingSupervisor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *PostStart:
	case *PanicSignal:
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

// MockReinstateRaceActor is an actor that counts its own PostStop calls so a test can detect a double stop.
type MockReinstateRaceActor struct {
	MockNoopActor

	// postStopCount counts the PostStop calls; instances share it through the pointer.
	postStopCount *atomic.Int32
}

// Receive ignores every message.
func (x *MockReinstateRaceActor) Receive(*ReceiveContext) {}

// PostStop increments postStopCount when one is set.
func (x *MockReinstateRaceActor) PostStop(*Context) error {
	if x.postStopCount != nil {
		x.postStopCount.Inc()
	}
	return nil
}

// MockOrderRecordingActor is an actor that records the order in which TestCount values arrive.
// Done is closed once the expected number of values has been recorded.
type MockOrderRecordingActor struct {
	MockNoopActor

	// expected is the number of values awaited before done is closed.
	expected int
	// seen records the received values in arrival order.
	seen []int32
	// done is closed once expected values have been recorded.
	done chan struct{}
	once sync.Once
}

// NewMockOrderRecordingActor returns a MockOrderRecordingActor that closes Done after expected messages.
func NewMockOrderRecordingActor(expected int) *MockOrderRecordingActor {
	return &MockOrderRecordingActor{
		expected: expected,
		done:     make(chan struct{}),
	}
}

// Receive appends the TestCount value and closes done once the expected count is reached.
func (x *MockOrderRecordingActor) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *testpb.TestCount:
		x.seen = append(x.seen, msg.GetValue())
		if len(x.seen) == x.expected {
			x.once.Do(func() { close(x.done) })
		}
	default:
		ctx.Unhandled()
	}
}

// Seen returns a copy of the recorded values in arrival order.
func (x *MockOrderRecordingActor) Seen() []int32 {
	copySeen := make([]int32, len(x.seen))
	copy(copySeen, x.seen)
	return copySeen
}

// Done returns the channel closed once the expected number of values has arrived.
func (x *MockOrderRecordingActor) Done() <-chan struct{} {
	return x.done
}

// MockPingActor is an actor that answers TestPing with TestPong.
type MockPingActor struct {
	MockNoopActor
}

// NewMockPingActor returns a MockPingActor.
func NewMockPingActor() *MockPingActor {
	return &MockPingActor{}
}

// Receive answers TestPing with TestPong, shuts down on TestBye and marks anything else unhandled.
func (x *MockPingActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *PostStart:
	case *testpb.TestSend:
	case *testpb.TestPing:
		ctx.Response(new(testpb.TestPong))
	case *testpb.TestBye:
		ctx.Shutdown()
	default:
		ctx.Unhandled()
	}
}

// MockUnimplementedActor deliberately does not implement the Actor interface to exercise reflection error paths.
type MockUnimplementedActor struct{}

// MockSubscriber is an actor that counts topic acknowledgements and published messages.
type MockSubscriber struct {
	MockNoopActor

	// counter rises on SubscribeAck and TestCount and falls on UnsubscribeAck.
	counter *atomic.Int64
}

// NewMockSubscriber returns a MockSubscriber with a zero counter.
func NewMockSubscriber() *MockSubscriber {
	return &MockSubscriber{
		counter: atomic.NewInt64(0),
	}
}

// Receive raises the counter on SubscribeAck and TestCount and lowers it on UnsubscribeAck.
func (x *MockSubscriber) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *SubscribeAck:
		x.counter.Inc()
	case *testpb.TestCount:
		x.counter.Inc()
	case *UnsubscribeAck:
		x.counter.Dec()
	}
}

// MockInstanceCountingActor is an actor that counts the instances that reach PreStart.
// Instances share the count through the pointer.
type MockInstanceCountingActor struct {
	MockNoopActor

	// starts counts the instances that reached PreStart.
	starts *atomic.Int64
}

// PreStart increments the shared start count.
func (x *MockInstanceCountingActor) PreStart(*Context) error {
	x.starts.Add(1)
	return nil
}

// Receive ignores every message.
func (x *MockInstanceCountingActor) Receive(*ReceiveContext) {}

// MockMismatchedReplyActor is an actor that answers every request with the wrong response type.
type MockMismatchedReplyActor struct {
	MockNoopActor
}

// Receive answers every message other than PostStart with a testpb.Reply.
func (x *MockMismatchedReplyActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *PostStart:
	default:
		ctx.Response(new(testpb.Reply))
	}
}

// MockContextEchoActor is an actor that records the context value under key and echoes it in its reply.
type MockContextEchoActor struct {
	MockNoopActor

	// key is the context key whose value is recorded.
	key any
	mu  sync.RWMutex
	// seen holds the value observed on the most recent message.
	seen any
}

// Receive records the context value under key and echoes it on TestReply.
func (x *MockContextEchoActor) Receive(ctx *ReceiveContext) {
	x.setSeen(ctx.Context().Value(x.key))

	switch ctx.Message().(type) {
	case *testpb.TestReply:
		ctx.Response(testpb.Reply_builder{Content: fmt.Sprint(x.Seen())}.Build())
	case *testpb.TestSend:
	default:
	}
}

// setSeen stores the value observed on the last message.
func (x *MockContextEchoActor) setSeen(val any) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.seen = val
}

// Seen returns the value observed on the last message.
func (x *MockContextEchoActor) Seen() any {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.seen
}

// MockContextRecordingActor is an actor that records the context value carried under key by every message.
// It is used to check per-message context propagation through the coalesced send path.
type MockContextRecordingActor struct {
	MockNoopActor

	// key is the context key whose value is recorded.
	key any
	mu  sync.Mutex
	// seen records one value per message, in arrival order.
	seen []string
}

// Receive appends the string context value under key for every TestSend.
func (x *MockContextRecordingActor) Receive(ctx *ReceiveContext) {
	if _, ok := ctx.Message().(*testpb.TestSend); !ok {
		return
	}
	v := ctx.Context().Value(x.key)
	if s, ok := v.(string); ok {
		x.mu.Lock()
		x.seen = append(x.seen, s)
		x.mu.Unlock()
	}
}

// Count returns the number of recorded values.
func (x *MockContextRecordingActor) Count() int {
	x.mu.Lock()
	defer x.mu.Unlock()
	return len(x.seen)
}

// Seen returns a copy of the recorded values in arrival order.
func (x *MockContextRecordingActor) Seen() []string {
	x.mu.Lock()
	defer x.mu.Unlock()
	out := make([]string, len(x.seen))
	copy(out, x.seen)
	return out
}

// MockTerminatedProbeActor is an actor that publishes every Terminated signal it receives on a channel.
type MockTerminatedProbeActor struct {
	// received carries the observed Terminated signals out to the test.
	received chan *Terminated
}

// NewMockTerminatedProbeActor returns a MockTerminatedProbeActor with a 64-slot signal channel.
func NewMockTerminatedProbeActor() *MockTerminatedProbeActor {
	return &MockTerminatedProbeActor{received: make(chan *Terminated, 64)}
}

// PreStart does nothing.
func (x *MockTerminatedProbeActor) PreStart(*Context) error { return nil }

// Receive publishes every Terminated signal on received and marks anything else unhandled.
func (x *MockTerminatedProbeActor) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *PostStart:
	case *Terminated:
		x.received <- msg
	default:
		ctx.Unhandled()
	}
}

// PostStop does nothing.
func (x *MockTerminatedProbeActor) PostStop(*Context) error { return nil }

// MockCountingActor is an actor that counts the TestSend messages it receives.
// Remote-tell deadletter tests use it to prove valid siblings in a batch are still delivered when others fail.
type MockCountingActor struct {
	mu sync.Mutex
	// count is the number of TestSend messages received.
	count int
	// lastCtx is the context carried by the most recent TestSend.
	lastCtx context.Context
}

// PreStart does nothing.
func (*MockCountingActor) PreStart(*Context) error { return nil }

// PostStop does nothing.
func (*MockCountingActor) PostStop(*Context) error { return nil }

// Count returns the number of TestSend messages received so far.
func (x *MockCountingActor) Count() int {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.count
}

// Receive counts every TestSend and records the context it arrived with.
func (x *MockCountingActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
		x.mu.Lock()
		x.count++
		x.lastCtx = ctx.Context()
		x.mu.Unlock()
	default:
	}
}

// MockNoLogActor is an actor that never logs, so tests capturing log output see no writes from it.
type MockNoLogActor struct{}

// PreStart does nothing.
func (x *MockNoLogActor) PreStart(*Context) error { return nil }

// Receive ignores every message.
func (x *MockNoLogActor) Receive(*ReceiveContext) {}

// PostStop does nothing.
func (x *MockNoLogActor) PostStop(*Context) error { return nil }

// MockPipeTargetActor is an actor that publishes the piped testpb.Reply it receives on a channel.
type MockPipeTargetActor struct {
	// received carries the piped reply out to the test.
	received chan any
}

// NewMockPipeTargetActor returns a MockPipeTargetActor with a one-slot reply channel.
func NewMockPipeTargetActor() *MockPipeTargetActor {
	return &MockPipeTargetActor{
		received: make(chan any, 1),
	}
}

// PreStart does nothing.
func (x *MockPipeTargetActor) PreStart(*Context) error {
	return nil
}

// PostStop does nothing.
func (x *MockPipeTargetActor) PostStop(*Context) error {
	return nil
}

// Receive publishes a testpb.Reply on received when the channel still has room.
func (x *MockPipeTargetActor) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *testpb.Reply:
		select {
		case x.received <- msg:
		default:
		}
	}
}

// MockRestartMarkerActor is an actor that samples the restarting state bit on its own PID when PostStop runs.
// A restart tears the actor down through PostStop, which is where the marker restartSubtree installs is observable.
type MockRestartMarkerActor struct {
	// self is the PID the test hands the actor so PostStop can read its state bits.
	self atomic.Pointer[PID]
	// markedAtStop is the restarting bit sampled by the last PostStop call.
	markedAtStop atomic.Bool
}

// PreStart is a no-op: the actor carries no state to initialize.
func (x *MockRestartMarkerActor) PreStart(*Context) error { return nil }

// Receive ignores every message; the actor exists only for its stop hook.
func (x *MockRestartMarkerActor) Receive(*ReceiveContext) {}

// PostStop samples the restarting bit on the PID the test handed the actor.
func (x *MockRestartMarkerActor) PostStop(*Context) error {
	if pid := x.self.Load(); pid != nil {
		x.markedAtStop.Store(pid.isStateSet(restartingState))
	}
	return nil
}

// MockMailboxBlockingActor is an actor that holds its processing turn on the first user message until released.
// Later messages stay queued behind it, so a test can read the mailbox size at rest instead of racing the dispatcher.
type MockMailboxBlockingActor struct {
	// entered is closed once the actor has parked on its first user message.
	entered chan struct{}
	// release is closed by the test to let the parked turn finish.
	release chan struct{}
	once    sync.Once
}

// NewMockMailboxBlockingActor returns a MockMailboxBlockingActor with both synchronization channels ready.
func NewMockMailboxBlockingActor() *MockMailboxBlockingActor {
	return &MockMailboxBlockingActor{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

// PreStart is a no-op: the actor carries no state to initialize.
func (x *MockMailboxBlockingActor) PreStart(*Context) error { return nil }

// Receive parks the turn on the first user message and lets every later one
// through untouched.
func (x *MockMailboxBlockingActor) Receive(ctx *ReceiveContext) {
	if _, ok := ctx.Message().(*testpb.TestSend); !ok {
		return
	}

	x.once.Do(func() {
		close(x.entered)
		<-x.release
	})
}

// PostStop is a no-op: the actor owns no resource to release.
func (x *MockMailboxBlockingActor) PostStop(*Context) error { return nil }

// MockRecordingErrorSink is an asyncErrorSink that records the errors handed to it and returns a fixed result.
// It is not a process, so it shows the request machinery depends only on the sink contract.
type MockRecordingErrorSink struct {
	// errs carries the recorded errors out to the test.
	errs chan error
	// ret is the error enqueueAsyncError reports on every call.
	ret error
}

// NewMockRecordingErrorSink returns a MockRecordingErrorSink whose enqueueAsyncError reports ret.
func NewMockRecordingErrorSink(ret error) *MockRecordingErrorSink {
	return &MockRecordingErrorSink{errs: make(chan error, 1), ret: ret}
}

// enqueueAsyncError records the error when there is room and reports the configured result.
func (x *MockRecordingErrorSink) enqueueAsyncError(_ context.Context, _ string, err error) error {
	select {
	case x.errs <- err:
	default:
	}
	return x.ret
}

// MockReentrancyActor is an actor that runs a per-test Receive function.
type MockReentrancyActor struct {
	// receive handles every message; nil ignores them all.
	receive func(*ReceiveContext)
}

// PreStart does nothing.
func (x *MockReentrancyActor) PreStart(*Context) error { return nil }

// Receive defers to the receive function when one is set.
func (x *MockReentrancyActor) Receive(ctx *ReceiveContext) {
	if x.receive != nil {
		x.receive(ctx)
	}
}

// PostStop does nothing.
func (x *MockReentrancyActor) PostStop(*Context) error { return nil }

// produceSubmission commands the reliable producer mock to submit one
// application message through its controller.
type produceSubmission struct {
	messageID string
	payload   any
}

// askSubmission is a produce submission sent with Ask. The producer answers
// it from local knowledge only, before any storage or delivery work happens.
type askSubmission struct {
	messageID string
	payload   any
}

// submissionAccepted is the producer's reply to askSubmission: the message
// was accepted into its buffer. It deliberately cannot say anything about
// storage or delivery.
type submissionAccepted struct {
	queued int
}

// MockReliableProducer is a producer endpoint that answers the controller handshake the way an application would.
// It queues submissions, spends one RequestNext grant per submission, resends the same Produced for a retried grant,
// and acknowledges Stored, holding all of that state inside its own mailbox turns.
type MockReliableProducer struct {
	// controller is the reliable controller that issued the last grant.
	controller *PID
	// request is the unspent RequestNext grant, if one is held.
	request *RequestNext
	// pending holds the submissions still waiting for a grant, oldest first.
	pending []*produceSubmission
	// lastToken is the grant token the last Produced was built from.
	lastToken string
	// lastProduced is resent verbatim when the matching grant is retried.
	lastProduced *Produced
}

// PreStart does nothing.
func (x *MockReliableProducer) PreStart(*Context) error { return nil }

// PostStop does nothing.
func (x *MockReliableProducer) PostStop(*Context) error { return nil }

// Receive spends RequestNext grants on queued submissions, acknowledges Stored and queues both submission commands.
func (x *MockReliableProducer) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *PostStart:
	case *RequestNext:
		if !msg.IsAuthorizedFor(ctx.Self(), ctx.Sender()) {
			return
		}

		x.controller = ctx.Sender()

		if msg.Token() == x.lastToken && x.lastProduced != nil {
			ctx.Tell(x.controller, x.lastProduced)
			return
		}

		x.request = msg
		x.flush(ctx)
	case *Stored:
		ack, err := NewStoredAck(msg)
		if err != nil {
			ctx.Err(err)
			return
		}

		ctx.Tell(ctx.Sender(), ack)
	case *produceSubmission:
		x.pending = append(x.pending, msg)
		x.flush(ctx)
	case *askSubmission:
		x.pending = append(x.pending, &produceSubmission{messageID: msg.messageID, payload: msg.payload})
		ctx.Response(&submissionAccepted{queued: len(x.pending)})
		x.flush(ctx)
	default:
		ctx.Unhandled()
	}
}

// flush spends the held grant on the oldest queued submission.
func (x *MockReliableProducer) flush(ctx *ReceiveContext) {
	if x.request == nil || len(x.pending) == 0 {
		return
	}

	submission := x.pending[0]
	produced, err := NewProduced(x.request, submission.messageID, submission.payload)
	if err != nil {
		ctx.Err(err)
		return
	}

	x.pending = x.pending[1:]
	x.lastToken = x.request.Token()
	x.lastProduced = produced
	x.request = nil
	ctx.Tell(x.controller, produced)
}

// getDeliverySenders asks MockSenderRecordingConsumer for the sender of each recorded delivery.
type getDeliverySenders struct{}

// MockSenderRecordingConsumer is a consumer that confirms every delivery and records which PID delivered it.
// Tests use the recorded senders to assert deliveries arrive only from the consumer's own controller.
type MockSenderRecordingConsumer struct {
	// deliveries holds every received delivery in arrival order.
	deliveries []*Delivery
	// senders holds the sender of each recorded delivery, at the same index.
	senders []*PID
}

// PreStart does nothing.
func (x *MockSenderRecordingConsumer) PreStart(*Context) error { return nil }

// PostStop does nothing.
func (x *MockSenderRecordingConsumer) PostStop(*Context) error { return nil }

// Receive records and confirms every delivery and answers the two snapshot queries.
func (x *MockSenderRecordingConsumer) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *PostStart:
	case *Delivery:
		x.deliveries = append(x.deliveries, msg)
		x.senders = append(x.senders, ctx.Sender())

		confirmed, err := NewConfirmed(msg)
		if err != nil {
			ctx.Err(err)
			return
		}

		ctx.Tell(ctx.Sender(), confirmed)
	case *getDeliveries:
		ctx.Response(append([]*Delivery(nil), x.deliveries...))
	case *getDeliverySenders:
		ctx.Response(append([]*PID(nil), x.senders...))
	default:
		ctx.Unhandled()
	}
}

// startCheckout commands the checkout actor to hand one finished order to the
// reliable flow.
type startCheckout struct {
	orderID string
}

// processedNotice is the consumer's business-level notification to the
// checkout actor, sent through ordinary messaging.
type processedNotice struct {
	orderID string
}

// getNotices asks the checkout actor which orders were confirmed processed.
type getNotices struct{}

// MockCheckout is an ordinary actor that feeds the reliable flow the same way it messages any other actor.
// The producer PID is plain constructor state and the handoff is a plain Tell from its own Receive.
type MockCheckout struct {
	// producer is the reliable producer endpoint the orders are handed to.
	producer *PID
	// notices records the orders the consumer reported as processed.
	notices []string
}

// PreStart does nothing.
func (x *MockCheckout) PreStart(*Context) error { return nil }

// PostStop does nothing.
func (x *MockCheckout) PostStop(*Context) error { return nil }

// Receive submits an order on startCheckout, records processedNotice and answers getNotices.
func (x *MockCheckout) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *PostStart:
	case *startCheckout:
		// the payload carries the reply-to actor name: a Delivery's sender is the controller, never the business origin
		ctx.Tell(x.producer, &produceSubmission{
			messageID: msg.orderID,
			payload:   testpb.Reply_builder{Content: ctx.Self().Name()}.Build(),
		})
	case *processedNotice:
		x.notices = append(x.notices, msg.orderID)
	case *getNotices:
		ctx.Response(append([]string(nil), x.notices...))
	default:
		ctx.Unhandled()
	}
}

// MockReplyingConsumer is a consumer that processes each delivery once and notifies the origin before confirming.
// The origin actor is named in the delivery payload and is reached through ordinary messaging.
type MockReplyingConsumer struct {
	// seen marks the message IDs already processed, making redelivery a no-op.
	seen map[string]bool
}

// PreStart creates the empty set of processed message IDs.
func (x *MockReplyingConsumer) PreStart(*Context) error {
	x.seen = make(map[string]bool)
	return nil
}

// PostStop does nothing.
func (x *MockReplyingConsumer) PostStop(*Context) error { return nil }

// Receive notifies the payload's origin actor the first time a delivery is seen, then confirms it.
func (x *MockReplyingConsumer) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *PostStart:
	case *Delivery:
		if !x.seen[msg.MessageID()] {
			reply, ok := msg.Payload().(*testpb.Reply)
			if !ok {
				ctx.Err(fmt.Errorf("unexpected payload type %T", msg.Payload()))
				return
			}

			origin, err := ctx.ActorSystem().ActorOf(ctx.Context(), reply.GetContent())
			if err != nil {
				ctx.Err(err)
				return
			}

			x.seen[msg.MessageID()] = true
			ctx.Tell(origin, &processedNotice{orderID: msg.MessageID()})
		}

		confirmed, err := NewConfirmed(msg)
		if err != nil {
			ctx.Err(err)
			return
		}

		ctx.Tell(ctx.Sender(), confirmed)
	default:
		ctx.Unhandled()
	}
}

// deliveryForward commands a test double to send a message so that the
// double becomes the sender.
type deliveryForward struct {
	to      *PID
	message any
}

// getRecorded asks a test double for a snapshot of its recorded messages.
type getRecorded struct{}

// getDeliveries asks the consumer mock for a snapshot of its deliveries.
type getDeliveries struct{}

// MockDeliveryRecorder is an actor that records every message it receives, forwards commanded sends and answers
// snapshot queries, keeping all of that state inside its own mailbox turns.
type MockDeliveryRecorder struct {
	// messages holds every recorded message in arrival order.
	messages []any
}

// PreStart does nothing.
func (x *MockDeliveryRecorder) PreStart(*Context) error { return nil }

// PostStop does nothing.
func (x *MockDeliveryRecorder) PostStop(*Context) error { return nil }

// Receive sends a deliveryForward, answers getRecorded and records everything else.
func (x *MockDeliveryRecorder) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *PostStart:
	case *deliveryForward:
		ctx.Tell(msg.to, msg.message)
	case *getRecorded:
		ctx.Response(append([]any(nil), x.messages...))
	default:
		x.messages = append(x.messages, msg)
	}
}

// MockReliableConsumer is a consumer endpoint that records deliveries and optionally confirms them immediately.
// Its state is only touched inside its own mailbox turns, so tests read it through getDeliveries snapshots.
type MockReliableConsumer struct {
	// autoConfirm sends a Confirmed for every delivery when set.
	autoConfirm bool
	// deliveries holds every received delivery in arrival order.
	deliveries []*Delivery
}

// PreStart does nothing.
func (x *MockReliableConsumer) PreStart(*Context) error { return nil }

// PostStop does nothing.
func (x *MockReliableConsumer) PostStop(*Context) error { return nil }

// Receive records each delivery and confirms it when autoConfirm is set, sends a deliveryForward and answers
// getDeliveries.
func (x *MockReliableConsumer) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *Delivery:
		x.deliveries = append(x.deliveries, msg)

		if x.autoConfirm {
			confirmed, err := NewConfirmed(msg)
			if err != nil {
				ctx.Err(err)
				return
			}

			ctx.Tell(ctx.Sender(), confirmed)
		}
	case *deliveryForward:
		ctx.Tell(msg.to, msg.message)
	case *getDeliveries:
		ctx.Response(append([]*Delivery(nil), x.deliveries...))
	default:
		ctx.Unhandled()
	}
}

// MockConfirmingReliableProducer is a MockReliableProducer that also captures the DeliveryConfirmed notices it gets.
type MockConfirmingReliableProducer struct {
	MockReliableProducer

	// confirmations holds every captured notice in arrival order.
	confirmations []*DeliveryConfirmed
}

// Receive captures DeliveryConfirmed, answers getConfirmations and defers everything else to the embedded producer.
func (x *MockConfirmingReliableProducer) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *DeliveryConfirmed:
		x.confirmations = append(x.confirmations, msg)
	case *getConfirmations:
		ctx.Response(append([]*DeliveryConfirmed(nil), x.confirmations...))
	default:
		x.MockReliableProducer.Receive(ctx)
	}
}

// getConfirmations asks the producer mock for captured DeliveryConfirmed notices.
type getConfirmations struct{}

// MockSlowReplyActor is an actor that answers TestReply only after a fixed delay.
// The delay is long enough to show the ask server honors a caller deadline beyond the system askTimeout.
type MockSlowReplyActor struct {
	// delay is how long Receive stalls before replying.
	delay time.Duration
}

// PreStart does nothing.
func (x *MockSlowReplyActor) PreStart(*Context) error { return nil }

// PostStop does nothing.
func (x *MockSlowReplyActor) PostStop(*Context) error { return nil }

// Receive waits out the configured delay and then answers TestReply.
func (x *MockSlowReplyActor) Receive(ctx *ReceiveContext) {
	if _, ok := ctx.Message().(*testpb.TestReply); ok {
		pause.For(x.delay)
		ctx.Response(testpb.Reply_builder{Content: "slow reply"}.Build())
	}
}

// MockSerializer is a remote.Serializer that reports the injected error when there is one and the injected message
// otherwise.
type MockSerializer struct {
	// msg is what Deserialize returns when err is nil.
	msg any
	// err is the error both methods report when it is set.
	err error
}

// Serialize fails with the injected error or returns a fixed payload.
func (x *MockSerializer) Serialize(_ any) ([]byte, error) {
	if x.err != nil {
		return nil, x.err
	}
	return []byte("serialized"), nil
}

// Deserialize fails with the injected error or returns the injected message.
func (x *MockSerializer) Deserialize(_ []byte) (any, error) {
	if x.err != nil {
		return nil, x.err
	}
	return x.msg, nil
}

// MockRemoteClient is a remote client that hands out a configured MockSerializer and leaves every other call to the
// embedded nil client.
type MockRemoteClient struct {
	// Client is embedded so the double need not define the whole interface.
	remoteclient.Client

	// serializer is returned for every Serializer call.
	serializer remote.Serializer
}

// Serializer returns the configured serializer.
func (x *MockRemoteClient) Serializer(_ any) remote.Serializer {
	return x.serializer
}

// MockBlockerActor is an actor that parks on a gate for every remote payload so its mailbox accumulates remote tells.
type MockBlockerActor struct {
	// gate is closed by the test to let the parked payloads through.
	gate chan struct{}
	// received counts the messages that made it past the gate.
	received *syncatomic.Int64
}

// PreStart does nothing.
func (x *MockBlockerActor) PreStart(*Context) error { return nil }

// PostStop does nothing.
func (x *MockBlockerActor) PostStop(*Context) error { return nil }

// Receive parks on the gate for every remote payload, simulating a stalled
// consumer, and counts the messages that get through once the gate opens.
func (x *MockBlockerActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *structpb.Value:
		<-x.gate
		x.received.Add(1)
	default:
	}
}

// MockRouter is an actor that counts the routing messages it receives.
type MockRouter struct {
	MockNoopActor

	counter int
	logger  log.Logger
}

// PreStart installs a discard logger.
func (x *MockRouter) PreStart(*Context) error {
	x.logger = log.DiscardLogger
	return nil
}

// Receive counts TestLog and answers TestGetCount with the running count.
func (x *MockRouter) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *testpb.TestLog:
		x.counter++
		x.logger.Infof("Got message: %s", msg.GetText())
	case *testpb.TestGetCount:
		x.counter++
		ctx.Response(testpb.TestCount_builder{Value: int32(x.counter)}.Build())
	default:
		ctx.Unhandled()
	}
}

// MockRoutee is a routee that counts messages and answers TestSum with the sum of its operands.
type MockRoutee struct {
	MockNoopActor

	counter int
	logger  log.Logger
}

// PreStart installs a discard logger.
func (x *MockRoutee) PreStart(*Context) error {
	x.logger = log.DiscardLogger
	return nil
}

// Receive counts TestLog, answers TestGetCount with the running count and answers TestSum after the requested delay.
func (x *MockRoutee) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *testpb.TestLog:
		x.counter++
		x.logger.Infof("Got message: %s", msg.GetText())
	case *testpb.TestGetCount:
		x.counter++
		ctx.Response(testpb.TestCount_builder{Value: int32(x.counter)}.Build())
	case *testpb.TestSum:
		if msg.HasDelay() {
			wg := sync.WaitGroup{}
			wg.Go(func() {
				pause.For(msg.GetDelay().AsDuration())
			})
			wg.Wait()
		}

		sum := msg.GetA() + msg.GetB()
		ctx.Response(testpb.TestSumResult_builder{Result: sum}.Build())
	default:
		ctx.Unhandled()
	}
}

// MockBlockingRoutee is a routee that blocks for ten seconds on TestSum.
type MockBlockingRoutee struct {
	MockNoopActor
}

// Receive blocks for ten seconds on TestSum and marks anything else unhandled.
func (x *MockBlockingRoutee) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSum:
		pause.For(10 * time.Second)
	default:
		ctx.Unhandled()
	}
}

// MockSummingRoutee is a routee that keeps the last sum result and counts the failures reported to it.
type MockSummingRoutee struct {
	MockNoopActor

	sum          int64
	failureCount int32
}

// NewMockSummingRoutee returns a MockSummingRoutee.
func NewMockSummingRoutee() *MockSummingRoutee {
	return &MockSummingRoutee{}
}

// Receive stores TestSumResult, counts StatusFailure and answers the matching query messages.
func (x *MockSummingRoutee) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *testpb.TestSumResult:
		x.sum = msg.GetResult()
	case *testpb.TestGetSumResult:
		ctx.Response(testpb.TestSumResult_builder{Result: x.sum}.Build())
	case *StatusFailure:
		x.failureCount++
	case *testpb.TestGetCount:
		ctx.Response(testpb.TestCount_builder{Value: x.failureCount}.Build())
	}
}

// MockTailChopRoutee is a routee that records sum results and failures and signals every failure on a channel.
type MockTailChopRoutee struct {
	MockNoopActor

	sum      atomic.Int64
	failures atomic.Int32
	// failureNotifs carries one signal per observed failure for WaitForFailure.
	failureNotifs chan struct{}
}

// NewMockTailChopRoutee returns a MockTailChopRoutee with a one-slot failure channel.
func NewMockTailChopRoutee() *MockTailChopRoutee {
	return &MockTailChopRoutee{
		failureNotifs: make(chan struct{}, 1),
	}
}

// Receive stores TestSumResult, counts and signals StatusFailure and answers the matching query messages.
func (x *MockTailChopRoutee) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *testpb.TestSumResult:
		x.sum.Store(msg.GetResult())
	case *StatusFailure:
		x.failures.Inc()
		select {
		case x.failureNotifs <- struct{}{}:
		default:
		}
	case *testpb.TestGetSumResult:
		ctx.Response(testpb.TestSumResult_builder{Result: x.sum.Load()}.Build())
	case *testpb.TestGetCount:
		ctx.Response(testpb.TestCount_builder{Value: x.failures.Load()}.Build())
	}
}

// WaitForFailure reports whether a failure was signalled before the timeout elapsed.
func (x *MockTailChopRoutee) WaitForFailure(timeout time.Duration) bool {
	select {
	case <-x.failureNotifs:
		return true
	case <-time.After(timeout):
		return false
	}
}

// FailureCount returns the number of failures observed.
func (x *MockTailChopRoutee) FailureCount() int32 {
	return x.failures.Load()
}

// Sum returns the last recorded sum result.
func (x *MockTailChopRoutee) Sum() int64 {
	return x.sum.Load()
}

// MockFaultyRoutee is a routee that fails on every message other than PostStart.
type MockFaultyRoutee struct {
	MockNoopActor
}

// Receive reports an error for every message other than PostStart.
func (x *MockFaultyRoutee) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *PostStart:
	default:
		ctx.Err(errors.New("routee failure"))
	}
}

// MockGrain is a grain that exercises replies, actor messaging and grain-to-grain messaging.
type MockGrain struct {
	MockNoopGrain

	// name is the grain identity captured at activation.
	name string
}

// NewMockGrain returns a MockGrain.
func NewMockGrain() *MockGrain {
	return &MockGrain{}
}

// OnActivate records the grain identity name.
func (x *MockGrain) OnActivate(ctx context.Context, props *GrainProps) error {
	x.name = props.Identity().Name()
	return nil
}

// OnReceive answers TestReply and TestPing, relays TestBye to Actor20 and messages Grain2 on TestMessage and TestReady.
func (x *MockGrain) OnReceive(ctx *GrainContext) {
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
		ctx.Response(testpb.Reply_builder{Content: "received message"}.Build())
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

// MockActivationFailingGrain is a grain that never activates.
type MockActivationFailingGrain struct {
	MockNoopGrain
}

// NewMockActivationFailingGrain returns a MockActivationFailingGrain.
func NewMockActivationFailingGrain() *MockActivationFailingGrain {
	return &MockActivationFailingGrain{}
}

// OnActivate fails on every call.
func (x *MockActivationFailingGrain) OnActivate(ctx context.Context, props *GrainProps) error {
	return errors.New("failed to activate grain")
}

// OnReceive succeeds without a reply.
func (x *MockActivationFailingGrain) OnReceive(ctx *GrainContext) {
	ctx.NoErr()
}

// MockDeactivationFailingGrain is a grain that fails while deactivating.
type MockDeactivationFailingGrain struct {
	MockNoopGrain
}

// NewMockDeactivationFailingGrain returns a MockDeactivationFailingGrain.
func NewMockDeactivationFailingGrain() *MockDeactivationFailingGrain {
	return &MockDeactivationFailingGrain{}
}

// OnDeactivate fails on every call.
func (x *MockDeactivationFailingGrain) OnDeactivate(ctx context.Context, props *GrainProps) error {
	return errors.New("failed to deactivate grain")
}

// OnReceive succeeds without a reply.
func (x *MockDeactivationFailingGrain) OnReceive(ctx *GrainContext) {
	ctx.NoErr()
}

// MockReceiveFailingGrain is a grain that fails while handling TestSend.
type MockReceiveFailingGrain struct {
	MockNoopGrain
}

// NewMockReceiveFailingGrain returns a MockReceiveFailingGrain.
func NewMockReceiveFailingGrain() *MockReceiveFailingGrain {
	return &MockReceiveFailingGrain{}
}

// OnReceive fails on TestSend and marks anything else unhandled.
func (x *MockReceiveFailingGrain) OnReceive(ctx *GrainContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
		ctx.Err(errors.New("failed to process message"))
	default:
		ctx.Unhandled()
	}
}

// MockPanickingGrain is a grain that panics while handling a message.
type MockPanickingGrain struct {
	MockNoopGrain
}

// NewMockPanickingGrain returns a MockPanickingGrain.
func NewMockPanickingGrain() *MockPanickingGrain {
	return &MockPanickingGrain{}
}

// OnReceive panics with a string on TestSend and with an internal error on TestReply.
func (x *MockPanickingGrain) OnReceive(ctx *GrainContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
		panic("test panic")
	case *testpb.TestReply:
		panic(gerrors.NewInternalError(errors.New("test panic")))
	}
}

// MockLifecyclePanickingGrain is a grain that panics in its activation and deactivation hooks.
type MockLifecyclePanickingGrain struct {
	// activatePanic is the value OnActivate panics with; nil lets activation succeed.
	activatePanic any
	// deactivatePanic is the value OnDeactivate panics with; nil falls back to a fixed message.
	deactivatePanic any
}

// OnActivate panics with activatePanic, or succeeds when it is nil.
func (x *MockLifecyclePanickingGrain) OnActivate(context.Context, *GrainProps) error {
	if x.activatePanic != nil {
		panic(x.activatePanic)
	}
	return nil
}

// OnDeactivate panics with deactivatePanic, or with a fixed message when it is nil.
func (x *MockLifecyclePanickingGrain) OnDeactivate(context.Context, *GrainProps) error {
	if x.deactivatePanic != nil {
		panic(x.deactivatePanic)
	}
	panic("deactivate panic")
}

// OnReceive ignores every message.
func (x *MockLifecyclePanickingGrain) OnReceive(*GrainContext) {}

// MockPersistentGrain is a grain that keeps its account state in the MockStateStore extension across deactivations.
type MockPersistentGrain struct {
	// persistenceID is the key the account state is stored under.
	persistenceID string
	// currentState holds the state recovered at activation and updated by every command.
	currentState *atomic.Pointer[testpb.Account]
	// stateStore is the extension the state is read from and written to.
	stateStore MockStateStore
}

// NewMockPersistentGrain returns a MockPersistentGrain.
func NewMockPersistentGrain() *MockPersistentGrain {
	return &MockPersistentGrain{}
}

// OnActivate resolves the state store extension and recovers the account state.
func (x *MockPersistentGrain) OnActivate(ctx context.Context, props *GrainProps) error {
	x.currentState = atomic.NewPointer(new(testpb.Account))
	x.stateStore = props.ActorSystem().Extension("MockStateStore").(MockStateStore)
	x.persistenceID = props.Identity().Name()
	return x.recoverFromStore()
}

// OnDeactivate writes the current account state back to the store.
func (x *MockPersistentGrain) OnDeactivate(ctx context.Context, props *GrainProps) error {
	return x.stateStore.WriteState(x.persistenceID, x.currentState.Load())
}

// OnReceive applies CreateAccount and CreditAccount, persists the balance and answers GetAccount.
func (x *MockPersistentGrain) OnReceive(ctx *GrainContext) {
	switch received := ctx.Message().(type) {
	case *testpb.CreateAccount:
		balance := received.GetAccountBalance()
		newBalance := x.currentState.Load().GetAccountBalance() + balance
		x.currentState.Store(testpb.Account_builder{
			AccountId:      x.persistenceID,
			AccountBalance: newBalance,
		}.Build())

		if err := x.stateStore.WriteState(x.persistenceID, x.currentState.Load()); err != nil {
			ctx.Err(err)
			return
		}

		ctx.NoErr()
	case *testpb.CreditAccount:
		balance := received.GetBalance()
		newBalance := x.currentState.Load().GetAccountBalance() + balance
		x.currentState.Store(testpb.Account_builder{
			AccountId:      x.persistenceID,
			AccountBalance: newBalance,
		}.Build())

		if err := x.stateStore.WriteState(x.persistenceID, x.currentState.Load()); err != nil {
			ctx.Err(err)
			return
		}

		ctx.Response(x.currentState.Load())
	case *testpb.GetAccount:
		ctx.Response(x.currentState.Load())
	default:
		ctx.Unhandled()
	}
}

// recoverFromStore loads the last persisted account state, when there is one.
func (x *MockPersistentGrain) recoverFromStore() error {
	latestState, err := x.stateStore.GetLatestState(x.persistenceID)
	if err != nil {
		return fmt.Errorf("failed to get the latest state: %w", err)
	}

	if latestState != nil {
		x.currentState.Store(latestState)
	}

	return nil
}

// MockContextReleasingGrain is a grain that hands every GrainContext it receives to a channel.
type MockContextReleasingGrain struct {
	MockNoopGrain

	// done carries every received context out to the test.
	done chan *GrainContext
}

// OnReceive publishes the context on done and succeeds.
func (x *MockContextReleasingGrain) OnReceive(ctx *GrainContext) {
	x.done <- ctx
	ctx.NoErr()
}

// MockContextEchoGrain is a grain that records the context value under key and echoes it in its reply.
type MockContextEchoGrain struct {
	MockNoopGrain

	// key is the context key whose value is recorded.
	key any
	mu  sync.RWMutex
	// seen holds the value observed on the most recent message.
	seen any
}

// OnReceive records the context value under key and echoes it on TestReply.
func (x *MockContextEchoGrain) OnReceive(ctx *GrainContext) {
	x.setSeen(ctx.Context().Value(x.key))
	if _, ok := ctx.Message().(*testpb.TestReply); ok {
		ctx.Response(testpb.Reply_builder{Content: fmt.Sprint(x.Seen())}.Build())
		return
	}
	ctx.NoErr()
}

// setSeen stores the value observed on the last message.
func (x *MockContextEchoGrain) setSeen(val any) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.seen = val
}

// Seen returns the value observed on the last message.
func (x *MockContextEchoGrain) Seen() any {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.seen
}

// MockPipeTargetGrain is a grain that publishes the piped result it receives on one of two channels.
type MockPipeTargetGrain struct {
	// received carries a piped success value out to the test.
	received chan any
	// failures carries a piped StatusFailure out to the test.
	failures chan *StatusFailure
}

// NewMockPipeTargetGrain returns a MockPipeTargetGrain with one-slot success and failure channels.
func NewMockPipeTargetGrain() *MockPipeTargetGrain {
	return &MockPipeTargetGrain{
		received: make(chan any, 1),
		failures: make(chan *StatusFailure, 1),
	}
}

// OnActivate does nothing.
func (x *MockPipeTargetGrain) OnActivate(context.Context, *GrainProps) error {
	return nil
}

// OnDeactivate does nothing.
func (x *MockPipeTargetGrain) OnDeactivate(context.Context, *GrainProps) error {
	return nil
}

// OnReceive publishes a StatusFailure on failures and anything else on received, when the channel still has room.
func (x *MockPipeTargetGrain) OnReceive(ctx *GrainContext) {
	switch msg := ctx.Message().(type) {
	case *StatusFailure:
		select {
		case x.failures <- msg:
		default:
		}
	default:
		select {
		case x.received <- msg:
		default:
		}
	}
	ctx.NoErr()
}

// MockGrainPipeSystem is a grain pipe target system that records the last piped message and reports a fixed error.
type MockGrainPipeSystem struct {
	// err is the error TellGrain reports on every call.
	err error
	// lastMessage is the message passed to the last TellGrain call.
	lastMessage any
}

// TellGrain records the message and reports the injected error.
func (x *MockGrainPipeSystem) TellGrain(ctx context.Context, identity *GrainIdentity, message any) error {
	x.lastMessage = message
	return x.err
}

// Logger returns a logger that discards everything written to it.
func (x *MockGrainPipeSystem) Logger() log.Logger {
	return log.DiscardLogger
}

// MockScriptedGrain is a grain that runs a per-test OnReceive function.
type MockScriptedGrain struct {
	// receive handles every message.
	receive func(*GrainContext)
}

// OnActivate does nothing.
func (x *MockScriptedGrain) OnActivate(context.Context, *GrainProps) error { return nil }

// OnDeactivate does nothing.
func (x *MockScriptedGrain) OnDeactivate(context.Context, *GrainProps) error { return nil }

// OnReceive defers to the receive function.
func (x *MockScriptedGrain) OnReceive(gctx *GrainContext) { x.receive(gctx) }

// activationProbe is the shared state MockActivationProbeGrain reports its activations through.
type activationProbe struct {
	// started carries one signal per activation that reaches the probe.
	started chan struct{}
	// release is closed by the test to let a parked activation finish.
	release chan struct{}
	// count is the number of activations observed.
	count syncatomic.Int32
}

// MockActivationProbeGrain is a grain that reports its activation to the probe in activationProbePtr and parks there.
type MockActivationProbeGrain struct{}

// OnActivate counts the activation, signals the probe and waits for its release.
func (x *MockActivationProbeGrain) OnActivate(ctx context.Context, props *GrainProps) error {
	probe := activationProbePtr.Load()
	if probe != nil {
		probe.count.Add(1)
		select {
		case probe.started <- struct{}{}:
		default:
		}
		<-probe.release
	}
	return nil
}

// OnReceive succeeds without a reply.
func (x *MockActivationProbeGrain) OnReceive(ctx *GrainContext) {
	ctx.NoErr()
}

// OnDeactivate does nothing.
func (x *MockActivationProbeGrain) OnDeactivate(ctx context.Context, props *GrainProps) error {
	return nil
}

// MockEnvelopeReplyingGrain is a grain that answers envelope asks inside the turn, routing the reply through the
// system from the request metadata stamped on the context.
type MockEnvelopeReplyingGrain struct{}

// OnActivate does nothing.
func (x *MockEnvelopeReplyingGrain) OnActivate(context.Context, *GrainProps) error { return nil }

// OnDeactivate does nothing.
func (x *MockEnvelopeReplyingGrain) OnDeactivate(context.Context, *GrainProps) error { return nil }

// OnReceive replies with a payload to TestPing, with a failure to TestBye, and never replies to anything else.
func (x *MockEnvelopeReplyingGrain) OnReceive(gctx *GrainContext) {
	if gctx.requestID == "" {
		if gctx.err != nil {
			gctx.NoErr()
		}
		return
	}

	system := gctx.ActorSystem()

	switch gctx.Message().(type) {
	case *testpb.TestPing:
		_ = system.routeAsyncReply(context.Background(), nil, gctx.requestReplyTo, gctx.requestID, testpb.Reply_builder{Content: "in-turn"}.Build(), nil)
	case *testpb.TestBye:
		_ = system.routeAsyncReply(context.Background(), nil, gctx.requestReplyTo, gctx.requestID, nil, errors.New("grain boom"))
	}
}

// MockEnvelopeDeferringGrain is a grain that answers envelope asks from a later turn, so the reply outlives the turn
// and the recycled context that carried the request.
type MockEnvelopeDeferringGrain struct {
	mu sync.Mutex
	// pending holds the correlation IDs still waiting for a flush.
	pending []string
	// requests carries each received correlation ID out to the test.
	requests chan string
}

// OnActivate does nothing.
func (x *MockEnvelopeDeferringGrain) OnActivate(context.Context, *GrainProps) error { return nil }

// OnDeactivate does nothing.
func (x *MockEnvelopeDeferringGrain) OnDeactivate(context.Context, *GrainProps) error { return nil }

// OnReceive stores and signals each request's correlation ID, and answers them all when a TestSend flush arrives.
func (x *MockEnvelopeDeferringGrain) OnReceive(gctx *GrainContext) {
	if gctx.requestID != "" {
		x.mu.Lock()
		x.pending = append(x.pending, gctx.requestID)
		x.mu.Unlock()

		select {
		case x.requests <- gctx.requestID:
		default:
		}
		return
	}

	if _, ok := gctx.Message().(*testpb.TestSend); ok {
		system := gctx.ActorSystem()

		x.mu.Lock()
		pending := x.pending
		x.pending = nil
		x.mu.Unlock()

		for _, correlationID := range pending {
			_ = system.routeAsyncReply(context.Background(), nil, nil, correlationID, testpb.Reply_builder{Content: "deferred"}.Build(), nil)
		}
	}

	if gctx.err != nil {
		gctx.NoErr()
	}
}

// MockReactivationGrain is a grain that survives reflective re-instantiation, since a bare send on a stored identity
// recreates it as a zero value with no wiring.
type MockReactivationGrain struct{}

// OnActivate does nothing.
func (*MockReactivationGrain) OnActivate(context.Context, *GrainProps) error { return nil }

// OnDeactivate does nothing.
func (*MockReactivationGrain) OnDeactivate(context.Context, *GrainProps) error { return nil }

// OnReceive succeeds without a reply.
func (*MockReactivationGrain) OnReceive(gctx *GrainContext) { gctx.NoErr() }

// MockActivationCountingGrain is a zero-value constructible grain that records its activations.
// It counts them in the package-level activationCount so a test can read them without holding the instance.
type MockActivationCountingGrain struct{}

// OnActivate increments activationCount.
func (x *MockActivationCountingGrain) OnActivate(context.Context, *GrainProps) error {
	activationCount.Add(1)
	return nil
}

// OnReceive succeeds without a reply.
func (x *MockActivationCountingGrain) OnReceive(ctx *GrainContext) {
	ctx.NoErr()
}

// OnDeactivate does nothing.
func (x *MockActivationCountingGrain) OnDeactivate(context.Context, *GrainProps) error {
	return nil
}

// MockCollisionGrain is a grain that exercises the kind-conflict detection in GrainOf together with
// MockCollisiongrain: they differ only in letter case, and the type registry lowercases names, so both now map to the
// kind "actor.mockcollisiongrain".
type MockCollisionGrain struct{}

// OnActivate does nothing.
func (x *MockCollisionGrain) OnActivate(context.Context, *GrainProps) error { return nil }

// OnReceive succeeds without a reply.
func (x *MockCollisionGrain) OnReceive(ctx *GrainContext) { ctx.NoErr() }

// OnDeactivate does nothing.
func (x *MockCollisionGrain) OnDeactivate(context.Context, *GrainProps) error { return nil }

// MockCollisiongrain collides by kind name with MockCollisionGrain: the type registry lowercases names, so both now
// map to the kind "actor.mockcollisiongrain".
type MockCollisiongrain struct{}

// OnActivate does nothing.
func (x *MockCollisiongrain) OnActivate(context.Context, *GrainProps) error { return nil }

// OnReceive succeeds without a reply.
func (x *MockCollisiongrain) OnReceive(ctx *GrainContext) { ctx.NoErr() }

// OnDeactivate does nothing.
func (x *MockCollisiongrain) OnDeactivate(context.Context, *GrainProps) error { return nil }

// MockValueKindGrain is a grain with value receivers, so the non-pointer type itself satisfies the Grain interface.
type MockValueKindGrain struct{}

// OnActivate does nothing.
func (MockValueKindGrain) OnActivate(context.Context, *GrainProps) error { return nil }

// OnReceive succeeds without a reply.
func (MockValueKindGrain) OnReceive(ctx *GrainContext) { ctx.NoErr() }

// OnDeactivate does nothing.
func (MockValueKindGrain) OnDeactivate(context.Context, *GrainProps) error { return nil }

// MockReentrantRecordingGrain is a grain that records every message OnReceive handles with its request metadata, so
// the pause and envelope tests can assert what was processed and in which order.
type MockReentrantRecordingGrain struct {
	mu sync.Mutex
	// records holds one entry per handled message, in processing order.
	records []recordedGrainMessage
}

// OnActivate does nothing.
func (x *MockReentrantRecordingGrain) OnActivate(context.Context, *GrainProps) error { return nil }

// OnDeactivate does nothing.
func (x *MockReentrantRecordingGrain) OnDeactivate(context.Context, *GrainProps) error { return nil }

// OnReceive records the message with its request metadata and acknowledges ordinary messages.
func (x *MockReentrantRecordingGrain) OnReceive(gctx *GrainContext) {
	x.mu.Lock()
	x.records = append(x.records, recordedGrainMessage{
		message:   gctx.Message(),
		requestID: gctx.requestID,
		replyTo:   gctx.requestReplyTo,
	})
	x.mu.Unlock()

	// Envelope contexts carry no channels; only ordinary messages ack.
	if gctx.err != nil {
		gctx.NoErr()
	}
}

// recorded returns a copy of the recorded entries in processing order.
func (x *MockReentrantRecordingGrain) recorded() []recordedGrainMessage {
	x.mu.Lock()
	defer x.mu.Unlock()
	return append([]recordedGrainMessage(nil), x.records...)
}

// messages returns the recorded messages in processing order, without their request metadata.
func (x *MockReentrantRecordingGrain) messages() []any {
	recorded := x.recorded()
	messages := make([]any, 0, len(recorded))

	for _, record := range recorded {
		messages = append(messages, record.message)
	}
	return messages
}

// recordedGrainMessage is one message captured by MockReentrantRecordingGrain with the request metadata it carried.
type recordedGrainMessage struct {
	message any
	// requestID is the correlation ID of the request, empty for an ordinary message.
	requestID string
	// replyTo is where the answer to the request would be routed.
	replyTo *commands.AsyncReplyTo
}

// MockDeactivationCountingGrain is a grain that counts its OnDeactivate calls so a test can assert it ran once.
type MockDeactivationCountingGrain struct {
	// deactivations is the number of OnDeactivate calls made.
	deactivations atomic.Int32
}

// OnActivate does nothing.
func (x *MockDeactivationCountingGrain) OnActivate(context.Context, *GrainProps) error { return nil }

// OnDeactivate increments the deactivation count.
func (x *MockDeactivationCountingGrain) OnDeactivate(context.Context, *GrainProps) error {
	x.deactivations.Inc()
	return nil
}

// OnReceive succeeds without a reply.
func (x *MockDeactivationCountingGrain) OnReceive(gctx *GrainContext) { gctx.NoErr() }

// MockTimerProbeGrain is a grain that publishes every message OnReceive sees and drives the tick failure paths.
// The message "panic" makes the handler panic and "fail" makes it report an error.
type MockTimerProbeGrain struct {
	// received carries every handled message out to the test.
	received chan any
	// onActivate runs inside OnActivate, to register timers from the lifecycle hook.
	onActivate func(*GrainProps) error
	// onDeactivate runs inside OnDeactivate, to release timers from the lifecycle hook.
	onDeactivate func(*GrainProps) error
	// onReceive, when set and returning true, handles the message in place of
	// the default behavior.
	onReceive func(*GrainContext) bool
}

// NewMockTimerProbeGrain returns a MockTimerProbeGrain with a 64-slot message channel.
func NewMockTimerProbeGrain() *MockTimerProbeGrain {
	return &MockTimerProbeGrain{received: make(chan any, 64)}
}

// OnActivate defers to the onActivate hook when one is set.
func (x *MockTimerProbeGrain) OnActivate(_ context.Context, props *GrainProps) error {
	if x.onActivate != nil {
		return x.onActivate(props)
	}
	return nil
}

// OnDeactivate defers to the onDeactivate hook when one is set.
func (x *MockTimerProbeGrain) OnDeactivate(_ context.Context, props *GrainProps) error {
	if x.onDeactivate != nil {
		return x.onDeactivate(props)
	}
	return nil
}

// OnReceive publishes the message, defers to the onReceive hook, then panics on "panic" and fails on "fail".
func (x *MockTimerProbeGrain) OnReceive(ctx *GrainContext) {
	message := ctx.Message()

	select {
	case x.received <- message:
	default:
	}

	if x.onReceive != nil && x.onReceive(ctx) {
		return
	}

	switch message {
	case "panic":
		panic("boom")
	case "fail":
		ctx.Err(errors.New("boom"))
	default:
		ctx.NoErr()
	}
}

// MockTimerSink is a timer sink that records the ticks a registry fires, in place of a grain process.
type MockTimerSink struct {
	// ticks carries every delivered entry out to the test. Buffered so a stray
	// late fire never blocks a timer goroutine after the test ends.
	ticks chan *grainTimerEntry
}

// NewMockTimerSink returns a MockTimerSink with a 16-slot tick channel.
func NewMockTimerSink() *MockTimerSink {
	return &MockTimerSink{ticks: make(chan *grainTimerEntry, 16)}
}

// deliverTimerTick publishes the fired entry.
func (x *MockTimerSink) deliverTimerTick(entry *grainTimerEntry) {
	x.ticks <- entry
}

// MockShutdownHook is a shutdown hook whose outcome and recovery are driven by the configured strategy.
type MockShutdownHook struct {
	// strategy selects both the error Execute returns and the recovery Recovery advertises.
	strategy RecoveryStrategy
	// executionCount counts the Execute calls.
	executionCount *atomic.Int32
	// maxRetries is the retry count advertised by Recovery.
	maxRetries int
}

// Execute counts the call and returns the error matching the configured strategy.
func (x *MockShutdownHook) Execute(context.Context, ActorSystem) error {
	defer func() {
		x.executionCount.Inc()
	}()

	switch x.strategy {
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

// Recovery returns a recovery built from the configured strategy and retry count.
func (x *MockShutdownHook) Recovery() *ShutdownHookRecovery {
	return NewShutdownHookRecovery(
		WithShutdownHookRecoveryStrategy(x.strategy),
		WithShutdownHookRetry(x.maxRetries, 100*time.Millisecond),
	)
}

// MockPanickingShutdownHook is a shutdown hook that panics with a value selected by its test case.
type MockPanickingShutdownHook struct {
	// executionCount counts the Execute calls.
	executionCount *atomic.Int32
	// testCase selects the value Execute panics with.
	testCase string
}

// Execute counts the call and panics with the value selected by testCase.
func (x *MockPanickingShutdownHook) Execute(context.Context, ActorSystem) error {
	defer func() {
		x.executionCount.Inc()
	}()

	switch x.testCase {
	case "case1":
		panic(errors.New("case1 panic error"))
	case "case2":
		panic(gerrors.NewPanicError(errors.New("case2 panic error")))
	default:
		panic("implement me")
	}
}

// Recovery returns nil so the hook runs without recovery.
func (x *MockPanickingShutdownHook) Recovery() *ShutdownHookRecovery {
	return nil
}

// MockShutdownHookWithoutRecovery is a shutdown hook that fails and offers no recovery.
type MockShutdownHookWithoutRecovery struct {
	// executionCount counts the Execute calls.
	executionCount *atomic.Int32
}

// Execute counts the call and fails.
func (x *MockShutdownHookWithoutRecovery) Execute(context.Context, ActorSystem) error {
	x.executionCount.Inc()
	return errors.New("mock shutdown hook without recovery")
}

// Recovery returns nil so the hook runs without recovery.
func (x *MockShutdownHookWithoutRecovery) Recovery() *ShutdownHookRecovery {
	return nil
}

// MockStateStore is the extension contract backing the persistent actor and grain mocks.
type MockStateStore interface {
	extension.Extension
	WriteState(persistenceID string, state *testpb.Account) error
	GetLatestState(persistenceID string) (*testpb.Account, error)
}

// MockExtension is an extension that keeps account state in memory.
type MockExtension struct {
	// db maps a persistence ID to its latest account state.
	db *sync.Map
}

// NewMockExtension returns a MockExtension with an empty store.
func NewMockExtension() *MockExtension {
	return &MockExtension{
		db: &sync.Map{},
	}
}

// ID returns the identifier the state store is resolved under.
func (x *MockExtension) ID() string {
	return "MockStateStore"
}

// GetLatestState returns the stored account, or an empty one when nothing was written.
func (x *MockExtension) GetLatestState(persistenceID string) (*testpb.Account, error) {
	value, ok := x.db.Load(persistenceID)
	if !ok {
		return new(testpb.Account), nil
	}
	return value.(*testpb.Account), nil
}

// WriteState stores the account under its persistence ID.
func (x *MockExtension) WriteState(persistenceID string, state *testpb.Account) error {
	x.db.Store(persistenceID, state)
	return nil
}

// MockDependency is a dependency that round-trips all of its fields through JSON.
type MockDependency struct {
	id       string
	Username string
	Email    string
}

// NewMockDependency returns a MockDependency with the given identifier, username and email.
func NewMockDependency(id, userName, email string) *MockDependency {
	return &MockDependency{
		id:       id,
		Username: userName,
		Email:    email,
	}
}

// MarshalBinary encodes the dependency, including its unexported identifier, as JSON.
func (x *MockDependency) MarshalBinary() (data []byte, err error) {
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

// UnmarshalBinary decodes the JSON produced by MarshalBinary.
func (x *MockDependency) UnmarshalBinary(data []byte) error {
	serializable := struct {
		ID       string `json:"id"`
		Username string `json:"Username"`
		Email    string `json:"Email"`
	}{}

	if err := json.Unmarshal(data, &serializable); err != nil {
		return err
	}

	x.id = serializable.ID
	x.Username = serializable.Username
	x.Email = serializable.Email

	return nil
}

// ID returns the dependency identifier.
func (x *MockDependency) ID() string {
	return x.id
}

// MockFailingDependency is a dependency whose marshalling always fails.
type MockFailingDependency struct {
	// err is the error MarshalBinary reports.
	err error
}

// ID returns a fixed identifier.
func (x *MockFailingDependency) ID() string {
	return "failing-dependency"
}

// MarshalBinary fails with the injected error.
func (x *MockFailingDependency) MarshalBinary() ([]byte, error) {
	return nil, x.err
}

// UnmarshalBinary does nothing.
func (x *MockFailingDependency) UnmarshalBinary(_ []byte) error {
	return nil
}

// MockDurableWorkQueue is an in-memory DurableWorkQueue that records the operations performed against it.
type MockDurableWorkQueue struct {
	mu sync.Mutex
	// epoch is the fencing token, bumped by every Load.
	epoch QueueEpoch
	// currentSeq is the highest sequence number stored so far.
	currentSeq int64
	// entries holds every stored message with its accept and confirm state.
	entries []workQueueEntry
	// loads counts the Load calls made.
	loads int
	// operations records the mutating calls in order, as "verb:messageID".
	operations []string
	// loadErr is the error Load reports when it is set.
	loadErr error
}

// ID returns the identifier the queue is resolved under.
func (x *MockDurableWorkQueue) ID() string { return "MockDurableWorkQueue" }

// MarshalBinary encodes the queue as its identifier.
func (x *MockDurableWorkQueue) MarshalBinary() ([]byte, error) { return []byte(x.ID()), nil }

// UnmarshalBinary does nothing.
func (x *MockDurableWorkQueue) UnmarshalBinary([]byte) error { return nil }

// Load counts the call, bumps the epoch and returns the accepted but unconfirmed messages.
func (x *MockDurableWorkQueue) Load(context.Context) (WorkQueueState, QueueEpoch, error) {
	x.mu.Lock()
	defer x.mu.Unlock()

	x.loads++

	if x.loadErr != nil {
		return WorkQueueState{}, 0, x.loadErr
	}

	x.epoch++

	unconfirmed := make([]UnconfirmedMessage, 0, len(x.entries))

	for _, entry := range x.entries {
		if entry.accepted && !entry.confirmed {
			unconfirmed = append(unconfirmed, entry.message)
		}
	}

	state, err := NewWorkQueueState(x.currentSeq, unconfirmed)
	if err != nil {
		return WorkQueueState{}, 0, err
	}

	return state, x.epoch, nil
}

// Store appends the message at the proposed sequence, answering a resubmitted MessageID with the original result.
func (x *MockDurableWorkQueue) Store(_ context.Context, epoch QueueEpoch, request StoreRequest) (StoreResult, error) {
	x.mu.Lock()
	defer x.mu.Unlock()

	if epoch != x.epoch {
		return StoreResult{}, gerrors.ErrQueueFenced
	}

	for _, entry := range x.entries {
		if entry.message.MessageID() == request.MessageID() {
			return NewStoreResult(entry.message.Seq(), true, entry.message.Payload())
		}
	}

	if request.ProposedSeq() != x.currentSeq+1 {
		return StoreResult{}, gerrors.ErrQueueConflict
	}

	message, err := NewUnconfirmedMessage(request.MessageID(), request.ProposedSeq(), request.Payload())
	if err != nil {
		return StoreResult{}, err
	}

	x.currentSeq = request.ProposedSeq()
	x.entries = append(x.entries, workQueueEntry{message: message})
	x.operations = append(x.operations, "store:"+request.MessageID())
	return NewStoreResult(request.ProposedSeq(), false, request.Payload())
}

// Accept marks the stored message accepted, or reports a conflict when it is unknown.
func (x *MockDurableWorkQueue) Accept(_ context.Context, epoch QueueEpoch, messageID string) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if epoch != x.epoch {
		return gerrors.ErrQueueFenced
	}

	for index := range x.entries {
		if x.entries[index].message.MessageID() != messageID {
			continue
		}

		x.entries[index].accepted = true
		x.operations = append(x.operations, "accept:"+messageID)
		return nil
	}

	return gerrors.ErrQueueConflict
}

// ConfirmMessage marks the stored message confirmed, or reports a conflict when it is unknown.
func (x *MockDurableWorkQueue) ConfirmMessage(_ context.Context, epoch QueueEpoch, messageID string) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if epoch != x.epoch {
		return gerrors.ErrQueueFenced
	}

	for index := range x.entries {
		if x.entries[index].message.MessageID() != messageID {
			continue
		}

		x.entries[index].confirmed = true
		x.operations = append(x.operations, "confirm:"+messageID)
		return nil
	}

	return gerrors.ErrQueueConflict
}

// seedAccepted installs an accepted, unconfirmed message for reload tests.
func (x *MockDurableWorkQueue) seedAccepted(messageID string, seq int64, payload ReliablePayload) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	message, err := NewUnconfirmedMessage(messageID, seq, payload)
	if err != nil {
		return err
	}

	if seq > x.currentSeq {
		x.currentSeq = seq
	}

	x.entries = append(x.entries, workQueueEntry{message: message, accepted: true})
	return nil
}

// snapshot returns the load count and a copy of the recorded operations.
func (x *MockDurableWorkQueue) snapshot() (int, []string) {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.loads, append([]string(nil), x.operations...)
}

// confirmedCount returns how many stored messages have been confirmed.
func (x *MockDurableWorkQueue) confirmedCount() int {
	x.mu.Lock()
	defer x.mu.Unlock()

	count := 0

	for _, entry := range x.entries {
		if entry.confirmed {
			count++
		}
	}

	return count
}

// workQueueEntry is one stored message in MockDurableWorkQueue with its accept and confirm state.
type workQueueEntry struct {
	message   UnconfirmedMessage
	accepted  bool
	confirmed bool
}

// MockDurableQueue is a DurableProducerQueue that models an external linearizable store.
// It is shared with the controller's asynchronous tasks, so it guards its state with a mutex as a storage client would.
type MockDurableQueue struct {
	mu sync.Mutex
	// epoch is the fencing token, bumped by every Load.
	epoch QueueEpoch
	// currentSeq is the highest sequence number stored so far.
	currentSeq int64
	// confirmedSeq is the highest sequence number confirmed so far.
	confirmedSeq int64
	// stored holds the messages the store still knows about, in sequence order.
	stored []UnconfirmedMessage
	// loads counts the Load calls made.
	loads int
	// operations records the mutating calls in order, as "verb:messageID".
	operations []string
	// storeErr is the error Store and StoreChunked report when it is set.
	storeErr error
	// acceptErr is the error Accept reports when it is set.
	acceptErr error
	// confirmErr is the error Confirm reports when it is set.
	confirmErr error
	// loadErr is the error Load reports when it is set.
	loadErr error
	// storeDelay stalls Store and StoreChunked before they touch the state.
	storeDelay time.Duration
	// confirmDelay stalls Confirm before it touches the state.
	confirmDelay time.Duration
	// retainConfirmed keeps confirmed entries in the MessageID index, which
	// the contract permits until Accept and Confirm both cover a message, so
	// first-write-wins still answers a resubmission of a confirmed MessageID.
	retainConfirmed bool
}

// ID returns the identifier the queue is resolved under.
func (x *MockDurableQueue) ID() string { return "MockDurableQueue" }

// MarshalBinary encodes the queue as its identifier.
func (x *MockDurableQueue) MarshalBinary() ([]byte, error) { return []byte(x.ID()), nil }

// UnmarshalBinary does nothing.
func (x *MockDurableQueue) UnmarshalBinary([]byte) error { return nil }

// Load counts the call, bumps the epoch and returns the messages past the confirmed sequence.
func (x *MockDurableQueue) Load(context.Context) (DurableQueueState, QueueEpoch, error) {
	x.mu.Lock()
	defer x.mu.Unlock()

	x.loads++

	if x.loadErr != nil {
		return DurableQueueState{}, 0, x.loadErr
	}

	x.epoch++

	unconfirmed := make([]UnconfirmedMessage, 0, len(x.stored))

	for _, message := range x.stored {
		if message.Seq() > x.confirmedSeq {
			unconfirmed = append(unconfirmed, message)
		}
	}

	state, err := NewDurableQueueState(x.currentSeq, x.confirmedSeq, unconfirmed)
	if err != nil {
		return DurableQueueState{}, 0, err
	}

	return state, x.epoch, nil
}

// Store appends the message at the proposed sequence, answering a resubmitted MessageID with the original result.
func (x *MockDurableQueue) Store(_ context.Context, epoch QueueEpoch, request StoreRequest) (StoreResult, error) {
	x.mu.Lock()
	delay, storeErr := x.storeDelay, x.storeErr
	x.mu.Unlock()

	if delay > 0 {
		pause.For(delay)
	}

	x.mu.Lock()
	defer x.mu.Unlock()

	if storeErr != nil {
		return StoreResult{}, storeErr
	}

	if epoch != x.epoch {
		return StoreResult{}, gerrors.ErrQueueFenced
	}

	for _, message := range x.stored {
		if message.MessageID() == request.MessageID() {
			return NewStoreResult(message.Seq(), true, message.Payload())
		}
	}

	for _, message := range x.stored {
		if strings.HasPrefix(message.MessageID(), durableChunkIDPrefix) && idFrom(message.MessageID()) == request.MessageID() {
			// the business MessageID is owned by a stored chunked batch that a
			// single StoreResult cannot carry: the contract directs the caller
			// to recover it through a StoreChunked retry
			return StoreResult{}, gerrors.ErrQueueChunkedBatch
		}
	}

	if request.ProposedSeq() != x.currentSeq+1 {
		return StoreResult{}, gerrors.ErrQueueConflict
	}

	message, err := NewUnconfirmedMessage(request.MessageID(), request.ProposedSeq(), request.Payload())
	if err != nil {
		return StoreResult{}, err
	}

	x.currentSeq = request.ProposedSeq()
	x.stored = append(x.stored, message)
	x.operations = append(x.operations, "store:"+request.MessageID())
	return NewStoreResult(request.ProposedSeq(), false, request.Payload())
}

// StoreChunked appends a complete derived-ID batch at contiguous sequences, replaying the original batch on a retry.
func (x *MockDurableQueue) StoreChunked(_ context.Context, epoch QueueEpoch, requests []StoreRequest) ([]StoreResult, error) {
	x.mu.Lock()
	delay, storeErr := x.storeDelay, x.storeErr
	x.mu.Unlock()

	if delay > 0 {
		pause.For(delay)
	}

	x.mu.Lock()
	defer x.mu.Unlock()

	if storeErr != nil {
		return nil, storeErr
	}

	if epoch != x.epoch {
		return nil, gerrors.ErrQueueFenced
	}

	if len(requests) == 0 {
		return nil, gerrors.NewErrInvalidMessage(errors.New("chunked store requires at least one chunk"))
	}

	businessID, index, count, ok := parseDurableChunkMessageID(requests[0].MessageID())
	if !ok || index != 1 || count != len(requests) {
		return nil, gerrors.NewErrInvalidMessage(errors.New("chunked store requests must be a complete derived-ID batch"))
	}

	for position, request := range requests {
		requestBusiness, requestIndex, requestCount, requestOK := parseDurableChunkMessageID(request.MessageID())
		if !requestOK || requestBusiness != businessID || requestIndex != position+1 || requestCount != count {
			return nil, gerrors.NewErrInvalidMessage(errors.New("chunked store requests must share one business MessageID and contiguous positions"))
		}
	}

	existing := make([]UnconfirmedMessage, 0, count)

	for _, message := range x.stored {
		if idFrom(message.MessageID()) == businessID {
			existing = append(existing, message)
		}
	}

	if len(existing) > 0 {
		// first-write-wins for the business MessageID: return the original
		// batch even when the retry proposes a different chunk count or bytes
		results := make([]StoreResult, 0, len(existing))

		for _, message := range existing {
			result, err := NewStoreResult(message.Seq(), true, message.Payload())
			if err != nil {
				return nil, err
			}

			results = append(results, result)
		}

		x.operations = append(x.operations, "storechunked:"+businessID)
		return results, nil
	}

	if requests[0].ProposedSeq() != x.currentSeq+1 {
		return nil, gerrors.ErrQueueConflict
	}

	for position, request := range requests {
		if request.ProposedSeq() != x.currentSeq+int64(position)+1 {
			return nil, gerrors.ErrQueueConflict
		}
	}

	results := make([]StoreResult, 0, count)
	appended := make([]UnconfirmedMessage, 0, count)

	for position, request := range requests {
		entry, err := newChunkUnconfirmedMessage(request.MessageID(), request.ProposedSeq(), request.Payload(), position == 0, position == count-1)
		if err != nil {
			return nil, err
		}

		result, err := NewStoreResult(request.ProposedSeq(), false, request.Payload())
		if err != nil {
			return nil, err
		}

		appended = append(appended, entry)
		results = append(results, result)
	}

	x.stored = append(x.stored, appended...)
	x.currentSeq = requests[len(requests)-1].ProposedSeq()
	x.operations = append(x.operations, "storechunked:"+businessID)
	return results, nil
}

// Accept records the acceptance, or reports the injected error or a fencing failure.
func (x *MockDurableQueue) Accept(_ context.Context, epoch QueueEpoch, messageID string) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if x.acceptErr != nil {
		return x.acceptErr
	}

	if epoch != x.epoch {
		return gerrors.ErrQueueFenced
	}

	x.operations = append(x.operations, "accept:"+messageID)
	return nil
}

// Confirm raises the confirmed sequence and drops the covered messages unless retainConfirmed is set.
func (x *MockDurableQueue) Confirm(_ context.Context, epoch QueueEpoch, upToSeq int64) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	delay, confirmErr := x.confirmDelay, x.confirmErr

	if delay > 0 {
		pause.For(delay)
	}

	if confirmErr != nil {
		return confirmErr
	}

	if epoch != x.epoch {
		return gerrors.ErrQueueFenced
	}

	x.confirmedSeq = max(x.confirmedSeq, upToSeq)
	x.operations = append(x.operations, "confirm")

	if !x.retainConfirmed {
		cut := 0

		for cut < len(x.stored) && x.stored[cut].Seq() <= x.confirmedSeq {
			cut++
		}

		x.stored = x.stored[cut:]
	}

	return nil
}

// snapshot returns copies of the observable queue state.
func (x *MockDurableQueue) snapshot() (int, []string, int64) {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.loads, append([]string(nil), x.operations...), x.confirmedSeq
}

// MockSharedDurableQueue is a relocatable DurableProducerQueue whose instances all delegate to one process-global
// MockDurableQueue, since only the ID crosses the wire.
type MockSharedDurableQueue struct {
	// id names both this handle and the process-global state it delegates to.
	id string
}

// NewMockSharedDurableQueue creates a queue handle and its backing state.
func NewMockSharedDurableQueue(id string) *MockSharedDurableQueue {
	queue := &MockSharedDurableQueue{id: id}
	queue.backing()
	return queue
}

// backing resolves the process-global state of this queue, creating it on
// first use so a reconstructed instance attaches to the same store.
func (x *MockSharedDurableQueue) backing() *MockDurableQueue {
	sharedQueueStatesMu.Lock()
	defer sharedQueueStatesMu.Unlock()

	state, ok := sharedQueueStates[x.id]
	if !ok {
		state = &MockDurableQueue{}
		sharedQueueStates[x.id] = state
	}

	return state
}

// ID returns the identifier the queue is resolved under.
func (x *MockSharedDurableQueue) ID() string { return x.id }

// MarshalBinary encodes the queue as its identifier.
func (x *MockSharedDurableQueue) MarshalBinary() ([]byte, error) { return []byte(x.id), nil }

// UnmarshalBinary reattaches the handle to the state registered under the decoded identifier.
func (x *MockSharedDurableQueue) UnmarshalBinary(data []byte) error {
	x.id = string(data)
	return nil
}

// Load defers to the backing state.
func (x *MockSharedDurableQueue) Load(ctx context.Context) (DurableQueueState, QueueEpoch, error) {
	return x.backing().Load(ctx)
}

// Store defers to the backing state.
func (x *MockSharedDurableQueue) Store(ctx context.Context, epoch QueueEpoch, request StoreRequest) (StoreResult, error) {
	return x.backing().Store(ctx, epoch, request)
}

// StoreChunked defers to the backing state.
func (x *MockSharedDurableQueue) StoreChunked(ctx context.Context, epoch QueueEpoch, requests []StoreRequest) ([]StoreResult, error) {
	return x.backing().StoreChunked(ctx, epoch, requests)
}

// Accept defers to the backing state.
func (x *MockSharedDurableQueue) Accept(ctx context.Context, epoch QueueEpoch, messageID string) error {
	return x.backing().Accept(ctx, epoch, messageID)
}

// Confirm defers to the backing state.
func (x *MockSharedDurableQueue) Confirm(ctx context.Context, epoch QueueEpoch, upToSeq int64) error {
	return x.backing().Confirm(ctx, epoch, upToSeq)
}

// MockSharedDurableWorkQueue is a relocatable DurableWorkQueue whose instances all delegate to one process-global
// MockDurableWorkQueue, since only the ID crosses the wire.
type MockSharedDurableWorkQueue struct {
	// id names both this handle and the process-global state it delegates to.
	id string
}

// NewMockSharedDurableWorkQueue creates a work-queue handle and its backing state.
func NewMockSharedDurableWorkQueue(id string) *MockSharedDurableWorkQueue {
	queue := &MockSharedDurableWorkQueue{id: id}
	queue.backing()
	return queue
}

// backing resolves the process-global state of this work queue, creating it on
// first use so a reconstructed instance attaches to the same store.
func (x *MockSharedDurableWorkQueue) backing() *MockDurableWorkQueue {
	sharedWorkQueueStatesMu.Lock()
	defer sharedWorkQueueStatesMu.Unlock()

	state, ok := sharedWorkQueueStates[x.id]
	if !ok {
		state = &MockDurableWorkQueue{}
		sharedWorkQueueStates[x.id] = state
	}

	return state
}

// ID returns the identifier the queue is resolved under.
func (x *MockSharedDurableWorkQueue) ID() string { return x.id }

// MarshalBinary encodes the queue as its identifier.
func (x *MockSharedDurableWorkQueue) MarshalBinary() ([]byte, error) { return []byte(x.id), nil }

// UnmarshalBinary reattaches the handle to the state registered under the decoded identifier.
func (x *MockSharedDurableWorkQueue) UnmarshalBinary(data []byte) error {
	x.id = string(data)
	return nil
}

// Load defers to the backing state.
func (x *MockSharedDurableWorkQueue) Load(ctx context.Context) (WorkQueueState, QueueEpoch, error) {
	return x.backing().Load(ctx)
}

// Store defers to the backing state.
func (x *MockSharedDurableWorkQueue) Store(ctx context.Context, epoch QueueEpoch, request StoreRequest) (StoreResult, error) {
	return x.backing().Store(ctx, epoch, request)
}

// Accept defers to the backing state.
func (x *MockSharedDurableWorkQueue) Accept(ctx context.Context, epoch QueueEpoch, messageID string) error {
	return x.backing().Accept(ctx, epoch, messageID)
}

// ConfirmMessage defers to the backing state.
func (x *MockSharedDurableWorkQueue) ConfirmMessage(ctx context.Context, epoch QueueEpoch, messageID string) error {
	return x.backing().ConfirmMessage(ctx, epoch, messageID)
}

// MockReliableRelocationConsumer is a MockReliableConsumer that turns on auto-confirmation at start, so the fresh
// instance a relocation creates behaves like the original spawn.
type MockReliableRelocationConsumer struct {
	MockReliableConsumer
}

// PreStart turns on auto-confirmation.
func (x *MockReliableRelocationConsumer) PreStart(*Context) error {
	x.autoConfirm = true
	return nil
}

// MockFailingMailbox is a mailbox that rejects every enqueued message.
type MockFailingMailbox struct{}

// NewMockFailingMailbox returns a MockFailingMailbox.
func NewMockFailingMailbox() *MockFailingMailbox {
	return &MockFailingMailbox{}
}

// Dequeue always returns nil.
func (x *MockFailingMailbox) Dequeue() (msg *ReceiveContext) {
	return nil
}

// Dispose does nothing.
func (x *MockFailingMailbox) Dispose() {}

// Enqueue fails on every call.
func (x *MockFailingMailbox) Enqueue(_ *ReceiveContext) error {
	return fmt.Errorf("mock error mailbox: failed to enqueue message")
}

// IsEmpty always reports true.
func (x *MockFailingMailbox) IsEmpty() bool {
	return true
}

// Len always returns zero.
func (x *MockFailingMailbox) Len() int64 {
	return 0
}

// MockNoopMailbox is a mailbox that accepts every message and holds none.
type MockNoopMailbox struct{}

// Enqueue accepts the message and drops it.
func (MockNoopMailbox) Enqueue(*ReceiveContext) error {
	return nil
}

// Dequeue always returns nil.
func (MockNoopMailbox) Dequeue() *ReceiveContext {
	return nil
}

// IsEmpty always reports true.
func (MockNoopMailbox) IsEmpty() bool {
	return true
}

// Len always returns zero.
func (MockNoopMailbox) Len() int64 {
	return 0
}

// Dispose does nothing.
func (MockNoopMailbox) Dispose() {}

// MockPassivationStrategy is a passivation strategy that carries a name and nothing else.
type MockPassivationStrategy struct{}

// Name returns the strategy name.
func (x *MockPassivationStrategy) Name() string {
	return "MockPassivationStrategy"
}

// String returns the strategy name.
func (x *MockPassivationStrategy) String() string {
	return "MockPassivationStrategy"
}

// MockPassivationParticipant is a passivation candidate with a fixed identity and last activity time.
type MockPassivationParticipant struct {
	id string
	// last is the activity timestamp reported to the passivation manager.
	last time.Time
	// passivate decides the outcome of passivationTry; nil accepts every attempt.
	passivate func(string) bool
}

// passivationID returns the participant identifier.
func (x *MockPassivationParticipant) passivationID() string {
	return x.id
}

// passivationLatestActivity returns the configured last activity time.
func (x *MockPassivationParticipant) passivationLatestActivity() time.Time {
	return x.last
}

// passivationTry defers to the passivate function, or accepts when none is set.
func (x *MockPassivationParticipant) passivationTry(reason string) bool {
	if x.passivate != nil {
		return x.passivate(reason)
	}
	return true
}

// MockHeaderPropagator is a context propagator that carries a single context value in a single HTTP header.
type MockHeaderPropagator struct {
	// headerKey is the HTTP header the value travels in.
	headerKey string
	// ctxKey is the context key the value is read from and written to.
	ctxKey any
}

// Inject writes the context value into headerKey when the context carries one.
func (x *MockHeaderPropagator) Inject(ctx context.Context, headers http.Header) error {
	if val := ctx.Value(x.ctxKey); val != nil {
		headers.Set(x.headerKey, fmt.Sprint(val))
	}
	return nil
}

// Extract puts the header value into the context under ctxKey when the header is present.
func (x *MockHeaderPropagator) Extract(ctx context.Context, headers http.Header) (context.Context, error) {
	if val := headers.Get(x.headerKey); val != "" {
		ctx = context.WithValue(ctx, x.ctxKey, val)
	}
	return ctx, nil
}

// MockFailingContextPropagator is a context propagator whose extraction always fails.
type MockFailingContextPropagator struct {
	// err is the error Extract reports.
	err error
}

// Inject does nothing.
func (x *MockFailingContextPropagator) Inject(_ context.Context, _ http.Header) error {
	return nil
}

// Extract fails with the injected error.
func (x *MockFailingContextPropagator) Extract(ctx context.Context, _ http.Header) (context.Context, error) {
	return ctx, x.err
}

// MockPanickingContextPropagator is a context propagator that panics during extraction.
type MockPanickingContextPropagator struct{}

// Inject does nothing.
func (MockPanickingContextPropagator) Inject(_ context.Context, _ http.Header) error {
	return nil
}

// Extract panics on every call.
func (MockPanickingContextPropagator) Extract(context.Context, http.Header) (context.Context, error) {
	panic("context propagation panic")
}

// MockRecordingPeerStateStore is a peer state store that records the calls made against it.
type MockRecordingPeerStateStore struct {
	// err is the error PersistPeerState reports.
	err error
	// called records whether PersistPeerState was invoked.
	called bool
	// lastPeer is the peer state passed to the last PersistPeerState call.
	lastPeer *internalpb.PeerState
	// deleteCalled records whether DeletePeerState was invoked.
	deleteCalled bool
	// deletedAddr is the address passed to the last DeletePeerState call.
	deletedAddr string
	// deleteErr is the error DeletePeerState reports.
	deleteErr error
}

// PersistPeerState records the peer state and returns the injected error.
func (x *MockRecordingPeerStateStore) PersistPeerState(_ context.Context, peer *internalpb.PeerState) error {
	x.called = true
	x.lastPeer = peer
	return x.err
}

// GetPeerState never finds a peer.
func (x *MockRecordingPeerStateStore) GetPeerState(_ context.Context, _ string) (*internalpb.PeerState, bool) {
	return nil, false
}

// DeletePeerState records the address and returns the injected error.
func (x *MockRecordingPeerStateStore) DeletePeerState(_ context.Context, address string) error {
	x.deleteCalled = true
	x.deletedAddr = address
	return x.deleteErr
}

// Close does nothing.
func (x *MockRecordingPeerStateStore) Close() error {
	return nil
}

// MockControlPlane is a control plane whose active data centers come from an injected function.
type MockControlPlane struct {
	// listActive supplies the ListActive result; nil reports no data centers.
	listActive func(context.Context) ([]datacenter.DataCenterRecord, error)
}

// Register accepts the record and returns its identifier at version one.
func (*MockControlPlane) Register(_ context.Context, record datacenter.DataCenterRecord) (string, uint64, error) {
	return record.ID, 1, nil
}

// Heartbeat bumps the version and extends the lease by one hour.
func (*MockControlPlane) Heartbeat(_ context.Context, _ string, version uint64) (uint64, time.Time, error) {
	return version + 1, time.Now().Add(time.Hour), nil
}

// SetState bumps the version and keeps no state.
func (*MockControlPlane) SetState(_ context.Context, _ string, _ datacenter.DataCenterState, version uint64) (uint64, error) {
	return version + 1, nil
}

// ListActive defers to the injected function, or reports no data centers when none is set.
func (x *MockControlPlane) ListActive(ctx context.Context) ([]datacenter.DataCenterRecord, error) {
	if x.listActive != nil {
		return x.listActive(ctx)
	}
	return nil, nil
}

// Watch reports that watching is not supported.
func (*MockControlPlane) Watch(_ context.Context) (<-chan datacenter.ControlPlaneEvent, error) {
	return nil, gerrors.ErrWatchNotSupported
}

// Deregister does nothing.
func (*MockControlPlane) Deregister(_ context.Context, _ string) error {
	return nil
}

// MockPath is a Path whose String value is chosen by the test, to drive the parse-failure branch of pathToAddress.
type MockPath struct {
	// s is the value String reports, usually one pathToAddress cannot parse.
	s string
	// incarnation is the value incarnationID reports.
	incarnation string
}

// Host returns an empty host.
func (x *MockPath) Host() string { return "" }

// HostPort returns an empty host and port.
func (x *MockPath) HostPort() string { return "" }

// incarnationID returns the configured incarnation.
func (x *MockPath) incarnationID() string { return x.incarnation }

// Port returns a zero port.
func (x *MockPath) Port() int { return 0 }

// Name returns an empty name.
func (x *MockPath) Name() string { return "" }

// Parent reports that the path has no parent.
func (x *MockPath) Parent() Path { return nil }

// String returns the configured path string.
func (x *MockPath) String() string { return x.s }

// System returns an empty actor system name.
func (x *MockPath) System() string { return "" }

// Equals never matches another path.
func (x *MockPath) Equals(other Path) bool { return false }

// MockScriptedControlPlane is a control plane whose four main calls are each supplied by an optional test function.
type MockScriptedControlPlane struct {
	// registerFn overrides Register when set.
	registerFn func(context.Context, datacenter.DataCenterRecord) (string, uint64, error)
	// heartbeatFn overrides Heartbeat when set.
	heartbeatFn func(context.Context, string, uint64) (uint64, time.Time, error)
	// setStateFn overrides SetState when set.
	setStateFn func(context.Context, string, datacenter.DataCenterState, uint64) (uint64, error)
	// listActiveFn overrides ListActive when set.
	listActiveFn func(context.Context) ([]datacenter.DataCenterRecord, error)
}

// Register defers to registerFn, or accepts the record at version one.
func (x *MockScriptedControlPlane) Register(ctx context.Context, record datacenter.DataCenterRecord) (string, uint64, error) {
	if x.registerFn != nil {
		return x.registerFn(ctx, record)
	}
	return record.ID, 1, nil
}

// Heartbeat defers to heartbeatFn, or bumps the version and extends the lease by one hour.
func (x *MockScriptedControlPlane) Heartbeat(ctx context.Context, id string, version uint64) (uint64, time.Time, error) {
	if x.heartbeatFn != nil {
		return x.heartbeatFn(ctx, id, version)
	}
	return version + 1, time.Now().Add(time.Hour), nil
}

// SetState defers to setStateFn, or bumps the version and keeps no state.
func (x *MockScriptedControlPlane) SetState(ctx context.Context, id string, state datacenter.DataCenterState, version uint64) (uint64, error) {
	if x.setStateFn != nil {
		return x.setStateFn(ctx, id, state, version)
	}
	return version + 1, nil
}

// ListActive defers to listActiveFn, or reports no data centers.
func (x *MockScriptedControlPlane) ListActive(ctx context.Context) ([]datacenter.DataCenterRecord, error) {
	if x.listActiveFn != nil {
		return x.listActiveFn(ctx)
	}
	return nil, nil
}

// Watch returns no event stream.
func (*MockScriptedControlPlane) Watch(_ context.Context) (<-chan datacenter.ControlPlaneEvent, error) {
	return nil, nil
}

// Deregister does nothing.
func (*MockScriptedControlPlane) Deregister(_ context.Context, _ string) error {
	return nil
}

// MockFailingSetStateControlPlane is a control plane that fails every state change except the one made at register.
// Keeping the active transition working lets a test start the controller and then fail its stop.
type MockFailingSetStateControlPlane struct{}

// Register accepts the record and returns its identifier at version one.
func (*MockFailingSetStateControlPlane) Register(_ context.Context, record datacenter.DataCenterRecord) (string, uint64, error) {
	return record.ID, 1, nil
}

// Heartbeat bumps the version and extends the lease by one hour.
func (*MockFailingSetStateControlPlane) Heartbeat(_ context.Context, _ string, version uint64) (uint64, time.Time, error) {
	return version + 1, time.Now().Add(time.Hour), nil
}

// SetState succeeds for the active state and fails for every other.
func (*MockFailingSetStateControlPlane) SetState(_ context.Context, _ string, state datacenter.DataCenterState, version uint64) (uint64, error) {
	if state == datacenter.DataCenterActive {
		return version + 1, nil
	}
	return 0, errors.New("set state failed")
}

// ListActive reports no data centers.
func (*MockFailingSetStateControlPlane) ListActive(_ context.Context) ([]datacenter.DataCenterRecord, error) {
	return nil, nil
}

// Watch returns no event stream.
func (*MockFailingSetStateControlPlane) Watch(_ context.Context) (<-chan datacenter.ControlPlaneEvent, error) {
	return nil, nil
}

// Deregister does nothing.
func (*MockFailingSetStateControlPlane) Deregister(_ context.Context, _ string) error {
	return nil
}

// MockFailingRegisterControlPlane is a control plane that fails every Register call, so the controller cannot start.
type MockFailingRegisterControlPlane struct{}

// Register fails on every call.
func (*MockFailingRegisterControlPlane) Register(_ context.Context, _ datacenter.DataCenterRecord) (string, uint64, error) {
	return "", 0, errors.New("register failed")
}

// Heartbeat bumps the version and extends the lease by one hour.
func (*MockFailingRegisterControlPlane) Heartbeat(_ context.Context, _ string, version uint64) (uint64, time.Time, error) {
	return version + 1, time.Now().Add(time.Hour), nil
}

// SetState bumps the version and keeps no state.
func (*MockFailingRegisterControlPlane) SetState(_ context.Context, _ string, _ datacenter.DataCenterState, version uint64) (uint64, error) {
	return version + 1, nil
}

// ListActive reports no data centers.
func (*MockFailingRegisterControlPlane) ListActive(_ context.Context) ([]datacenter.DataCenterRecord, error) {
	return nil, nil
}

// Watch returns no event stream.
func (*MockFailingRegisterControlPlane) Watch(_ context.Context) (<-chan datacenter.ControlPlaneEvent, error) {
	return nil, nil
}

// Deregister does nothing.
func (*MockFailingRegisterControlPlane) Deregister(_ context.Context, _ string) error {
	return nil
}

// MockCountingSchedulable is a schedulable that counts its turns and reschedules itself a bounded number of times
// through the worker that ran it.
type MockCountingSchedulable struct {
	// remaining is the number of turns still to be scheduled.
	remaining syncatomic.Int32
	// done is the number of turns run so far.
	done syncatomic.Int32
	// resume re-pushes the schedulable onto the worker; nil stops after one turn.
	resume func(w *worker)
}

// runTurn counts the turn and reschedules while turns remain.
func (x *MockCountingSchedulable) runTurn(w *worker) {
	x.done.Add(1)
	if x.remaining.Add(-1) > 0 && x.resume != nil {
		x.resume(w)
	}
}

// MockReschedulingSchedulable is a schedulable whose turn is run by a callback that receives the schedulable and the
// worker running it, so the callback can re-push through either the local queue or the dispatcher.
type MockReschedulingSchedulable struct {
	// self is the value handed to the callback, so it can re-push this schedulable.
	self schedulable
	// onRun runs the turn.
	onRun func(self schedulable, w *worker)
}

// runTurn defers to the onRun callback.
func (x *MockReschedulingSchedulable) runTurn(w *worker) { x.onRun(x.self, w) }

// MockSchedulable is a schedulable that only counts the turns it is given.
type MockSchedulable struct {
	// id distinguishes the schedulables a test pushes onto one worker.
	id int
	// turns is the number of turns run so far.
	turns syncatomic.Int32
}

// NewMockSchedulable returns a MockSchedulable with the given identifier and no turns run.
func NewMockSchedulable(id int) *MockSchedulable { return &MockSchedulable{id: id} }

// runTurn counts the turn.
func (x *MockSchedulable) runTurn(*worker) { x.turns.Add(1) }

// reliableProtocolMessage is a plain payload carried through the reliable protocol tests.
type reliableProtocolMessage struct {
	value string
}

// MockTimeoutError is a net.Error whose Timeout reports true, used to exercise the
// network-timeout branch of isHandoffRetryable.
type MockTimeoutError struct{}

// Error returns a message that reads like a network timeout.
func (MockTimeoutError) Error() string { return "i/o timeout" }

// Timeout reports that the error is a timeout.
func (MockTimeoutError) Timeout() bool { return true }

// Temporary reports that the error is temporary.
func (MockTimeoutError) Temporary() bool { return true }

// MockHandoffSystem is an ActorSystem test double that defines only the methods deliverAcrossHandoff consults.
// Every other call panics through the nil embedded interface, so an accidental extra dependency is caught.
type MockHandoffSystem struct {
	ActorSystem

	// inCluster is what InCluster reports.
	inCluster bool
	// resolve answers each ActorOf call, keyed by the attempt number.
	resolve func(attempt int) (*PID, error)
	// relocating marks the host:port endpoints reported as relocating.
	relocating map[string]bool
	// inFlight is what relocationInFlight reports.
	inFlight bool
	// handoffs counts the recorded relocation handoffs.
	handoffs int
	// attempts counts the ActorOf calls made.
	attempts int
}

// InCluster reports the configured cluster membership.
func (x *MockHandoffSystem) InCluster() bool { return x.inCluster }

// ActorOf counts the attempt and defers to the resolve function.
func (x *MockHandoffSystem) ActorOf(context.Context, string) (*PID, error) {
	x.attempts++
	return x.resolve(x.attempts)
}

// isEndpointRelocating reports whether the address is one of the endpoints marked relocating.
func (x *MockHandoffSystem) isEndpointRelocating(addr *address.Address) bool {
	if addr == nil {
		return false
	}
	return x.relocating[address.FormatHostPort(addr.Host(), addr.Port())]
}

// relocationInFlight reports the configured relocation state.
func (x *MockHandoffSystem) relocationInFlight() bool { return x.inFlight }

// recordRelocationHandoff counts the handoff.
func (x *MockHandoffSystem) recordRelocationHandoff(context.Context) { x.handoffs++ }

// MockSpawnSingletonSpy is an actor system that records the arguments of the SpawnSingleton call instead of spawning.
type MockSpawnSingletonSpy struct {
	*actorSystem

	// called records whether SpawnSingleton was invoked.
	called bool
	// actorName is the name passed to the last call.
	actorName string
	// actor is the actor passed to the last call.
	actor Actor
	// config is the singleton configuration built from the last call's options.
	config *clusterSingletonConfig
}

// SpawnSingleton records the call arguments and spawns nothing.
func (x *MockSpawnSingletonSpy) SpawnSingleton(ctx context.Context, name string, actor Actor, opts ...ClusterSingletonOption) (*PID, error) {
	x.called = true
	x.actorName = name
	x.actor = actor
	x.config = newClusterSingletonConfig(opts...)
	return nil, nil
}

// MockEnqueueSpySystem is an ActorSystem test double that intercepts the three recreation and release calls
// enqueueRelocation makes, so every dispatch branch and its failure path can run without a live cluster.
type MockEnqueueSpySystem struct {
	ActorSystem

	// recreateActorFn answers recreateActorFromWire.
	recreateActorFn func(*internalpb.Actor) error
	// recreateGrainFn answers recreateGrainFromWire.
	recreateGrainFn func(*internalpb.Grain) error
	// releaseLazyFn answers releaseGrainForLazyRelocation.
	releaseLazyFn func(*internalpb.Grain) error
}

// recreateActorFromWire defers to the recreateActorFn hook.
func (x *MockEnqueueSpySystem) recreateActorFromWire(_ context.Context, a *internalpb.Actor, _ string) error {
	return x.recreateActorFn(a)
}

// recreateGrainFromWire defers to the recreateGrainFn hook.
func (x *MockEnqueueSpySystem) recreateGrainFromWire(_ context.Context, g *internalpb.Grain, _ string) error {
	return x.recreateGrainFn(g)
}

// releaseGrainForLazyRelocation defers to the releaseLazyFn hook.
func (x *MockEnqueueSpySystem) releaseGrainForLazyRelocation(_ context.Context, g *internalpb.Grain, _ string) error {
	return x.releaseLazyFn(g)
}

// MockSupervisionSignalError is the error a test raises to trigger a supervision signal.
type MockSupervisionSignalError struct{}

// Error returns the fixed supervision signal message.
func (MockSupervisionSignalError) Error() string { return "supervision-signal" }

// MockMeterProvider is a meter provider that hands out an injected meter.
type MockMeterProvider struct {
	otelmetric.MeterProvider

	// meter replaces the embedded provider's meter when set.
	meter otelmetric.Meter
}

// Meter returns the injected meter, or the embedded provider's meter when none is set.
func (x *MockMeterProvider) Meter(name string, opts ...otelmetric.MeterOption) otelmetric.Meter {
	if x.meter != nil {
		return x.meter
	}
	return x.MeterProvider.Meter(name, opts...)
}

// MockRegisterCallbackFailingMeter is a meter whose callback registration always fails.
type MockRegisterCallbackFailingMeter struct {
	otelmetric.Meter

	// err is the error RegisterCallback reports.
	err error
}

// RegisterCallback fails with the injected error.
func (x MockRegisterCallbackFailingMeter) RegisterCallback(_ otelmetric.Callback, _ ...otelmetric.Observable) (otelmetric.Registration, error) {
	return nil, x.err
}

// MockInstrumentFailingMeter is a meter that fails to create the instruments named in failures.
type MockInstrumentFailingMeter struct {
	otelmetric.Meter

	// failures maps an instrument name to the error its creation reports.
	failures map[string]error
}

// Int64ObservableCounter fails for an instrument listed in failures and delegates otherwise.
func (x MockInstrumentFailingMeter) Int64ObservableCounter(name string, options ...otelmetric.Int64ObservableCounterOption) (otelmetric.Int64ObservableCounter, error) {
	if err, ok := x.failures[name]; ok {
		return nil, err
	}
	return x.Meter.Int64ObservableCounter(name, options...)
}

// Int64ObservableGauge fails for an instrument listed in failures and delegates otherwise.
func (x MockInstrumentFailingMeter) Int64ObservableGauge(name string, options ...otelmetric.Int64ObservableGaugeOption) (otelmetric.Int64ObservableGauge, error) {
	if err, ok := x.failures[name]; ok {
		return nil, err
	}
	return x.Meter.Int64ObservableGauge(name, options...)
}

// MockManualMeterProvider is a meter provider that hands out a MockManualMeter.
type MockManualMeterProvider struct {
	otelmetric.MeterProvider
	meter otelmetric.Meter
}

// NewMockManualMeterProvider returns a MockManualMeterProvider backed by a no-op meter.
func NewMockManualMeterProvider() *MockManualMeterProvider {
	delegate := noopmetric.NewMeterProvider()
	return &MockManualMeterProvider{
		MeterProvider: delegate,
		meter: &MockManualMeter{
			Meter: delegate.Meter("test"),
		},
	}
}

// Meter returns the manual meter.
func (x *MockManualMeterProvider) Meter(_ string, _ ...otelmetric.MeterOption) otelmetric.Meter {
	return x.meter
}

// MockManualMeter is a meter that keeps every registered callback so a test can invoke it by hand.
type MockManualMeter struct {
	otelmetric.Meter

	// callbacks holds the registered callbacks in registration order.
	callbacks []otelmetric.Callback
	// unregistered counts the registrations released through Unregister.
	unregistered int
}

// RegisterCallback keeps the callback and returns a registration that reports its release.
func (x *MockManualMeter) RegisterCallback(cb otelmetric.Callback, _ ...otelmetric.Observable) (otelmetric.Registration, error) {
	x.callbacks = append(x.callbacks, cb)
	return &MockTrackingRegistration{meter: x}, nil
}

// MockTrackingRegistration is a registration that counts its release on its meter.
type MockTrackingRegistration struct {
	noopmetric.Registration
	meter *MockManualMeter
}

// Unregister increments the meter's released registration count.
func (x *MockTrackingRegistration) Unregister() error {
	x.meter.unregistered++
	return nil
}

// MockImmediateMeter is a meter that runs every callback at registration time so its error surfaces at once.
type MockImmediateMeter struct {
	*MockManualMeter

	// system is switched into cluster mode before the callback runs.
	system *actorSystem
	// cluster is installed on system so the callback takes the cluster path.
	cluster cluster.Cluster
}

// RegisterCallback puts the system on the cluster path, runs the callback once and returns its error.
func (x *MockImmediateMeter) RegisterCallback(cb otelmetric.Callback, _ ...otelmetric.Observable) (otelmetric.Registration, error) {
	// the callback only reaches the cluster branch on a cluster-enabled system
	if x.system != nil {
		x.system.clusterEnabled.Store(true)
		x.system.cluster = x.cluster
	}

	observer := &MockManualObserver{}
	err := cb(context.Background(), observer)
	return noopmetric.Registration{}, err
}

// MockManualObserver is an observer that records every ObserveInt64 call by instrument type and value.
type MockManualObserver struct {
	noopmetric.Observer
	records []observeRecord
}

// ObserveInt64 records the observation under the instrument's Go type name.
func (x *MockManualObserver) ObserveInt64(obsrv otelmetric.Int64Observable, value int64, _ ...otelmetric.ObserveOption) {
	x.records = append(x.records, observeRecord{
		instrument: fmt.Sprintf("%T", obsrv),
		value:      value,
	})
}

// observeRecord is a single observation captured by MockManualObserver.
type observeRecord struct {
	instrument string
	value      int64
}

// MockRecordingMeterProvider is a meter provider that hands out a MockRecordingMeter.
type MockRecordingMeterProvider struct {
	otelmetric.MeterProvider
	meter *MockRecordingMeter
}

// NewMockRecordingMeterProvider returns a MockRecordingMeterProvider backed by a no-op meter.
func NewMockRecordingMeterProvider() *MockRecordingMeterProvider {
	delegate := noopmetric.NewMeterProvider()
	return &MockRecordingMeterProvider{
		MeterProvider: delegate,
		meter: &MockRecordingMeter{
			Meter: delegate.Meter("test"),
		},
	}
}

// Meter returns the recording meter.
func (x *MockRecordingMeterProvider) Meter(_ string, _ ...otelmetric.MeterOption) otelmetric.Meter {
	return x.meter
}

// MockRecordingMeter is a meter that keeps registered callbacks and creates instruments that remember their names.
type MockRecordingMeter struct {
	otelmetric.Meter
	callbacks []otelmetric.Callback
}

// Int64ObservableCounter returns a counter that remembers its instrument name.
func (x *MockRecordingMeter) Int64ObservableCounter(name string, _ ...otelmetric.Int64ObservableCounterOption) (otelmetric.Int64ObservableCounter, error) {
	return &MockNamedObservableCounter{name: name}, nil
}

// Int64ObservableGauge returns a gauge that remembers its instrument name.
func (x *MockRecordingMeter) Int64ObservableGauge(name string, _ ...otelmetric.Int64ObservableGaugeOption) (otelmetric.Int64ObservableGauge, error) {
	return &MockNamedObservableGauge{name: name}, nil
}

// RegisterCallback keeps the callback for a test to invoke by hand.
func (x *MockRecordingMeter) RegisterCallback(cb otelmetric.Callback, _ ...otelmetric.Observable) (otelmetric.Registration, error) {
	x.callbacks = append(x.callbacks, cb)
	return noopmetric.Registration{}, nil
}

// MockNamedObservableCounter is a no-op observable counter that remembers the instrument name it was created with.
type MockNamedObservableCounter struct {
	noopmetric.Int64ObservableCounter
	name string
}

// MockNamedObservableGauge is a no-op observable gauge that remembers the instrument name it was created with.
type MockNamedObservableGauge struct {
	noopmetric.Int64ObservableGauge
	name string
}

// MockAttrObserver is an observer that records every ObserveInt64 call with its instrument, value and attributes.
type MockAttrObserver struct {
	noopmetric.Observer
	records []attrObserveRecord
}

// ObserveInt64 records the observation under the instrument name, falling back to its Go type name.
func (x *MockAttrObserver) ObserveInt64(obsrv otelmetric.Int64Observable, value int64, opts ...otelmetric.ObserveOption) {
	name := fmt.Sprintf("%T", obsrv)
	switch named := obsrv.(type) {
	case *MockNamedObservableCounter:
		name = named.name
	case *MockNamedObservableGauge:
		name = named.name
	}

	x.records = append(x.records, attrObserveRecord{
		instrument: name,
		value:      value,
		attrs:      otelmetric.NewObserveConfig(opts).Attributes(),
	})
}

// attrObserveRecord is a single observation captured by MockAttrObserver.
type attrObserveRecord struct {
	instrument string
	value      int64
	attrs      attribute.Set
}

// MockNthCallbackFailingMeter is a meter that fails the failOn-th RegisterCallback call and succeeds on every other.
type MockNthCallbackFailingMeter struct {
	otelmetric.Meter

	// calls counts the RegisterCallback calls made so far.
	calls int
	// failOn is the one-based index of the call that fails.
	failOn int
	// err is the error the failing call reports.
	err error
}

// RegisterCallback fails on the failOn-th call and succeeds otherwise.
func (x *MockNthCallbackFailingMeter) RegisterCallback(_ otelmetric.Callback, _ ...otelmetric.Observable) (otelmetric.Registration, error) {
	x.calls++
	if x.calls == x.failOn {
		return nil, x.err
	}

	return noopmetric.Registration{}, nil
}

// MockCallbackCapturingMeter is a meter that records the callbacks a metrics-enabled system registers, so a test can
// drive a full scrape by invoking them directly.
type MockCallbackCapturingMeter struct {
	otelmetric.Meter

	// callbacks holds the registered callbacks in registration order.
	callbacks []otelmetric.Callback
}

// RegisterCallback captures the callback and returns a no-op registration.
func (x *MockCallbackCapturingMeter) RegisterCallback(cb otelmetric.Callback, _ ...otelmetric.Observable) (otelmetric.Registration, error) {
	x.callbacks = append(x.callbacks, cb)
	return noopmetric.Registration{}, nil
}

// MockCallbackCapturingMeterProvider is a meter provider that hands out its MockCallbackCapturingMeter for every
// requested meter name.
type MockCallbackCapturingMeterProvider struct {
	otelmetric.MeterProvider
	meter *MockCallbackCapturingMeter
}

// Meter returns the capturing meter.
func (x *MockCallbackCapturingMeterProvider) Meter(string, ...otelmetric.MeterOption) otelmetric.Meter {
	return x.meter
}
