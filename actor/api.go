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

package actor

import (
	"context"
	"strings"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/goakt/v2/goaktpb"
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

	receiveContext, err := toReceiveContext(ctx, to, message)
	if err != nil {
		return nil, err
	}

	to.doReceive(receiveContext)

	// await patiently to receive the response from the actor
	select {
	case response = <-receiveContext.response:
		to.recordLatestReceiveDurationMetric(ctx)
		return
	case <-time.After(timeout):
		to.recordLatestReceiveDurationMetric(ctx)
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

	receiveContext, err := toReceiveContext(ctx, to, message)
	if err != nil {
		return err
	}

	to.doReceive(receiveContext)
	to.recordLatestReceiveDurationMetric(ctx)
	return nil
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
	)

	request := &internalpb.RemoteTellRequest{
		RemoteMessage: &internalpb.RemoteMessage{
			Sender:   RemoteNoSender,
			Receiver: to,
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

	return response.Msg.GetAddress(), nil
}

// RemoteSpawn creates an actor on a remote node. The given actor needs to be registered on the remote node using the Register method of ActorSystem
func RemoteSpawn(ctx context.Context, host string, port int, name, actorType string) error {
	interceptor, err := otelconnect.NewInterceptor()
	if err != nil {
		return err
	}

	remoteClient := internalpbconnect.NewRemotingServiceClient(
		http.NewClient(),
		http.URL(host, port),
		connect.WithInterceptors(interceptor),
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
func toReceiveContext(ctx context.Context, to *PID, message proto.Message) (*ReceiveContext, error) {
	switch msg := message.(type) {
	case *internalpb.RemoteMessage:
		actual, err := msg.GetMessage().UnmarshalNew()
		if err != nil {
			return nil, ErrInvalidRemoteMessage(err)
		}
		return newReceiveContext(ctx, NoSender, to, actual).WithRemoteSender(msg.GetSender()), nil
	default:
		return newReceiveContext(ctx, NoSender, to, message).WithRemoteSender(RemoteNoSender), nil
	}
}
