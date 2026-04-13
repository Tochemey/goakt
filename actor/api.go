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

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/internalpb"
)

// Ask sends a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func Ask(ctx context.Context, to *PID, message any, timeout time.Duration) (response any, err error) {
	if to == nil {
		return nil, gerrors.ErrDead
	}

	// Remote PIDs have no ActorSystem; route through remoting instead.
	// Must check before IsRunning, which can panic when remoting is nil.
	if to.IsRemote() {
		if to.remoting == nil {
			return nil, gerrors.ErrRemotingDisabled
		}
		if timeout <= 0 {
			return nil, gerrors.ErrInvalidTimeout
		}
		return to.remoting.RemoteAsk(ctx, address.NoSender(), to.getAddress(), message, timeout)
	}

	if !to.IsRunning() {
		return nil, gerrors.ErrDead
	}

	noSender := to.ActorSystem().NoSender()
	decoded, from, err := resolveDispatch(to, noSender, message)
	if err != nil {
		return nil, err
	}
	message = decoded

	receiveContext := toReceiveContext(ctx, from, to, message, false)

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
	if to == nil {
		return gerrors.ErrDead
	}

	// Remote PIDs have no ActorSystem; route through remoting instead.
	// Must check before IsRunning, which can panic when remoting is nil.
	if to.IsRemote() {
		if to.remoting == nil {
			return gerrors.ErrRemotingDisabled
		}
		return to.remoting.RemoteTell(ctx, address.NoSender(), to.getAddress(), message)
	}

	if !to.IsRunning() {
		return gerrors.ErrDead
	}

	decoded, from, err := resolveDispatch(to, to.ActorSystem().NoSender(), message)
	if err != nil {
		return err
	}
	to.doReceive(toReceiveContext(ctx, from, to, decoded, true))
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

// toReceiveContext builds a ReceiveContext for delivery into to's mailbox.
// The caller is responsible for resolving the sender PID and decoding the
// message into a typed payload; this function performs no interpretation.
//
// RemoteMessage wire payloads must be passed through unwrapRemoteMessage
// first so a single helper owns the serializer + sender-address resolution
// path (see handleRemoteTell, handleRemoteAsk).
func toReceiveContext(ctx context.Context, from, to *PID, message any, async bool) *ReceiveContext {
	rc := getContext()
	rc.build(ctx, from, to, message, async)
	return rc
}

// unwrapRemoteMessage turns a wire RemoteMessage into the two runtime values
// required to deliver it into a local mailbox: the typed payload and the
// sender PID. Returns ErrInvalidRemoteMessage for unknown payload types or
// unparseable sender addresses. The returned sender is never nil — wire
// messages with an empty sender yield the target system's NoSender.
func unwrapRemoteMessage(to *PID, msg *internalpb.RemoteMessage) (any, *PID, error) {
	payload, err := to.remoting.Serializer(nil).Deserialize(msg.GetMessage())
	if err != nil {
		return nil, nil, gerrors.NewErrInvalidRemoteMessage(err)
	}

	raw := msg.GetSender()
	if raw == "" {
		return payload, to.ActorSystem().NoSender(), nil
	}

	addr, err := address.Parse(raw)
	if err != nil {
		return nil, nil, gerrors.NewErrInvalidRemoteMessage(err)
	}
	return payload, newRemotePID(addr, to.remoting), nil
}

// resolveDispatch produces the (payload, sender) pair that the dispatcher
// ultimately needs, whatever the shape of the incoming message.
//
// When message is a wire RemoteMessage it is unwrapped (see
// unwrapRemoteMessage); the caller's from wins if it is a concrete sender
// (anything other than the target's NoSender), otherwise the sender
// materialized from the wire is used. For non-RemoteMessage inputs message
// and from are returned unchanged.
//
// This lets the remote tell handler — which has already parsed the sender
// once for dead-letter routing — skip a redundant address parse, while
// legacy call sites that only have NoSender keep getting the wire sender
// resolved for them.
func resolveDispatch(to, from *PID, message any) (any, *PID, error) {
	rm, ok := message.(*internalpb.RemoteMessage)
	if !ok {
		return message, from, nil
	}
	payload, wireFrom, err := unwrapRemoteMessage(to, rm)
	if err != nil {
		return nil, nil, err
	}
	if from != nil && from != to.ActorSystem().NoSender() {
		return payload, from, nil
	}
	return payload, wireFrom, nil
}
