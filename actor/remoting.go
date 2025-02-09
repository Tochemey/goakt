/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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
	"crypto/tls"
	"errors"
	nethttp "net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v3/address"
	"github.com/tochemey/goakt/v3/internal/http"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/internalpb/internalpbconnect"
)

// RemotingOption sets the remoting option
type RemotingOption func(*Remoting)

// WithRemotingTLS configures the remoting system to use a secure connection
// for communication with the specified remote node. This requires a TLS
// client configuration to enable secure interactions with the remote actor system.
//
// Ensure that the remote actor system is configured with TLS enabled and
// capable of completing a successful handshake. It is recommended that both
// systems share the same root Certificate Authority (CA) for mutual trust and
// secure communication.
func WithRemotingTLS(tlsConfig *tls.Config) RemotingOption {
	return func(r *Remoting) {
		r.clientTLS = tlsConfig
	}
}

// WithRemotingMaxReadFameSize sets both the maximum framesize to send and receive
func WithRemotingMaxReadFameSize(size int) RemotingOption {
	return func(r *Remoting) {
		r.maxReadFrameSize = size
	}
}

// Remoting defines the Remoting APIs
// This requires Remoting is enabled on the connected actor system
type Remoting struct {
	client           *nethttp.Client
	clientTLS        *tls.Config
	maxReadFrameSize int
}

// NewRemoting creates an instance Remoting with an insecure connection. To use a secure connection
// one need to call the WithTLS method of the remoting instance to set the certificates of the secure connection
// This requires Remoting is enabled on the connected actor system
// Make sure to call Close to free up resources otherwise you may be leaking socket connections
//
// One can also override the remoting option when calling any of the method for custom one.
func NewRemoting(opts ...RemotingOption) *Remoting {
	r := &Remoting{
		maxReadFrameSize: DefaultMaxReadFrameSize,
	}

	// apply the options
	for _, opt := range opts {
		opt(r)
	}

	r.client = http.NewClient(uint32(DefaultMaxReadFrameSize))
	if r.clientTLS != nil {
		r.client = http.NewTLSClient(r.clientTLS, uint32(r.maxReadFrameSize)) // nolint
	}
	return r
}

