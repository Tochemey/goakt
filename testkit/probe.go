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

	"github.com/tochemey/goakt/v2/actors"
	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/internal/timer"
)

const (
	MessagesQueueMax int           = 1000
	DefaultTimeout   time.Duration = 3 * time.Second
)

// Probe defines the probe interface that helps perform some assertions
// when implementing unit tests with actors
type Probe interface {
	// ExpectMessage asserts that the message received from the test actor is the expected one
	ExpectMessage(message proto.Message)
	// ExpectMessageWithin asserts that the message received from the test actor is the expected one within a time duration
	ExpectMessageWithin(duration time.Duration, message proto.Message)
	// ExpectNoMessage asserts that no message is expected
	ExpectNoMessage()
	// ExpectAnyMessage asserts that any message is expected
	ExpectAnyMessage() proto.Message
	// ExpectAnyMessageWithin asserts that any message within a time duration
	ExpectAnyMessageWithin(duration time.Duration) proto.Message
	// ExpectMessageOfType asserts the expectation of a given message type
	ExpectMessageOfType(messageType protoreflect.MessageType)
	// ExpectMessageOfTypeWithin asserts the expectation of a given message type within a time duration
	ExpectMessageOfTypeWithin(duration time.Duration, messageType protoreflect.MessageType)
	// Send sends a message to the actor to be tested.
	// This method is only used when one to assert that the actor to be tested is able to respond when a Tell message is sent.
	Send(actorName string, message proto.Message)
	// SendSync sends a message to the actor to be tested and expect a response within a time duration.
	// This method is only used when one to assert that the actor to be tested is able to respond when an Ask message is sent.
	SendSync(actorName string, message proto.Message, timeout time.Duration)
	// Sender returns the sender of last received message.
	Sender() *actors.PID
	// PID returns the pid of the test actor
	PID() *actors.PID
	// Stop stops the test probe
	Stop()
}

type message struct {
	sender  *actors.PID
	payload proto.Message
}

type probeActor struct {
	messageQueue chan message
}

// ensure that probeActor implements the Actor interface
var _ actors.Actor = &probeActor{}

// PreStart is called before the actor starts
func (x *probeActor) PreStart(_ context.Context) error {
	return nil
}

// Receive handle message received
func (x *probeActor) Receive(ctx *actors.ReceiveContext) {
	switch ctx.Message().(type) {
	// skip system message
	case *goaktpb.PoisonPill,
		*goaktpb.Terminated,
		*goaktpb.PostStart:
	// pass
	default:
		// any message received is pushed to the queue
		x.messageQueue <- message{
			sender:  ctx.Sender(),
			payload: ctx.Message(),
		}
	}
}

// PostStop handles stop routines
func (x *probeActor) PostStop(_ context.Context) error {
	return nil
}

// probe defines the test probe implementation
type probe struct {
	pt *testing.T

	testCtx        context.Context
	pid            *actors.PID
	lastMessage    proto.Message
	lastSender     *actors.PID
	messageQueue   chan message
	defaultTimeout time.Duration
	timers         *timer.Pool
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
	pid, err := actorSystem.Spawn(ctx, "probActor", actor)
	if err != nil {
		return nil, err
	}
	// create an instance of the testProbe and return it
	return &probe{
		pt:             t,
		testCtx:        ctx,
		pid:            pid,
		messageQueue:   msgQueue,
		defaultTimeout: DefaultTimeout,
		timers:         timer.NewPool(),
	}, nil
}

// ExpectMessageOfType asserts the expectation of a given message type
func (x *probe) ExpectMessageOfType(messageType protoreflect.MessageType) {
	x.expectMessageOfType(x.defaultTimeout, messageType)
}

// ExpectMessageOfTypeWithin asserts the expectation of a given message type within a time duration
func (x *probe) ExpectMessageOfTypeWithin(duration time.Duration, messageType protoreflect.MessageType) {
	x.expectMessageOfType(duration, messageType)
}

// ExpectMessage assert message expectation
func (x *probe) ExpectMessage(message proto.Message) {
	x.expectMessage(x.defaultTimeout, message)
}

// ExpectMessageWithin expects message within a time duration
func (x *probe) ExpectMessageWithin(duration time.Duration, message proto.Message) {
	x.expectMessage(duration, message)
}

