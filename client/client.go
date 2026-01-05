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

package client

import (
	"context"
	"sync"
	"time"

	"connectrpc.com/connect"
	"go.akshayshah.org/connectproto"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/address"
	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/internal/chain"
	"github.com/tochemey/goakt/v3/internal/compression/brotli"
	"github.com/tochemey/goakt/v3/internal/compression/zstd"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/internalpb/internalpbconnect"
	"github.com/tochemey/goakt/v3/internal/locker"
	"github.com/tochemey/goakt/v3/internal/ticker"
	"github.com/tochemey/goakt/v3/internal/types"
	"github.com/tochemey/goakt/v3/internal/validation"
	"github.com/tochemey/goakt/v3/remote"
)

// Client provides connectivity to Go-Akt actor system nodes within a cluster.
//
// The Client enables remote communication with Go-Akt nodes and requires remoting
// to be enabled on all target nodes in the cluster. It is specifically designed
// for interacting with Go-Akt clusters and handles the underlying networking,
// serialization, and actor addressing concerns.
//
// Key capabilities:
//   - Remote actor discovery and communication
//   - Cluster topology awareness
//   - Connection pooling and management
//   - Message routing and delivery
//
// Thread safety: Client is safe for concurrent use by multiple goroutines.
type Client struct {
	_               locker.NoCopy
	nodes           []*Node
	locker          sync.Mutex
	strategy        BalancerStrategy
	balancer        Balancer
	closeSignal     chan types.Unit
	refreshInterval time.Duration
}

// New creates and initializes a new Client instance for interacting with a cluster of nodes.
//
// The provided `nodes` parameter defines the initial list of cluster nodes, where each node
// is specified as `host:port` representing the remoting host and port of the actor system.
//
// By default, the client uses a round-robin load-balancing strategy to distribute actor-related
// operations across the provided nodes. This behavior can be customized via optional parameters.
//
// Parameters:
//   - ctx: Context for initialization and potential cancellation.
//   - nodes: A slice of pointers to Node, each representing a cluster node.
//   - opts: Optional configuration values (e.g., custom balancer).
//
// Returns:
//   - *Client: A thread-safe client instance that can be reused across goroutines.
//   - error: An error if client initialization fails (e.g., invalid nodes, config issues).
//
// Note:
//   - The returned Client is safe for concurrent use.
//   - Nodes are assumed to be reachable and running a compatible actor runtime.
func New(ctx context.Context, nodes []*Node, opts ...Option) (*Client, error) {
	if err := chain.
		New(chain.WithFailFast()).
		AddRunner(func() error { return validateNodes(nodes) }).
		AddRunner(func() error { return setNodesMetric(ctx, nodes) }).
		Run(); err != nil {
		return nil, err
	}

	cl := &Client{
		strategy:        RoundRobinStrategy,
		refreshInterval: -1,
	}
	// apply the various options
	for _, opt := range opts {
		opt.Apply(cl)
	}

	cl.balancer = getBalancer(cl.strategy)
	cl.nodes = nodes
	cl.balancer.Set(cl.nodes...)
	// only refresh addresses when a refresh interval is set
	if cl.refreshInterval > 0 {
		cl.closeSignal = make(chan types.Unit, 1)
		go cl.refreshNodesLoop()
	}
	return cl, nil
}

// Close closes the Client connection
func (x *Client) Close() {
	x.locker.Lock()
	for _, node := range x.nodes {
		node.Free()
	}
	x.nodes = make([]*Node, 0)
	if x.refreshInterval > 0 {
		close(x.closeSignal)
	}
	x.locker.Unlock()
}

