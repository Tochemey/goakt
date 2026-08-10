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

// Package main measures duplex remoting Tell throughput for
// github.com/Tochemey/goakt/issues/1301. It adapts the two-node ping/pong
// topology from https://github.com/Tochemey/goakt-examples/tree/main/goakt-remoting
// into a single process so the sample stays playground-friendly, then runs a
// timed fire-and-forget blast over ProtocolPinDuplex (no compression) and
// prints msgs/sec as the receiver sees them.
//
//	go run ./playground/issue-1301
//	go run ./playground/issue-1301 -duration=10s -senders=20
//	go run ./playground/issue-1301 -mode=pingpong -rounds=100000
//	go run ./playground/issue-1301 -mode=lanes
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	dynaport "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/supervisor"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// ordinaryLanes is the -lanes flag value applied to both nodes.
var ordinaryLanes int

// creditWindow is the -window flag value applied to both nodes; zero keeps
// the remote config default.
var creditWindow uint64

func main() {
	mode := flag.String("mode", "blast", "measurement mode: blast, pingpong, lanes, ask, isolation, largesmall, controllatency, or stalledrecv")
	duration := flag.Duration("duration", 10*time.Second, "blast duration")
	senders := flag.Int("senders", 20, "concurrent blast senders")
	rounds := flag.Int("rounds", 100_000, "pingpong round-trips before stopping")
	pinName := flag.String("pin", "duplex", "protocol pin: duplex, legacy, or auto")
	receivers := flag.Int("receivers", 1, "number of Pong receiver actors the blast fans over")
	lanes := flag.Int("lanes", 1, "ordinary duplex lanes per peer (coalescer shards)")
	window := flag.Uint64("window", 0, "credit window bytes per duplex connection (0 = default 16MiB); bounds end-to-end receiver residency")
	pongPinName := flag.String("pongpin", "", "protocol pin for the pong node only (default: same as -pin); -pin=auto -pongpin=legacy is the mixed-version auto-pin cell")
	cpuProfile := flag.String("cpuprofile", "", "write a CPU profile to this file")
	flag.Parse()

	pin, err := parsePin(*pinName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(2)
	}

	pongPin := pin
	if *pongPinName != "" {
		pongPin, err = parsePin(*pongPinName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(2)
		}
	}

	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cpuprofile: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()

		if err := pprof.StartCPUProfile(f); err != nil {
			fmt.Fprintf(os.Stderr, "cpuprofile: %v\n", err)
			os.Exit(1)
		}
		defer pprof.StopCPUProfile()
	}

	ordinaryLanes = *lanes
	creditWindow = *window
	ctx := context.Background()
	ports := dynaport.Get(2)
	pingPort, pongPort := ports[0], ports[1]

	pongSys, pongPID, err := startNode(ctx, "pong-node", pongPort, pongPin, "Pong", NewPong())
	if err != nil {
		fmt.Fprintf(os.Stderr, "start pong: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = pongSys.Stop(ctx) }()

	pingSys, pingPID, err := startNode(ctx, "ping-node", pingPort, pin, "Ping", NewPing())
	if err != nil {
		fmt.Fprintf(os.Stderr, "start ping: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = pingSys.Stop(ctx) }()

	remotePong, err := pingPID.RemoteLookup(ctx, "127.0.0.1", pongPort, "Pong")
	if err != nil {
		fmt.Fprintf(os.Stderr, "lookup Pong: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("issue-1301 remoting throughput\n")
	fmt.Printf("  pin=%s mode=%s ping=127.0.0.1:%d pong=127.0.0.1:%d\n",
		pin, *mode, pingPort, pongPort)

	targets := []*actor.PID{remotePong}
	pongs := []*actor.PID{pongPID}

	for i := 1; i < *receivers; i++ {
		name := fmt.Sprintf("Pong-%d", i)
		pongs = append(pongs, spawnOn(ctx, pongSys, name, NewPong()))
		targets = append(targets, lookupFrom(ctx, pingPID, pongPort, name))
	}

	switch *mode {
	case "blast":
		runBlast(ctx, pingPID, targets, pongs, *duration, *senders)
	case "pingpong":
		runPingPong(ctx, pingPID, remotePong, pongPID, *rounds)
	case "lanes":
		runLanes(ctx, pingPID, remotePong)
	case "ask":
		runAsk(ctx, pingPID, pongSys, pongPort, *duration, *senders)
	case "isolation":
		runIsolation(ctx, pingPID, remotePong, pongPID, pongSys, pongPort, *duration, *senders)
	case "largesmall":
		runLargeSmall(ctx, pingPID, pongSys, pongPort, *duration, *senders)
	case "controllatency":
		runControlLatency(ctx, pingPID, remotePong, pongPID, pongPort, *duration, *senders)
	case "stalledrecv":
		runStalledRecv(ctx, pingPID, pongSys, pongPort, *duration, *senders)
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %q (want blast, pingpong, lanes, ask, isolation, largesmall, controllatency, or stalledrecv)\n", *mode)
		os.Exit(2)
	}
}

func parsePin(name string) (remote.ProtocolPin, error) {
	switch name {
	case "duplex":
		return remote.ProtocolPinDuplex, nil
	case "legacy":
		return remote.ProtocolPinLegacy, nil
	case "auto":
		return remote.ProtocolPinAuto, nil
	default:
		return 0, fmt.Errorf("unknown pin %q (want duplex, legacy, or auto)", name)
	}
}

func startNode(ctx context.Context, name string, port int, pin remote.ProtocolPin, actorName string, a actor.Actor) (actor.ActorSystem, *actor.PID, error) {
	opts := []remote.Option{
		remote.WithProtocolPin(pin),
		remote.WithCompression(remote.NoCompression),
		remote.WithOrdinaryLanes(uint32(ordinaryLanes)),
	}

	if creditWindow > 0 {
		opts = append(opts, remote.WithCreditWindow(creditWindow))
	}

	cfg := remote.NewConfig("127.0.0.1", port, opts...)
	sys, err := actor.NewActorSystem(name,
		actor.WithLogger(log.DiscardLogger),
		actor.WithActorInitMaxRetries(1),
		actor.WithRemote(cfg),
	)
	if err != nil {
		return nil, nil, err
	}
	if err := sys.Start(ctx); err != nil {
		return nil, nil, err
	}
	pid, err := sys.Spawn(ctx, actorName, a,
		actor.WithLongLived(),
		actor.WithSupervisor(supervisor.NewSupervisor(
			supervisor.WithAnyErrorDirective(supervisor.ResumeDirective),
		)),
	)
	if err != nil {
		_ = sys.Stop(ctx)
		return nil, nil, err
	}
	return sys, pid, nil
}

func runBlast(ctx context.Context, ping *actor.PID, targets []*actor.PID, pongs []*actor.PID, duration time.Duration, senders int) {
	var sent atomic.Int64
	var sendErrs atomic.Int64
	msg := new(testpb.TestSend)

	var wg sync.WaitGroup
	deadline := time.Now().Add(duration)
	wg.Add(senders)
	for i := range senders {
		target := targets[i%len(targets)]
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				if err := ping.Tell(ctx, target, msg); err != nil {
					sendErrs.Add(1)
					continue
				}
				sent.Add(1)
			}
		}()
	}
	goroutines := runtime.NumGoroutine()
	libGoroutines := countLibraryGoroutines()
	wg.Wait()
	time.Sleep(time.Second)

	s := sent.Load()
	r := int64(0)

	for _, pong := range pongs {
		r += askCount(ctx, pong)
	}

	e := sendErrs.Load()
	ratio := 0.0
	if s > 0 {
		ratio = float64(r) / float64(s)
	}
	fmt.Printf("blast results\n")
	fmt.Printf("  duration=%s senders=%d\n", duration, senders)
	fmt.Printf("  sent=%d received=%d errors=%d delivered/sent=%.3f\n", s, r, e, ratio)
	fmt.Printf("  throughput=%.0f messages/sec\n", float64(r)/duration.Seconds())
	fmt.Printf("  goroutines at peak=%d (library-owned=%d)\n", goroutines, libGoroutines)
	printTransportDecomposition("post-blast steady state")
}

// transportFunctions maps a display label to the fully qualified function
// substring counted in the whole-process goroutine dump. Each function runs
// at most once per goroutine and none of them call each other, so substring
// occurrence count equals goroutine count.
var transportFunctions = []struct {
	label  string
	symbol string
}{
	{"duplex readLoop", "(*duplexConn).readLoop("},
	{"duplex writeLoop", "(*duplexConn).writeLoop("},
	{"duplex livenessLoop", "(*duplexConn).livenessLoop("},
	{"client session monitor", "(*peer).monitorSession("},
	{"client tell pump", "(*peer).runTellPump("},
	{"client coalescer writer", "(*coalescer).run("},
	{"server session serve loop", "(*RemotingServer).handleDuplexConn("},
}

// countLibraryGoroutines reports how many goroutines goakt itself owns:
// those whose "created by" frame lives inside the module. Harness senders
// and runtime goroutines that merely pass through library calls do not
// count, so this is the library's goroutine footprint, not the process's.
func countLibraryGoroutines() int {
	buf := make([]byte, 8<<20)
	n := runtime.Stack(buf, true)

	count := 0
	for block := range strings.SplitSeq(string(buf[:n]), "\n\ngoroutine ") {
		idx := strings.LastIndex(block, "created by ")
		if idx < 0 {
			continue
		}

		if strings.Contains(block[idx:], "github.com/tochemey/goakt/v4/") {
			count++
		}
	}

	return count
}

// printTransportDecomposition dumps all goroutine stacks and prints how many
// goroutines sit in each known transport function, so per-component savings
// are measured rather than derived. Both nodes run in this process, so the
// counts cover client and server sides together. The library-owned total
// counts every goroutine goakt spawned (actor system included); the process
// total adds harness and runtime goroutines on top.
func printTransportDecomposition(label string) {
	buf := make([]byte, 8<<20)
	n := runtime.Stack(buf, true)
	dump := string(buf[:n])

	total := runtime.NumGoroutine()
	transport := 0
	fmt.Printf("transport goroutines (%s)\n", label)

	for _, fn := range transportFunctions {
		count := strings.Count(dump, fn.symbol)
		transport += count
		fmt.Printf("  %-26s %d\n", fn.label, count)
	}

	fmt.Printf("  %-26s %d\n", "transport total", transport)
	fmt.Printf("  %-26s %d\n", "library-owned total", countLibraryGoroutines())
	fmt.Printf("  %-26s %d\n", "process total", total)
}

// runLanes warms the duplex lanes the public API can reach (the startup
// RemoteLookup dials the control lane; one remote tell dials the ordinary
// lane), waits for the dials and flushes to settle, then prints the
// transport goroutine decomposition. This is the warmed-lane cell from the
// goroutine budget plan: the per-peer number is measured, not derived.
func runLanes(ctx context.Context, ping *actor.PID, remotePong *actor.PID) {
	if err := ping.Tell(ctx, remotePong, new(testpb.TestSend)); err != nil {
		fmt.Fprintf(os.Stderr, "warm ordinary lane: %v\n", err)
		os.Exit(1)
	}

	pause.For(2 * time.Second)
	printTransportDecomposition("warmed lanes, idle")
}

// askCount reads an actor's message counter through Ask/Response so the
// driver never touches actor state and never shares concurrency primitives
// with the mailbox.
func askCount(ctx context.Context, pid *actor.PID) int64 {
	resp, err := actor.Ask(ctx, pid, new(testpb.TestGetCount), 5*time.Second)
	if err != nil {
		return -1
	}
	count, ok := resp.(*testpb.TestCount)
	if !ok {
		return -1
	}
	return int64(count.GetValue())
}

func runPingPong(ctx context.Context, pingPID *actor.PID, remotePong *actor.PID, pongPID *actor.PID, rounds int) {
	if err := actor.Tell(ctx, pingPID, &startPingPong{rounds: int64(rounds)}); err != nil {
		fmt.Fprintf(os.Stderr, "arm pingpong: %v\n", err)
		os.Exit(1)
	}

	start := time.Now()
	if err := pingPID.Tell(ctx, remotePong, new(testpb.TestReply)); err != nil {
		fmt.Fprintf(os.Stderr, "start pingpong: %v\n", err)
		os.Exit(1)
	}

	deadline := time.Now().Add(2 * time.Minute)
	for askCount(ctx, pingPID) < int64(rounds) {
		if time.Now().After(deadline) {
			fmt.Fprintf(os.Stderr, "pingpong timed out after %d/%d rounds\n", askCount(ctx, pingPID), rounds)
			os.Exit(1)
		}
		pause.For(10 * time.Millisecond)
	}
	elapsed := time.Since(start)

	// Each round-trip is Ping→Pong + Pong→Ping (two remoting tells).
	roundTrips := askCount(ctx, pingPID)
	messages := roundTrips * 2
	fmt.Printf("pingpong results\n")
	fmt.Printf("  rounds=%d elapsed=%s\n", roundTrips, elapsed)
	fmt.Printf("  remoting messages=%d (2 per round-trip)\n", messages)
	fmt.Printf("  round-trips/sec=%.0f  messages/sec=%.0f\n",
		float64(roundTrips)/elapsed.Seconds(),
		float64(messages)/elapsed.Seconds())
	fmt.Printf("  pong received=%d\n", askCount(ctx, pongPID))
}

// startPingPong arms the Ping actor for a round-trip run. Completion is
// observed by the harness via Ask on the counter, not a shared channel.
type startPingPong struct {
	rounds int64
}

// Ping drives either the blast sender identity or the pingpong RTT loop. All
// state is plain fields owned by the actor: the mailbox serializes access, so
// no synchronization primitives are needed (actor-model idiom).
type Ping struct {
	target int64
	count  int64
}

func NewPing() *Ping { return &Ping{} }

func (x *Ping) PreStart(*actor.Context) error { return nil }
func (x *Ping) PostStop(*actor.Context) error { return nil }

func (x *Ping) Receive(ctx *actor.ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *actor.PostStart:
	case *startPingPong:
		x.target = msg.rounds
		x.count = 0
	case *testpb.TestGetCount:
		ctx.Response(&testpb.TestCount{Value: int32(x.count)})
	case *testpb.Reply:
		x.count++
		if x.count >= x.target {
			return
		}
		ctx.Tell(ctx.Sender(), new(testpb.TestReply))
	default:
		ctx.Unhandled()
	}
}

// Pong counts inbound fire-and-forget messages for blast mode, and answers
// TestReply with Reply for pingpong mode. The counter is a plain field: the
// mailbox is the synchronization.
type Pong struct {
	received int64
}

func NewPong() *Pong { return &Pong{} }

func (x *Pong) PreStart(*actor.Context) error { return nil }
func (x *Pong) PostStop(*actor.Context) error { return nil }

func (x *Pong) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *actor.PostStart:
	case *testpb.TestGetCount:
		ctx.Response(&testpb.TestCount{Value: int32(x.received)})
	case *testpb.TestSend:
		x.received++
	case *testpb.TestReply:
		x.received++
		ctx.Tell(ctx.Sender(), new(testpb.Reply))
	default:
		ctx.Unhandled()
	}
}

