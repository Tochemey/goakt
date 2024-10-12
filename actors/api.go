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
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v2/address"
	"github.com/tochemey/goakt/v2/internal/http"
	"github.com/tochemey/goakt/v2/internal/internalpb"
	"github.com/tochemey/goakt/v2/internal/internalpb/internalpbconnect"
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

// RemoteTell sends a message to an actor remotely without expecting any reply
func RemoteTell(ctx context.Context, to *address.Address, message proto.Message) error {
	marshaled, err := anypb.New(message)
	if err != nil {
		return ErrInvalidRemoteMessage(err)
	}

	remoteClient := internalpbconnect.NewRemotingServiceClient(
		http.NewClient(),
		http.URL(to.GetHost(), int(to.GetPort())),
	)

	request := &internalpb.RemoteTellRequest{
		RemoteMessage: &internalpb.RemoteMessage{
			Sender:   address.NoSender,
			Receiver: to.Address,
			Message:  marshaled,
		},
	}

	stream := remoteClient.RemoteTell(ctx)
	if err := stream.Send(request); err != nil {
		if eof(err) {
			if _, err := stream.CloseAndReceive(); err != nil {
				return err
			}
			return nil
		}
		return err
	}

	// close the connection
	if _, err := stream.CloseAndReceive(); err != nil {
		return err
	}

	return nil
}

// RemoteAsk sends a synchronous message to another actor remotely and expect a response.
func RemoteAsk(ctx context.Context, to *address.Address, message proto.Message, timeout time.Duration) (response *anypb.Any, err error) {
	marshaled, err := anypb.New(message)
	if err != nil {
		return nil, ErrInvalidRemoteMessage(err)
	}

	remotingService := internalpbconnect.NewRemotingServiceClient(
		http.NewClient(),
		http.URL(to.GetHost(), int(to.GetPort())),
	)

	request := &internalpb.RemoteAskRequest{
		RemoteMessage: &internalpb.RemoteMessage{
			Sender:   address.NoSender,
			Receiver: to.Address,
			Message:  marshaled,
		},
		Timeout: durationpb.New(timeout),
	}
	stream := remotingService.RemoteAsk(ctx)
	errc := make(chan error, 1)

	go func() {
		defer close(errc)
		for {
			resp, err := stream.Receive()
			if err != nil {
				errc <- err
				return
			}

			response = resp.GetMessage()
		}
	}()

	err = stream.Send(request)
	if err != nil {
		return nil, err
	}

	if err := stream.CloseRequest(); err != nil {
		return nil, err
	}

	err = <-errc
	if eof(err) {
		return response, nil
	}

	if err != nil {
		return nil, err
	}

	return
}

// RemoteLookup look for an actor address on a remote node.
func RemoteLookup(ctx context.Context, host string, port int, name string) (addr *address.Address, err error) {
	remoteClient := internalpbconnect.NewRemotingServiceClient(
		http.NewClient(),
		http.URL(host, port),
	)

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

	return address.From(response.Msg.GetAddress()), nil
}

// RemoteBatchTell sends bulk asynchronous messages to an actor
func RemoteBatchTell(ctx context.Context, to *address.Address, messages ...proto.Message) error {
	var requests []*internalpb.RemoteTellRequest
	for _, message := range messages {
		packed, err := anypb.New(message)
		if err != nil {
			return ErrInvalidRemoteMessage(err)
		}

		requests = append(requests, &internalpb.RemoteTellRequest{
			RemoteMessage: &internalpb.RemoteMessage{
				Sender:   address.NoSender,
				Receiver: to.Address,
				Message:  packed,
			},
		})
	}

	remoteClient := internalpbconnect.NewRemotingServiceClient(
		http.NewClient(),
		http.URL(to.GetHost(), int(to.GetPort())),
	)

	stream := remoteClient.RemoteTell(ctx)
	for _, request := range requests {
		err := stream.Send(request)
		if eof(err) {
			if _, err := stream.CloseAndReceive(); err != nil {
				return err
			}
			return nil
		}

		if err != nil {
			return err
		}
	}

	// close the connection
	if _, err := stream.CloseAndReceive(); err != nil {
		return err
	}

	return nil
}

