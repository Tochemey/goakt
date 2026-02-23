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

	goakt "github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/timer"
)

// GrainProbe defines a test helper for validating grain message handling.
// It provides expectations for responses, timeouts, and termination, plus
// helpers for Tell/Ask-style interactions in tests.
type GrainProbe interface {
	// ExpectResponse asserts that the next message received by the probe
	// exactly matches the given protobuf message.
	// It fails the test if no message is received or the message does not match.
	ExpectResponse(message any)

	// ExpectResponseWithin asserts that the expected message is received within the given duration.
	// It fails the test if the timeout is reached or the message differs.
	ExpectResponseWithin(duration time.Duration, message any)

	// ExpectNoResponse asserts that no message is received within a short, default time window.
	// This is useful for asserting inactivity or idle grains.
	ExpectNoResponse()

	// ExpectAnyResponse waits for and returns the next message received by the probe.
	// It fails the test if no message is received in a reasonable default timeout.
	ExpectAnyResponse() any

	// ExpectAnyResponseWithin waits for and returns the next message received within the specified duration.
	// It fails the test if no message is received in the given time window.
	ExpectAnyResponseWithin(duration time.Duration) any

	// ExpectResponseOfType asserts that the next received message matches the given message type.
	// It fails the test if no message is received or the type does not match.
	ExpectResponseOfType(message any)

	// ExpectResponseOfTypeWithin asserts that a message of the given type is received within the specified duration.
	// It fails the test if the type does not match or if the timeout is reached.
	ExpectResponseOfTypeWithin(duration time.Duration, message any)

	// ExpectTerminated asserts that the grain with the specified identity has terminated.
	// This is useful when verifying grain shutdown behavior.
	ExpectTerminated(identity *goakt.GrainIdentity, duration time.Duration)

	// Send sends a message to the grain with the specified identity.
	// This simulates a "Tell" pattern (fire-and-forget), where the sender does not expect a reply.
	//
	// This method is primarily used in test scenarios to validate how a Grain handles incoming messages
	// without waiting for a response. It is especially useful for verifying side effects or internal state changes
	// triggered by the message.
	//
	// Parameters:
	//   - identity: the identity of the target grain registered within the actor system.
	//   - message: the message to send to the grain.
	Send(identity *goakt.GrainIdentity, message any)

	// SendSync sends a message to the specified grain and waits for a synchronous response within the given timeout duration.
	// This method simulates the "Ask" pattern (request-response) and is primarily used in test scenarios to
	// validate a grain's reply behavior and response correctness.
	//
	// Unlike SendContext, which uses a context for cancellation and timeout, SendSync directly relies on the provided
	// timeout value for awaiting a response.
	//
	// Parameters:
	//   - identity: the registered identity of the target grain to which the message should be sent.
	//   - msg: the message to send to the grain.
	//   - timeout: the duration to wait for a response before failing the test.
	//
	// Notes:
	//   - This method is intended for use within testing frameworks via a test probe.
	//   - Should not be used in production code as it couples testing and actor system internals.
	SendSync(identity *goakt.GrainIdentity, message any, timeout time.Duration)
}

type grainProbe struct {
	testingT *testing.T

	testCtx        context.Context
	messageQueue   chan any
	defaultTimeout time.Duration
	timers         *timer.Pool
	actorSystem    goakt.ActorSystem
}

var _ GrainProbe = (*grainProbe)(nil)

// newGrainProbe creates a new grain probe for testing grain interactions.
func newGrainProbe(ctx context.Context, t *testing.T, actorSystem goakt.ActorSystem) (*grainProbe, error) {
	// create the message queue
	msgQueue := make(chan any, MessagesQueueMax)
	return &grainProbe{
		testingT:       t,
		testCtx:        ctx,
		messageQueue:   msgQueue,
		actorSystem:    actorSystem,
		timers:         timer.NewPool(),
		defaultTimeout: GrainDefaultTimeout,
	}, nil
}

// ExpectResponse asserts that the next message received by the probe
// exactly matches the given protobuf message.
// It fails the test if no message is received or the message does not match.
func (x *grainProbe) ExpectResponse(message any) {
	x.expectMessage(x.defaultTimeout, message)
}

// ExpectResponseWithin asserts that the expected message is received within the given duration.
// It fails the test if the timeout is reached or the message differs.
func (x *grainProbe) ExpectResponseWithin(duration time.Duration, message any) {
	x.expectMessage(duration, message)
}

// ExpectNoResponse asserts that no message is received within a short, default time window.
// This is useful for asserting inactivity or idle grains.
func (x *grainProbe) ExpectNoResponse() {
	x.expectNoMessage(x.defaultTimeout)
}

// ExpectAnyResponse waits for and returns the next message received by the probe.
// It fails the test if no message is received in a reasonable default timeout.
func (x *grainProbe) ExpectAnyResponse() any {
	return x.expectAnyMessage(x.defaultTimeout)
}

// ExpectAnyResponseWithin waits for and returns the next message received within the specified duration.
// It fails the test if no message is received in the given time window.
func (x *grainProbe) ExpectAnyResponseWithin(duration time.Duration) any {
	return x.expectAnyMessage(duration)
}

