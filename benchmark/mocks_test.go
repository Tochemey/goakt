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

package benchmark

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/discovery/static"
	"github.com/tochemey/goakt/v4/internal/address"
	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

const (
	// clusterSpawnNodeCount is the size of the cluster the spawns are measured
	// on. With three nodes most registry keys are owned by a node other than
	// the spawning one, so a spawn pays the network round trips of a real
	// cluster rather than local map writes.
	clusterSpawnNodeCount = 3

	// clusterSpawnPortsPerNode is the number of ports one node binds:
	// discovery, peers and remoting.
	clusterSpawnPortsPerNode = 3

	// clusterSpawnHost is the loopback address every node binds to.
	clusterSpawnHost = "127.0.0.1"

	// clusterSpawnSystemName is the actor system name shared by the nodes.
	clusterSpawnSystemName = "bench-cluster"

	// clusterSpawnBootstrapTimeout bounds how long a node waits for its peers
	// while joining. The nodes start together, so a short wait is enough.
	clusterSpawnBootstrapTimeout = time.Second

	// clusterSpawnPeersTimeout bounds the wait for every node to see the
	// others before the measurement starts.
	clusterSpawnPeersTimeout = 30 * time.Second

	// clusterSpawnPeersQueryTimeout bounds one peers query during that wait.
	clusterSpawnPeersQueryTimeout = time.Second

	// clusterSpawnPeersPoll is the interval between two peers queries.
	clusterSpawnPeersPoll = 100 * time.Millisecond

	// clusterSpawnActorNamePrefix prefixes the name of every spawned actor.
	clusterSpawnActorNamePrefix = "bench-cluster-actor-"
)

var replyMessage = testpb.Reply_builder{Content: "reply"}.Build()

var errRequestBenchFailed = errors.New("request benchmark failed")

// throughputBudgets is the sweep used by the *Throughput variants of the
// benchmarks below. It spans from below-default (fair / latency-friendly) to
// well above default (throughput-oriented) so the curve is visible.
var throughputBudgets = []int{8, 32, 64, 128, 256}

var _ actor.Actor = (*stopSignalActor)(nil)

// replicaReceiveCount is incremented each time a replicaActor observes a
// TestSend message. Reset at the start of every run.
var replicaReceiveCount atomic.Int64

type Actor struct{}

// countingActor counts TestSend deliveries and closes done exactly once
// when received reaches target. received is a plain int64 because an
// actor's Receive is invoked serially by a single dispatcher worker;
// no synchronisation is required.
type countingActor struct {
	received int64
	done     chan struct{}
	target   int64
}

type requestBenchActor struct {
	target *actor.PID
	doneCh chan struct{}
	errCh  chan error
}

// payloadMsg carries a sized byte payload for BenchmarkTellPayload. The
// slice header is shared across producers; the receiver does not mutate
// it, so the cost reported is the dispatch path plus the per-message
// pointer hand-off, not allocations of the payload itself.
type payloadMsg struct{ Data []byte }

type payloadCounterActor struct {
	received int64
	done     chan struct{}
	target   int64
}

// benchGrain is a minimal Grain used by the throughput benchmarks. It
// performs the same dispatch work as Actor (no business logic), so the
// numbers reflect the dispatcher + mailbox path rather than user code.
type benchGrain struct{}

// countingGrain is the grain counterpart of countingActor. received is a
// plain int64 because a grain's OnReceive is invoked serially on a
// single dispatcher worker; no synchronisation is required.
type countingGrain struct {
	received int64
	done     chan struct{}
	target   int64
}

// pingPongStart tells a pingPongClient to send its first batch.
type pingPongStart struct{}

// pingPongEcho answers every TestSend with the same message, to its sender.
type pingPongEcho struct{}

// pingPongClient sends batches of TestSend to echo and sends the next batch
// once every reply of the current one is back, until it has sent remaining
// messages; then it closes done. Only its own turns touch its fields.
type pingPongClient struct {
	// echo is the actor the client exchanges messages with.
	echo *actor.PID
	// batch is the number of messages the client keeps in flight.
	batch int
	// remaining is the number of messages still to send.
	remaining int
	// outstanding is the number of replies the current batch still awaits.
	outstanding int
	// message is the payload sent in every request, reused to keep
	// allocation out of the measurement.
	message *testpb.TestSend
	// done is closed once the last reply is back.
	done chan struct{}
}

