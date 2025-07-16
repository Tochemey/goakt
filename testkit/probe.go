/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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

package testkit

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	actors "github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/address"
	"github.com/tochemey/goakt/v4/goaktpb"
	"github.com/tochemey/goakt/v4/internal/timer"
)

const (
	MessagesQueueMax int           = 1000
	DefaultTimeout   time.Duration = 3 * time.Second
)

// Probe defines the interface for a test probe used in actor-based unit testing.
// A Probe acts as a test actor that can send and receive messages, making it
// useful for verifying the behavior of other actors in controlled scenarios.
type Probe interface {
	// ExpectMessage asserts that the next message received by the probe
	// exactly matches the given protobuf message.
	// It fails the test if no message is received or the message does not match.
	ExpectMessage(message proto.Message)

	// ExpectMessageWithin asserts that the expected message is received within the given duration.
	// It fails the test if the timeout is reached or the message differs.
	ExpectMessageWithin(duration time.Duration, message proto.Message)

	// ExpectNoMessage asserts that no message is received within a short, default time window.
	// This is useful for asserting inactivity or idle actors.
	ExpectNoMessage()

	// ExpectAnyMessage waits for and returns the next message received by the probe.
	// It fails the test if no message is received in a reasonable default timeout.
	ExpectAnyMessage() proto.Message

	// ExpectAnyMessageWithin waits for and returns the next message received within the specified duration.
	// It fails the test if no message is received in the given time window.
	ExpectAnyMessageWithin(duration time.Duration) proto.Message

	// ExpectMessageOfType asserts that the next received message matches the given message type.
	// It fails the test if no message is received or the type does not match.
	ExpectMessageOfType(messageType protoreflect.MessageType)

	// ExpectMessageOfTypeWithin asserts that a message of the given type is received within the specified duration.
	// It fails the test if the type does not match or if the timeout is reached.
	ExpectMessageOfTypeWithin(duration time.Duration, messageType protoreflect.MessageType)

	// ExpectTerminated asserts that the actor with the specified name has terminated.
	// This is useful when verifying actor shutdown behavior.
	ExpectTerminated(actorName string)

	// Send delivers a message to the actor identified by its registered name.
	// This simulates a "Tell" pattern (fire-and-forget), where the sender does not expect a reply.
	//
	// This method is primarily used in test scenarios to validate how an actor handles incoming messages
	// without waiting for a response. It is especially useful for verifying side effects or internal state changes
	// triggered by the message.
	//
	// Parameters:
	//   - actorName: the name of the target actor registered within the actor system.
	//   - message: the protocol buffer message to send to the actor.
	//
	// Behavior:
	//   - If the target actor does not exist, is unregistered, or is unreachable, the test will immediately fail.
	//   - This method is designed for use with test probes and assumes that actor names are globally unique
	//     or properly namespaced within the actor system.
	//
	// Notes:
	//   - This is a convenience method for testing fire-and-forget scenarios.
	//   - For request-response testing, consider using SendSync or SendSyncContext instead.
	Send(actorName string, message proto.Message)

	// SendSync sends a message to the specified actor and waits for a synchronous response within the given timeout duration.
	// This method simulates the "Ask" pattern (request-response) and is primarily used in test scenarios to
	// validate an actor's reply behavior and response correctness.
	//
	//
	// Parameters:
	//   - actorName: the registered name of the target actor to which the message should be sent.
	//   - msg: the protocol buffer message to send to the actor.
	//   - timeout: the duration to wait for a response before failing the test.
	//
	// Returns:
	//   - The response as a proto.Message if the actor responds within the timeout period.
	//   - The test will fail immediately if:
	//       - The actor does not exist or is not reachable.
	//       - The actor fails to respond within the timeout.
	//       - An internal error occurs during the send or receive operation.
	//
	// Notes:
	//   - This method is intended for use within testing frameworks via a test probe.
	//   - Should not be used in production code as it couples testing and actor system internals.
	SendSync(actorName string, message proto.Message, timeout time.Duration)

	// Sender returns the PID (Process Identifier) of the sender of the last received message.
	// This is useful when testing interactions involving message origins.
	// When running the multi-node test, this will return a nil PID if the message was sent from a remote actor.
	// In that case the remote sender can be retrieved using the SenderAddress() method.
	Sender() *actors.PID

	// SenderAddress returns the address of the sender of the last received message.
	// This is useful when testing interactions involving message origins, especially in multi-node scenarios.
	SenderAddress() *address.Address

	// PID returns the PID of the probe itself, which can be used as the sender
	// in test scenarios where the tested actor expects a sender reference.
	PID() *actors.PID

	// WatchNamed subscribes the probe to termination notifications for the specified actor given its name.
	// Once the watched actor stops or crashes, the probe will receive a Terminated message.
	//
	// This is useful for asserting that an actor shuts down as expected during the test.
	//
	// Example usage:
	//   probe.WatchNamed("worker-actor")
	//   // perform actions that should lead to actor termination
	//   probe.ExpectTerminated("worker-actor")
	WatchNamed(actorName string)

	// Watch subscribes the probe to termination notifications for the specified actor PID.
	// When the watched actor stops—either gracefully or due to failure—the probe will
	// receive a Terminated message containing the PID of the terminated actor.
	//
	// This method is typically used to assert that an actor under test shuts down as expected.
	//
	// Example usage:
	//   workerPID, _ := system.Spawn(....)
	//   probe.Watch(workerPID)
	//   // trigger actor shutdown
	//   probe.ExpectTerminated(workerPID.Name())
	Watch(pid *actors.PID)

	// Stop stops the probe actor and releases any associated resources.
	// This should be called at the end of a test to clean up the probe.
	Stop()
}

