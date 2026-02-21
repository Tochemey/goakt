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
	"errors"
	"time"

	"github.com/tochemey/goakt/v3/address"
	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/internal/internalpb"
)

// Ask sends a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func Ask(ctx context.Context, to *PID, message any, timeout time.Duration) (response any, err error) {
	if !to.IsRunning() {
		return nil, gerrors.ErrDead
	}

	noSender := to.ActorSystem().NoSender()
	receiveContext, err := toReceiveContext(ctx, noSender, to, message, false)
	if err != nil {
		return nil, err
	}

	responseCh := receiveContext.response
	to.doReceive(receiveContext)
	timer := timers.Get(timeout)

	// await patiently to receive the response from the actor
	// or wait for the context to be done
	select {
	case response = <-responseCh:
		timers.Put(timer)
		receiveContext.responseClosed.Store(true)
		putResponseChannel(responseCh)
		return
	case <-ctx.Done():
		err = errors.Join(ctx.Err(), gerrors.ErrRequestTimeout)
		to.handleReceivedErrorWithMessage(noSender, message, err)
		timers.Put(timer)
		receiveContext.responseClosed.Store(true)
		putResponseChannel(responseCh)
		return nil, err
	case <-timer.C:
		err = gerrors.ErrRequestTimeout
		to.handleReceivedErrorWithMessage(noSender, message, err)
		timers.Put(timer)
		receiveContext.responseClosed.Store(true)
		putResponseChannel(responseCh)
		return
	}
}

// Tell sends an asynchronous message to an actor
func Tell(ctx context.Context, to *PID, message any) error {
	if !to.IsRunning() {
		return gerrors.ErrDead
	}

	receiveContext, err := toReceiveContext(ctx, to.ActorSystem().NoSender(), to, message, true)
	if err != nil {
		return err
	}

	to.doReceive(receiveContext)
	return nil
}

// BatchTell sends bulk asynchronous messages to an actor
// The messages will be processed one after the other in the order they are sent
// This is a design choice to follow the simple principle of one message at a time processing by actors.
func BatchTell(ctx context.Context, to *PID, messages ...any) error {
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
func BatchAsk(ctx context.Context, to *PID, timeout time.Duration, messages ...any) (responses chan any, err error) {
	responses = make(chan any, len(messages))
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
func toReceiveContext(ctx context.Context, from, to *PID, message any, async bool) (*ReceiveContext, error) {
	receiveContext := getContext()
	switch msg := message.(type) {
	case *internalpb.RemoteMessage:
		serializer := to.remoting.Serializer(nil)
		actual, err := serializer.Deserialize(msg.GetMessage())
		if err != nil {
			return nil, gerrors.NewErrInvalidRemoteMessage(err)
		}

		receiveContext.build(ctx, from, to, actual, async)

		remoteSender := address.NoSender()
		if sender := msg.GetSender(); sender != "" {
			addr, err := address.Parse(sender)
			if err != nil {
				return nil, gerrors.NewErrInvalidRemoteMessage(err)
			}
			remoteSender = addr
		}

		return receiveContext.withRemoteSender(remoteSender), nil
	default:
		receiveContext.build(ctx, from, to, message, async)
		return receiveContext.withRemoteSender(address.NoSender()), nil
	}
}
