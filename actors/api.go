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
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/address"
	"github.com/tochemey/goakt/v2/internal/internalpb"
)

// Ask sends a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func Ask(ctx context.Context, to *PID, message proto.Message, timeout time.Duration) (response proto.Message, err error) {
	if !to.IsRunning() {
		return nil, ErrDead
	}

	receiveContext, err := toReceiveContext(ctx, to, message, false)
	if err != nil {
		return nil, err
	}

	to.doReceive(receiveContext)

	// await patiently to receive the response from the actor
	select {
	case response = <-receiveContext.response:
		return
	case <-time.After(timeout):
		err = ErrRequestTimeout
		to.toDeadletterQueue(receiveContext, err)
		return
	}
}

// Tell sends an asynchronous message to an actor
func Tell(ctx context.Context, to *PID, message proto.Message) error {
	if !to.IsRunning() {
		return ErrDead
	}

	receiveContext, err := toReceiveContext(ctx, to, message, true)
	if err != nil {
		return err
	}

	to.doReceive(receiveContext)
	return nil
}

// BatchTell sends bulk asynchronous messages to an actor
// The messages will be processed one after the other in the order they are sent
// This is a design choice to follow the simple principle of one message at a time processing by actors.
func BatchTell(ctx context.Context, to *PID, messages ...proto.Message) error {
	// messages are processed one after the other
	for _, mesage := range messages {
		if err := Tell(ctx, to, mesage); err != nil {
			return err
		}
	}
	return nil
}

// BatchAsk sends a synchronous bunch of messages to the given PID and expect responses in the same order as the messages.
// The messages will be processed one after the other in the order they are sent
// This is a design choice to follow the simple principle of one message at a time processing by actors.
func BatchAsk(ctx context.Context, to *PID, timeout time.Duration, messages ...proto.Message) (responses chan proto.Message, err error) {
	responses = make(chan proto.Message, len(messages))
	defer close(responses)
	for _, mesage := range messages {
		response, err := Ask(ctx, to, mesage, timeout)
		if err != nil {
			return nil, err
		}
		responses <- response
	}
	return
}

// toReceiveContext creates a ReceiveContext provided a message and a receiver
func toReceiveContext(ctx context.Context, to *PID, message proto.Message, async bool) (*ReceiveContext, error) {
	switch msg := message.(type) {
	case *internalpb.RemoteMessage:
		actual, err := msg.GetMessage().UnmarshalNew()
		if err != nil {
			return nil, ErrInvalidRemoteMessage(err)
		}
		receiveContext := contextFromPool()
		receiveContext.build(ctx, NoSender, to, actual, async)
		return receiveContext.withRemoteSender(address.From(msg.GetSender())), nil
	default:
		receiveContext := contextFromPool()
		receiveContext.build(ctx, NoSender, to, message, async)
		return receiveContext.withRemoteSender(address.From(address.NoSender)), nil
	}
}
