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
	"github.com/tochemey/goakt/v4/internal/xsync"
	"github.com/tochemey/goakt/v4/log"
)

// deadletter is a synthetic actor that houses all deadletter
// in GoAkt deadletter are messages that have not been handled
type deadLetter struct {
	eventsStream eventstream.Stream
	pid          *PID
	logger       log.Logger
	counter      *atomic.Int64
	letters      *xsync.Map[string, *Deadletter]
	counters     *xsync.Map[string, *atomic.Int64]
}

// enforce the implementation of the Actor interface
var _ Actor = (*deadLetter)(nil)

// newDeadLetter creates an instance of deadletter
func newDeadLetter() *deadLetter {
	counter := atomic.NewInt64(0)
	return &deadLetter{
		letters:  xsync.NewMap[string, *Deadletter](),
		counters: xsync.NewMap[string, *atomic.Int64](),
		counter:  counter,
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
	default:
		// simply ignore anyhing else
	}
}

// PostStop handles post procedures
func (x *deadLetter) PostStop(ctx *Context) error {
	ctx.ActorSystem().Logger().Infof("%s stopped successfully", ctx.ActorName())
	return nil
}

func (x *deadLetter) handlePostStart(ctx *ReceiveContext) {
	x.eventsStream = ctx.Self().eventsStream
	x.logger = ctx.Logger()
	x.pid = ctx.Self()
	x.letters = xsync.NewMap[string, *Deadletter]()
	x.counters = xsync.NewMap[string, *atomic.Int64]()
	x.counter.Store(0)
	x.logger.Infof("%s started successfully", x.pid.Name())
}

func (x *deadLetter) handleDeadletter(msg *commands.Deadletter) {
	// increment the counter
	x.counter.Inc()
	// publish the deadletter message to the event stream
	deadLetter := NewDeadletter(msg.Sender, msg.Receiver, msg.Message, msg.SendTime, msg.Reason)

	x.eventsStream.Publish(eventsTopic, deadLetter)

	// letters the message for future query
	id := msg.Receiver
	x.letters.Set(id, deadLetter)
	if counter, ok := x.counters.Get(id); ok {
		counter.Inc()
		return
	}

	counter := atomic.NewInt64(1)
	x.counters.Set(id, counter)
}

// handlePublishDeadletters pushes the actor state back to the stream
func (x *deadLetter) handlePublishDeadletters() {
	x.letters.Range(func(_ string, deadletter *Deadletter) {
		x.eventsStream.Publish(eventsTopic, deadletter)
	})
}

// count returns the deadletter count
func (x *deadLetter) count(msg *commands.DeadlettersCountRequest) int64 {
	if msg.ActorID != nil {
		if counter, ok := x.counters.Get(*msg.ActorID); ok {
			return counter.Load()
		}
		return 0
	}
	return x.counter.Load()
}