type message struct {
	sender        *actors.PID
	senderAddress *address.Address
	payload       proto.Message
}

type probeActor struct {
	messageQueue chan message
}

// ensure that probeActor implements the Actor interface
var _ actors.Actor = &probeActor{}

// PreStart is called before the actor starts
func (x *probeActor) PreStart(_ *actors.Context) error {
	return nil
}

// Receive handles received messages for the probe actor.
func (x *probeActor) Receive(ctx *actors.ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PoisonPill,
		*goaktpb.PostStart:
	// pass
	default:
		// any message received is pushed to the queue
		x.messageQueue <- message{
			sender:        ctx.Sender(),
			payload:       ctx.Message(),
			senderAddress: ctx.RemoteSender(),
		}
	}
}

// PostStop handles stop routines
func (x *probeActor) PostStop(_ *actors.Context) error {
	return nil
}

// probe defines the test probe implementation
type probe struct {
	testingT *testing.T

	testCtx           context.Context
	pid               *actors.PID
	lastMessage       proto.Message
	lastSender        *actors.PID
	lastSenderAddress *address.Address
	messageQueue      chan message
	defaultTimeout    time.Duration
	timers            *timer.Pool
}

// ensure that probe implements Probe
var _ Probe = (*probe)(nil)

// newProbe creates an instance of probe
func newProbe(ctx context.Context, actorSystem actors.ActorSystem, t *testing.T) (*probe, error) {
	// create the message queue
	msgQueue := make(chan message, MessagesQueueMax)
	// create the test probe actor
	actor := &probeActor{messageQueue: msgQueue}
	// spawn the probe actor
	pid, err := actorSystem.Spawn(ctx, "probeActor", actor)
	if err != nil {
		return nil, err
	}
	// create an instance of the testProbe and return it
	return &probe{
		testingT:       t,
		testCtx:        ctx,
		pid:            pid,
		messageQueue:   msgQueue,
		defaultTimeout: DefaultTimeout,
		timers:         timer.NewPool(),
	}, nil
}

// ExpectMessageOfType asserts that the next received message matches the given message type.
// It fails the test if no message is received or the type does not match.
func (x *probe) ExpectMessageOfType(messageType protoreflect.MessageType) {
	x.expectMessageOfType(x.defaultTimeout, messageType)
}

// ExpectMessageOfTypeWithin asserts that a message of the given type is received within the specified duration.
// It fails the test if the type does not match or if the timeout is reached.
func (x *probe) ExpectMessageOfTypeWithin(duration time.Duration, messageType protoreflect.MessageType) {
	x.expectMessageOfType(duration, messageType)
}

// ExpectTerminated asserts that the actor with the specified name has terminated.
// This is useful when verifying actor shutdown behavior.
func (x *probe) ExpectTerminated(actorName string) {
	// receive one message
	received := x.receiveOne(x.defaultTimeout)
	require.NotNil(x.testingT, received, fmt.Sprintf("timeout (%v) during expectAnyMessage while waiting", x.defaultTimeout))
	require.IsType(x.testingT, &goaktpb.Terminated{}, received)
	lastSenderName := x.lastSender.Name()
	require.Equal(x.testingT, actorName, lastSenderName, fmt.Sprintf("expected Terminated from %v", actorName))
}

