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

// Package benchmark holds GoAkt's benchmarks: sends from outside the system
// and between actors, the Savina ping-pong, thread ring, fork-join and big
// exchanges, Skynet, spawn and stop, the live heap per idle actor and grain,
// and the grain asks and activation. The shared workload, messages, sizes,
// Latch, Signal and the Skynet ranges, is at the end of this file; each
// benchmark is in the file named after it. The soak tests behind the scale
// build tag share this file's helpers.
package benchmark

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/discovery/static"
	dynaport "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

var system actor.ActorSystem

// grains is the single-node cluster the grain benchmarks share. It is only
// started when one of them runs, so its remoting and gossip stay out of the
// actor benchmarks.
var (
	grains     actor.ActorSystem
	grainsOnce sync.Once
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	var err error
	system, err = actor.NewActorSystem("bench", actor.WithLogger(log.DiscardLogger))
	if err != nil {
		panic(err)
	}

	if err := system.Start(ctx); err != nil {
		panic(err)
	}

	code := m.Run()
	_ = system.Stop(ctx)
	if grains != nil {
		_ = grains.Stop(ctx)
	}
	os.Exit(code)
}

// sink counts every Ping it receives.
type sink struct {
	latch *Latch
}

var _ actor.Actor = (*sink)(nil)

func (x *sink) PreStart(*actor.Context) error { return nil }

func (x *sink) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *Ping:
		x.latch.Count()
	}
}

func (x *sink) PostStop(*actor.Context) error { return nil }

// teller sends n Pings to a target from inside its handler once it receives Start.
type teller struct {
	target *actor.PID
	n      int
}

var _ actor.Actor = (*teller)(nil)

func (x *teller) PreStart(*actor.Context) error { return nil }

func (x *teller) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *Start:
		msg := new(Ping)
		for range x.n {
			ctx.Tell(x.target, msg)
		}
	}
}

func (x *teller) PostStop(*actor.Context) error { return nil }

// responder answers every Ping with a Pong.
type responder struct{}

var _ actor.Actor = (*responder)(nil)

func (x *responder) PreStart(*actor.Context) error { return nil }

func (x *responder) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *Ping:
		ctx.Response(new(Pong))
	}
}

func (x *responder) PostStop(*actor.Context) error { return nil }

// pinger keeps a ping-pong exchange going with a ponger until the latch opens.
type pinger struct {
	ponger *actor.PID
	latch  *Latch
}

var _ actor.Actor = (*pinger)(nil)

func (x *pinger) PreStart(*actor.Context) error { return nil }

func (x *pinger) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *Start:
		ctx.Tell(x.ponger, new(Ping))
	case *Pong:
		if !x.latch.Count() {
			ctx.Tell(x.ponger, new(Ping))
		}
	}
}

func (x *pinger) PostStop(*actor.Context) error { return nil }

// ponger replies to the sender of every Ping.
type ponger struct{}

var _ actor.Actor = (*ponger)(nil)

func (x *ponger) PreStart(*actor.Context) error { return nil }

func (x *ponger) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *Ping:
		ctx.Tell(ctx.Sender(), new(Pong))
	}
}

func (x *ponger) PostStop(*actor.Context) error { return nil }

// forkJoinWorker counts the messages it receives and opens its slot of the
// shared latch once it has received its share.
type forkJoinWorker struct {
	latch  *Latch
	target int
	count  int
}

var _ actor.Actor = (*forkJoinWorker)(nil)

func (x *forkJoinWorker) PreStart(*actor.Context) error { return nil }

func (x *forkJoinWorker) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *Ping:
		x.count++
		if x.count == x.target {
			x.latch.Count()
		}
	}
}

func (x *forkJoinWorker) PostStop(*actor.Context) error { return nil }

// pairPinger is a pinger with its own share of round trips, for the
// parallel ping-pong: it counts its own round trips and opens its slot of the
// shared latch once its share is done.
type pairPinger struct {
	ponger    *actor.PID
	latch     *Latch
	remaining int
}

var _ actor.Actor = (*pairPinger)(nil)

func (x *pairPinger) PreStart(*actor.Context) error { return nil }