// ExpectResponseOfType asserts that the next received message matches the given message type.
// It fails the test if no message is received or the type does not match.
func (x *grainProbe) ExpectResponseOfType(message any) {
	x.expectMessageOfType(x.defaultTimeout, reflect.TypeOf(message))
}

// ExpectResponseOfTypeWithin asserts that a message of the given type is received within the specified duration.
// It fails the test if the type does not match or if the timeout is reached.
func (x *grainProbe) ExpectResponseOfTypeWithin(duration time.Duration, message any) {
	x.expectMessageOfType(duration, reflect.TypeOf(message))
}

// ExpectTerminated asserts that the grain with the specified identity has terminated.
// This is useful when verifying grain shutdown behavior.
func (x *grainProbe) ExpectTerminated(identity *goakt.GrainIdentity, duration time.Duration) {
	require.True(x.testingT, x.actorSystem.Running(), "actor system is not running")
	require.NotNil(x.testingT, identity)

	deadline := time.Now().Add(duration)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			require.Failf(x.testingT, "timeout", "timeout (%v) during expectTerminated while waiting", duration)
			return
		}

		identities := x.actorSystem.Grains(x.testCtx, remaining)
		found := false
		for _, id := range identities {
			if id.Equal(identity) {
				found = true
				break
			}
		}

		if !found {
			return
		}

		if remaining < 10*time.Millisecond {
			pause.For(remaining)
			continue
		}
		pause.For(10 * time.Millisecond)
	}
}

// Send sends a message to the grain with the specified identity.
// This simulates a "Send" pattern (fire-and-forget), where the sender does not expect a reply.
//
// This method is primarily used in test scenarios to validate how a Grain handles incoming messages
// without waiting for a response. It is especially useful for verifying side effects or internal state changes
// triggered by the message.
//
// Parameters:
//   - identity: the identity of the target grain registered within the actor system.
//   - message: the message to send to the grain.
func (x *grainProbe) Send(identity *goakt.GrainIdentity, message any) {
	err := x.actorSystem.TellGrain(x.testCtx, identity, message)
	require.NoError(x.testingT, err)
}

// SendSync sends a message to the specified grain and waits for a synchronous response within the given timeout duration.
// This method simulates the "SendSync" pattern (request-response) and is primarily used in test scenarios to
// validate an grain's reply behavior and response correctness.
//
// Unlike SendContext, which uses a context for cancellation and timeout, SendSync directly relies on the provided
// timeout value for awaiting a response.
//
// Parameters:
//   - identity: the registered identity of the target grain to which the message should be sent.
//   - msg: the message to send to the grain.
//   - timeout: the duration to wait for a response before failing the test.
//
// Notes:
//   - This method is intended for use within testing frameworks via a test probe.
//   - Should not be used in production code as it couples testing and actor system internals.
func (x *grainProbe) SendSync(identity *goakt.GrainIdentity, message any, timeout time.Duration) {
	reply, err := x.actorSystem.AskGrain(x.testCtx, identity, message, timeout)
	require.NoError(x.testingT, err)
	x.messageQueue <- reply
}

// receiveOne receives one message within a maximum time duration
func (x *grainProbe) receiveOne(duration time.Duration) any {
	t := x.timers.Get(duration)

	select {
	// attempt to read some message from the message queue
	case message, ok := <-x.messageQueue:
		x.timers.Put(t)
		if !ok {
			return nil
		}
		return message
	case <-t.C:
		x.timers.Put(t)
		return nil
	}
}

// expectMessage assert the expectation of a message within a maximum time duration
func (x *grainProbe) expectMessage(duration time.Duration, message any) {
	// receive one message
	received := x.receiveOne(duration)
	require.NotNil(x.testingT, received, fmt.Sprintf("timeout (%v) during expectMessage while waiting for %T", duration, message))
	require.IsType(x.testingT, message, received)
	require.True(x.testingT, reflect.DeepEqual(message, received), fmt.Sprintf("expected %v, found %v", message, received))
}

// expectNoMessage asserts that no message is expected
func (x *grainProbe) expectNoMessage(duration time.Duration) {
	// receive one message
	received := x.receiveOne(duration)
	require.Nil(x.testingT, received, fmt.Sprintf("received unexpected message %v", received))
}

// expectedAnyMessage asserts that any message is expected
func (x *grainProbe) expectAnyMessage(duration time.Duration) any {
	// receive one message
	received := x.receiveOne(duration)
	require.NotNil(x.testingT, received, fmt.Sprintf("timeout (%v) during expectAnyMessage while waiting", duration))
	return received
}

// expectMessageOfType asserts that a message of a given type is expected within a maximum time duration
func (x *grainProbe) expectMessageOfType(duration time.Duration, messageType any) {
	received := x.receiveOne(duration)
	require.NotNil(x.testingT, received, fmt.Sprintf("timeout (%v) during expectAnyMessage while waiting", duration))
	expectedType := reflect.TypeOf(received) == messageType
	require.True(x.testingT, expectedType, fmt.Sprintf("expected %v, found %v", messageType, reflect.TypeOf(received)))
}