// Kinds returns the list of all actor kinds currently registered in the cluster.
//
// Actor kinds represent the different types or categories of gerrors that
// are available for spawning or interaction within the cluster.
//
// Parameters:
//   - ctx: Context used for cancellation and timeout control.
//
// Returns:
//   - []string: A slice of strings representing the registered actor kinds.
//   - Error: An error if the request to retrieve kinds fails.
func (x *Client) Kinds(ctx context.Context) ([]string, error) {
	x.locker.Lock()
	defer x.locker.Unlock()

	node := nextNode(x.balancer)
	service := clusterClient(node)

	response, err := service.GetKinds(
		ctx, connect.NewRequest(
			&internalpb.GetKindsRequest{
				NodeAddress: node.Address(),
			},
		),
	)
	if err != nil {
		return nil, err
	}
	return response.Msg.GetKinds(), nil
}

// Spawn creates and starts an actor using the default balancing strategy.
//
// This method initializes the provided actor and places it on an available node
// using the default balancing strategy configured in the Client. It supports
// singleton enforcement and relocatability.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control.
//   - spawnRequest: The spawn request containing actor details and configuration.
//
// Returns:
//   - err: An error if actor creation or placement fails.
//
// Note:
//   - If you require custom balancing logic, use SpawnBalanced instead.
func (x *Client) Spawn(ctx context.Context, spawnRequest *remote.SpawnRequest) (err error) {
	x.locker.Lock()
	node := nextNode(x.balancer)
	x.locker.Unlock()
	remoteHost, remotePort := node.HostAndPort()
	return node.Remoting().RemoteSpawn(ctx, remoteHost, remotePort, spawnRequest)
}

// SpawnBalanced creates and starts an actor with the specified balancing strategy.
//
// This method spawns the given actor across a cluster or local node using a
// provided `BalancerStrategy` to determine optimal placement. It supports
// singleton enforcement and relocatability of the actor.
//
// Parameters:
//   - ctx: Context used for cancellation and timeout control.
//   - spawnRequest: The spawn request containing actor details and configuration.
//   - strategy: Defines the balancing strategy (e.g., round-robin, least-loaded) to use
//     for actor placement.
//
// Returns:
//   - err: Non-nil if spawning the actor fails (e.g., invalid actor, placement error, etc.)
func (x *Client) SpawnBalanced(ctx context.Context, spawnRequest *remote.SpawnRequest, strategy BalancerStrategy) (err error) {
	x.locker.Lock()
	balancer := getBalancer(strategy)
	balancer.Set(x.nodes...)
	node := nextNode(balancer)
	remoteHost, remotePort := node.HostAndPort()
	x.locker.Unlock()
	return node.Remoting().RemoteSpawn(ctx, remoteHost, remotePort, spawnRequest)
}

// ReSpawn restarts a previously spawned actor.
//
// This method stops the given actor (if running) and starts a new instance of it,
// preserving its identity. It is typically used in scenarios where the actor has
// failed or needs to be re-initialized due to updated state or configuration.
//
// Parameters:
//   - ctx: Context used for cancellation and timeout control.
//   - actorName: The actor name to be restarted.
//
// Returns:
//   - err: An error if the actor could not be stopped, restarted, or re-registered.
//
// Note:
//   - ReSpawn does not guarantee preservation of the in-memory state unless the actor
//     is designed to persist and recover it (e.g., via snapshotting or event sourcing).
func (x *Client) ReSpawn(ctx context.Context, actorName string) (err error) {
	x.locker.Lock()
	node := nextNode(x.balancer)
	x.locker.Unlock()
	remoteHost, remotePort := node.HostAndPort()
	remoting := node.Remoting()

	addr, err := remoting.RemoteLookup(ctx, remoteHost, remotePort, actorName)
	if err != nil {
		return err
	}

	if addr.Equals(address.NoSender()) {
		return nil
	}

	return remoting.RemoteReSpawn(ctx, addr.Host(), addr.Port(), actorName)
}