func (x *pairPinger) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *Start:
		ctx.Tell(x.ponger, new(Ping))
	case *Pong:
		x.remaining--
		if x.remaining > 0 {
			ctx.Tell(x.ponger, new(Ping))
		}
		if x.remaining == 0 {
			x.latch.Count()
		}
	}
}

func (x *pairPinger) PostStop(*actor.Context) error { return nil }

// pipelinedPinger keeps PipelineDepth pings in flight to its ponger and sends
// the next batch once every reply of the current one is back, until its share
// of round trips is done; then it opens its slot of the shared latch.
type pipelinedPinger struct {
	ponger      *actor.PID
	latch       *Latch
	remaining   int
	outstanding int
	ping        *Ping
}

var _ actor.Actor = (*pipelinedPinger)(nil)

func (x *pipelinedPinger) PreStart(*actor.Context) error { return nil }

func (x *pipelinedPinger) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *Start:
		x.send(ctx)
	case *Pong:
		x.outstanding--
		if x.outstanding == 0 && x.remaining > 0 {
			x.send(ctx)
		}
		if x.outstanding == 0 && x.remaining == 0 {
			x.latch.Count()
		}
	}
}

// send sends the next batch, at most the round trips left.
func (x *pipelinedPinger) send(ctx *actor.ReceiveContext) {
	n := min(PipelineDepth, x.remaining)
	x.remaining -= n
	x.outstanding = n
	for range n {
		ctx.Tell(x.ponger, x.ping)
	}
}

func (x *pipelinedPinger) PostStop(*actor.Context) error { return nil }

// setNext closes a thread ring: it tells the last actor which one follows it.
type setNext struct {
	next *actor.PID
}

// ringActor passes the token to the next actor in the ring until its hops run
// out, then opens the latch.
type ringActor struct {
	next  *actor.PID
	latch *Latch
}

var _ actor.Actor = (*ringActor)(nil)

func (x *ringActor) PreStart(*actor.Context) error { return nil }

func (x *ringActor) Receive(ctx *actor.ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *setNext:
		x.next = msg.next
	case *Token:
		if msg.Hops == 0 {
			x.latch.Count()
			return
		}
		msg.Hops--
		ctx.Tell(x.next, msg)
	}
}

func (x *ringActor) PostStop(*actor.Context) error { return nil }

// bigActor is one of the actors of a Big exchange: it pings a peer per round,
// answers every Ping it receives, and opens its slot of the shared latch after
// its last round.
type bigActor struct {
	index  int
	peers  []*actor.PID
	rounds int
	round  int
	latch  *Latch
	ping   *Ping
	pong   *Pong
}

var _ actor.Actor = (*bigActor)(nil)

func (x *bigActor) PreStart(*actor.Context) error { return nil }

func (x *bigActor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *BigRound:
		ctx.Tell(x.peers[NextPeer(x.index, x.round, len(x.peers))], x.ping)
	case *Ping:
		ctx.Tell(ctx.Sender(), x.pong)
	case *Pong:
		x.round++
		if x.round < x.rounds {
			ctx.Tell(x.peers[NextPeer(x.index, x.round, len(x.peers))], x.ping)
		}
		if x.round == x.rounds {
			x.latch.Count()
		}
	}
}

func (x *bigActor) PostStop(*actor.Context) error { return nil }

// skynetNode is one node of a Skynet tree. On its range it either reports its
// leaf number to its parent, or creates its children and sums their results;
// a node reports once and then stops.
type skynetNode struct {
	total   *SkynetTotal
	parent  *actor.PID
	pending int
	sum     int64
}

var _ actor.Actor = (*skynetNode)(nil)

func (x *skynetNode) PreStart(*actor.Context) error { return nil }

func (x *skynetNode) Receive(ctx *actor.ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *SkynetRange:
		x.parent = ctx.Sender()
		if msg.Size == 1 {
			x.report(ctx, msg.Start)
			return
		}

		for _, child := range msg.Children() {
			ctx.Tell(ctx.Spawn(Name("n"), &skynetNode{}, actor.WithLongLived()), &child)
		}
		x.pending = SkynetFanOut
	case *SkynetResult:
		x.sum += msg.Sum
		x.pending--
		if x.pending == 0 {
			x.report(ctx, x.sum)
		}
	}
}