// latencies collects per-operation durations for percentile reporting.
type latencies struct {
	mu sync.Mutex
	ds []time.Duration
}

func (l *latencies) add(d time.Duration) {
	l.mu.Lock()
	l.ds = append(l.ds, d)
	l.mu.Unlock()
}

// percentile returns the p-quantile (0 < p <= 1) of the recorded durations.
func (l *latencies) percentile(p float64) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.ds) == 0 {
		return 0
	}

	sorted := make([]time.Duration, len(l.ds))
	copy(sorted, l.ds)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	idx := int(float64(len(sorted))*p) - 1
	if idx < 0 {
		idx = 0
	}
	return sorted[idx]
}

// spawnOn spawns a long-lived helper actor on sys and returns its PID.
func spawnOn(ctx context.Context, sys actor.ActorSystem, name string, a actor.Actor) *actor.PID {
	pid, err := sys.Spawn(ctx, name, a,
		actor.WithLongLived(),
		actor.WithSupervisor(supervisor.NewSupervisor(
			supervisor.WithAnyErrorDirective(supervisor.ResumeDirective),
		)),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spawn %s: %v\n", name, err)
		os.Exit(1)
	}
	return pid
}

// lookupFrom resolves a remote actor by name through pid's remoting client.
func lookupFrom(ctx context.Context, pid *actor.PID, port int, name string) *actor.PID {
	remote, err := pid.RemoteLookup(ctx, "127.0.0.1", port, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lookup %s: %v\n", name, err)
		os.Exit(1)
	}
	return remote
}