// windowActor counts the messages it has processed so a producer can keep the
// number of in-flight messages below the mailbox capacity.
type windowActor struct {
	outstanding *atomic.Int64
}

type noopActor struct{}

// idleFootprint is the heap and goroutine growth attributable to one idle
// population, read after the population settled and the heap was collected.
type idleFootprint struct {
	// heapAlloc is the growth in live heap bytes (runtime.MemStats.HeapAlloc).
	heapAlloc uint64
	// heapInuse is the growth in heap spans in use (runtime.MemStats.HeapInuse),
	// which adds the size-class slack the allocator pays around live objects.
	heapInuse uint64
	// goroutines is the growth in live goroutines.
	goroutines int
}

// ringToken is the one message BenchmarkThreadRing passes around the ring.
// Only one actor holds it at a time, so its hop count is decremented in place.
type ringToken struct {
	hops int
}

// ringNext closes the ring: it tells the last actor which actor follows it.
type ringNext struct {
	next *actor.PID
}

// ringActor passes the token to the next actor in the ring until its hops run
// out, then closes done.
type ringActor struct {
	next *actor.PID
	done chan struct{}
}

// stopSignalActor is an empty actor that reports its PostStop on a channel,
// so BenchmarkSpawnStop can time a lifecycle to the end of the stop.
type stopSignalActor struct {
	stopped chan struct{}
}

// benchCluster is a cluster of nodes started in one process for a benchmark.
// The first node spawns; the others only hold their share of the registry.
type benchCluster struct {
	nodes []actor.ActorSystem
	// spawned counts the names handed out so far, so every spawn of the
	// benchmark claims a name the registry has not seen, whichever
	// sub-benchmark or iteration it belongs to.
	spawned atomic.Int64
}

type replicaActor struct{}

type replicaEngine struct {
	system actor.ActorSystem
	host   string
	port   int
	actors []*address.Address
}

func (a *Actor) PreStart(*actor.Context) error {
	return nil
}

func (a *Actor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
	case *testpb.TestReply:
		ctx.Response(replyMessage)
	default:
		ctx.Unhandled()
	}
}

func (a *Actor) PostStop(*actor.Context) error {
	return nil
}

func (a *countingActor) PreStart(*actor.Context) error { return nil }

func (a *countingActor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
		a.received++
		if a.received == a.target {
			close(a.done)
		}
	default:
		ctx.Unhandled()
	}
}

func (a *countingActor) PostStop(*actor.Context) error { return nil }

func (a *requestBenchActor) PreStart(*actor.Context) error { return nil }

func (a *requestBenchActor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
		call := ctx.Request(a.target, new(testpb.TestReply))
		if call == nil {
			a.signalError(errRequestBenchFailed)
			a.signalDone()
			return
		}
		call.Then(func(_ any, err error) {
			if err != nil {
				a.signalError(err)
			}
			a.signalDone()
		})
	default:
		ctx.Unhandled()
	}
}

func (a *requestBenchActor) PostStop(*actor.Context) error { return nil }

func (a *payloadCounterActor) PreStart(*actor.Context) error { return nil }

func (a *payloadCounterActor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *payloadMsg:
		a.received++
		if a.received == a.target {
			close(a.done)
		}
	default:
		ctx.Unhandled()
	}
}

func (a *payloadCounterActor) PostStop(*actor.Context) error { return nil }

func (*benchGrain) OnActivate(context.Context, *actor.GrainProps) error { return nil }

func (*benchGrain) OnDeactivate(context.Context, *actor.GrainProps) error { return nil }

func (*benchGrain) OnReceive(ctx *actor.GrainContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
		ctx.NoErr()
	case *testpb.TestReply:
		ctx.Response(replyMessage)
	default:
		ctx.Unhandled()
	}
}

func (*countingGrain) OnActivate(context.Context, *actor.GrainProps) error { return nil }

func (*countingGrain) OnDeactivate(context.Context, *actor.GrainProps) error { return nil }

func (g *countingGrain) OnReceive(ctx *actor.GrainContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
		g.received++
		if g.received == g.target {
			close(g.done)
		}
		ctx.NoErr()
	default:
		ctx.Unhandled()
	}
}

