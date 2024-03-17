/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	"github.com/tochemey/goakt/goaktpb"
	"github.com/tochemey/goakt/internal/http"
	"github.com/tochemey/goakt/internal/internalpb"
	"github.com/tochemey/goakt/internal/internalpb/internalpbconnect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

// Ask sends a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func Ask(ctx context.Context, to PID, message proto.Message, timeout time.Duration) (response proto.Message, err error) {
	if !to.IsRunning() {
		return nil, ErrDead
	}

	var messageContext *receiveContext

	switch msg := message.(type) {
	case *internalpb.RemoteMessage:
		var actual proto.Message
		if actual, err = msg.GetMessage().UnmarshalNew(); err != nil {
			return nil, ErrInvalidRemoteMessage(err)
		}
		messageContext = newReceiveContext(ctx, NoSender, to, actual, false).WithRemoteSender(msg.GetSender())
	default:
		messageContext = newReceiveContext(ctx, NoSender, to, message, false).WithRemoteSender(RemoteNoSender)
	}

	to.doReceive(messageContext)

	// await patiently to receive the response from the actor
	for await := time.After(timeout); ; {
		select {
		case response = <-messageContext.response:
			return
		case <-await:
			err = ErrRequestTimeout
			to.handleError(messageContext, err)
			return
		}
	}
}

// Tell sends an asynchronous message to an actor
func Tell(ctx context.Context, to PID, message proto.Message) error {
	if !to.IsRunning() {
		return ErrDead
	}

	var messageContext *receiveContext

	switch msg := message.(type) {
	case *internalpb.RemoteMessage:
		var (
			actual proto.Message
			err    error
		)

		if actual, err = msg.GetMessage().UnmarshalNew(); err != nil {
			return ErrInvalidRemoteMessage(err)
		}
		messageContext = newReceiveContext(ctx, NoSender, to, actual, true).WithRemoteSender(msg.GetSender())
	default:
		messageContext = newReceiveContext(ctx, NoSender, to, message, true).WithRemoteSender(RemoteNoSender)
	}

	to.doReceive(messageContext)

	return nil
}

// BatchTell sends bulk asynchronous messages to an actor
func BatchTell(ctx context.Context, to PID, messages ...proto.Message) error {
	if !to.IsRunning() {
		return ErrDead
	}

	for i := 0; i < len(messages); i++ {
		message := messages[i]
		messageContext := newReceiveContext(ctx, NoSender, to, message, true).WithRemoteSender(RemoteNoSender)
		to.doReceive(messageContext)
	}
	return nil
}

// BatchAsk sends a synchronous bunch of messages to the given PID and expect responses in the same order as the messages.
// The messages will be processed one after the other in the order they are sent
// This is a design choice to follow the simple principle of one message at a time processing by actors.
func BatchAsk(ctx context.Context, to PID, timeout time.Duration, messages ...proto.Message) (responses chan proto.Message, err error) {
	if !to.IsRunning() {
		return nil, ErrDead
	}

	responses = make(chan proto.Message, len(messages))
	defer close(responses)

	for i := 0; i < len(messages); i++ {
		message := messages[i]
		messageContext := newReceiveContext(ctx, NoSender, to, message, false)
		to.doReceive(messageContext)

		// await patiently to receive the response from the actor
	timerLoop:
		for await := time.After(timeout); ; {
			select {
			case resp := <-messageContext.response:
				responses <- resp
				break timerLoop
			case <-await:
				err = ErrRequestTimeout
				to.handleError(messageContext, err)
				return
			}
		}
	}
	return
}

// RemoteTell sends a message to an actor remotely without expecting any reply
func RemoteTell(ctx context.Context, to *goaktpb.Address, message proto.Message) error {
	marshaled, err := anypb.New(message)
	if err != nil {
		return ErrInvalidRemoteMessage(err)
	}

	interceptor, err := otelconnect.NewInterceptor()
	if err != nil {
		return err
	}

	remoteClient := internalpbconnect.NewRemotingServiceClient(
		http.NewClient(),
		http.URL(to.GetHost(), int(to.GetPort())),
		connect.WithInterceptors(interceptor),
		connect.WithGRPC(),
	)

	request := connect.NewRequest(&internalpb.RemoteTellRequest{
		RemoteMessage: &internalpb.RemoteMessage{
			Sender:   RemoteNoSender,
			Receiver: to,
			Message:  marshaled,
		},
	})

	if _, err := remoteClient.RemoteTell(ctx, request); err != nil {
		return err
	}
	return nil
}

