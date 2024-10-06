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
	"fmt"
	"net"
	sdhttp "net/http"
	"strconv"
	"sync"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/actors"
	"github.com/tochemey/goakt/v2/address"
	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/internal/http"
	"github.com/tochemey/goakt/v2/internal/internalpb"
	"github.com/tochemey/goakt/v2/internal/internalpb/internalpbconnect"
	"github.com/tochemey/goakt/v2/internal/types"
	"github.com/tochemey/goakt/v2/internal/validation"
)

// Client connects to af Go-Akt nodes.
// This client can only be used when remoting is enabled on the various nodes.
// The client is only used against a Go-Akt cluster
type Client struct {
	nodes           []*Node
	locker          *sync.Mutex
	strategy        BalancerStrategy
	balancer        Balancer
	closeSignal     chan types.Unit
	refreshInterval time.Duration
	client          *sdhttp.Client
}

// New creates an instance of Client. The provided nodes are the cluster nodes.
// A node is the form of host:port where host and port represents the remoting host
// and remoting port of the nodes. The nodes list will be load balanced based upon the load-balancing
// strategy defined by default round-robin will be used.
// An instance of the Client can be reused and it is thread safe.
func New(ctx context.Context, addresses []string, opts ...Option) (*Client, error) {
	chain := validation.
		New(validation.FailFast()).
		AddAssertion(len(addresses) != 0, "addresses are required")
	for _, host := range addresses {
		chain = chain.AddValidator(validation.NewTCPAddressValidator(host))
	}
	if err := chain.Validate(); err != nil {
		return nil, err
	}
	client := &Client{
		locker:          &sync.Mutex{},
		strategy:        RoundRobinStrategy,
		refreshInterval: -1,
		client:          http.NewClient(),
	}
	// apply the various options
	for _, opt := range opts {
		opt.Apply(client)
	}

	var nodes []*Node
	for _, url := range addresses {
		weight, ok, err := client.getNodeMetric(ctx, url)
		if err != nil {
			return nil, err
		}

		if !ok {
			continue
		}
		nodes = append(nodes, NewNode(url, weight))
	}
	client.balancer = getBalancer(client.strategy)
	client.nodes = nodes
	client.balancer.Set(client.nodes...)
	// only refresh addresses when refresh interval is set
	if client.refreshInterval > 0 {
		client.closeSignal = make(chan types.Unit, 1)
		go client.refreshNodesLoop()
	}
	return client, nil
}

// Close closes the Client connection
func (x *Client) Close() {
	x.locker.Lock()
	x.nodes = make([]*Node, 0)
	if x.refreshInterval > 0 {
		close(x.closeSignal)
	}
	x.client.CloseIdleConnections()
	x.locker.Unlock()
}

// Kinds returns the list of all the Client kinds registered
func (x *Client) Kinds(ctx context.Context) ([]string, error) {
	x.locker.Lock()
	defer x.locker.Unlock()

	host, port := nextRemotingHostAndPort(x.balancer)
	service := internalpbconnect.NewClusterServiceClient(
		http.NewClient(),
		http.URL(host, port))

	response, err := service.GetKinds(ctx, connect.NewRequest(&internalpb.GetKindsRequest{
		NodeAddress: fmt.Sprintf("%s:%d", host, port),
	}))
	if err != nil {
		return nil, err
	}
	return response.Msg.GetKinds(), nil
}

// Spawn creates an actor provided the actor name.
func (x *Client) Spawn(ctx context.Context, actor *Actor) (err error) {
	x.locker.Lock()
	remoteHost, remotePort := nextRemotingHostAndPort(x.balancer)
	x.locker.Unlock()
	return actors.RemoteSpawn(ctx, remoteHost, remotePort, actor.Name(), actor.Kind())
}

// SpawnWithBalancer creates an actor provided the actor name and the balancer strategy
func (x *Client) SpawnWithBalancer(ctx context.Context, actor *Actor, strategy BalancerStrategy) (err error) {
	x.locker.Lock()
	balancer := getBalancer(strategy)
	balancer.Set(x.nodes...)
	remoteHost, remotePort := nextRemotingHostAndPort(balancer)
	x.locker.Unlock()
	return actors.RemoteSpawn(ctx, remoteHost, remotePort, actor.Name(), actor.Kind())
}

// ReSpawn restarts a given actor
func (x *Client) ReSpawn(ctx context.Context, actor *Actor) (err error) {
	x.locker.Lock()
	remoteHost, remotePort := nextRemotingHostAndPort(x.balancer)
	x.locker.Unlock()
	return actors.RemoteReSpawn(ctx, remoteHost, remotePort, actor.Name())
}