// PreStart does nothing.
func (x *pingPongEcho) PreStart(*actor.Context) error { return nil }

// Receive answers a TestSend with the same message.
func (x *pingPongEcho) Receive(ctx *actor.ReceiveContext) {
	if message, ok := ctx.Message().(*testpb.TestSend); ok {
		ctx.Tell(ctx.Sender(), message)
	}
}

// PostStop does nothing.
func (x *pingPongEcho) PostStop(*actor.Context) error { return nil }

// PreStart does nothing.
func (x *pingPongClient) PreStart(*actor.Context) error { return nil }

// Receive starts the exchange on pingPongStart and counts the replies.
func (x *pingPongClient) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *pingPongStart:
		x.send(ctx)
	case *testpb.TestSend:
		x.outstanding--
		if x.outstanding > 0 {
			return
		}

		if x.remaining == 0 {
			close(x.done)
			return
		}

		x.send(ctx)
	}
}

// PostStop does nothing.
func (x *pingPongClient) PostStop(*actor.Context) error { return nil }

func (a *windowActor) PreStart(*actor.Context) error { return nil }

func (a *windowActor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
		a.outstanding.Add(-1)
	default:
		ctx.Unhandled()
	}
}

func (a *windowActor) PostStop(*actor.Context) error { return nil }

func (x *noopActor) PreStart(*actor.Context) error { return nil }

func (x *noopActor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
	default:
		ctx.Unhandled()
	}
}

func (x *noopActor) PostStop(*actor.Context) error { return nil }

// PreStart does nothing.
func (x *ringActor) PreStart(*actor.Context) error { return nil }

// Receive forwards the token or, on the last hop, closes done.
func (x *ringActor) Receive(ctx *actor.ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *ringNext:
		x.next = msg.next
	case *ringToken:
		if msg.hops == 0 {
			close(x.done)
			return
		}

		msg.hops--
		ctx.Tell(x.next, msg)
	}
}

// PostStop does nothing.
func (x *ringActor) PostStop(*actor.Context) error { return nil }

func (x *stopSignalActor) PreStart(*actor.Context) error { return nil }

func (x *stopSignalActor) Receive(*actor.ReceiveContext) {}

func (x *stopSignalActor) PostStop(*actor.Context) error {
	x.stopped <- struct{}{}
	return nil
}

func (*replicaActor) PreStart(*actor.Context) error { return nil }

func (*replicaActor) PostStop(*actor.Context) error { return nil }

func (*replicaActor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
		replicaReceiveCount.Add(1)
	default:
		ctx.Unhandled()
	}
}

func (a *requestBenchActor) signalDone() {
	a.doneCh <- struct{}{}
}

func (a *requestBenchActor) signalError(err error) {
	if err == nil {
		return
	}
	select {
	case a.errCh <- err:
	default:
	}
}

// setupFanOutGrains provisions grainCount grains for the round-robin
// fan-out benchmarks. Each grain gets a private int64 counter, a private
// done channel, and a target computed from the round-robin distribution
// counter.Add(1) % grainCount over b.N messages:
//   - grain 0 receives messages at positions grainCount, 2*grainCount, ...
//   - grain i (1..grainCount-1) receives messages at positions i, i+grainCount, ...
//
// The returned slices are aligned: dones[i] completes when identities[i]
// has received all its messages.
func setupFanOutGrains(b *testing.B, ctx context.Context, system actor.ActorSystem, grainCount uint64) ([]*actor.GrainIdentity, []chan struct{}) {
	n := uint64(b.N)
	identities := make([]*actor.GrainIdentity, grainCount)
	dones := make([]chan struct{}, grainCount)
	for i := range grainCount {
		var target int64
		if i == 0 {
			target = int64(n / grainCount)
		} else if i <= n {
			target = int64((n-i)/grainCount) + 1
		}
		done := make(chan struct{})
		if target == 0 {
			close(done)
		}
		//nolint:staticcheck // the deprecated factory path is kept here: the benchmark injects per-run state that zero-value construction cannot carry
		id, err := system.GrainIdentity(ctx, fmt.Sprintf("g-%d", i), func(context.Context) (actor.Grain, error) {
			return &countingGrain{done: done, target: target}, nil
		})
		if err != nil {
			b.Fatalf("failed to create grain identity: %v", err)
		}
		identities[i] = id
		dones[i] = done
	}
	return identities, dones
}