// RemoteTell sends a message to an actor remotely without expecting any reply
func (r *Remoting) RemoteTell(ctx context.Context, from, to *address.Address, message proto.Message) error {
	marshaled, err := anypb.New(message)
	if err != nil {
		return ErrInvalidMessage(err)
	}

	remoteClient := r.serviceClient(to.GetHost(), int(to.GetPort()))
	request := &internalpb.RemoteTellRequest{
		RemoteMessage: &internalpb.RemoteMessage{
			Sender:   from.Address,
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
func (r *Remoting) RemoteAsk(ctx context.Context, from, to *address.Address, message proto.Message, timeout time.Duration) (response *anypb.Any, err error) {
	marshaled, err := anypb.New(message)
	if err != nil {
		return nil, ErrInvalidMessage(err)
	}

	remoteClient := r.serviceClient(to.GetHost(), int(to.GetPort()))
	request := &internalpb.RemoteAskRequest{
		RemoteMessage: &internalpb.RemoteMessage{
			Sender:   from.Address,
			Receiver: to.Address,
			Message:  marshaled,
		},
		Timeout: durationpb.New(timeout),
	}
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
func (r *Remoting) RemoteLookup(ctx context.Context, host string, port int, name string) (addr *address.Address, err error) {
	remoteClient := r.serviceClient(host, port)
	request := connect.NewRequest(
		&internalpb.RemoteLookupRequest{
			Host: host,
			Port: int32(port),
			Name: name,
		},
	)

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
func (r *Remoting) RemoteBatchTell(ctx context.Context, from, to *address.Address, messages []proto.Message) error {
	var requests []*internalpb.RemoteTellRequest
	for _, message := range messages {
		packed, err := anypb.New(message)
		if err != nil {
			return ErrInvalidMessage(err)
		}

		requests = append(
			requests, &internalpb.RemoteTellRequest{
				RemoteMessage: &internalpb.RemoteMessage{
					Sender:   from.Address,
					Receiver: to.Address,
					Message:  packed,
				},
			},
		)
	}

	remoteClient := r.serviceClient(to.GetHost(), int(to.GetPort()))
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
func (r *Remoting) RemoteBatchAsk(ctx context.Context, from, to *address.Address, messages []proto.Message, timeout time.Duration) (responses []*anypb.Any, err error) {
	var requests []*internalpb.RemoteAskRequest
	for _, message := range messages {
		packed, err := anypb.New(message)
		if err != nil {
			return nil, ErrInvalidMessage(err)
		}

		requests = append(
			requests, &internalpb.RemoteAskRequest{
				RemoteMessage: &internalpb.RemoteMessage{
					Sender:   from.Address,
					Receiver: to.Address,
					Message:  packed,
				},
				Timeout: durationpb.New(timeout),
			},
		)
	}

	remoteClient := r.serviceClient(to.GetHost(), int(to.GetPort()))
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

// RemoteSpawn creates an actor on a remote node. The given actor needs to be registered on the remote node using the Register method of ActorSystem
func (r *Remoting) RemoteSpawn(ctx context.Context, host string, port int, name, actorType string) error {
	remoteClient := r.serviceClient(host, port)
	request := connect.NewRequest(
		&internalpb.RemoteSpawnRequest{
			Host:      host,
			Port:      int32(port),
			ActorName: name,
			ActorType: actorType,
		},
	)

	if _, err := remoteClient.RemoteSpawn(ctx, request); err != nil {
		code := connect.CodeOf(err)
		if code == connect.CodeFailedPrecondition {
			var connectErr *connect.Error
			errors.As(err, &connectErr)
			e := connectErr.Unwrap()
			if strings.Contains(e.Error(), ErrTypeNotRegistered.Error()) {
				return ErrTypeNotRegistered
			}
		}
		return err
	}
	return nil
}

// RemoteReSpawn restarts actor on a remote node.
func (r *Remoting) RemoteReSpawn(ctx context.Context, host string, port int, name string) error {
	remoteClient := r.serviceClient(host, port)
	request := connect.NewRequest(
		&internalpb.RemoteReSpawnRequest{
			Host: host,
			Port: int32(port),
			Name: name,
		},
	)

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
func (r *Remoting) RemoteStop(ctx context.Context, host string, port int, name string) error {
	remoteClient := r.serviceClient(host, port)
	request := connect.NewRequest(
		&internalpb.RemoteStopRequest{
			Host: host,
			Port: int32(port),
			Name: name,
		},
	)

	if _, err := remoteClient.RemoteStop(ctx, request); err != nil {
		code := connect.CodeOf(err)
		if code == connect.CodeNotFound {
			return nil
		}
		return err
	}

	return nil
}

// HTTPClient returns the underlying http client
func (r *Remoting) HTTPClient() *nethttp.Client {
	return r.client
}

// MaxReadFrameSize returns the read max framesize
func (r *Remoting) MaxReadFrameSize() int {
	return r.maxReadFrameSize
}

// Close closes the serviceClient connection
func (r *Remoting) Close() {
	r.client.CloseIdleConnections()
}

// serviceClient returns a Remoting service client instance
func (r *Remoting) serviceClient(host string, port int) internalpbconnect.RemotingServiceClient {
	endpoint := http.URL(host, port)
	if r.clientTLS != nil {
		endpoint = http.URLs(host, port)
	}

	return internalpbconnect.NewRemotingServiceClient(
		r.client,
		endpoint,
		connect.WithGRPC(),
		connect.WithSendMaxBytes(r.maxReadFrameSize),
		connect.WithReadMaxBytes(r.maxReadFrameSize),
		connect.WithSendGzip(),
	)
}
