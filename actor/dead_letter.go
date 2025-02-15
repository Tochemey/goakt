/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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

package actors

import (
	"context"

	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v3/address"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/collection/syncmap"
	"github.com/tochemey/goakt/v3/internal/eventstream"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/log"
)

// deadletters is a synthetic actor that houses all deadletter
// in GoAkt deadletter are messages that have not been handled
type deadLetter struct {
	eventsStream *eventstream.EventsStream
	pid          *PID
	logger       log.Logger
	counter      *atomic.Int64
	letters      *syncmap.Map[string, *goaktpb.Deadletter]
	counters     *syncmap.Map[string, *atomic.Int64]
}

// enforce the implementation of the Actor interface
var _ Actor = (*deadLetter)(nil)

// newDeadLetter creates an instance of deadletter
func newDeadLetter() *deadLetter {
	counter := atomic.NewInt64(0)
	return &deadLetter{
		letters:  syncmap.New[string, *goaktpb.Deadletter](),
		counters: syncmap.New[string, *atomic.Int64](),
		counter:  counter,
	}
}

// PreStart pre-starts the deadletter actor
func (x *deadLetter) PreStart(context.Context) error {
	return nil
}

// Receive handles messages
func (x *deadLetter) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *goaktpb.PostStart:
		x.handlePostStart(ctx)
	case *internalpb.EmitDeadletter:
		x.handleDeadletter(msg.GetDeadletter())
	case *internalpb.GetDeadletters:
		x.handleGetDeadletters()
	case *internalpb.GetDeadlettersCount:
		count := x.count(msg)
		ctx.Response(&internalpb.DeadlettersCount{
			TotalCount: count,
		})
	default:
		// simply ignore anyhing else
	}
}

// PostStop handles post procedures
func (x *deadLetter) PostStop(context.Context) error {
	x.logger.Infof("%s stopped successfully", x.pid.Name())
	return nil
}

func (x *deadLetter) handlePostStart(ctx *ReceiveContext) {
	x.eventsStream = ctx.Self().eventsStream
	x.logger = ctx.Logger()
	x.pid = ctx.Self()
	x.letters = syncmap.New[string, *goaktpb.Deadletter]()
	x.counters = syncmap.New[string, *atomic.Int64]()
	x.counter.Store(0)
	x.logger.Infof("%s started successfully", x.pid.Name())
}

func (x *deadLetter) handleDeadletter(msg *goaktpb.Deadletter) {
	// increment the counter
	x.counter.Inc()
	// publish the deadletter message to the event stream
	x.eventsStream.Publish(eventsTopic, msg)

	// letters the message for future query
	id := address.From(msg.GetReceiver()).String()
	x.letters.Set(id, msg)
	if counter, ok := x.counters.Get(id); ok {
		counter.Inc()
		return
	}

	counter := atomic.NewInt64(1)
	x.counters.Set(id, counter)
}

// handleGetDeadletters pushes the actor state back to the stream
func (x *deadLetter) handleGetDeadletters() {
	x.letters.Range(func(_ string, deadletter *goaktpb.Deadletter) {
		x.eventsStream.Publish(eventsTopic, deadletter)
	})
}

// count returns the deadletter count
func (x *deadLetter) count(msg *internalpb.GetDeadlettersCount) int64 {
	if msg.ActorId != nil {
		if counter, ok := x.counters.Get(msg.GetActorId()); ok {
			return counter.Load()
		}
		return 0
	}
	return x.counter.Load()
}
