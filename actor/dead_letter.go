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

package actor

import (
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v4/eventstream"
	"github.com/tochemey/goakt/v4/internal/commands"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/internal/xsync"
	"github.com/tochemey/goakt/v4/log"
)

// deadletterPruneThreshold is the number of per-receiver buckets below which
// the deadletter actor never prunes. Above it, a prune runs each time the
// bucket count doubles since the last one, which keeps its cost amortized.
const deadletterPruneThreshold = 1024

// deadletterKey identifies one deadletter bucket. Counting by receiver address
// alone answers how many messages an actor dropped; adding the message type
// answers which messages it dropped, which is what an operator needs to act on
// the number.
type deadletterKey struct {
	// address is the string form of the receiver address.
	address string
	// messageType is the type name of the dropped message.
	messageType string
}

// deadletter is a synthetic actor that houses all deadletter
// in GoAkt deadletter are messages that have not been handled
type deadLetter struct {
	eventsStream eventstream.Stream
	pid          *PID
	logger       log.Logger
	counter      *atomic.Int64
	letters      *xsync.Map[string, *Deadletter]
	counters     *xsync.Map[deadletterKey, *atomic.Int64]
	// pruneAt is the bucket count at which the next prune runs. The
	// per-receiver buckets are only read for actors in the actor tree, so a
	// prune drops the buckets of receivers that have left it; without it, the
	// buckets would grow with every distinct receiver name ever seen.
	pruneAt int
}

// enforce the implementation of the Actor interface
var _ Actor = (*deadLetter)(nil)

// newDeadLetter creates an instance of deadletter
func newDeadLetter() *deadLetter {
	counter := atomic.NewInt64(0)
	return &deadLetter{
		letters:  xsync.NewMap[string, *Deadletter](),
		counters: xsync.NewMap[deadletterKey, *atomic.Int64](),
		counter:  counter,
		pruneAt:  deadletterPruneThreshold,
	}
}

// PreStart pre-starts the deadletter actor
func (x *deadLetter) PreStart(*Context) error {
	return nil
}

// Receive handles messages
func (x *deadLetter) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *PostStart:
		x.handlePostStart(ctx)
	case *commands.SendDeadletter:
		x.handleDeadletter(&msg.Deadletter)
	case *commands.PublishDeadletters:
		x.handlePublishDeadletters()
	case *commands.DeadlettersCountRequest:
		count := x.count(msg)
		ctx.Response(&commands.DeadlettersCountResponse{TotalCount: count})
	case *commands.DeadlettersSnapshotRequest:
		ctx.Response(&commands.DeadlettersSnapshotResponse{Counts: x.snapshot(), TotalCount: x.counter.Load()})
	default:
		// simply ignore anyhing else
	}
}

// PostStop handles post procedures
func (x *deadLetter) PostStop(ctx *Context) error {
	logger := ctx.ActorSystem().Logger()
	if logger.Enabled(log.InfoLevel) {
		logger.Infof("actor=%s stopped successfully", ctx.ActorName())
	}
	return nil
}

func (x *deadLetter) handlePostStart(ctx *ReceiveContext) {
	x.eventsStream = ctx.Self().getEventsStream()
	x.logger = ctx.Logger()
	x.pid = ctx.Self()
	x.letters = xsync.NewMap[string, *Deadletter]()
	x.counters = xsync.NewMap[deadletterKey, *atomic.Int64]()
	x.counter.Store(0)
	x.pruneAt = deadletterPruneThreshold
	if x.logger.Enabled(log.InfoLevel) {
		x.logger.Infof("actor=%s started successfully", x.pid.Name())
	}
}

func (x *deadLetter) handleDeadletter(msg *commands.Deadletter) {
	// increment the counter
	x.counter.Inc()
	// publish the deadletter message to the event stream
	deadLetter := NewDeadletter(newPath(msg.Sender), newPath(msg.Receiver), msg.Message, msg.SendTime, msg.Reason)

	x.eventsStream.Publish(eventsTopic, deadLetter)

	// The per-receiver buckets are only read for actors in the actor tree: a
	// receiver that is not there, a stopped actor, a grain or a remote actor,
	// is counted in the total only.
	id := msg.Receiver.String()
	if !x.inTree(id) {
		return
	}

	// letters the message for future query
	x.letters.Set(id, deadLetter)

	key := deadletterKey{address: id, messageType: types.NameOf(msg.Message)}
	if counter, ok := x.counters.Get(key); ok {
		counter.Inc()
		return
	}

	counter := atomic.NewInt64(1)
	x.counters.Set(key, counter)

	if x.counters.Len() >= x.pruneAt {
		x.prune()
		x.pruneAt = max(deadletterPruneThreshold, 2*x.counters.Len())
	}
}

// inTree reports whether the actor at the given address string is in the
// actor tree, which is where every reader of the per-receiver buckets looks
// actors up. Without an actor system to ask, every receiver is kept.
func (x *deadLetter) inTree(address string) bool {
	if x.pid == nil || x.pid.ActorSystem() == nil {
		return true
	}

	_, ok := x.pid.ActorSystem().tree().node(address)
	return ok
}

// prune drops the buckets of receivers that have left the actor tree since
// they were recorded: an actor that stopped or passivated. An actor spawned
// again under the same name later starts its counts from zero.
func (x *deadLetter) prune() {
	for _, key := range x.counters.Keys() {
		if !x.inTree(key.address) {
			x.counters.Delete(key)
		}
	}

	for _, address := range x.letters.Keys() {
		if !x.inTree(address) {
			x.letters.Delete(address)
		}
	}
}

// handlePublishDeadletters pushes the actor state back to the stream
func (x *deadLetter) handlePublishDeadletters() {
	x.letters.Range(func(_ string, deadletter *Deadletter) {
		x.eventsStream.Publish(eventsTopic, deadletter)
	})
}

// count returns the deadletter count.
//
// The registry buckets counts per (receiver address, message type), so the
// total for one address is the sum of every bucket it owns. Callers see the
// same number they saw when the registry was keyed by address alone.
func (x *deadLetter) count(msg *commands.DeadlettersCountRequest) int64 {
	if msg.Address == nil {
		return x.counter.Load()
	}

	address := msg.Address.String()
	var total int64

	x.counters.Range(func(key deadletterKey, counter *atomic.Int64) {
		if key.address == address {
			total += counter.Load()
		}
	})

	return total
}

// snapshot returns a copy of the deadletter counts, one entry per recorded
// (receiver address, message type) pair. It runs on the deadletter actor's own
// goroutine while handling a DeadlettersSnapshotRequest, so it reads the
// registry without racing the receive loop. The metrics collector asks for this
// once per scrape to observe actor.deadletters.count across the whole tree with
// a single message instead of one ask per actor.
func (x *deadLetter) snapshot() []commands.DeadletterCount {
	counts := make([]commands.DeadletterCount, 0, x.counters.Len())

	x.counters.Range(func(key deadletterKey, counter *atomic.Int64) {
		counts = append(counts, commands.DeadletterCount{
			Address:     key.address,
			MessageType: key.messageType,
			Count:       counter.Load(),
		})
	})

	return counts
}