// ExpectNoMessage expects no message
func (x *probe) ExpectNoMessage() {
	x.expectNoMessage(x.defaultTimeout)
}

// ExpectAnyMessage expects any message
func (x *probe) ExpectAnyMessage() proto.Message {
	return x.expectAnyMessage(x.defaultTimeout)
}

// ExpectAnyMessageWithin expects any message within a time duration
func (x *probe) ExpectAnyMessageWithin(duration time.Duration) proto.Message {
	return x.expectAnyMessage(duration)
}

// Send sends a message to the actor to be tested.
// This method is only used when one to assert that the actor to be tested is able to respond when a Tell message is sent.
func (x *probe) Send(actorName string, message proto.Message) {
	to, err := x.pid.ActorSystem().LocalActor(actorName)
	require.NoError(x.pt, err)
	require.NoError(x.pt, x.pid.Tell(x.testCtx, to, message))
}

// SendSync sends a message to the actor to be tested and expect a response within a time duration.
// This method is only used when one to assert that the actor to be tested is able to respond when an Ask message is sent.
func (x *probe) SendSync(actorName string, msg proto.Message, timeout time.Duration) {
	to, err := x.pid.ActorSystem().LocalActor(actorName)
	require.NoError(x.pt, err)
	received, err := x.pid.Ask(x.testCtx, to, msg, timeout)
	require.NoError(x.pt, err)
	x.messageQueue <- message{
		sender:  to,
		payload: received,
	}
}

// Sender returns the last sender
func (x *probe) Sender() *actors.PID {
	return x.lastSender
}

// PID returns the pid of the test actor
func (x *probe) PID() *actors.PID {
	return x.pid
}

// Stop stops the test probe
func (x *probe) Stop() {
	// stop the prob
	err := x.pid.Shutdown(x.testCtx)
	// TODO: add some graceful context cancellation
	require.NoError(x.pt, err)
}

// receiveOne receives one message within a maximum time duration
func (x *probe) receiveOne(max time.Duration) proto.Message {
	timer := x.timers.Get(max)

	select {
	// attempt to read some message from the message queue
	case m, ok := <-x.messageQueue:
		x.timers.Put(timer)
		// nothing found
		if !ok {
			return nil
		}

		// found some message then set the lastMessage and lastSender
		if m.payload != nil {
			x.lastMessage = m.payload
			x.lastSender = m.sender
		}
		return m.payload
	case <-timer.C:
		x.timers.Put(timer)
		return nil
	}
}

// expectMessage assert the expectation of a message within a maximum time duration
func (x *probe) expectMessage(max time.Duration, message proto.Message) {
	// receive one message
	received := x.receiveOne(max)
	// let us assert the received message
	require.NotNil(x.pt, received, fmt.Sprintf("timeout (%v) during expectMessage while waiting for %v", max, message))
	require.Equal(x.pt, prototext.Format(message), prototext.Format(received), fmt.Sprintf("expected %v, found %v", message, received))
}

// expectNoMessage asserts that no message is expected
func (x *probe) expectNoMessage(max time.Duration) {
	// receive one message
	received := x.receiveOne(max)
	require.Nil(x.pt, received, fmt.Sprintf("received unexpected message %v", received))
}

// expectedAnyMessage asserts that any message is expected
func (x *probe) expectAnyMessage(max time.Duration) proto.Message {
	// receive one message
	received := x.receiveOne(max)
	require.NotNil(x.pt, received, fmt.Sprintf("timeout (%v) during expectAnyMessage while waiting", max))
	return received
}

// expectMessageOfType asserts that a message of a given type is expected within a maximum time duration
func (x *probe) expectMessageOfType(max time.Duration, messageType protoreflect.MessageType) proto.Message {
	// receive one message
	received := x.receiveOne(max)
	require.NotNil(x.pt, received, fmt.Sprintf("timeout (%v) , during expectAnyMessage while waiting", max))

	// assert the message type
	expectedType := received.ProtoReflect().Type() == messageType
	require.True(x.pt, expectedType, fmt.Sprintf("expected %v, found %v", messageType, received.ProtoReflect().Type()))
	return received
}