// ExpectMessage asserts that the next message received by the probe
// exactly matches the given protobuf message.
// It fails the test if no message is received or the message does not match.
func (x *probe) ExpectMessage(message proto.Message) {
	x.expectMessage(x.defaultTimeout, message)
}

// ExpectMessageWithin asserts that the expected message is received within the given duration.
// It fails the test if the timeout is reached or the message differs.
func (x *probe) ExpectMessageWithin(duration time.Duration, message proto.Message) {
	x.expectMessage(duration, message)
}

// ExpectNoMessage asserts that no message is received within a short, default time window.
// This is useful for asserting inactivity or silenced actors.
func (x *probe) ExpectNoMessage() {
	x.expectNoMessage(x.defaultTimeout)
}

// ExpectAnyMessage waits for and returns the next message received by the probe.
// It fails the test if no message is received in a reasonable default timeout.
func (x *probe) ExpectAnyMessage() proto.Message {
	return x.expectAnyMessage(x.defaultTimeout)
}

// ExpectAnyMessageWithin waits for and returns the next message received within the specified duration.
// It fails the test if no message is received in the given time window.
func (x *probe) ExpectAnyMessageWithin(duration time.Duration) proto.Message {
	return x.expectAnyMessage(duration)
}

// Send delivers a message to the actor identified by its registered name.
// This simulates a "Tell" pattern (fire-and-forget), where the sender does not expect a reply.
//
// This method is primarily used in test scenarios to validate how an actor handles incoming messages
// without waiting for a response. It is especially useful for verifying side effects or internal state changes
// triggered by the message.
//
// Parameters:
//   - actorName: the name of the target actor registered within the actor system.
//   - message: the protocol buffer message to send to the actor.
//
// Behavior:
//   - If the target actor does not exist, is unregistered, or is unreachable, the test will immediately fail.
//   - This method is designed for use with test probes and assumes that actor names are globally unique
//     or properly namespaced within the actor system.
//
// Notes:
//   - This is a convenience method for testing fire-and-forget scenarios.
//   - For request-response testing, consider using SendSync or SendSyncContext instead.
func (x *probe) Send(actorName string, message proto.Message) {
	if x.pid.ActorSystem().InCluster() {
		err := x.pid.SendAsync(x.testCtx, actorName, message)
		require.NoError(x.testingT, err)
		return
	}

	to, err := x.pid.ActorSystem().LocalActor(actorName)
	require.NoError(x.testingT, err)
	require.NoError(x.testingT, x.pid.Tell(x.testCtx, to, message))
}

// SendSync sends a message to the specified actor and waits for a synchronous response within the given timeout duration.
// This method simulates the "Ask" pattern (request-response) and is primarily used in test scenarios to
// validate an actor's reply behavior and response correctness.
//
// Unlike SendContext, which uses a context for cancellation and timeout, SendSync directly relies on the provided
// timeout value for awaiting a response.
//
// Parameters:
//   - actorName: the registered name of the target actor to which the message should be sent.
//   - msg: the protocol buffer message to send to the actor.
//   - timeout: the duration to wait for a response before failing the test.
//
// Returns:
//   - The response as a proto.Message if the actor responds within the timeout period.
//   - The test will fail immediately if:
//   - The actor does not exist or is not reachable.
//   - The actor fails to respond within the timeout.
//   - An internal error occurs during the send or receive operation.
//
// Notes:
//   - This method is intended for use within testing frameworks via a test probe.
//   - Should not be used in production code as it couples testing and actor system internals.
func (x *probe) SendSync(actorName string, msg proto.Message, timeout time.Duration) {
	var (
		received  proto.Message
		err       error
		to        *actors.PID
		toAddress *address.Address
	)

	if x.pid.ActorSystem().InCluster() {
		toAddress, err = x.pid.ActorSystem().RemoteActor(x.testCtx, actorName)
		require.NoError(x.testingT, err)
		require.NotNil(x.testingT, toAddress)
		require.False(x.testingT, toAddress.Equals(address.NoSender()))
		received, err = x.pid.SendSync(x.testCtx, actorName, msg, timeout)
		require.NoError(x.testingT, err)
	} else {
		to, err = x.pid.ActorSystem().LocalActor(actorName)
		require.NoError(x.testingT, err)
		received, err = x.pid.Ask(x.testCtx, to, msg, timeout)
		require.NoError(x.testingT, err)
		toAddress = to.Address()
	}

	x.messageQueue <- message{
		sender:        to,
		senderAddress: toAddress,
		payload:       received,
	}
}

