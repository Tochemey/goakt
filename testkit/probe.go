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

package testkit

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/eventstream"
	"github.com/tochemey/goakt/v4/internal/timer"
)

// probeCounter is a global atomic counter used to generate unique probe actor names.
// Each call to newProbe increments this counter to avoid name collisions when
// multiple probes are created within the same test or across tests in the same process.
var probeCounter atomic.Uint64

const (
	// MessagesQueueMax is the maximum number of messages that can be buffered
	// in a probe's internal queue before senders block.
	MessagesQueueMax int = 1000

	// DefaultTimeout is the default duration used by Probe assertion methods
	// (e.g., ExpectMessage, ExpectAnyMessage) when no explicit timeout is provided.
	DefaultTimeout time.Duration = 3 * time.Second

	// GrainDefaultTimeout is the default duration used by GrainProbe assertion methods.
	GrainDefaultTimeout time.Duration = 500 * time.Millisecond
)

// Probe defines the interface for a test probe used in actor-based unit testing.
// A Probe acts as a test actor that can send and receive messages, making it
// useful for verifying the behavior of other actors in controlled scenarios.
//
// Typical usage:
//
//	probe := testkit.NewProbe(ctx)
//	probe.Send("my-actor", &MyRequest{})
//	probe.ExpectMessage(&MyResponse{})
//	probe.ExpectNoMessage()
//	probe.Stop()
type Probe interface {
	// ExpectMessage asserts that the next message received by the probe
	// exactly matches the given message using reflect.DeepEqual.
	// It fails the test if no message is received within the default timeout
	// or the message does not match.
	ExpectMessage(message any)

	// ExpectMessageWithin asserts that the expected message is received within the given duration.
	// It fails the test if the timeout is reached or the message differs.
	ExpectMessageWithin(duration time.Duration, message any)

	// ExpectNoMessage asserts that no message is received within the default timeout.
	// This is useful for asserting inactivity or idle actors.
	ExpectNoMessage()

	// ExpectAnyMessage waits for and returns the next message received by the probe.
	// It fails the test if no message is received within the default timeout.
	ExpectAnyMessage() any

	// ExpectAnyMessageWithin waits for and returns the next message received within the specified duration.
	// It fails the test if no message is received in the given time window.
	ExpectAnyMessageWithin(duration time.Duration) any

	// ExpectMessageOfType asserts that the next received message has the same Go type
	// as the provided example value. It fails the test if no message is received
	// within the default timeout or the type does not match.
	ExpectMessageOfType(message any)

	// ExpectMessageOfTypeWithin asserts that a message of the given type is received within the specified duration.
	// It fails the test if the type does not match or if the timeout is reached.
	ExpectMessageOfTypeWithin(duration time.Duration, message any)

	// ExpectTerminated asserts that the actor with the specified name has terminated
	// within the default timeout. The probe must be watching the actor via Watch or WatchNamed
	// before calling this method.
	ExpectTerminated(actorName string)

	// ExpectTerminatedWithin asserts that the actor with the specified name has terminated
	// within the given duration. The probe must be watching the actor via Watch or WatchNamed
	// before calling this method.
	//
	// Example:
	//
	//	probe.WatchNamed("worker")
	//	kit.Kill(ctx, "worker")
	//	probe.ExpectTerminatedWithin("worker", 5*time.Second)
	ExpectTerminatedWithin(actorName string, duration time.Duration)

	// ExpectMessageMatching waits for the next message and asserts that it satisfies
	// the given predicate function. It fails the test if no message is received within
	// the default timeout or the predicate returns false.
	//
	// This is useful when you need to assert on a subset of message fields rather than
	// requiring an exact match:
	//
	//	probe.ExpectMessageMatching(func(msg any) bool {
	//	    resp, ok := msg.(*OrderResponse)
	//	    return ok && resp.Status == "confirmed"
	//	})
	ExpectMessageMatching(predicate func(any) bool)

	// ClearMessages drains all pending messages from the probe's internal queue.
	// This is useful between test phases when earlier messages are no longer relevant
	// and you want to assert only on subsequent messages.
	//
	// Note: calling ClearMessages does not affect the value returned by Sender(),
	// which continues to reflect the last message received via an Expect method.
	ClearMessages()

	// Send delivers a message to the actor identified by its registered name.
	// This simulates a "Tell" pattern (fire-and-forget), where the sender does not expect a reply.
	//
	// This method is primarily used in test scenarios to validate how an actor handles incoming messages
	// without waiting for a response. It is especially useful for verifying side effects or internal state changes
	// triggered by the message.
	//
	// Parameters:
	//   - actorName: the name of the target actor registered within the actor system.
	//   - message: the message to send to the actor.
	//
	// Behavior:
	//   - If the target actor does not exist, is unregistered, or is unreachable, the test will immediately fail.
	//   - This method is designed for use with test probes and assumes that actor names are globally unique
	//     or properly namespaced within the actor system.
	//
	// Notes:
	//   - This is a convenience method for testing fire-and-forget scenarios.
	//   - For request-response testing, consider using SendSync instead.
	Send(actorName string, message any)

	// SendSync sends a message to the specified actor and waits for a synchronous response within the given timeout duration.
	// This method simulates the "Ask" pattern (request-response) and is primarily used in test scenarios to
	// validate an actor's reply behavior and response correctness.
	//
	// The response is placed on the probe's internal queue and can be asserted using
	// ExpectMessage, ExpectAnyMessage, or any other Expect method.
	//
	// Parameters:
	//   - actorName: the registered name of the target actor to which the message should be sent.
	//   - message: the message to send to the actor.
	//   - timeout: the duration to wait for a response before failing the test.
	//
	// Example:
	//
	//	probe.SendSync("calculator", &AddRequest{A: 1, B: 2}, time.Second)
	//	probe.ExpectMessage(&AddResponse{Result: 3})
	SendSync(actorName string, message any, timeout time.Duration)

	// Sender returns the PID of the sender of the last received message.
	// This is set whenever an Expect method successfully receives a message with a non-nil payload.
	// For remote senders, use pid.IsRemote() / pid.Path() on the returned PID.
	Sender() *actor.PID

	// PID returns the PID of the probe actor itself.
	// This can be passed to other actors when they need a sender reference to reply to.
	PID() *actor.PID

	// WatchNamed subscribes the probe to termination notifications for the specified actor given its name.
	// Once the watched actor stops or crashes, the probe will receive a Terminated message
	// that can be asserted with ExpectTerminated or ExpectTerminatedWithin.
	//
	// Example:
	//
	//	probe.WatchNamed("worker-actor")
	//	// perform actions that should lead to actor termination
	//	probe.ExpectTerminated("worker-actor")
	WatchNamed(actorName string)

	// Watch subscribes the probe to termination notifications for the specified actor PID.
	// When the watched actor stops—either gracefully or due to failure—the probe will
	// receive a Terminated message that can be asserted with ExpectTerminated.
	//
	// Example:
	//
	//	workerPID, _ := system.Spawn(ctx, "worker", &Worker{})
	//	probe.Watch(workerPID)
	//	// trigger actor shutdown
	//	probe.ExpectTerminated(workerPID.Name())
	Watch(pid *actor.PID)

	// ExpectChildSpawned asserts that a child actor with the given name was spawned
	// by the specified parent actor within the default timeout.
	// It subscribes to the actor system's event stream and waits for a matching
	// ActorChildCreated event.
	ExpectChildSpawned(parentName string, childName string)

	// ExpectChildSpawnedWithin asserts that a child actor with the given name was spawned
	// by the specified parent actor within the given duration.
	// It subscribes to the actor system's event stream and waits for a matching
	// ActorChildCreated event.
	ExpectChildSpawnedWithin(parentName string, childName string, duration time.Duration)

	// Stop stops the probe actor and releases any associated resources.
	// This should be called at the end of a test to clean up the probe.
	Stop()
}