// Echo answers ask-style messages with a Reply and counts everything it sees.
// Account carries the large-payload cells; TestSend the small tells.
type Echo struct {
	received int64
}

func NewEcho() *Echo { return &Echo{} }

func (x *Echo) PreStart(*actor.Context) error { return nil }
func (x *Echo) PostStop(*actor.Context) error { return nil }

func (x *Echo) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *actor.PostStart:
	case *testpb.TestGetCount:
		ctx.Response(&testpb.TestCount{Value: int32(x.received)})
	case *testpb.TestReply:
		x.received++
		ctx.Response(new(testpb.Reply))
	case *testpb.Account:
		x.received++
	case *testpb.TestSend:
		x.received++
	default:
		ctx.Unhandled()
	}
}

// Sleeper counts messages while sleeping per message, modeling a slow or
// stalled consumer whose mailbox lags the transport.
type Sleeper struct {
	delay    time.Duration
	received int64
}

func NewSleeper(delay time.Duration) *Sleeper { return &Sleeper{delay: delay} }

func (x *Sleeper) PreStart(*actor.Context) error { return nil }
func (x *Sleeper) PostStop(*actor.Context) error { return nil }

func (x *Sleeper) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *actor.PostStart:
	case *testpb.TestGetCount:
		ctx.Response(&testpb.TestCount{Value: int32(x.received)})
	case *testpb.TestSend:
		x.received++
		pause.For(x.delay)
	default:
		ctx.Unhandled()
	}
}

