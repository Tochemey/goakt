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
	"errors"
	"fmt"
	nethttp "net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"go.akshayshah.org/connectproto"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v3/address"
	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/internal/brotli"
	"github.com/tochemey/goakt/v3/internal/codec"
	"github.com/tochemey/goakt/v3/internal/http"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/internalpb/internalpbconnect"
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
	HTTPClient() *nethttp.Client
	MaxReadFrameSize() int
	Close()
	RemotingServiceClient(host string, port int) internalpbconnect.RemotingServiceClient
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
	client           *nethttp.Client
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

	if r.tlsConfig != nil {
		r.client = http.NewHTTPSClient(r.tlsConfig, uint32(r.maxReadFrameSize)) // nolint
	} else {
		r.client = http.NewHTTPClient(uint32(r.maxReadFrameSize))
	}
	return r
}

// RemoteTell sends a message to an actor remotely without expecting any reply
func (r *remoting) RemoteTell(ctx context.Context, from, to *address.Address, message proto.Message) error {
	marshaled, err := anypb.New(message)
	if err != nil {
		return gerrors.NewErrInvalidMessage(err)
	}

	remoteClient := r.RemotingServiceClient(to.GetHost(), int(to.GetPort()))
	request := connect.NewRequest(&internalpb.RemoteTellRequest{
		RemoteMessages: []*internalpb.RemoteMessage{
			{
				Sender:   from.Address,
				Receiver: to.Address,
				Message:  marshaled,
			},
		},
	})

	_, err = remoteClient.RemoteTell(ctx, request)
	return err
}

// RemoteAsk sends a synchronous message to another actor remotely and expect a response.
func (r *remoting) RemoteAsk(ctx context.Context, from, to *address.Address, message proto.Message, timeout time.Duration) (response *anypb.Any, err error) {
	marshaled, err := anypb.New(message)
	if err != nil {
		return nil, gerrors.NewErrInvalidMessage(err)
	}

	remoteClient := r.RemotingServiceClient(to.GetHost(), int(to.GetPort()))
	request := connect.NewRequest(&internalpb.RemoteAskRequest{
		RemoteMessages: []*internalpb.RemoteMessage{
			{
				Sender:   from.Address,
				Receiver: to.Address,
				Message:  marshaled,
			},
		},
		Timeout: durationpb.New(timeout),
	})

	resp, err := remoteClient.RemoteAsk(ctx, request)
	if err != nil {
		return nil, err
	}

	if resp != nil {
		for _, msg := range resp.Msg.GetMessages() {
			response = msg
			break
		}
	}

	return
}

// RemoteLookup look for an actor address on a remote node.
func (r *remoting) RemoteLookup(ctx context.Context, host string, port int, name string) (addr *address.Address, err error) {
	remoteClient := r.RemotingServiceClient(host, port)
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
			return address.NoSender(), nil
		}
		return nil, err
	}

	return address.From(response.Msg.GetAddress()), nil
}

// RemoteBatchTell sends bulk asynchronous messages to an actor
func (r *remoting) RemoteBatchTell(ctx context.Context, from, to *address.Address, messages []proto.Message) error {
	remoteClient := r.RemotingServiceClient(to.GetHost(), int(to.GetPort()))
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

	_, err := remoteClient.RemoteTell(ctx, connect.NewRequest(&internalpb.RemoteTellRequest{
		RemoteMessages: remoteMessages,
	}))
	return err
}

// RemoteBatchAsk sends bulk messages to an actor with responses expected
func (r *remoting) RemoteBatchAsk(ctx context.Context, from, to *address.Address, messages []proto.Message, timeout time.Duration) (responses []*anypb.Any, err error) {
	remoteClient := r.RemotingServiceClient(to.GetHost(), int(to.GetPort()))

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

	resp, err := remoteClient.RemoteAsk(ctx, connect.NewRequest(&internalpb.RemoteAskRequest{
		RemoteMessages: remoteMessages,
		Timeout:        durationpb.New(timeout),
	}))

	if err != nil {
		return nil, err
	}

	if resp != nil {
		responses = append(responses, resp.Msg.GetMessages()...)
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

	remoteClient := r.RemotingServiceClient(host, port)
	request := connect.NewRequest(
		&internalpb.RemoteSpawnRequest{
			Host:                host,
			Port:                int32(port),
			ActorName:           spawnRequest.Name,
			ActorType:           spawnRequest.Kind,
			IsSingleton:         spawnRequest.Singleton,
			Relocatable:         spawnRequest.Relocatable,
			PassivationStrategy: codec.EncodePassivationStrategy(spawnRequest.PassivationStrategy),
			Dependencies:        dependencies,
			EnableStash:         spawnRequest.EnableStashing,
		},
	)

	if _, err := remoteClient.RemoteSpawn(ctx, request); err != nil {
		code := connect.CodeOf(err)
		if code == connect.CodeFailedPrecondition {
			var connectErr *connect.Error
			errors.As(err, &connectErr)
			e := connectErr.Unwrap()
			if strings.Contains(e.Error(), gerrors.ErrTypeNotRegistered.Error()) {
				return gerrors.ErrTypeNotRegistered
			}
		}
		return err
	}
	return nil
}

// RemoteReSpawn restarts actor on a remote node.
func (r *remoting) RemoteReSpawn(ctx context.Context, host string, port int, name string) error {
	remoteClient := r.RemotingServiceClient(host, port)
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
func (r *remoting) RemoteStop(ctx context.Context, host string, port int, name string) error {
	remoteClient := r.RemotingServiceClient(host, port)
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

// RemoteReinstate reinstates an actor on a remote node.
func (r *remoting) RemoteReinstate(ctx context.Context, host string, port int, name string) error {
	remoteClient := r.RemotingServiceClient(host, port)
	request := connect.NewRequest(
		&internalpb.RemoteReinstateRequest{
			Host: host,
			Port: int32(port),
			Name: name,
		},
	)

	if _, err := remoteClient.RemoteReinstate(ctx, request); err != nil {
		code := connect.CodeOf(err)
		if code == connect.CodeNotFound {
			return nil
		}
		return err
	}

	return nil
}

// HTTPClient returns the underlying http client
func (r *remoting) HTTPClient() *nethttp.Client {
	return r.client
}

// MaxReadFrameSize returns the read max framesize
func (r *remoting) MaxReadFrameSize() int {
	return r.maxReadFrameSize
}

// Close closes the serviceClient connection
func (r *remoting) Close() {
	r.client.CloseIdleConnections()
}

// RemotingServiceClient returns a Remoting service client instance
func (r *remoting) RemotingServiceClient(host string, port int) internalpbconnect.RemotingServiceClient {
	endpoint := http.URL(host, port)
	if r.tlsConfig != nil {
		endpoint = http.URLs(host, port)
	}

	return internalpbconnect.NewRemotingServiceClient(
		r.client,
		endpoint,
		brotli.WithCompression(),
		connect.WithSendCompression(brotli.Name),
		connect.WithSendMaxBytes(r.maxReadFrameSize),
		connect.WithReadMaxBytes(r.maxReadFrameSize),
		connectproto.WithBinary(
			proto.MarshalOptions{},
			proto.UnmarshalOptions{DiscardUnknown: true},
		),
	)
}
