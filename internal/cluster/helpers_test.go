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

package cluster

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"strconv"
	"testing"
	"time"
	"unsafe"

	goset "github.com/deckarep/golang-set/v2"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/v4/discovery"
	"github.com/tochemey/goakt/v4/discovery/nats"
	dynaport "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/log"
	gtls "github.com/tochemey/goakt/v4/tls"
	"github.com/tochemey/olric"
	"github.com/tochemey/olric/events"
	"github.com/tochemey/olric/pkg/storage"
	"go.uber.org/atomic"
)

// testCoordinator is the peers address of the coordinator announcing membership changes in the event tests.
const testCoordinator = "127.0.0.1:4100"

// useTempHome points HOME and USERPROFILE at a temporary directory for the duration of the test.
func useTempHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
}

// withBoltPathGenerator swaps the package bolt path generator for fn and restores it when the test ends.
func withBoltPathGenerator(t *testing.T, fn func() (string, error)) {
	t.Helper()
	original := boltPathGenerator
	boltPathGenerator = fn
	t.Cleanup(func() { boltPathGenerator = original })
}

// startNatsServer starts a NATS server on a random local port and waits until it accepts connections.
func startNatsServer(t *testing.T) *natsserver.Server {
	t.Helper()
	serv, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1",
		Port: -1,
	})

	require.NoError(t, err)

	ready := make(chan bool)
	go func() {
		ready <- true
		serv.Start()
	}()
	<-ready

	if !serv.ReadyForConnections(2 * time.Second) {
		t.Fatalf("nats-io server failed to start")
	}

	return serv
}

// startEngine starts a cluster node discovered through the NATS server at serverAddr and returns it with its provider.
func startEngine(t *testing.T, serverAddr string, opts ...ConfigOption) (Cluster, discovery.Provider) {
	ctx := context.TODO()

	nodePorts := dynaport.Get(3)
	gossipPort := nodePorts[0]
	clusterPort := nodePorts[1]
	remotingPort := nodePorts[2]

	host := "127.0.0.1"
	actorSystemName := "testSystem"
	natsSubject := "some-subject"

	config := nats.Config{
		NatsServer:    fmt.Sprintf("nats://%s", serverAddr),
		NatsSubject:   natsSubject,
		Host:          host,
		DiscoveryPort: gossipPort,
	}

	hostNode := discovery.Node{
		Name:          host,
		Host:          host,
		DiscoveryPort: gossipPort,
		PeersPort:     clusterPort,
		RemotingPort:  remotingPort,
	}

	provider := nats.NewDiscovery(&config)

	engine := New(actorSystemName, provider, &hostNode, append([]ConfigOption{WithLogger(log.DiscardLogger)}, opts...)...)
	require.NotNil(t, engine)
	require.NoError(t, engine.Start(ctx))

	return engine, provider
}

// startEngineWithTLS starts a cluster node whose peer traffic is secured with the given TLS configurations.
func startEngineWithTLS(t *testing.T, serverAddr string, server, client *tls.Config) (Cluster, discovery.Provider) {
	ctx := context.TODO()

	nodePorts := dynaport.Get(3)
	gossipPort := nodePorts[0]
	clusterPort := nodePorts[1]
	remotingPort := nodePorts[2]

	host := "127.0.0.1"
	actorSystemName := "testSystem"
	natsSubject := "some-subject"

	config := nats.Config{
		NatsServer:    fmt.Sprintf("nats://%s", serverAddr),
		NatsSubject:   natsSubject,
		Host:          host,
		DiscoveryPort: gossipPort,
	}

	hostNode := discovery.Node{
		Name:          host,
		Host:          host,
		DiscoveryPort: gossipPort,
		PeersPort:     clusterPort,
		RemotingPort:  remotingPort,
	}

	provider := nats.NewDiscovery(&config)

	engine := New(actorSystemName, provider, &hostNode,
		WithTLS(&gtls.Info{
			ClientConfig: client,
			ServerConfig: server,
		}),
		WithLogger(log.DiscardLogger))
	require.NotNil(t, engine)
	require.NoError(t, engine.Start(ctx))

	return engine, provider
}

// newOlricMember builds a cluster member whose metadata carries a discovery node at host and peersPort.
func newOlricMember(t *testing.T, host string, peersPort int, coordinator bool) olric.Member {
	t.Helper()
	node := &discovery.Node{
		Name:          host,
		Host:          host,
		DiscoveryPort: 1,
		PeersPort:     peersPort,
		RemotingPort:  2,
	}
	meta, err := json.Marshal(node)
	require.NoError(t, err)
	return olric.Member{
		Name:        net.JoinHostPort(host, strconv.Itoa(peersPort)),
		Meta:        string(meta),
		Coordinator: coordinator,
	}
}

// collectLeaderChanges drains a node's event stream for up to timeout, returning
// every LeaderChanged event observed in that window.
func collectLeaderChanges(t *testing.T, node Cluster, timeout time.Duration) []*LeaderChangedEvent {
	t.Helper()
	var changes []*LeaderChangedEvent
	deadline := time.After(timeout)
	for {
		select {
		case event, ok := <-node.Events():
			if !ok {
				return changes
			}
			if changed, ok := event.Payload.(*LeaderChangedEvent); ok {
				changes = append(changes, changed)
			}
		case <-deadline:
			return changes
		}
	}
}