// Tell sends a message to a given actor provided the actor name.
// If the given actor does not exist it will be created automatically when
// Client mode is enabled
func (x *Client) Tell(ctx context.Context, actor *Actor, message proto.Message) error {
	// lookup the actor address
	address, err := x.Whereis(ctx, actor)
	if err != nil {
		return err
	}
	return actors.RemoteTell(ctx, address, message)
}

// Ask sends a message to a given actor provided the actor name and expects a response.
// If the given actor does not exist it will be created automatically when
// Client mode is enabled. This will block until a response is received or timed out.
func (x *Client) Ask(ctx context.Context, actor *Actor, message proto.Message, timeout time.Duration) (reply proto.Message, err error) {
	// lookup the actor address
	address, err := x.Whereis(ctx, actor)
	if err != nil {
		return nil, err
	}
	response, err := actors.RemoteAsk(ctx, address, message, timeout)
	if err != nil {
		return nil, err
	}
	return response.UnmarshalNew()
}

// Stop stops or kills a given actor in the Client
func (x *Client) Stop(ctx context.Context, actor *Actor) error {
	x.locker.Lock()
	remoteHost, remotePort := nextRemotingHostAndPort(x.balancer)
	x.locker.Unlock()
	return actors.RemoteStop(ctx, remoteHost, remotePort, actor.Name())
}

// Whereis finds and returns the address of a given actor
func (x *Client) Whereis(ctx context.Context, actor *Actor) (*address.Address, error) {
	x.locker.Lock()
	remoteHost, remotePort := nextRemotingHostAndPort(x.balancer)
	x.locker.Unlock()
	// lookup the actor address
	address, err := actors.RemoteLookup(ctx, remoteHost, remotePort, actor.Name())
	if err != nil {
		return nil, err
	}
	// no address found
	if address == nil || proto.Equal(address, new(goaktpb.Address)) {
		return nil, actors.ErrActorNotFound(actor.Name())
	}
	return address, nil
}

// nextRemotingHostAndPort returns the next node host and port
func nextRemotingHostAndPort(balancer Balancer) (host string, port int) {
	node := balancer.Next()
	host, p, _ := net.SplitHostPort(node.Address())
	port, _ = strconv.Atoi(p)
	return
}

// updateNodes updates the list of nodes availables in the pool
// the old nodes pool is completely replaced by the new nodes pool
func (x *Client) updateNodes(ctx context.Context) error {
	x.locker.Unlock()
	defer x.locker.Lock()

	for _, node := range x.nodes {
		weight, ok, err := x.getNodeMetric(ctx, node.Address())
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		node.SetWeight(float64(weight))
	}
	return nil
}

// refreshNodesLoop refreshes the nodes
func (x *Client) refreshNodesLoop() {
	ticker := time.NewTicker(x.refreshInterval)
	tickerStopSig := make(chan types.Unit, 1)
	go func() {
		for {
			select {
			case <-ticker.C:
				if err := x.updateNodes(context.Background()); err != nil {
					// TODO: is it good to panic?
					panic(err)
				}
			case <-x.closeSignal:
				tickerStopSig <- types.Unit{}
				return
			}
		}
	}()
	<-tickerStopSig
	ticker.Stop()
}

// getBalancer returns the balancer based upon the strategy
func getBalancer(strategy BalancerStrategy) Balancer {
	switch strategy {
	case RoundRobinStrategy:
		return NewRoundRobin()
	case RandomStrategy:
		return NewRandom()
	case LeastLoadStrategy:
		return NewLeastLoad()
	default:
		return NewRoundRobin()
	}
}

// getNodeMetric pings a given node and get the node metric info and
func (x *Client) getNodeMetric(ctx context.Context, node string) (int, bool, error) {
	host, p, _ := net.SplitHostPort(node)
	port, _ := strconv.Atoi(p)
	service := internalpbconnect.NewClusterServiceClient(
		x.client,
		http.URL(host, port))

	response, err := service.GetNodeMetric(ctx, connect.NewRequest(&internalpb.GetNodeMetricRequest{NodeAddress: node}))
	if err != nil {
		code := connect.CodeOf(err)
		// here node may not be available
		if code == connect.CodeUnavailable ||
			code == connect.CodeCanceled ||
			code == connect.CodeDeadlineExceeded {
			return 0, false, nil
		}

		return 0, false, err
	}
	return int(response.Msg.GetActorsCount()), true, nil
}