func (x *skynetNode) report(ctx *actor.ReceiveContext, sum int64) {
	if x.total != nil {
		x.total.Complete(sum)
	} else {
		ctx.Tell(x.parent, &SkynetResult{Sum: sum})
	}
	ctx.Shutdown()
}

func (x *skynetNode) PostStop(*actor.Context) error { return nil }

// stopper fires a signal once it has stopped.
type stopper struct {
	stopped Signal
}

var _ actor.Actor = (*stopper)(nil)

func (x *stopper) PreStart(*actor.Context) error { return nil }

func (x *stopper) Receive(*actor.ReceiveContext) {}

func (x *stopper) PostStop(*actor.Context) error {
	x.stopped.Fire()
	return nil
}

// noopActor handles TestSend and nothing else. The scale soak tests spawn it
// by the million.
type noopActor struct{}

var _ actor.Actor = (*noopActor)(nil)

func (x *noopActor) PreStart(*actor.Context) error { return nil }

func (x *noopActor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
	default:
		ctx.Unhandled()
	}
}

func (x *noopActor) PostStop(*actor.Context) error { return nil }

// humanReadableBytes renders b in the largest binary unit that keeps it above
// one, for the soak tests' reports.
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

// humanCount renders n with thousands separators for the soak tests' reports.
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

// grain answers every Ping with a Pong.
type grain struct{}

var _ actor.Grain = (*grain)(nil)

func (x *grain) OnActivate(context.Context, *actor.GrainProps) error { return nil }

func (x *grain) OnReceive(ctx *actor.GrainContext) {
	switch ctx.Message().(type) {
	case *Ping:
		ctx.Response(new(Pong))
	default:
		ctx.Unhandled()
	}
}

func (x *grain) OnDeactivate(context.Context, *actor.GrainProps) error { return nil }

func spawn(b *testing.B, a actor.Actor) *actor.PID {
	b.Helper()
	ctx := context.Background()
	pid, err := system.Spawn(ctx, Name("actor"), a, actor.WithLongLived())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = pid.Shutdown(ctx) })
	return pid
}

// startCluster starts a single-node cluster on an actor system of its own, so
// grains are registered in the cluster as they would be in production. Static
// discovery lists only this node; discovery, peers and remoting listen on
// localhost, but every grain is local, so no message crosses them.
func startCluster() (actor.ActorSystem, error) {
	ports := dynaport.Get(3)
	discoveryPort, peersPort, remotingPort := ports[0], ports[1], ports[2]

	config := actor.NewClusterConfig().
		WithDiscovery(static.NewDiscovery(&static.Config{Hosts: []string{fmt.Sprintf("127.0.0.1:%d", discoveryPort)}})).
		WithDiscoveryPort(discoveryPort).
		WithPeersPort(peersPort).
		WithReplicaCount(1).
		WithGrains(new(grain))

	sys, err := actor.NewActorSystem(Name("grains"),
		actor.WithLogger(log.DiscardLogger),
		actor.WithRemote(remote.NewConfig("127.0.0.1", remotingPort)),
		actor.WithCluster(config))
	if err != nil {
		return nil, err
	}
	if err := sys.Start(context.Background()); err != nil {
		return nil, err
	}
	return sys, nil
}

func sharedCluster(b *testing.B) actor.ActorSystem {
	b.Helper()
	var err error
	grainsOnce.Do(func() { grains, err = startCluster() })
	if err != nil {
		b.Fatal(err)
	}
	if grains == nil {
		b.Fatal("the shared grain cluster failed to start")
	}
	return grains
}

// grainCluster starts a cluster of its own for a benchmark that activates a new
// grain per iteration, so those grains are deactivated with it instead of
// accumulating in the shared cluster.
func grainCluster(b *testing.B) actor.ActorSystem {
	b.Helper()
	sys, err := startCluster()
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = sys.Stop(context.Background()) })
	return sys
}

func activate(b *testing.B, sys actor.ActorSystem) *actor.GrainIdentity {
	b.Helper()
	identity, err := actor.GrainOf[*grain](context.Background(), sys, Name("grain"), actor.WithLongLivedGrain())
	if err != nil {
		b.Fatal(err)
	}
	return identity
}

// warmUp activates WarmupGrains grains that the benchmark does not
// measure.
func warmUp(b *testing.B, sys actor.ActorSystem) {
	b.Helper()
	for range WarmupGrains {
		activate(b, sys)
	}
}