// send sends the client's next batch, at most the messages it has left.
func (x *pingPongClient) send(ctx *actor.ReceiveContext) {
	n := min(x.batch, x.remaining)
	x.remaining -= n
	x.outstanding = n
	for range n {
		ctx.Tell(x.echo, x.message)
	}
}

// benchEqualPriority ranks every message the same. It drives the priority
// mailboxes in the throughput benchmarks: equal priorities keep the heap busy
// and, for the stable variant, exercise the insertion-order tiebreaker.
func benchEqualPriority(any, any) bool { return false }

// measureIdleFootprint starts a fresh actor system, takes a collected baseline,
// spawns count idle actors with opts, waits for their PostStart to drain and
// returns the collected growth against the baseline. The slice holding the
// spawned PIDs is allocated before the baseline so the benchmark's own
// bookkeeping is not charged to the population.
func measureIdleFootprint(b *testing.B, count int, opts ...actor.SpawnOption) idleFootprint {
	b.Helper()

	ctx := context.Background()
	system, err := actor.NewActorSystem("mem-bench", actor.WithLogger(log.DiscardLogger), actor.WithActorInitMaxRetries(1))
	require.NoError(b, err)
	require.NoError(b, system.Start(ctx))
	defer func() { _ = system.Stop(ctx) }()

	pids := make([]*actor.PID, count)
	before := collectedMemStats()
	baselineGoroutines := runtime.NumGoroutine()

	for i := range count {
		pid, err := system.Spawn(ctx, benchActorName(i), new(noopActor), opts...)
		require.NoError(b, err)
		pids[i] = pid
	}

	settlePostStart(b, pids)
	after := collectedMemStats()

	return idleFootprint{
		heapAlloc:  after.HeapAlloc - before.HeapAlloc,
		heapInuse:  after.HeapInuse - before.HeapInuse,
		goroutines: runtime.NumGoroutine() - baselineGoroutines,
	}
}

// settlePostStart blocks until every actor in pids has processed its
// PostStart, the one message a spawn leaves in flight, so the heap is measured
// with the dispatcher quiescent rather than mid-drain.
func settlePostStart(b *testing.B, pids []*actor.PID) {
	b.Helper()

	deadline := time.Now().Add(footprintSettleTimeout)
	for i, pid := range pids {
		for pid.ProcessedCount() == 0 {
			if time.Now().After(deadline) {
				b.Fatalf("actor %d did not process PostStart within %s", i, footprintSettleTimeout)
			}

			pause.For(time.Millisecond)
		}
	}
}

// collectedMemStats forces two full collections and reads the heap
// statistics. Two, not one: sync.Pool contents survive the first collection
// in the pool's victim cache and are only released by the second, so a single
// collection would charge pooled scratch objects the spawns went through to
// the population.
func collectedMemStats() runtime.MemStats {
	runtime.GC()
	runtime.GC()

	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats
}

// benchActorName returns the name of the i-th actor of the idle population.
func benchActorName(i int) string {
	return "bench-noop-" + strconv.Itoa(i)
}

