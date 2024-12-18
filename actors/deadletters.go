/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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
	"sync"

	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/address"
	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/internal/eventstream"
	"github.com/tochemey/goakt/v2/internal/internalpb"
	"github.com/tochemey/goakt/v2/log"
)

// deadletters is a synthetic actor that houses all deadletter
// in GoAkt deadletter are messages that have not been handled
type deadLetters struct {
	eventsStream *eventstream.EventsStream
	pid          *PID
	lettersMap   map[string][]byte
	logger       log.Logger

	// necessary for metrics
	counter     *atomic.Int64
	countersMap map[string]*atomic.Int64

	// this is purposefully needed for PreStart and PostStop hooks only
	mux *sync.Mutex
}

// enforce the implementation of the Actor interface
var _ Actor = (*deadLetters)(nil)

// newDeadLetters creates an instance of deadletters
func newDeadLetters() *deadLetters {
	counter := atomic.NewInt64(0)
	return &deadLetters{
		lettersMap:  make(map[string][]byte),
		countersMap: make(map[string]*atomic.Int64),
		mux:         &sync.Mutex{},
		counter:     counter,
	}
}

// PreStart pre-starts the deadletter actor
func (d *deadLetters) PreStart(context.Context) error {
	d.mux.Lock()
	d.lettersMap = make(map[string][]byte)
	d.countersMap = make(map[string]*atomic.Int64)
	d.counter.Store(0)
	d.mux.Unlock()
	return nil
}

// Receive handles messages
func (d *deadLetters) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *goaktpb.PostStart:
		d.init(ctx)
	case *internalpb.EmitDeadletter:
		d.handle(msg.GetDeadletter())
	case *internalpb.GetDeadletters:
		d.letters()
	case *internalpb.GetDeadlettersCount:
		count := d.count(msg)
		ctx.Response(&internalpb.DeadlettersCount{
			TotalCount: count,
		})
	default:
		// simply ignore anyhing else
	}
}

// PostStop handles post procedures
func (d *deadLetters) PostStop(context.Context) error {
	d.logger.Infof("%s stopped successfully", d.pid.Name())
	d.mux.Lock()
	d.lettersMap = make(map[string][]byte)
	d.countersMap = make(map[string]*atomic.Int64)
	d.counter.Store(0)
	d.mux.Unlock()
	return nil
}

func (d *deadLetters) init(ctx *ReceiveContext) {
	d.eventsStream = ctx.Self().eventsStream
	d.logger = ctx.Logger()
	d.pid = ctx.Self()
	d.logger.Infof("%s started successfully", d.pid.Name())
}

func (d *deadLetters) handle(msg *goaktpb.Deadletter) {
	// increment the counter
	d.counter.Inc()
	// publish the deadletter message to the event stream
	d.eventsStream.Publish(eventsTopic, msg)

	// lettersMap the message for future query
	id := address.From(msg.GetReceiver()).String()
	bytea, _ := proto.Marshal(msg)
	d.lettersMap[id] = bytea
	if counter, ok := d.countersMap[id]; ok {
		counter.Inc()
		return
	}
	counter := atomic.NewInt64(1)
	d.countersMap[id] = counter
}

// letters pushes the actor state back to the stream
func (d *deadLetters) letters() {
	for id := range d.lettersMap {
		if value, ok := d.lettersMap[id]; ok {
			msg := new(goaktpb.Deadletter)
			_ = proto.Unmarshal(value, msg)
			d.eventsStream.Publish(eventsTopic, msg)
		}
	}
}

// count returns the deadletter count
func (d *deadLetters) count(msg *internalpb.GetDeadlettersCount) int64 {
	if msg.ActorId != nil {
		if counter, ok := d.countersMap[msg.GetActorId()]; ok {
			return counter.Load()
		}
		return 0
	}
	return d.counter.Load()
}