// RemoteBatchAsk sends bulk messages to an actor with responses expected
func RemoteBatchAsk(ctx context.Context, to *address.Address, messages ...proto.Message) (responses []*anypb.Any, err error) {
	var requests []*internalpb.RemoteAskRequest
	for _, message := range messages {
		packed, err := anypb.New(message)
		if err != nil {
			return nil, ErrInvalidRemoteMessage(err)
		}

		requests = append(requests, &internalpb.RemoteAskRequest{
			RemoteMessage: &internalpb.RemoteMessage{
				Sender:   address.NoSender,
				Receiver: to.Address,
				Message:  packed,
			},
		})
	}

	remoteClient := internalpbconnect.NewRemotingServiceClient(
		http.NewClient(),
		http.URL(to.GetHost(), int(to.GetPort())),
	)

	stream := remoteClient.RemoteAsk(ctx)
	errc := make(chan error, 1)

	go func() {
		defer close(errc)
		for {
			resp, err := stream.Receive()
			if err != nil {
				errc <- err
				return
			}

			responses = append(responses, resp.GetMessage())
		}
	}()

	for _, request := range requests {
		err := stream.Send(request)
		if err != nil {
			return nil, err
		}
	}

	if err := stream.CloseRequest(); err != nil {
		return nil, err
	}

	err = <-errc
	if eof(err) {
		return responses, nil
	}

	if err != nil {
		return nil, err
	}

	return
}

// RemoteReSpawn restarts actor on a remote node.
func RemoteReSpawn(ctx context.Context, host string, port int, name string) error {
	remoteClient := internalpbconnect.NewRemotingServiceClient(
		http.NewClient(),
		http.URL(host, port),
	)

	request := connect.NewRequest(&internalpb.RemoteReSpawnRequest{
		Host: host,
		Port: int32(port),
		Name: name,
	})

	if _, err := remoteClient.RemoteReSpawn(ctx, request); err != nil {
		code := connect.CodeOf(err)
		if code == connect.CodeNotFound {
			return nil
		}
		return err
	}

	return nil
}

// RemoteStop stops an actor on a remote node.
func RemoteStop(ctx context.Context, host string, port int, name string) error {
	remoteClient := internalpbconnect.NewRemotingServiceClient(
		http.NewClient(),
		http.URL(host, port),
	)

	request := connect.NewRequest(&internalpb.RemoteStopRequest{
		Host: host,
		Port: int32(port),
		Name: name,
	})

	if _, err := remoteClient.RemoteStop(ctx, request); err != nil {
		code := connect.CodeOf(err)
		if code == connect.CodeNotFound {
			return nil
		}
		return err
	}

	return nil
}

// RemoteSpawn creates an actor on a remote node. The given actor needs to be registered on the remote node using the Register method of ActorSystem
func RemoteSpawn(ctx context.Context, host string, port int, name, actorType string) error {
	remoteClient := internalpbconnect.NewRemotingServiceClient(
		http.NewClient(),
		http.URL(host, port),
	)

	request := connect.NewRequest(&internalpb.RemoteSpawnRequest{
		Host:      host,
		Port:      int32(port),
		ActorName: name,
		ActorType: actorType,
	})

	if _, err := remoteClient.RemoteSpawn(ctx, request); err != nil {
		code := connect.CodeOf(err)
		if code == connect.CodeFailedPrecondition {
			connectErr := err.(*connect.Error)
			e := connectErr.Unwrap()
			// TODO: find a better way to use errors.Is with connect.Error
			if strings.Contains(e.Error(), ErrTypeNotRegistered.Error()) {
				return ErrTypeNotRegistered
			}
		}
		return err
	}
	return nil
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