// runAsk measures request/response throughput and latency over remoting:
// senders Ask a remote Echo actor and wait for each Reply.
func runAsk(ctx context.Context, ping *actor.PID, pongSys actor.ActorSystem, pongPort int, duration time.Duration, senders int) {
	spawnOn(ctx, pongSys, "Echo", NewEcho())
	remoteEcho := lookupFrom(ctx, ping, pongPort, "Echo")

	var asked, askErrs atomic.Int64
	lat := &latencies{}
	deadline := time.Now().Add(duration)

	var wg sync.WaitGroup
	wg.Add(senders)
	for range senders {
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				start := time.Now()
				if _, err := ping.Ask(ctx, remoteEcho, new(testpb.TestReply), 5*time.Second); err != nil {
					askErrs.Add(1)
					continue
				}
				lat.add(time.Since(start))
				asked.Add(1)
			}
		}()
	}
	wg.Wait()

	fmt.Printf("ask results\n")
	fmt.Printf("  duration=%s senders=%d\n", duration, senders)
	fmt.Printf("  asks=%d errors=%d throughput=%.0f asks/sec\n", asked.Load(), askErrs.Load(), float64(asked.Load())/duration.Seconds())
	fmt.Printf("  latency p50=%s p99=%s\n", lat.percentile(0.50), lat.percentile(0.99))
}