// RemoteAsk sends a synchronous message to another actor remotely and expect a response.
func RemoteAsk(ctx context.Context, to *goaktpb.Address, message proto.Message) (response *anypb.Any, err error) {
	marshaled, err := anypb.New(message)
	if err != nil {
		return nil, ErrInvalidRemoteMessage(err)
	}

	interceptor, err := otelconnect.NewInterceptor()
	if err != nil {
		return nil, err
	}

	remoteClient := internalpbconnect.NewRemotingServiceClient(
		http.NewClient(),
		http.URL(to.GetHost(), int(to.GetPort())),
		connect.WithInterceptors(interceptor),
		connect.WithGRPC(),
	)

	rpcRequest := connect.NewRequest(&internalpb.RemoteAskRequest{
		RemoteMessage: &internalpb.RemoteMessage{
			Sender:   RemoteNoSender,
			Receiver: to,
			Message:  marshaled,
		},
	})

	rpcResponse, rpcErr := remoteClient.RemoteAsk(ctx, rpcRequest)
	if rpcErr != nil {
		return nil, rpcErr
	}

	return rpcResponse.Msg.GetMessage(), nil
}

// RemoteLookup look for an actor address on a remote node.
func RemoteLookup(ctx context.Context, host string, port int, name string) (addr *goaktpb.Address, err error) {
	interceptor, err := otelconnect.NewInterceptor()
	if err != nil {
		return nil, err
	}

	remoteClient := internalpbconnect.NewRemotingServiceClient(
		http.NewClient(),
		http.URL(host, port),
		connect.WithInterceptors(interceptor),
		connect.WithGRPC(),
	)

	// prepare the request to send
	request := connect.NewRequest(&internalpb.RemoteLookupRequest{
		Host: host,
		Port: int32(port),
		Name: name,
	})

	response, err := remoteClient.RemoteLookup(ctx, request)
	if err != nil {
		code := connect.CodeOf(err)
		if code == connect.CodeNotFound {
			return nil, nil
		}
		return nil, err
	}

	return response.Msg.GetAddress(), nil
}

// RemoteBatchTell sends bulk asynchronous messages to an actor
func RemoteBatchTell(ctx context.Context, to *goaktpb.Address, messages ...proto.Message) error {
	interceptor, err := otelconnect.NewInterceptor()
	if err != nil {
		return err
	}

	var remoteMessages []*anypb.Any
	for _, message := range messages {
		packed, err := anypb.New(message)
		if err != nil {
			return ErrInvalidRemoteMessage(err)
		}
		remoteMessages = append(remoteMessages, packed)
	}

	remoteClient := internalpbconnect.NewRemotingServiceClient(
		http.NewClient(),
		http.URL(to.GetHost(), int(to.GetPort())),
		connect.WithInterceptors(interceptor),
		connect.WithGRPC(),
	)

	request := connect.NewRequest(&internalpb.RemoteBatchTellRequest{
		Messages: remoteMessages,
		Sender:   RemoteNoSender,
		Receiver: to,
	})

	if _, err := remoteClient.RemoteBatchTell(ctx, request); err != nil {
		return err
	}
	return nil
}

// RemoteBatchAsk sends bulk messages to an actor with responses expected
func RemoteBatchAsk(ctx context.Context, to *goaktpb.Address, messages ...proto.Message) (responses []*anypb.Any, err error) {
	interceptor, err := otelconnect.NewInterceptor()
	if err != nil {
		return nil, err
	}

	var remoteMessages []*anypb.Any
	for _, message := range messages {
		packed, err := anypb.New(message)
		if err != nil {
			return nil, ErrInvalidRemoteMessage(err)
		}
		remoteMessages = append(remoteMessages, packed)
	}

	remoteClient := internalpbconnect.NewRemotingServiceClient(
		http.NewClient(),
		http.URL(to.GetHost(), int(to.GetPort())),
		connect.WithInterceptors(interceptor),
		connect.WithGRPC(),
	)

	request := connect.NewRequest(&internalpb.RemoteBatchAskRequest{
		Messages: remoteMessages,
		Sender:   RemoteNoSender,
		Receiver: to,
	})

	response, err := remoteClient.RemoteBatchAsk(ctx, request)
	if err != nil {
		return nil, err
	}
	return response.Msg.GetMessages(), nil
}

// RemoteReSpawn restarts actor address on a remote node.
func RemoteReSpawn(ctx context.Context, host string, port int, name string) error {
	interceptor, err := otelconnect.NewInterceptor()
	if err != nil {
		return err
	}

	remoteClient := internalpbconnect.NewRemotingServiceClient(
		http.NewClient(),
		http.URL(host, port),
		connect.WithInterceptors(interceptor),
		connect.WithGRPC(),
	)

	request := connect.NewRequest(&internalpb.RemoteReSpawnRequest{
		Host: host,
		Port: int32(port),
		Name: name,
	})

	if _, err = remoteClient.RemoteReSpawn(ctx, request); err != nil {
		code := connect.CodeOf(err)
		if code == connect.CodeNotFound {
			return nil
		}
		return err
	}

	return nil
}