// humanReadableBytes renders b in the largest binary unit that keeps it above
// one, for the benchmark reports.
func humanReadableBytes(b uint64) string {
	const (
		kilobyte = 1024
		megabyte = 1024 * kilobyte
		gigabyte = 1024 * megabyte
	)

	switch {
	case b >= gigabyte:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(gigabyte))
	case b >= megabyte:
		return fmt.Sprintf("%.2f MB", float64(b)/float64(megabyte))
	case b >= kilobyte:
		return fmt.Sprintf("%.2f KB", float64(b)/float64(kilobyte))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// measureIdleGrainFootprint starts a fresh actor system, takes a collected
// baseline, activates count idle grains with opts and returns the collected
// growth against the baseline. The slice holding the identities is allocated
// before the baseline so the benchmark's own bookkeeping is not charged to the
// population; the identities themselves are retained by the grain processes
// and are part of what a grain costs, so they are measured.
func measureIdleGrainFootprint(b *testing.B, count int, opts ...actor.GrainOption) idleFootprint {
	b.Helper()

	ctx := context.Background()
	system, err := actor.NewActorSystem("grain-mem-bench", actor.WithLogger(log.DiscardLogger))
	require.NoError(b, err)
	require.NoError(b, system.Start(ctx))
	defer func() { _ = system.Stop(ctx) }()

	identities := make([]*actor.GrainIdentity, count)
	before := collectedMemStats()
	baselineGoroutines := runtime.NumGoroutine()

	for i := range count {
		identity, err := actor.GrainOf[*benchGrain](ctx, system, benchGrainName(i), opts...)
		require.NoError(b, err)
		identities[i] = identity
	}

	after := collectedMemStats()

	return idleFootprint{
		heapAlloc:  after.HeapAlloc - before.HeapAlloc,
		heapInuse:  after.HeapInuse - before.HeapInuse,
		goroutines: runtime.NumGoroutine() - baselineGoroutines,
	}
}

// grainProcessStructBytes returns the size of the unexported grain process
// struct, the per-grain object the activation path allocates. The type is
// reached through the process pointer GrainContext carries, so the benchmark
// needs no export from the actor package; it reports zero if that field is
// ever renamed, which only loses the report line.
func grainProcessStructBytes() uintptr {
	field, ok := reflect.TypeFor[actor.GrainContext]().FieldByName("pid")
	if !ok || field.Type.Kind() != reflect.Pointer {
		return 0
	}

	return field.Type.Elem().Size()
}

// benchGrainName returns the name of the i-th grain of the idle population.
func benchGrainName(i int) string {
	return "bench-grain-" + strconv.Itoa(i)
}

// startSpawnBenchSystem starts a fresh actor system for one benchmark run and
// stops it when the run ends, so every run spawns into an empty system.
func startSpawnBenchSystem(b *testing.B) (context.Context, actor.ActorSystem) {
	b.Helper()

	ctx := context.Background()
	system, err := actor.NewActorSystem("spawn-bench", actor.WithLogger(log.DiscardLogger), actor.WithActorInitMaxRetries(1))
	require.NoError(b, err)
	require.NoError(b, system.Start(ctx))
	b.Cleanup(func() { _ = system.Stop(ctx) })

	return ctx, system
}

// spawnBenchNames returns count distinct actor names with the given prefix,
// built before the timer starts so name formatting is not charged to the
// spawn.
func spawnBenchNames(prefix string, count int) []string {
	names := make([]string, count)
	for i := range names {
		names[i] = prefix + strconv.Itoa(i)
	}

	return names
}

// startBenchCluster starts clusterSpawnNodeCount nodes on loopback that
// discover each other through a static host list, waits until every node sees
// the others and stops them all when the benchmark ends.
func startBenchCluster(b *testing.B) *benchCluster {
	b.Helper()

	ctx := context.Background()
	ports := inet.Get(clusterSpawnPortsPerNode * clusterSpawnNodeCount)
	hosts := make([]string, clusterSpawnNodeCount)

	for i := range clusterSpawnNodeCount {
		hosts[i] = fmt.Sprintf("%s:%d", clusterSpawnHost, ports[clusterSpawnPortsPerNode*i])
	}

	nodes := make([]actor.ActorSystem, clusterSpawnNodeCount)

	for i := range clusterSpawnNodeCount {
		discoveryPort := ports[clusterSpawnPortsPerNode*i]
		peersPort := ports[clusterSpawnPortsPerNode*i+1]
		remotingPort := ports[clusterSpawnPortsPerNode*i+2]

		clusterConfig := actor.NewClusterConfig().
			WithDiscovery(static.NewDiscovery(&static.Config{Hosts: hosts})).
			WithDiscoveryPort(discoveryPort).
			WithPeersPort(peersPort).
			WithBootstrapTimeout(clusterSpawnBootstrapTimeout).
			WithKinds(new(noopActor))

		node, err := actor.NewActorSystem(clusterSpawnSystemName,
			actor.WithLogger(log.DiscardLogger),
			actor.WithActorInitMaxRetries(1),
			actor.WithRemote(remote.NewConfig(clusterSpawnHost, remotingPort)),
			actor.WithCluster(clusterConfig))
		if err != nil {
			b.Fatalf("failed to create node %d: %v", i, err)
		}

		nodes[i] = node
	}

	errs := make([]error, clusterSpawnNodeCount)
	var wg sync.WaitGroup

	for i, node := range nodes {
		wg.Go(func() {
			errs[i] = node.Start(ctx)
		})
	}

	wg.Wait()

	b.Cleanup(func() {
		for _, node := range nodes {
			_ = node.Stop(ctx)
		}
	})

	for i, err := range errs {
		if err != nil {
			b.Fatalf("failed to start node %d: %v", i, err)
		}
	}

	deadline := time.Now().Add(clusterSpawnPeersTimeout)

	for _, node := range nodes {
		for {
			peers, err := node.Peers(ctx, clusterSpawnPeersQueryTimeout)
			if err == nil && len(peers) == clusterSpawnNodeCount-1 {
				break
			}

			if time.Now().After(deadline) {
				b.Fatalf("the %d nodes did not see each other within %s", clusterSpawnNodeCount, clusterSpawnPeersTimeout)
			}

			pause.For(clusterSpawnPeersPoll)
		}
	}

	return &benchCluster{nodes: nodes}
}

// spawnNext spawns one actor under a name no earlier spawn of this cluster
// used, from the first node, and fails the benchmark when the spawn does. The
// actor is not relocatable so that stopping the cluster at the end does not
// move the population to the surviving nodes; its registry record is written
// the same way as a relocatable one.
func (x *benchCluster) spawnNext(b *testing.B, ctx context.Context) {
	name := clusterSpawnActorNamePrefix + strconv.FormatInt(x.spawned.Add(1), 10)
	if _, err := x.nodes[0].Spawn(ctx, name, new(noopActor), actor.WithRelocationDisabled()); err != nil {
		b.Fatalf("failed to spawn %s: %v", name, err)
	}
}

// humanCount renders n with thousands separators for the benchmark report.
func humanCount(n int64) string {
	s := fmt.Sprintf("%d", n)

	if len(s) <= 3 {
		return s
	}

	var out []byte
	lead := len(s) % 3
	if lead > 0 {
		out = append(out, s[:lead]...)
	}

	for i := lead; i < len(s); i += 3 {
		if len(out) > 0 {
			out = append(out, ',')
		}
		out = append(out, s[i:i+3]...)
	}

	return string(out)
}

// benchmarkTellPingPong runs BenchmarkTellPingPong for pairs client/echo pairs
// with inFlight messages in flight per pair. b.N is the number of messages,
// requests and replies together.
func benchmarkTellPingPong(b *testing.B, pairs, inFlight int) {
	ctx := context.Background()

	actorSystem, err := actor.NewActorSystem("bench",
		actor.WithLogger(log.DiscardLogger),
		actor.WithActorInitMaxRetries(1))
	if err != nil {
		b.Fatalf("failed to create actor system: %v", err)
	}

	if err := actorSystem.Start(ctx); err != nil {
		b.Fatalf("failed to start actor system: %v", err)
	}
	b.Cleanup(func() { _ = actorSystem.Stop(ctx) })

	// each request comes back as a reply, so a pair exchanges two messages
	// per request
	requests := max(b.N/(2*pairs), 1)
	clients := make([]*actor.PID, pairs)
	dones := make([]chan struct{}, pairs)

	for i := range pairs {
		echo, err := actorSystem.Spawn(ctx, fmt.Sprintf("echo-%d", i), new(pingPongEcho))
		if err != nil {
			b.Fatalf("failed to spawn echo: %v", err)
		}

		dones[i] = make(chan struct{})
		clients[i], err = actorSystem.Spawn(ctx, fmt.Sprintf("client-%d", i), &pingPongClient{
			echo:      echo,
			batch:     inFlight,
			remaining: requests,
			message:   new(testpb.TestSend),
			done:      dones[i],
		})
		if err != nil {
			b.Fatalf("failed to spawn client: %v", err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()

	for _, client := range clients {
		if err := actor.Tell(ctx, client, new(pingPongStart)); err != nil {
			b.Fatal(err)
		}
	}

	for _, done := range dones {
		<-done
	}

	b.StopTimer()
	messagesPerSec := float64(2*requests*pairs) / b.Elapsed().Seconds()
	b.ReportMetric(messagesPerSec, "messages/sec")
}