// runIsolation blasts a slow actor and a fast actor on the same peer at the
// same time: the fast actor's throughput shows whether a slow consumer can
// stall unrelated traffic on the shared transport.
func runIsolation(ctx context.Context, ping *actor.PID, remotePong *actor.PID, pongPID *actor.PID, pongSys actor.ActorSystem, pongPort int, duration time.Duration, senders int) {
	spawnOn(ctx, pongSys, "Slow", NewSleeper(time.Millisecond))
	remoteSlow := lookupFrom(ctx, ping, pongPort, "Slow")

	slowSenders := senders / 4
	if slowSenders == 0 {
		slowSenders = 1
	}
	fastSenders := senders - slowSenders

	var fastSent, slowSent atomic.Int64
	deadline := time.Now().Add(duration)

	var wg sync.WaitGroup
	wg.Add(fastSenders + slowSenders)

	for range fastSenders {
		go func() {
			defer wg.Done()
			msg := new(testpb.TestSend)
			for time.Now().Before(deadline) {
				if err := ping.Tell(ctx, remotePong, msg); err == nil {
					fastSent.Add(1)
				}
			}
		}()
	}

	for range slowSenders {
		go func() {
			defer wg.Done()
			msg := new(testpb.TestSend)
			for time.Now().Before(deadline) {
				if err := ping.Tell(ctx, remoteSlow, msg); err == nil {
					slowSent.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	time.Sleep(time.Second)

	fastReceived := askCount(ctx, pongPID)
	fmt.Printf("isolation results\n")
	fmt.Printf("  duration=%s fastSenders=%d slowSenders=%d (slow actor sleeps 1ms/msg)\n", duration, fastSenders, slowSenders)
	fmt.Printf("  fast: sent=%d received=%d throughput=%.0f msgs/sec\n", fastSent.Load(), fastReceived, float64(fastReceived)/duration.Seconds())
	fmt.Printf("  slow: sent=%d (delivery lags by design)\n", slowSent.Load())
}

// runLargeSmall interleaves chunked large transfers with small asks and
// reports the small-message latency the large traffic induces.
func runLargeSmall(ctx context.Context, ping *actor.PID, pongSys actor.ActorSystem, pongPort int, duration time.Duration, senders int) {
	spawnOn(ctx, pongSys, "Echo", NewEcho())
	remoteEcho := lookupFrom(ctx, ping, pongPort, "Echo")

	largeSenders := 2
	smallSenders := senders - largeSenders
	if smallSenders <= 0 {
		smallSenders = 1
	}

	payload := strings.Repeat("x", 1<<20)
	var largeSent, smallAsked, errs atomic.Int64
	lat := &latencies{}
	deadline := time.Now().Add(duration)

	var wg sync.WaitGroup
	wg.Add(largeSenders + smallSenders)

	for range largeSenders {
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				if err := ping.Tell(ctx, remoteEcho, &testpb.Account{AccountId: payload}); err == nil {
					largeSent.Add(1)
				} else {
					errs.Add(1)
				}
			}
		}()
	}

	for range smallSenders {
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				start := time.Now()
				if _, err := ping.Ask(ctx, remoteEcho, new(testpb.TestReply), 5*time.Second); err != nil {
					errs.Add(1)
					continue
				}
				lat.add(time.Since(start))
				smallAsked.Add(1)
			}
		}()
	}
	wg.Wait()

	fmt.Printf("largesmall results\n")
	fmt.Printf("  duration=%s largeSenders=%d (1 MiB tells) smallSenders=%d (asks)\n", duration, largeSenders, smallSenders)
	fmt.Printf("  large sent=%d (%.0f MiB/sec) small asks=%d errors=%d\n", largeSent.Load(), float64(largeSent.Load())/duration.Seconds(), smallAsked.Load(), errs.Load())
	fmt.Printf("  small latency p50=%s p99=%s\n", lat.percentile(0.50), lat.percentile(0.99))
}