type message struct {
	sender  *actor.PID
	payload any
}

type probeActor struct {
	messageQueue chan message
}

var _ actor.Actor = &probeActor{}

// probe defines the test probe implementation.
type probe struct {
	testingT *testing.T

	testCtx        context.Context
	actorSystem    actor.ActorSystem
	pid            *actor.PID
	lastSender     *actor.PID
	messageQueue   chan message
	subscriber     eventstream.Subscriber
	defaultTimeout time.Duration
	timers         *timer.Pool
}

var _ Probe = (*probe)(nil)

// ExpectMessageOfType asserts that the next received message matches the given message type.
// It fails the test if no message is received or the type does not match.
func (x *probe) ExpectMessageOfType(message any) {
	x.expectMessageOfType(x.defaultTimeout, reflect.TypeOf(message))
}

// ExpectMessageOfTypeWithin asserts that a message of the given type is received within the specified duration.
// It fails the test if the type does not match or if the timeout is reached.
func (x *probe) ExpectMessageOfTypeWithin(duration time.Duration, message any) {
	x.expectMessageOfType(duration, reflect.TypeOf(message))
}

// ExpectTerminated asserts that the actor with the specified name has terminated.
// It uses the default timeout.
func (x *probe) ExpectTerminated(actorName string) {
	x.ExpectTerminatedWithin(actorName, x.defaultTimeout)
}

