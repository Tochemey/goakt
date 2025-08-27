/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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

package remote

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v3/address"
	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/internal/codec"
	"github.com/tochemey/goakt/v3/internal/grpcc"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/size"
)

const DefaultMaxReadFrameSize = 16 * size.MB

type Remoting interface {
	RemoteTell(ctx context.Context, from, to *address.Address, message proto.Message) error
	RemoteAsk(ctx context.Context, from, to *address.Address, message proto.Message, timeout time.Duration) (response *anypb.Any, err error)
	RemoteLookup(ctx context.Context, host string, port int, name string) (addr *address.Address, err error)
	RemoteBatchTell(ctx context.Context, from, to *address.Address, messages []proto.Message) error
	RemoteBatchAsk(ctx context.Context, from, to *address.Address, messages []proto.Message, timeout time.Duration) (responses []*anypb.Any, err error)
	RemoteSpawn(ctx context.Context, host string, port int, spawnRequest *SpawnRequest) error
	RemoteReSpawn(ctx context.Context, host string, port int, name string) error
	RemoteStop(ctx context.Context, host string, port int, name string) error
	RemoteReinstate(ctx context.Context, host string, port int, name string) error
	MaxReadFrameSize() int
	RemotingServiceClient(host string, port int) (internalpb.RemotingServiceClient, *grpc.ClientConn)
}

// RemotingOption sets the remoting option
type RemotingOption func(*remoting)

// WithRemotingTLS configures the remoting system to use a secure connection
// for communication with the specified remote node. This requires a TLS
// client configuration to enable secure interactions with the remote actor system.
//
// Ensure that the remote actor system is configured with TLS enabled and
// capable of completing a successful handshake. It is recommended that both
// systems share the same root Certificate Authority (CA) for mutual trust and
// secure communication.
func WithRemotingTLS(tlsConfig *tls.Config) RemotingOption {
	return func(r *remoting) {
		r.tlsConfig = tlsConfig
	}
}

// WithRemotingMaxReadFameSize sets both the maximum framesize to send and receive
func WithRemotingMaxReadFameSize(size int) RemotingOption {
	return func(r *remoting) {
		r.maxReadFrameSize = size
	}
}

// Remoting defines the Remoting APIs
// This requires Remoting is enabled on the connected actor system
type remoting struct {
	tlsConfig        *tls.Config
	maxReadFrameSize int
}

var _ Remoting = (*remoting)(nil)

// NewRemoting creates an instance Remoting with an insecure connection. To use a secure connection
// one need to call the WithTLS method of the remoting instance to set the certificates of the secure connection
// This requires Remoting is enabled on the connected actor system
// Make sure to call Close to free up resources otherwise you may be leaking socket connections
//
// One can also override the remoting option when calling any of the method for custom one.
func NewRemoting(opts ...RemotingOption) Remoting {
	r := &remoting{
		maxReadFrameSize: DefaultMaxReadFrameSize,
	}

	// apply the options
	for _, opt := range opts {
		opt(r)
	}

	return r
}

// RemoteTell sends a message to an actor remotely without expecting any reply
func (r *remoting) RemoteTell(ctx context.Context, from, to *address.Address, message proto.Message) error {
	marshaled, err := anypb.New(message)
	if err != nil {
		return gerrors.NewErrInvalidMessage(err)
	}

	client, conn := r.RemotingServiceClient(to.GetHost(), int(to.GetPort()))
	defer conn.Close()

	request := &internalpb.RemoteTellRequest{
		RemoteMessages: []*internalpb.RemoteMessage{
			{
				Sender:   from.Address,
				Receiver: to.Address,
				Message:  marshaled,
			},
		},
	}

	_, err = client.RemoteTell(ctx, request)
	return err
}

// RemoteAsk sends a synchronous message to another actor remotely and expect a response.
func (r *remoting) RemoteAsk(ctx context.Context, from, to *address.Address, message proto.Message, timeout time.Duration) (response *anypb.Any, err error) {
	marshaled, err := anypb.New(message)
	if err != nil {
		return nil, gerrors.NewErrInvalidMessage(err)
	}

	client, conn := r.RemotingServiceClient(to.GetHost(), int(to.GetPort()))
	defer conn.Close()

	request := &internalpb.RemoteAskRequest{
		RemoteMessages: []*internalpb.RemoteMessage{
			{
				Sender:   from.Address,
				Receiver: to.Address,
				Message:  marshaled,
			},
		},
		Timeout: durationpb.New(timeout),
	}

	resp, err := client.RemoteAsk(ctx, request)
	if err != nil {
		return nil, err
	}

	if resp != nil {
		for _, msg := range resp.GetMessages() {
			response = msg
			break
		}
	}

	return
}

// RemoteLookup look for an actor address on a remote node.
func (r *remoting) RemoteLookup(ctx context.Context, host string, port int, name string) (addr *address.Address, err error) {
	client, conn := r.RemotingServiceClient(host, port)
	defer conn.Close()

	request := &internalpb.RemoteLookupRequest{
		Host: host,
		Port: int32(port),
		Name: name,
	}

	response, err := client.RemoteLookup(ctx, request)
	if err != nil {
		code := status.Code(err)
		if code == codes.NotFound {
			return address.NoSender(), nil
		}
		return nil, err
	}

	return address.From(response.GetAddress()), nil
}

