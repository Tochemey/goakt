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

package client

import (
	"context"
	"net"
	"strconv"
	"sync"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/actors"
	"github.com/tochemey/goakt/v2/internal/http"
	"github.com/tochemey/goakt/v2/internal/internalpb"
	"github.com/tochemey/goakt/v2/internal/internalpb/internalpbconnect"
	"github.com/tochemey/goakt/v2/internal/validation"
)

// Client defines the API to interact with the Go-Akt cluster
// via a load balancing mechanism
type Client interface {
	// Kinds returns the list of all the client kinds registered
	Kinds(ctx context.Context) ([]string, error)
	// Spawn creates an actor provided the actor name.
	// The actor name will be generated and returned when the request is successful
	Spawn(ctx context.Context, actor *Actor) (err error)
	// Tell sends a message to a given actor provided the actor name.
	// If the given actor does not exist it will be created automatically when
	// client mode is enabled
	Tell(ctx context.Context, actor *Actor, message proto.Message) error
	// Ask sends a message to a given actor provided the actor name and expects a response.
	// If the given actor does not exist it will be created automatically when
	// client mode is enabled. This will block until a response is received or timed out.
	Ask(ctx context.Context, actor *Actor, message proto.Message) (reply proto.Message, err error)
	// Kill kills a given actor in the client
	Kill(ctx context.Context, actor *Actor) error
}

// client implements Client
type client struct {
	nodes    []string
	locker   *sync.Mutex
	strategy BalancerStrategy
	balancer Balancer
}

// enforce compilatoin error
var _ Client = (*client)(nil)

// New creates an instance of Client. The provided nodes are the cluster nodes.
// A node is the form of host:port where host and port represents the remoting host
// and remoting port of the nodes. The nodes list will be load balanced based upon the load-balancing
// strategy defined by default round-robin will be used.
func New(nodes []string, opts ...Option) (Client, error) {
	chain := validation.
		New(validation.FailFast()).
		AddAssertion(len(nodes) != 0, "nodes are required")
	for _, host := range nodes {
		chain = chain.AddValidator(validation.NewTCPAddressValidator(host))
	}

	if err := chain.Validate(); err != nil {
		return nil, err
	}

	cl := &client{
		nodes:    nodes,
		locker:   &sync.Mutex{},
		strategy: RoundRobinStrategy,
	}

	// apply the various options
	for _, opt := range opts {
		opt.Apply(cl)
	}

	switch cl.strategy {
	case RoundRobinStrategy:
		cl.balancer = NewRoundRobin(nodes...)
	case RandomStrategy:
		cl.balancer = NewRandom(nodes...)
	default:
		// TODO: add more balancer strategy
	}

	return cl, nil
}

// Kinds returns the list of all the client kinds registered
func (x *client) Kinds(ctx context.Context) ([]string, error) {
	x.locker.Lock()
	defer x.locker.Unlock()
	interceptor, err := otelconnect.NewInterceptor()
	if err != nil {
		return nil, err
	}

	host, port := x.getNextRemotingHostAndPort()
	service := internalpbconnect.NewClusterServiceClient(
		http.NewClient(),
		http.URL(host, port),
		connect.WithGRPC(),
		connect.WithInterceptors(interceptor))

	response, err := service.GetKinds(ctx, connect.NewRequest(new(internalpb.GetKindsRequest)))
	if err != nil {
		return nil, err
	}

	return response.Msg.GetKinds(), nil
}

// Spawn creates an actor provided the actor name.
// The actor name will be generated and returned when the request is successful
func (x *client) Spawn(ctx context.Context, actor *Actor) (err error) {
	x.locker.Lock()
	defer x.locker.Unlock()
	host, port := x.getNextRemotingHostAndPort()
	return actors.RemoteSpawn(ctx, host, port, actor.Name(), actor.Kind())
}

// Tell sends a message to a given actor provided the actor name.
// If the given actor does not exist it will be created automatically when
// client mode is enabled
func (x *client) Tell(ctx context.Context, actor *Actor, message proto.Message) error {
	x.locker.Lock()
	defer x.locker.Unlock()
	host, port := x.getNextRemotingHostAndPort()
	address, err := actors.RemoteLookup(ctx, host, port, actor.Name())
	if err != nil {
		return err
	}
	return actors.RemoteTell(ctx, address, message)
}

// Ask sends a message to a given actor provided the actor name and expects a response.
// If the given actor does not exist it will be created automatically when
// client mode is enabled. This will block until a response is received or timed out.
func (x *client) Ask(ctx context.Context, actor *Actor, message proto.Message) (reply proto.Message, err error) {
	x.locker.Lock()
	defer x.locker.Unlock()
	host, port := x.getNextRemotingHostAndPort()
	address, err := actors.RemoteLookup(ctx, host, port, actor.Name())
	if err != nil {
		return nil, err
	}
	return actors.RemoteAsk(ctx, address, message)
}

// Kill kills a given actor in the client
func (x *client) Kill(ctx context.Context, actor *Actor) error {
	x.locker.Lock()
	defer x.locker.Unlock()
	host, port := x.getNextRemotingHostAndPort()
	return actors.RemoteStop(ctx, host, port, actor.Name())
}

func (x *client) getNextRemotingHostAndPort() (host string, port int) {
	node := x.balancer.Next()
	host, p, _ := net.SplitHostPort(node)
	port, _ = strconv.Atoi(p)
	return
}