// Tell sends a message to the specified actor.
//
// This method delivers the given protobuf message to the target actor. If the actor
// is not currently running or registered in the system, a NOT_FOUND error is returned.
//
// Parameters:
//   - ctx: Context used for cancellation and timeout control.
//   - actorName: The actor name.
//   - Message: A protobuf message to send to the actor. This must be a valid, serializable message.
//
// Returns:
//   - error: Returns nil on success. Returns a NOT_FOUND error if the actor is not available.
//
// Note:
//   - This method is asynchronous; it does not wait for a response.
//   - For request-response patterns, consider using `Ask` instead of `Tell`.
func (x *Client) Tell(ctx context.Context, actorName string, message proto.Message) error {
	x.locker.Lock()
	node := nextNode(x.balancer)
	x.locker.Unlock()
	remoteHost, remotePort := node.HostAndPort()
	remoting := node.Remoting()

	to, err := remoting.RemoteLookup(ctx, remoteHost, remotePort, actorName)
	if err != nil {
		return err
	}

	if to.Equals(address.NoSender()) {
		return gerrors.NewErrActorNotFound(actorName)
	}

	from := address.NoSender()
	return remoting.RemoteTell(ctx, from, to, message)
}

// Ask sends a message to the specified actor and waits for a response.
//
// This method sends a protobuf message to the given actor and blocks until a reply is received
// or the timeout is reached. It is intended for request-response communication patterns.
//
// Parameters:
//   - ctx: Context used for cancellation and deadline control.
//   - actorName: The actor name..
//   - message: A protobuf message to send to the actor. This must be a valid, serializable message.
//   - timeout: The maximum duration to wait for a response from the actor.
//
// Returns:
//   - reply: The protobuf message returned by the actor.
//   - err: Returns an error if the actor is not found, if the timeout is exceeded,
//     or if message delivery or handling fails.
//
// Note:
//   - If the actor does not exist or is unreachable, a NOT_FOUND error is returned.
//   - Ensure the actor is designed to handle the incoming message and reply appropriately.
//   - For fire-and-forget messaging, use `Tell` instead of `Ask`.
func (x *Client) Ask(ctx context.Context, actorName string, message proto.Message, timeout time.Duration) (reply proto.Message, err error) {
	x.locker.Lock()
	node := nextNode(x.balancer)
	x.locker.Unlock()
	remoteHost, remotePort := node.HostAndPort()
	remoting := node.Remoting()

	to, err := remoting.RemoteLookup(ctx, remoteHost, remotePort, actorName)
	if err != nil {
		return nil, err
	}

	if to.Equals(address.NoSender()) {
		return nil, gerrors.NewErrActorNotFound(actorName)
	}

	from := address.NoSender()
	response, err := remoting.RemoteAsk(ctx, from, to, message, timeout)
	if err != nil {
		return nil, err
	}
	return response.UnmarshalNew()
}

// AskGrain sends a message to a Grain and waits for a response.
//
// This method sends a message to the specified Grain and blocks until
// a reply is received or the timeout is reached. It is intended for request-response
// communication patterns with Grains.
//
// Parameters:
//   - ctx: Context used for cancellation and deadline control.
//   - grainRequest: The GrainRequest identifying the target Grain.
//   - message: The message to send to the Grain.
//   - timeout: The maximum duration to wait for a response from the Grain.
//
// Returns:
//   - reply: The response returned by the Grain.
//   - err: Returns an error if the Grain is not found, if the timeout is exceeded,
//     or if message delivery or handling fails.
//
// Note:
//   - If the Grain does not exist or is unreachable, an error is returned.
//   - Ensure the Grain is designed to handle the incoming message and reply appropriately.
//   - The grain kind must be registered on the remote actor system using RegisterGrainKind.
func (x *Client) AskGrain(ctx context.Context, grainRequest *remote.GrainRequest, message proto.Message, timeout time.Duration) (reply proto.Message, err error) {
	x.locker.Lock()
	node := nextNode(x.balancer)
	remoteHost, remotePort := node.HostAndPort()
	remoting := node.Remoting()
	x.locker.Unlock()

	response, err := remoting.RemoteAskGrain(ctx, remoteHost, remotePort, grainRequest, message, timeout)
	if err != nil {
		return nil, err
	}
	return response.UnmarshalNew()
}