// ExpectTerminatedWithin asserts that the actor with the specified name has terminated
// within the given duration.
func (x *probe) ExpectTerminatedWithin(actorName string, duration time.Duration) {
	received := x.receiveOne(duration)
	require.NotNil(x.testingT, received, fmt.Sprintf("timeout (%v) during ExpectTerminatedWithin while waiting for %s", duration, actorName))
	require.IsType(x.testingT, &actor.Terminated{}, received)
	require.NotNil(x.testingT, x.lastSender, fmt.Sprintf("received Terminated but sender is nil while waiting for %s", actorName))
	require.Equal(x.testingT, actorName, x.lastSender.Name(), fmt.Sprintf("expected Terminated from %v", actorName))
}

// ExpectMessageMatching waits for the next message and asserts that it satisfies
// the given predicate function. It fails the test if no message is received or the
// predicate returns false.
func (x *probe) ExpectMessageMatching(predicate func(any) bool) {
	received := x.receiveOne(x.defaultTimeout)
	require.NotNil(x.testingT, received, fmt.Sprintf("timeout (%v) during ExpectMessageMatching while waiting", x.defaultTimeout))
	require.True(x.testingT, predicate(received), fmt.Sprintf("message %v did not match predicate", received))
}

// ClearMessages drains all pending messages from the probe's internal queue.
func (x *probe) ClearMessages() {
	for {
		select {
		case <-x.messageQueue:
		default:
			return
		}
	}
}

// ExpectMessage asserts that the next message received by the probe
// exactly matches the given message.
// It fails the test if no message is received or the message does not match.
func (x *probe) ExpectMessage(message any) {
	x.expectMessage(x.defaultTimeout, message)
}

// ExpectMessageWithin asserts that the expected message is received within the given duration.
// It fails the test if the timeout is reached or the message differs.
func (x *probe) ExpectMessageWithin(duration time.Duration, message any) {
	x.expectMessage(duration, message)
}

// ExpectNoMessage asserts that no message is received within the default timeout.
// This is useful for asserting inactivity or silenced actors.
func (x *probe) ExpectNoMessage() {
	x.expectNoMessage(x.defaultTimeout)
}

// ExpectAnyMessage waits for and returns the next message received by the probe.
// It fails the test if no message is received within the default timeout.
func (x *probe) ExpectAnyMessage() any {
	return x.expectAnyMessage(x.defaultTimeout)
}

// ExpectAnyMessageWithin waits for and returns the next message received within the specified duration.
// It fails the test if no message is received in the given time window.
func (x *probe) ExpectAnyMessageWithin(duration time.Duration) any {
	return x.expectAnyMessage(duration)
}

// Send delivers a message to the actor identified by its registered name.
// This simulates a "Tell" pattern (fire-and-forget), where the sender does not expect a reply.
func (x *probe) Send(actorName string, message any) {
	if x.pid.ActorSystem().InCluster() {
		err := x.pid.SendAsync(x.testCtx, actorName, message)
		require.NoError(x.testingT, err)
		return
	}

	to, err := x.pid.ActorSystem().ActorOf(x.testCtx, actorName)
	require.NoError(x.testingT, err)
	require.NoError(x.testingT, x.pid.Tell(x.testCtx, to, message))
}

// SendSync sends a message to the specified actor and waits for a synchronous response
// within the given timeout duration. The response is pushed onto the probe's internal queue
// and can be asserted with any Expect method.
func (x *probe) SendSync(actorName string, msg any, timeout time.Duration) {
	var (
		received any
		err      error
		to       *actor.PID
	)

	to, err = x.pid.ActorSystem().ActorOf(x.testCtx, actorName)
	require.NoError(x.testingT, err)
	if x.pid.ActorSystem().InCluster() {
		received, err = x.pid.SendSync(x.testCtx, actorName, msg, timeout)
		require.NoError(x.testingT, err)
	} else {
		received, err = x.pid.Ask(x.testCtx, to, msg, timeout)
		require.NoError(x.testingT, err)
	}

	x.messageQueue <- message{
		sender:  to,
		payload: received,
	}
}

// Sender returns the PID of the sender of the last received message.
func (x *probe) Sender() *actor.PID {
	return x.lastSender
}

// PID returns the PID of the probe actor itself.
func (x *probe) PID() *actor.PID {
	return x.pid
}

// WatchNamed subscribes the probe to termination notifications for the specified actor given its name.
func (x *probe) WatchNamed(actorName string) {
	to, err := x.pid.ActorSystem().ActorOf(x.testCtx, actorName)
	require.NoError(x.testingT, err)
	x.pid.Watch(to)
}

// Watch subscribes the probe to termination notifications for the specified actor PID.
func (x *probe) Watch(pid *actor.PID) {
	x.pid.Watch(pid)
}