// Timeout bounds every request/response call.
const Timeout = 5 * time.Second

// waitTimeout bounds how long a benchmark waits for an actor to drain the
// messages it was sent.
const waitTimeout = time.Minute

// Throughput units reported next to ns/op.
const (
	MsgsPerSec   = "msgs/s"
	ActorsPerSec = "actors/s"
	GrainsPerSec = "grains/s"
	// BytesPerActor is the live heap one idle actor keeps.
	BytesPerActor = "B/actor"
	// BytesPerGrain is the live heap one idle grain keeps.
	BytesPerGrain = "B/grain"
)

// Sizes of the scenarios that run a fixed population of actors. The Savina
// sizes are the suite's defaults (Imam and Sarkar, "Savina - An Actor
// Benchmark Suite", AGERE 2014); Skynet's is the size the original benchmark
// (github.com/atemerev/skynet) is run at.
const (
	// ThreadRingActors is the number of actors a ThreadRing token passes
	// through.
	ThreadRingActors = 100
	// ForkJoinActors is the number of workers a ForkJoin master fans out to.
	ForkJoinActors = 60
	// BigActors is the number of actors in a Big exchange.
	BigActors = 120
	// SkynetFanOut is the number of children every Skynet node above the
	// leaves creates.
	SkynetFanOut = 10
	// SkynetLeaves is the number of leaves of the Skynet tree: a million, as
	// in the original benchmark, over six levels of ten.
	SkynetLeaves = 1_000_000
	// SkynetActors is the number of actors a Skynet tree creates, the leaves
	// and every node above them.
	SkynetActors = SkynetLeaves + SkynetLeaves/10 + SkynetLeaves/100 + SkynetLeaves/1_000 + SkynetLeaves/10_000 + SkynetLeaves/100_000 + 1
	// SkynetSum is the sum every Skynet run must produce: the leaves are
	// numbered 0 to SkynetLeaves-1.
	SkynetSum = SkynetLeaves * (SkynetLeaves - 1) / 2
	// MemoryActors is the number of idle actors MemoryPerActor spawns.
	MemoryActors = 50_000
	// MemoryGrains is the number of idle grains MemoryPerGrain activates.
	MemoryGrains = 50_000
	// WarmupGrains is the number of grains GrainActivate and MemoryPerGrain
	// activate before they measure, so one-off allocations of a fresh
	// cluster, such as its storage tables, are not charged to the grains
	// measured.
	WarmupGrains = 10_000
	// PipelineDepth is the number of messages a PingPongPipelined pinger
	// keeps in flight to its ponger.
	PipelineDepth = 100
)

// ReportRate reports n units of work over the benchmark's measured time as a
// per-second rate. It must be called after the work has completed.
func ReportRate(b *testing.B, n int, unit string) {
	b.ReportMetric(float64(n)/b.Elapsed().Seconds(), unit)
}

// Ping is the message sent to an actor.
type Ping struct{}

// Pong is the reply an actor sends back for a Ping.
type Pong struct{}

// Start kicks off a ping-pong exchange between two actors.
type Start struct{}

// Token travels around a ThreadRing; Hops is the number of passes left.
type Token struct {
	Hops int
}

// SkynetResult carries the sum of a Skynet subtree up to its parent.
type SkynetResult struct {
	Sum int64
}

// SkynetTotal is the sum the root of a Skynet tree reports to the benchmark.
type SkynetTotal struct {
	Sum  int64
	Done chan struct{}
}

// NewSkynetTotal returns the result slot the root of a Skynet tree fills.
func NewSkynetTotal() *SkynetTotal {
	return &SkynetTotal{Done: make(chan struct{})}
}

// Complete records the total and wakes the benchmark.
func (t *SkynetTotal) Complete(sum int64) {
	t.Sum = sum
	close(t.Done)
}

// Wait blocks until the root has reported, then checks the total, failing the
// benchmark after waitTimeout.
func (t *SkynetTotal) Wait(b *testing.B) {
	b.Helper()
	select {
	case <-t.Done:
	case <-time.After(waitTimeout):
		b.Fatal("timed out waiting for the Skynet tree to complete")
	}

	if t.Sum != SkynetSum {
		b.Fatalf("skynet sum is %d, want %d", t.Sum, SkynetSum)
	}
}