// runControlLatency measures control-plane RPC latency (RemoteLookup) while
// the ordinary lane carries a full blast, showing control/data isolation.
func runControlLatency(ctx context.Context, ping *actor.PID, remotePong *actor.PID, pongPID *actor.PID, pongPort int, duration time.Duration, senders int) {
	var sent atomic.Int64
	var lookups, lookupErrs atomic.Int64
	lat := &latencies{}
	deadline := time.Now().Add(duration)

	var wg sync.WaitGroup
	wg.Add(senders + 1)

	for range senders {
		go func() {
			defer wg.Done()
			msg := new(testpb.TestSend)
			for time.Now().Before(deadline) {
				if err := ping.Tell(ctx, remotePong, msg); err == nil {
					sent.Add(1)
				}
			}
		}()
	}

	go func() {
		defer wg.Done()
		for time.Now().Before(deadline) {
			start := time.Now()
			if _, err := ping.RemoteLookup(ctx, "127.0.0.1", pongPort, "Pong"); err != nil {
				lookupErrs.Add(1)
			} else {
				lat.add(time.Since(start))
				lookups.Add(1)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	wg.Wait()
	time.Sleep(time.Second)

	fmt.Printf("controllatency results\n")
	fmt.Printf("  duration=%s blastSenders=%d\n", duration, senders)
	fmt.Printf("  blast sent=%d received=%d\n", sent.Load(), askCount(ctx, pongPID))
	fmt.Printf("  lookups=%d errors=%d p50=%s p99=%s\n", lookups.Load(), lookupErrs.Load(), lat.percentile(0.50), lat.percentile(0.99))
}

// runStalledRecv blasts an effectively stalled consumer and reports memory
// growth and admission behavior, showing what a wedged peer costs the sender
// process.
func runStalledRecv(ctx context.Context, ping *actor.PID, pongSys actor.ActorSystem, pongPort int, duration time.Duration, senders int) {
	spawnOn(ctx, pongSys, "Stalled", NewSleeper(100*time.Millisecond))
	remoteStalled := lookupFrom(ctx, ping, pongPort, "Stalled")

	var before runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	var sent, errs atomic.Int64
	deadline := time.Now().Add(duration)

	var wg sync.WaitGroup
	wg.Add(senders)
	for range senders {
		go func() {
			defer wg.Done()
			msg := new(testpb.TestSend)
			for time.Now().Before(deadline) {
				callCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
				err := ping.Tell(callCtx, remoteStalled, msg)
				cancel()
				if err != nil {
					errs.Add(1)
				} else {
					sent.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	fmt.Printf("stalledrecv results\n")
	fmt.Printf("  duration=%s senders=%d (receiver sleeps 100ms/msg)\n", duration, senders)
	fmt.Printf("  admitted=%d backpressure/errors=%d\n", sent.Load(), errs.Load())
	fmt.Printf("  heap before=%d MiB after=%d MiB sys after=%d MiB\n", before.HeapAlloc>>20, after.HeapAlloc>>20, after.Sys>>20)
}
