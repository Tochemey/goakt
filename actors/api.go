/*
 * MIT License
 *
 * Copyright (c) 2022-2023 Tochemey
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
	"sync"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	internalpb "github.com/tochemey/goakt/internal/v1"
	"github.com/tochemey/goakt/internal/v1/internalpbconnect"
	addresspb "github.com/tochemey/goakt/pb/address/v1"
	"github.com/tochemey/goakt/pkg/http"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

// Ask sends a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func Ask(ctx context.Context, to PID, message proto.Message, timeout time.Duration) (response proto.Message, err error) {
	// make sure the actor is live
	if !to.IsRunning() {
		return nil, ErrDead
	}

	// create a receiver context
	context := new(receiveContext)
	// set the needed properties of the message context
	context.ctx = ctx
	context.recipient = to
	context.sendTime.Store(time.Now())

	// set the actual message
	switch msg := message.(type) {
	case *internalpb.RemoteMessage:
		// define the actual message variable
		var actual proto.Message
		// unmarshal the message and handle the error
		if actual, err = msg.GetMessage().UnmarshalNew(); err != nil {
			return nil, ErrInvalidRemoteMessage(err)
		}
		// set the context message and the sender
		context.message = actual
		context.remoteSender = msg.GetSender()
		context.sender = NoSender
	default:
		// set the context message and the sender
		context.message = message
		context.sender = NoSender
		context.remoteSender = RemoteNoSender
	}

	context.isAsyncMessage = false
	context.mu = sync.Mutex{}
	context.response = make(chan proto.Message, 1)

	// put the message context in the mailbox of the recipient actor
	to.doReceive(context)

	// await patiently to receive the response from the actor
	for await := time.After(timeout); ; {
		select {
		case response = <-context.response:
			return
		case <-await:
			err = ErrRequestTimeout
			return
		}
	}
}

// Tell sends an asynchronous message to an actor
func Tell(ctx context.Context, to PID, message proto.Message) error {
	// make sure the recipient actor is live
	if !to.IsRunning() {
		return ErrDead
	}

	// create a message context
	context := new(receiveContext)

	// set the needed properties of the message context
	context.ctx = ctx
	context.recipient = to
	context.sendTime.Store(time.Now())

	// set the actual message
	switch msg := message.(type) {
	case *internalpb.RemoteMessage:
		var (
			// define the actual message variable
			actual proto.Message
			// define the error variable
			err error
		)
		// unmarshal the message and handle the error
		if actual, err = msg.GetMessage().UnmarshalNew(); err != nil {
			return ErrInvalidRemoteMessage(err)
		}

		// set the context message and sender
		context.message = actual
		context.remoteSender = msg.GetSender()
		context.sender = NoSender
	default:
		context.message = message
		context.sender = NoSender
		context.remoteSender = RemoteNoSender
	}

	context.isAsyncMessage = true
	context.mu = sync.Mutex{}

	// put the message context in the mailbox of the recipient actor
	to.doReceive(context)

	return nil
}

// BatchTell sends bulk asynchronous messages to an actor
func BatchTell(ctx context.Context, to PID, messages ...proto.Message) error {
	// make sure the recipient actor is live
	if !to.IsRunning() {
		return ErrDead
	}

	// create a message context
	context := new(receiveContext)

	// iterate the list messages
	for i := 0; i < len(messages); i++ {
		// get the message
		message := messages[i]
		// set the needed properties of the message context
		context.ctx = ctx
		context.recipient = to
		context.sendTime.Store(time.Now())
		context.message = message
		context.sender = NoSender
		context.remoteSender = RemoteNoSender
		// put the message context in the mailbox of the recipient actor
		to.doReceive(context)
	}
	return nil
}

// BatchAsk sends a synchronous bunch of messages to the given PID and expect responses in the same order as the messages.
// The messages will be processed one after the other in the order they are sent
// This is a design choice to follow the simple principle of one message at a time processing by actors.
func BatchAsk(ctx context.Context, to PID, timeout time.Duration, messages ...proto.Message) (responses chan proto.Message, err error) {
	// make sure the actor is live
	if !to.IsRunning() {
		return nil, ErrDead
	}

	// create a buffered channel to hold the responses
	responses = make(chan proto.Message, len(messages))

	// close the responses channel when done
	defer close(responses)

	// let us process the messages one after the other
	for i := 0; i < len(messages); i++ {
		// grab the message at index i
		message := messages[i]
		// create a message context
		messageContext := new(receiveContext)

		// set the needed properties of the message context
		messageContext.ctx = ctx
		messageContext.sender = NoSender
		messageContext.recipient = to
		messageContext.message = message
		messageContext.isAsyncMessage = false
		messageContext.mu = sync.Mutex{}
		messageContext.response = make(chan proto.Message, 1)
		messageContext.sendTime.Store(time.Now())

		// push the message to the actor mailbox
		to.doReceive(messageContext)

		// await patiently to receive the response from the actor
	timerLoop:
		for await := time.After(timeout); ; {
			select {
			case resp := <-messageContext.response:
				// send the response to the responses channel
				responses <- resp
				break timerLoop
			case <-await:
				// set the request timeout error
				err = ErrRequestTimeout
				// stop the whole processing
				return
			}
		}
	}
	return
}

// RemoteTell sends a message to an actor remotely without expecting any reply
func RemoteTell(ctx context.Context, to *addresspb.Address, message proto.Message) error {
	// marshal the message
	marshaled, err := anypb.New(message)
	if err != nil {
		return ErrInvalidRemoteMessage(err)
	}

	// create an instance of remote client service
	remoteClient := internalpbconnect.NewRemotingServiceClient(
		http.Client(),
		http.URL(to.GetHost(), int(to.GetPort())),
		connect.WithInterceptors(otelconnect.NewInterceptor()),
		connect.WithGRPC(),
	)
	// prepare the rpcRequest to send
	request := connect.NewRequest(&internalpb.RemoteTellRequest{
		RemoteMessage: &internalpb.RemoteMessage{
			Sender:   RemoteNoSender,
			Receiver: to,
			Message:  marshaled,
		},
	})
	// send the message and handle the error in case there is any
	if _, err := remoteClient.RemoteTell(ctx, request); err != nil {
		return err
	}
	return nil
}

// RemoteAsk sends a synchronous message to another actor remotely and expect a response.
func RemoteAsk(ctx context.Context, to *addresspb.Address, message proto.Message) (response *anypb.Any, err error) {
	// marshal the message
	marshaled, err := anypb.New(message)
	if err != nil {
		return nil, ErrInvalidRemoteMessage(err)
	}

	// create an instance of remote client service
	remoteClient := internalpbconnect.NewRemotingServiceClient(
		http.Client(),
		http.URL(to.GetHost(), int(to.GetPort())),
		connect.WithInterceptors(otelconnect.NewInterceptor()),
		connect.WithGRPC(),
	)
	// prepare the rpcRequest to send
	rpcRequest := connect.NewRequest(&internalpb.RemoteAskRequest{
		RemoteMessage: &internalpb.RemoteMessage{
			Sender:   RemoteNoSender,
			Receiver: to,
			Message:  marshaled,
		},
	})
	// send the request
	rpcResponse, rpcErr := remoteClient.RemoteAsk(ctx, rpcRequest)
	// handle the error
	if rpcErr != nil {
		return nil, rpcErr
	}

	return rpcResponse.Msg.GetMessage(), nil
}

// RemoteLookup look for an actor address on a remote node.
func RemoteLookup(ctx context.Context, host string, port int, name string) (addr *addresspb.Address, err error) {
	// create an instance of remote client service
	remoteClient := internalpbconnect.NewRemotingServiceClient(
		http.Client(),
		http.URL(host, port),
		connect.WithInterceptors(otelconnect.NewInterceptor()),
		connect.WithGRPC(),
	)

	// prepare the request to send
	request := connect.NewRequest(&internalpb.RemoteLookupRequest{
		Host: host,
		Port: int32(port),
		Name: name,
	})
	// send the message and handle the error in case there is any
	response, err := remoteClient.RemoteLookup(ctx, request)
	// we know the error will always be a grpc error
	if err != nil {
		code := connect.CodeOf(err)
		if code == connect.CodeNotFound {
			return nil, nil
		}
		return nil, err
	}

	// return the response
	return response.Msg.GetAddress(), nil
}

// RemoteBatchTell sends bulk asynchronous messages to an actor
func RemoteBatchTell(ctx context.Context, to *addresspb.Address, messages ...proto.Message) error {
	// define a variable holding the remote messages
	var remoteMessages []*anypb.Any
	// iterate the list of messages and pack them
	for _, message := range messages {
		// let us pack it
		packed, err := anypb.New(message)
		// handle the error
		if err != nil {
			return ErrInvalidRemoteMessage(err)
		}
		// add it to the list of remote messages
		remoteMessages = append(remoteMessages, packed)
	}

	// create an instance of remote client service
	remoteClient := internalpbconnect.NewRemotingServiceClient(
		http.Client(),
		http.URL(to.GetHost(), int(to.GetPort())),
		connect.WithInterceptors(otelconnect.NewInterceptor()),
		connect.WithGRPC(),
	)

	// prepare the remote batch tell request
	request := connect.NewRequest(&internalpb.RemoteBatchTellRequest{
		Messages: remoteMessages,
		Sender:   RemoteNoSender,
		Receiver: to,
	})
	// send the message and handle the error in case there is any
	if _, err := remoteClient.RemoteBatchTell(ctx, request); err != nil {
		return err
	}
	return nil
}

// RemoteBatchAsk sends bulk messages to an actor with responses expected
func RemoteBatchAsk(ctx context.Context, to *addresspb.Address, messages ...proto.Message) (responses []*anypb.Any, err error) {
	// define a variable holding the remote messages
	var remoteMessages []*anypb.Any
	// iterate the list of messages and pack them
	for _, message := range messages {
		// let us pack it
		packed, err := anypb.New(message)
		// handle the error
		if err != nil {
			return nil, ErrInvalidRemoteMessage(err)
		}
		// add it to the list of remote messages
		remoteMessages = append(remoteMessages, packed)
	}

	// create an instance of remote client service
	remoteClient := internalpbconnect.NewRemotingServiceClient(
		http.Client(),
		http.URL(to.GetHost(), int(to.GetPort())),
		connect.WithInterceptors(otelconnect.NewInterceptor()),
		connect.WithGRPC(),
	)

	// prepare the remote batch tell request
	request := connect.NewRequest(&internalpb.RemoteBatchAskRequest{
		Messages: remoteMessages,
		Sender:   RemoteNoSender,
		Receiver: to,
	})
	// send the message and handle the error in case there is any
	response, err := remoteClient.RemoteBatchAsk(ctx, request)
	// handle the error
	if err != nil {
		return nil, err
	}
	// return the responses
	return response.Msg.GetMessages(), nil
}