// SkynetRange gives a Skynet node the leaves it covers: the leaves numbered
// Start to Start+Size-1. A node with Size one is a leaf and reports Start; any
// other node divides its range among SkynetFanOut children and reports the
// sum of their results.
type SkynetRange struct {
	Start int64
	Size  int64
}

// Children returns the ranges of a node's children, in order.
func (r SkynetRange) Children() []SkynetRange {
	children := make([]SkynetRange, SkynetFanOut)
	size := r.Size / SkynetFanOut
	for i := range children {
		children[i] = SkynetRange{Start: r.Start + int64(i)*size, Size: size}
	}
	return children
}

// BigRound is one round of a Big exchange. Each of the actors sends a Ping to
// a peer it picks at random, waits for the Pong, and repeats until it has
// done Rounds rounds. The Latch counts the Pongs, so it opens after
// BigActors*Rounds exchanges.
type BigRound struct{}

// NextPeer returns a peer for actor i in a Big exchange, never i itself. The
// choice is a cheap deterministic pseudo-random walk, so every framework sends
// to the same peers in the same order and the work is identical.
func NextPeer(i, round, actors int) int {
	peer := (i + 1 + (round*7919+i*104729)%(actors-1)) % actors
	if peer == i {
		peer = (peer + 1) % actors
	}
	return peer
}

// Share returns how many of n units go to the i-th of count recipients when
// the units are dealt round-robin: the first n % count recipients get one more.
func Share(n, i, count int) int {
	share := n / count
	if i < n%count {
		share++
	}
	return share
}

// MeasureHeap returns the live heap in bytes after a full collection.
func MeasureHeap() uint64 {
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// ReportHeapPerActor reports how much live heap each of n actors kept: the
// growth from before to after, divided by n.
func ReportHeapPerActor(b *testing.B, before, after uint64, n int) {
	reportHeapPer(b, before, after, n, BytesPerActor)
}

// ReportHeapPerGrain reports how much live heap each of n grains kept: the
// growth from before to after, divided by n.
func ReportHeapPerGrain(b *testing.B, before, after uint64, n int) {
	reportHeapPer(b, before, after, n, BytesPerGrain)
}

func reportHeapPer(b *testing.B, before, after uint64, n int, unit string) {
	if after < before {
		after = before
	}
	b.ReportMetric(float64(after-before)/float64(n), unit)
}

// Latch is closed once an actor has handled the expected number of messages.
// It lets a benchmark block until all the work it enqueued has been processed,
// instead of only measuring how fast messages are enqueued.
type Latch struct {
	target int64
	count  atomic.Int64
	done   chan struct{}
}

// NewLatch creates a Latch that opens after n calls to Count.
func NewLatch(n int) *Latch {
	return &Latch{target: int64(n), done: make(chan struct{})}
}

// Count records one handled message and reports whether the latch is now open.
func (l *Latch) Count() bool {
	switch l.count.Add(1) {
	case l.target:
		close(l.done)
		return true
	default:
		return false
	}
}

// Wait blocks until the latch opens, failing the benchmark after waitTimeout.
func (l *Latch) Wait(b *testing.B) {
	b.Helper()
	select {
	case <-l.done:
	case <-time.After(waitTimeout):
		b.Fatalf("timed out: handled %d of %d messages", l.count.Load(), l.target)
	}
}

// Signal notifies a benchmark that an actor's stop hook has run.
type Signal chan struct{}

// NewSignal creates a buffered Signal so the actor never blocks on it. A single
// Signal is reused across iterations because each one is waited on before the
// next actor is spawned.
func NewSignal() Signal {
	return make(Signal, 1)
}

// Fire notifies the waiter.
func (s Signal) Fire() {
	s <- struct{}{}
}

// Wait blocks until the signal fires, failing the benchmark after Timeout.
func (s Signal) Wait(b *testing.B) {
	b.Helper()
	select {
	case <-s:
	case <-time.After(Timeout):
		b.Fatal("timed out waiting for the actor to stop")
	}
}

var seq atomic.Uint64

// Name returns a unique actor name for frameworks that require one.
func Name(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, seq.Add(1))
}
