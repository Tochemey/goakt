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

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/internal/address"
	dynaport "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/remoteclient"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

const (
	// mailboxCapacity is the capacity of the slow actor's BoundedMailbox.
	mailboxCapacity = 2
	// floodSize is how many tells the sender fires at the slow actor. It
	// exceeds the capacity plus the one message being handled, so the
	// overflow has to be either refused or absorbed by the connection.
	floodSize = 20
	// turnDuration is how long the slow actor spends on each message. The
	// flood arrives within milliseconds, so the mailbox is full for about
	// (floodSize - mailboxCapacity) turns.
	turnDuration = time.Second
	// askTimeout bounds the ask toward the echo actor. A lane that still
	// reads frames answers in milliseconds.
	askTimeout = 3 * time.Second
)

// slow is a consumer that takes turnDuration per message. A bounded mailbox
// is full only while its consumer is slower than its producer, and no state
// of the runtime keeps a mailbox full without a turn: a suspended, passivating
// or stopping actor is not running, so a remote tell to it is refused before
// the mailbox. The pause models the slow turn and nothing else.
//
// Its mailbox is a blocking BoundedMailbox, so once the mailbox is full the
// next enqueue blocks the producer. For a remote tell that producer is the
// connection's inbound dispatcher, which is the behaviour under test.
type slow struct{}

func (s *slow) PreStart(*actor.Context) error { return nil }
func (s *slow) PostStop(*actor.Context) error { return nil }
func (s *slow) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
		pause.For(turnDuration)
	default:
		ctx.Unhandled()
	}
}

// echo answers every TestReply at once. It shares the receiver node, and
// therefore the single ordinary lane, with the slow actor.
type echo struct{}

func (e *echo) PreStart(*actor.Context) error { return nil }
func (e *echo) PostStop(*actor.Context) error { return nil }
func (e *echo) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestReply:
		ctx.Response(new(testpb.Reply))
	default:
		ctx.Unhandled()
	}
}

func main() {
	ctx := context.Background()

	port := dynaport.Get(1)[0]
	receiver, err := actor.NewActorSystem("receiver",
		actor.WithLogger(log.DiscardLogger),
		actor.WithRemote(remote.NewConfig("127.0.0.1", port)),
	)
	exitOn(err, "failed to create the receiver node")
	exitOn(receiver.Start(ctx), "failed to start the receiver node")

	_, err = receiver.Spawn(ctx, "slow", &slow{}, actor.WithMailbox(actor.NewBoundedMailbox(mailboxCapacity)))
	exitOn(err, "failed to spawn the slow actor")

	_, err = receiver.Spawn(ctx, "echo", &echo{})
	exitOn(err, "failed to spawn the echo actor")

	sender := remoteclient.NewClient()
	slowAddr := lookup(ctx, sender, receiver, "slow")
	echoAddr := lookup(ctx, sender, receiver, "echo")

	// Control: the echo actor answers before the flood.
	_, err = sender.RemoteAsk(ctx, address.NoSender(), echoAddr, new(testpb.TestReply), askTimeout)
	exitOn(err, "control failed: echo did not answer before the flood")
	fmt.Println("OK: echo answers before the flood")

	// Flood the slow actor. The first message is being handled, the next
	// mailboxCapacity fill the mailbox, and every further one is an overflow.
	sendErrors := 0

	for i := range floodSize {
		tellCtx, cancel := context.WithTimeout(ctx, time.Second)
		err := sender.RemoteTell(tellCtx, address.NoSender(), slowAddr, new(testpb.TestSend))
		cancel()

		if err != nil {
			sendErrors++
			fmt.Printf("tell %d to the slow actor failed on the sender: %v\n", i+1, err)
		}
	}

	fmt.Printf("sent %d tells to the slow actor (%d failed on the sender)\n", floodSize, sendErrors)

	// Give the receiver time to dispatch what it read.
	pause.For(500 * time.Millisecond)

	// The probe: an ask to the echo actor on the same lane. It answers only
	// if the receiver still reads and dispatches frames on that lane.
	start := time.Now()
	_, askErr := sender.RemoteAsk(ctx, address.NoSender(), echoAddr, new(testpb.TestReply), askTimeout)
	elapsed := time.Since(start).Round(time.Millisecond)

	deadletters := receiver.Metric(ctx).DeadlettersCount()
	overflow := int64(floodSize - mailboxCapacity - 1)

	sender.Close()

	if err := receiver.Stop(ctx); err != nil {
		fmt.Printf("failed to stop the receiver node: %v\n", err)
	}

	laneAlive := askErr == nil
	overflowRefused := deadletters >= overflow

	if laneAlive {
		fmt.Printf("OK: echo answered in %s while the slow actor's mailbox was full\n", elapsed)
	} else {
		fmt.Printf("BUG: echo did not answer within %s while the slow actor's mailbox was full: %v\n", elapsed, askErr)
	}

	if overflowRefused {
		fmt.Printf("OK: the overflow was refused and routed to dead letters (dead letters: %d, expected at least %d)\n", deadletters, overflow)
	} else {
		fmt.Printf("BUG: %d overflow tells were absorbed by the connection instead of being refused (dead letters: %d, expected at least %d)\n", overflow, deadletters, overflow)
	}

	if !laneAlive || !overflowRefused {
		fmt.Println("FAIL: a full blocking BoundedMailbox on a remote receiver stalls the lane for every receiver on it")
		os.Exit(1)
	}

	fmt.Println("PASS: a full blocking BoundedMailbox on a remote receiver refuses the overflow and keeps the lane alive")
}

// lookup resolves the address of an actor on the receiver node, or exits.
func lookup(ctx context.Context, sender remoteclient.Client, receiver actor.ActorSystem, name string) *address.Address {
	addr, err := sender.RemoteLookup(ctx, receiver.Host(), int(receiver.Port()), name)
	exitOn(err, "failed to look up the "+name+" actor")
	return addr
}

// exitOn prints the message with the error and exits when err is not nil.
func exitOn(err error, message string) {
	if err != nil {
		fmt.Printf("%s: %v\n", message, err)
		os.Exit(1)
	}
}