// Sender returns the PID (Process Identifier) of the sender of the last received message.
// This is useful when testing interactions involving message origins.
func (x *probe) Sender() *actors.PID {
	return x.lastSender
}

// SenderAddress returns the address of the sender of the last received message.
// This is useful when testing interactions involving message origins, especially in multi-node scenarios.
func (x *probe) SenderAddress() *address.Address {
	return x.lastSenderAddress
}

// PID returns the PID of the probe itself, which can be used as the sender
// in test scenarios where the tested actor expects a sender reference.
func (x *probe) PID() *actors.PID {
	return x.pid
}

// WatchNamed subscribes the probe to termination notifications for the specified actor given its name.
// Once the watched actor stops or crashes, the probe will receive a Terminated message.
//
// This is useful for asserting that an actor shuts down as expected during the test.
//
// Example usage:
//
//	probe.WatchNamed("worker-actor")
//	// perform actions that should lead to actor termination
//	probe.ExpectTerminated("worker-actor")
func (x *probe) WatchNamed(actorName string) {
	to, err := x.pid.ActorSystem().LocalActor(actorName)
	require.NoError(x.testingT, err)
	x.pid.Watch(to)
}

// Watch subscribes the probe to termination notifications for the specified actor PID.
// When the watched actor stops—either gracefully or due to failure—the probe will
// receive a Terminated message containing the PID of the terminated actor.
//
// This method is typically used to assert that an actor under test shuts down as expected.
//
// Example usage:
//
//	workerPID := system.Spawn(workerProps)
//	probe.Watch(workerPID)
//	// trigger actor shutdown
//	probe.ExpectTerminated(workerPID.Name())
func (x *probe) Watch(pid *actors.PID) {
	x.pid.Watch(pid)
}

// Stop stops the probe actor and releases any associated resources.
// This should be called at the end of a test to clean up the probe.
func (x *probe) Stop() {
	// stop the prob
	err := x.pid.Shutdown(x.testCtx)
	// TODO: add some graceful context cancellation
	require.NoError(x.testingT, err)
}

// receiveOne receives one message within a maximum time duration
func (x *probe) receiveOne(duration time.Duration) proto.Message {
	t := x.timers.Get(duration)

	select {
	// attempt to read some message from the message queue
	case m, ok := <-x.messageQueue:
		x.timers.Put(t)
		// nothing found
		if !ok {
			return nil
		}

		// found some message then set the lastMessage and lastSender
		if m.payload != nil {
			x.lastMessage = m.payload
			x.lastSender = m.sender
			x.lastSenderAddress = m.senderAddress
		}
		return m.payload
	case <-t.C:
		x.timers.Put(t)
		return nil
	}
}

// expectMessage assert the expectation of a message within a maximum time duration
func (x *probe) expectMessage(duration time.Duration, message proto.Message) {
	// receive one message
	received := x.receiveOne(duration)
	// let us assert the received message
	require.NotNil(x.testingT, received, fmt.Sprintf("timeout (%v) during expectMessage while waiting for %v", duration, message.ProtoReflect().Descriptor().FullName()))
	require.Equal(x.testingT, prototext.Format(message), prototext.Format(received), fmt.Sprintf("expected %v, found %v", message, received))
}

// expectNoMessage asserts that no message is expected
func (x *probe) expectNoMessage(duration time.Duration) {
	// receive one message
	received := x.receiveOne(duration)
	require.Nil(x.testingT, received, fmt.Sprintf("received unexpected message %v", received))
}

// expectedAnyMessage asserts that any message is expected
func (x *probe) expectAnyMessage(duration time.Duration) proto.Message {
	// receive one message
	received := x.receiveOne(duration)
	require.NotNil(x.testingT, received, fmt.Sprintf("timeout (%v) during expectAnyMessage while waiting", duration))
	return received
}

// expectMessageOfType asserts that a message of a given type is expected within a maximum time duration
func (x *probe) expectMessageOfType(duration time.Duration, messageType protoreflect.MessageType) proto.Message {
	// receive one message
	received := x.receiveOne(duration)
	require.NotNil(x.testingT, received, fmt.Sprintf("timeout (%v) , during expectAnyMessage while waiting", duration))

	// assert the message type
	expectedType := received.ProtoReflect().Type() == messageType
	require.True(x.testingT, expectedType, fmt.Sprintf("expected %v, found %v", messageType, received.ProtoReflect().Type()))
	return received
}