// extractMessage returns the msg field of a JSON log record.
func extractMessage(data []byte) (string, error) {
	c := make(map[string]json.RawMessage)

	if err := json.Unmarshal(data, &c); err != nil {
		return "", err
	}

	for k, v := range c {
		if k == "msg" {
			return strconv.Unquote(string(v))
		}
	}

	return "", nil
}

// extractLevel returns the level field of a JSON log record.
func extractLevel(data []byte) (string, error) {
	c := make(map[string]json.RawMessage)

	if err := json.Unmarshal(data, &c); err != nil {
		return "", err
	}

	for k, v := range c {
		if k == "level" {
			return strconv.Unquote(string(v))
		}
	}

	return "", nil
}

// newEventCluster builds a running cluster carrying only the state the membership event tests drive.
func newEventCluster(host string, port int) *cluster {
	return &cluster{
		node:                   &discovery.Node{Host: host, PeersPort: port},
		events:                 make(chan *Event, defaultEventsBufSize),
		nodeJoinedEventsFilter: goset.NewSet[string](),
		nodeLeftEventsFilter:   goset.NewSet[string](),
		pendingJoins:           make(map[string]pendingEvent),
		pendingLeaves:          make(map[string]pendingEvent),
		pendingEmitTimeout:     pendingEventEmitTimeout,
		logger:                 log.DiscardLogger,
		shutdownTimeout:        5 * time.Second,
		running:                atomic.NewBool(true),
	}
}

// requireNextEvent drains one cluster event from cl and asserts that it
// announces node with the given type.
func requireNextEvent(t *testing.T, cl *cluster, eventType EventType, node string) {
	t.Helper()
	require.NotEmpty(t, cl.events)

	event := <-cl.events
	require.Equal(t, eventType, event.Type)

	switch payload := event.Payload.(type) {
	case *NodeJoinedEvent:
		require.Equal(t, node, payload.Address)
	case *NodeLeftEvent:
		require.Equal(t, node, payload.Address)
	default:
		t.Fatalf("unexpected payload %T", payload)
	}
}

// requireEmitted asserts that exactly one cluster event is pending on cl and
// that it announces node with the given type.
func requireEmitted(t *testing.T, cl *cluster, eventType EventType, node string) {
	t.Helper()
	require.Len(t, cl.events, 1)
	requireNextEvent(t, cl, eventType, node)
}

// trackJoin delivers to cl its own observation of a join of node, made while
// cl held the routing table of generation.
func trackJoin(cl *cluster, node string, generation uint64) {
	cl.trackNodeJoinEvent(events.NodeJoinEvent{NodeJoin: node, Source: cl.node.PeersAddress(), Generation: generation, Timestamp: time.Now().UnixNano()})
}

// trackLeft delivers to cl its own observation of a departure of node, made
// while cl held the routing table of generation.
func trackLeft(cl *cluster, node string, generation uint64) {
	cl.trackNodeLeftEvent(events.NodeLeftEvent{NodeLeft: node, Source: cl.node.PeersAddress(), Generation: generation, Timestamp: time.Now().UnixNano()})
}

// announceJoin delivers to cl the test coordinator's announcement that node
// joined, observed at generation.
func announceJoin(cl *cluster, node string, generation uint64) {
	announceChange(cl, events.MembershipChangeJoin, node, generation)
}

// announceLeft delivers to cl the test coordinator's announcement that node
// left, observed at generation.
func announceLeft(cl *cluster, node string, generation uint64) {
	announceChange(cl, events.MembershipChangeLeft, node, generation)
}

// announceChange delivers to cl the test coordinator's announcement of change
// for node, observed at generation.
func announceChange(cl *cluster, change string, node string, generation uint64) {
	cl.trackMembershipChangeEvent(events.MembershipChangeEvent{Change: change, Node: node, Source: testCoordinator, Generation: generation, Timestamp: time.Now().UnixNano()})
}

// converge delivers to cl the test coordinator's announcement that its routing
// table converged at generation for members.
func converge(cl *cluster, generation uint64, members ...string) {
	cl.processRebalanceComplete(events.RebalanceCompleteEvent{Source: testCoordinator, Epoch: generation, Generation: generation, Members: members, Timestamp: time.Now().UnixNano()})
}

// newGetResponseWithValue builds a lookup response whose entry carries value as its payload.
func newGetResponseWithValue(value []byte) *olric.GetResponse {
	entry := &MockEntry{}
	entry.SetValue(value)
	return newGetResponse(entry, 0)
}

// newGetResponse builds a lookup response whose unexported entry and partition fields are set by reflection.
func newGetResponse(entry storage.Entry, partition uint64) *olric.GetResponse {
	resp := &olric.GetResponse{}
	rv := reflect.ValueOf(resp).Elem()

	entryField := rv.FieldByName("entry")
	reflect.NewAt(entryField.Type(), unsafe.Pointer(entryField.UnsafeAddr())).Elem().Set(reflect.ValueOf(entry))

	partitionField := rv.FieldByName("partition")
	reflect.NewAt(partitionField.Type(), unsafe.Pointer(partitionField.UnsafeAddr())).Elem().SetUint(partition)

	return resp
}