// ExpectChildSpawned asserts that a child actor with the given name was spawned
// by the specified parent actor within the default timeout.
func (x *probe) ExpectChildSpawned(parentName string, childName string) {
	x.expectChildSpawned(parentName, childName, x.defaultTimeout)
}

// ExpectChildSpawnedWithin asserts that a child actor with the given name was spawned
// by the specified parent actor within the given duration.
func (x *probe) ExpectChildSpawnedWithin(parentName string, childName string, duration time.Duration) {
	x.expectChildSpawned(parentName, childName, duration)
}

// Stop stops the probe actor and releases any associated resources.
func (x *probe) Stop() {
	x.subscriber.Shutdown()
	_ = x.actorSystem.Unsubscribe(x.subscriber)
	err := x.pid.Shutdown(x.testCtx)
	require.NoError(x.testingT, err)
}

// newProbe creates an instance of probe with a unique actor name.
func newProbe(ctx context.Context, actorSystem actor.ActorSystem, t *testing.T) (*probe, error) {
	msgQueue := make(chan message, MessagesQueueMax)
	pa := &probeActor{messageQueue: msgQueue}
	name := fmt.Sprintf("probe-%d", probeCounter.Inc())
	pid, err := actorSystem.Spawn(ctx, name, pa)
	if err != nil {
		return nil, err
	}

	subscriber, err := actorSystem.Subscribe()
	if err != nil {
		return nil, err
	}

	return &probe{
		testingT:       t,
		testCtx:        ctx,
		actorSystem:    actorSystem,
		pid:            pid,
		messageQueue:   msgQueue,
		subscriber:     subscriber,
		defaultTimeout: DefaultTimeout,
		timers:         timer.NewPool(),
	}, nil
}

// PreStart is called before the actor starts.
func (x *probeActor) PreStart(_ *actor.Context) error {
	return nil
}

// Receive handles received messages for the probe actor.
// All messages except PoisonPill and PostStart are pushed to the internal queue.
func (x *probeActor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *actor.PoisonPill,
		*actor.PostStart:
	default:
		x.messageQueue <- message{
			sender:  ctx.Sender(),
			payload: ctx.Message(),
		}
	}
}

// PostStop handles stop routines.
func (x *probeActor) PostStop(_ *actor.Context) error {
	return nil
}

func (x *probe) receiveOne(duration time.Duration) any {
	t := x.timers.Get(duration)

	select {
	case m, ok := <-x.messageQueue:
		x.timers.Put(t)
		if !ok {
			return nil
		}

		if m.payload != nil {
			x.lastSender = m.sender
		}
		return m.payload
	case <-t.C:
		x.timers.Put(t)
		return nil
	}
}

func (x *probe) expectMessage(duration time.Duration, message any) {
	received := x.receiveOne(duration)
	require.NotNil(x.testingT, received, fmt.Sprintf("timeout (%v) during expectMessage while waiting for %T", duration, message))
	require.IsType(x.testingT, message, received)
	require.True(x.testingT, reflect.DeepEqual(message, received), fmt.Sprintf("expected %v, found %v", message, received))
}

func (x *probe) expectNoMessage(duration time.Duration) {
	received := x.receiveOne(duration)
	require.Nil(x.testingT, received, fmt.Sprintf("received unexpected message %v", received))
}

func (x *probe) expectAnyMessage(duration time.Duration) any {
	received := x.receiveOne(duration)
	require.NotNil(x.testingT, received, fmt.Sprintf("timeout (%v) during expectAnyMessage while waiting", duration))
	return received
}

func (x *probe) expectMessageOfType(duration time.Duration, messageType any) {
	received := x.receiveOne(duration)
	require.NotNil(x.testingT, received, fmt.Sprintf("timeout (%v) during expectMessageOfType while waiting", duration))
	expectedType := reflect.TypeOf(received) == messageType
	require.True(x.testingT, expectedType, fmt.Sprintf("expected %v, found %v", messageType, reflect.TypeOf(received)))
}

func (x *probe) expectChildSpawned(parentName, childName string, duration time.Duration) {
	deadline := time.After(duration)
	poll := time.NewTicker(50 * time.Millisecond)
	defer poll.Stop()

	for {
		for msg := range x.subscriber.Iterator() {
			if event, ok := msg.Payload().(*actor.ActorChildCreated); ok {
				if event.Parent().Name() == parentName && event.ActorPath().Name() == childName {
					return
				}
			}
		}

		select {
		case <-deadline:
			require.Fail(x.testingT, fmt.Sprintf("timeout (%v) waiting for child %q to be spawned by %q", duration, childName, parentName))
			return
		case <-poll.C:
		}
	}
}