// TellGrain sends a message to the specified Grain.
//
// This method delivers the given message to the target Grain.
// It is intended for fire-and-forget messaging patterns.
//
// Parameters:
//   - ctx: Context used for cancellation and timeout control.
//   - grainRequest: The GrainRequest identifying the target Grain.
//   - message: The message to send to the Grain.
//
// Returns:
//   - error: Returns nil on success. Returns an error if message delivery fails.
//
// Note:
//   - This method is asynchronous; it does not wait for a response.
//   - The grain kind must be registered on the remote actor system using RegisterGrainKind.
func (x *Client) TellGrain(ctx context.Context, grainRequest *remote.GrainRequest, message proto.Message) error {
	x.locker.Lock()
	node := nextNode(x.balancer)
	remoteHost, remotePort := node.HostAndPort()
	remoting := node.Remoting()
	x.locker.Unlock()
	return remoting.RemoteTellGrain(ctx, remoteHost, remotePort, grainRequest, message)
}

// Stop gracefully stops or forcefully terminates the specified actor.
//
// This method instructs the Client to stop the given actor, releasing any resources
// associated with it. Depending on the actor’s design and internal state, it may
// perform cleanup logic before termination.
//
// Parameters:
//   - ctx: Context used for cancellation and timeout control.
//   - actorName: The actor name.
//
// Returns:
//   - error: Returns an error if the actor could not be found, or if stopping it fails.
//
// Note:
//   - If the actor is not running or found, no error is returned.
//   - Graceful shutdown behavior depends on the actor implementation (e.g., handling termination signals).
func (x *Client) Stop(ctx context.Context, actorName string) error {
	x.locker.Lock()
	node := nextNode(x.balancer)
	x.locker.Unlock()
	remoteHost, remotePort := node.HostAndPort()
	remoting := node.Remoting()

	addr, err := remoting.RemoteLookup(ctx, remoteHost, remotePort, actorName)
	if err != nil {
		return err
	}

	if addr.Equals(address.NoSender()) {
		return nil
	}

	return remoting.RemoteStop(ctx, addr.Host(), addr.Port(), actorName)
}

// Whereis looks up the location of the specified actor and returns its address.
//
// This method queries the system to find the current network or logical address
// of the given actor. It can be used to inspect actor placement or route messages manually.
//
// Parameters:
//   - ctx: Context used for cancellation and timeout control.
//   - actorName: The actor name.
//
// Returns:
//   - *address.Address: The resolved address of the actor if found.
//   - error: Returns a NOT_FOUND error if the actor does not exist or is not currently registered.
//
// Note:
//   - This method does not send a message or interact with the actor directly.
//   - Use this for diagnostic, routing, or debugging purposes.
func (x *Client) Whereis(ctx context.Context, actorName string) (*address.Address, error) {
	x.locker.Lock()
	node := nextNode(x.balancer)
	x.locker.Unlock()
	remoteHost, remotePort := node.HostAndPort()
	addr, err := node.Remoting().RemoteLookup(ctx, remoteHost, remotePort, actorName)
	if err != nil {
		return nil, err
	}

	if addr.Equals(address.NoSender()) {
		return nil, gerrors.NewErrActorNotFound(actorName)
	}

	return addr, nil
}

// Reinstate transitions a previously suspended actor back to an active state.
//
// Upon reinstatement, the actor resumes processing incoming messages from its mailbox,
// continuing with the internal state it held before suspension.
//
// Important notes:
//   - The actor’s state is preserved; no state reset occurs during reinstatement.
//   - Messages processing resumed after reinstatement.
//   - If the actor cannot be reinstated (e.g., if it no longer exists or is in a terminal state),
//     an error will be returned.
//   - Reinstate should be used cautiously in distributed systems to avoid race conditions or
//     inconsistent actor states.
//
// Parameters:
//   - ctx: Context for cancellation and deadlines.
//   - actorName: The actor name.
//
// Returns:
//   - error: Non-nil if the operation fails due to invalid state, internal issues. In case the actor is not found it will return no error.
func (x *Client) Reinstate(ctx context.Context, actorName string) error {
	x.locker.Lock()
	node := nextNode(x.balancer)
	x.locker.Unlock()
	remoteHost, remotePort := node.HostAndPort()
	remoting := node.Remoting()

	addr, err := remoting.RemoteLookup(ctx, remoteHost, remotePort, actorName)
	if err != nil {
		return err
	}

	if addr.Equals(address.NoSender()) {
		return nil
	}

	return remoting.RemoteReinstate(ctx, addr.Host(), addr.Port(), actorName)
}