// RemoteBatchTell sends bulk asynchronous messages to an actor
func (r *remoting) RemoteBatchTell(ctx context.Context, from, to *address.Address, messages []proto.Message) error {
	client, conn := r.RemotingServiceClient(to.GetHost(), int(to.GetPort()))
	defer conn.Close()

	remoteMessages := make([]*internalpb.RemoteMessage, 0, len(messages))
	for _, message := range messages {
		if message != nil {
			packed, _ := anypb.New(message)
			remoteMessages = append(remoteMessages, &internalpb.RemoteMessage{
				Sender:   from.Address,
				Receiver: to.Address,
				Message:  packed,
			})
		}
	}

	_, err := client.RemoteTell(ctx, &internalpb.RemoteTellRequest{RemoteMessages: remoteMessages})
	return err
}

// RemoteBatchAsk sends bulk messages to an actor with responses expected
func (r *remoting) RemoteBatchAsk(ctx context.Context, from, to *address.Address, messages []proto.Message, timeout time.Duration) (responses []*anypb.Any, err error) {
	client, conn := r.RemotingServiceClient(to.GetHost(), int(to.GetPort()))
	defer conn.Close()

	remoteMessages := make([]*internalpb.RemoteMessage, 0, len(messages))
	for _, message := range messages {
		if message != nil {
			packed, err := anypb.New(message)
			if err != nil {
				return nil, gerrors.NewErrInvalidMessage(err)
			}
			remoteMessages = append(remoteMessages, &internalpb.RemoteMessage{
				Sender:   from.Address,
				Receiver: to.Address,
				Message:  packed,
			})
		}
	}

	resp, err := client.RemoteAsk(ctx, &internalpb.RemoteAskRequest{
		RemoteMessages: remoteMessages,
		Timeout:        durationpb.New(timeout),
	})

	if err != nil {
		return nil, err
	}

	if resp != nil {
		responses = append(responses, resp.GetMessages()...)
	}

	return
}

// RemoteSpawn creates an actor on a remote node. The given actor needs to be registered on the remote node using the Register method of ActorSystem
func (r *remoting) RemoteSpawn(ctx context.Context, host string, port int, spawnRequest *SpawnRequest) error {
	if err := spawnRequest.Validate(); err != nil {
		return fmt.Errorf("invalid spawn option: %w", err)
	}

	spawnRequest.Sanitize()

	var (
		dependencies []*internalpb.Dependency
		err          error
	)

	if len(spawnRequest.Dependencies) > 0 {
		dependencies, err = codec.EncodeDependencies(spawnRequest.Dependencies...)
		if err != nil {
			return err
		}
	}

	client, conn := r.RemotingServiceClient(host, port)
	defer conn.Close()

	request := &internalpb.RemoteSpawnRequest{
		Host:                host,
		Port:                int32(port),
		ActorName:           spawnRequest.Name,
		ActorType:           spawnRequest.Kind,
		IsSingleton:         spawnRequest.Singleton,
		Relocatable:         spawnRequest.Relocatable,
		PassivationStrategy: codec.EncodePassivationStrategy(spawnRequest.PassivationStrategy),
		Dependencies:        dependencies,
		EnableStash:         spawnRequest.EnableStashing,
	}

	if _, err := client.RemoteSpawn(ctx, request); err != nil {
		status := status.Convert(err)
		if status.Code() == codes.FailedPrecondition {
			if strings.Contains(status.Message(), gerrors.ErrTypeNotRegistered.Error()) {
				return gerrors.ErrTypeNotRegistered
			}
		}
		return err
	}
	return nil
}

// RemoteReSpawn restarts actor on a remote node.
func (r *remoting) RemoteReSpawn(ctx context.Context, host string, port int, name string) error {
	client, conn := r.RemotingServiceClient(host, port)
	defer conn.Close()

	request := &internalpb.RemoteReSpawnRequest{
		Host: host,
		Port: int32(port),
		Name: name,
	}

	if _, err := client.RemoteReSpawn(ctx, request); err != nil {
		code := status.Code(err)
		if code == codes.NotFound {
			return nil
		}
		return err
	}

	return nil
}

// RemoteStop stops an actor on a remote node.
func (r *remoting) RemoteStop(ctx context.Context, host string, port int, name string) error {
	client, conn := r.RemotingServiceClient(host, port)
	defer conn.Close()

	request := &internalpb.RemoteStopRequest{
		Host: host,
		Port: int32(port),
		Name: name,
	}

	if _, err := client.RemoteStop(ctx, request); err != nil {
		code := status.Code(err)
		if code == codes.NotFound {
			return nil
		}
		return err
	}

	return nil
}

// RemoteReinstate reinstates an actor on a remote node.
func (r *remoting) RemoteReinstate(ctx context.Context, host string, port int, name string) error {
	client, conn := r.RemotingServiceClient(host, port)
	defer conn.Close()

	request := &internalpb.RemoteReinstateRequest{
		Host: host,
		Port: int32(port),
		Name: name,
	}

	if _, err := client.RemoteReinstate(ctx, request); err != nil {
		code := status.Code(err)
		if code == codes.NotFound {
			return nil
		}
		return err
	}

	return nil
}

// MaxReadFrameSize returns the read max framesize
func (r *remoting) MaxReadFrameSize() int {
	return r.maxReadFrameSize
}

func (r *remoting) RemotingServiceClient(host string, port int) (internalpb.RemotingServiceClient, *grpc.ClientConn) {
	endpoint := fmt.Sprintf("%s:%d", host, port)
	conn := grpcc.NewConn(endpoint,
		grpcc.WithConnTLS(r.tlsConfig),
		grpcc.WithConnMaxRecvMsgSize(r.maxReadFrameSize),
		grpcc.WithConnMaxSendMsgSize(r.maxReadFrameSize))

	client, _ := conn.Dial()
	remoteClient := internalpb.NewRemotingServiceClient(client)
	return remoteClient, client
}