// nextNode returns the next node host and port
func nextNode(balancer Balancer) *Node {
	return balancer.Next()
}

// updateNodes updates the list of nodes availables in the pool
// the old nodes pool is completely replaced by the new nodes pool
func (x *Client) updateNodes(ctx context.Context) error {
	x.locker.Unlock()
	defer x.locker.Lock()

	for _, node := range x.nodes {
		weight, ok, err := getNodeMetric(ctx, node)
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
	ticker := ticker.New(x.refreshInterval)
	tickerStopSig := make(chan types.Unit, 1)
	go func() {
		for {
			select {
			case <-ticker.Ticks:
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

// clusterClient returns the cluster service client
func clusterClient(node *Node) internalpbconnect.ClusterServiceClient {
	opts := []connect.ClientOption{
		connectproto.WithBinary(
			proto.MarshalOptions{},
			proto.UnmarshalOptions{DiscardUnknown: true},
		),
	}

	if node.Remoting().MaxReadFrameSize() > 0 {
		opts = append(opts,
			connect.WithSendMaxBytes(node.Remoting().MaxReadFrameSize()),
			connect.WithReadMaxBytes(node.Remoting().MaxReadFrameSize()),
		)
	}

	switch node.Remoting().Compression() {
	case remote.GzipCompression:
		opts = append(opts, connect.WithSendGzip())
	case remote.ZstdCompression:
		opts = append(opts, zstd.WithCompression())
		opts = append(opts, connect.WithSendCompression(zstd.Name))
	case remote.BrotliCompression:
		opts = append(opts, brotli.WithCompression())
		opts = append(opts, connect.WithSendCompression(brotli.Name))
	}

	return internalpbconnect.NewClusterServiceClient(
		node.HTTPClient(),
		node.HTTPEndPoint(),
		opts...,
	)
}

// getBalancer returns the balancer based upon the strategy
func getBalancer(strategy BalancerStrategy) Balancer {
	var balancer Balancer
	balancer = NewRoundRobin()
	switch strategy {
	case RoundRobinStrategy:
		balancer = NewRoundRobin()
	case RandomStrategy:
		balancer = NewRandom()
	case LeastLoadStrategy:
		balancer = NewLeastLoad()
	}
	return balancer
}

// getNodeMetric pings a given node and get the node metric info and
func getNodeMetric(ctx context.Context, node *Node) (int, bool, error) {
	service := clusterClient(node)

	response, err := service.GetNodeMetric(ctx, connect.NewRequest(&internalpb.GetNodeMetricRequest{NodeAddress: node.Address()}))
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
	return int(response.Msg.GetLoad()), true, nil
}

// validateNodes validate the incoming nodes
func validateNodes(nodes []*Node) error {
	var runners []func() error
	runners = append(runners, func() error {
		return validation.
			New(validation.FailFast()).
			AddAssertion(len(nodes) != 0, "nodes are required").
			Validate()
	})

	for _, node := range nodes {
		runners = append(runners, func() error { return node.Validate() })
	}

	return chain.
		New(chain.WithFailFast()).
		AddRunners(runners...).
		Run()
}

// setNodesMetric
func setNodesMetric(ctx context.Context, nodes []*Node) error {
	for _, node := range nodes {
		weight, ok, err := getNodeMetric(ctx, node)
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
