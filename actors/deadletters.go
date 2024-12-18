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

	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/address"
	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/internal/eventstream"
	"github.com/tochemey/goakt/v2/log"
)

// deadletters is a synthetic actor that houses all deadletter
// in GoAkt deadletter are messages that have not been handled
type deadLetters struct {
	eventsStream *eventstream.EventsStream
	pid          *PID
	cache        *shardedMap
	logger       log.Logger

	// necessary for metrics
	count *atomic.Int32
}

// enforce the implementation of the Actor interface
var _ Actor = (*deadLetters)(nil)

// newDeadLetters creates an instance of deadletters
func newDeadLetters() *deadLetters {
	return &deadLetters{
		cache: newShardedMap(),
		count: atomic.NewInt32(0),
	}
}

// PreStart pre-starts the deadletter actor
func (d *deadLetters) PreStart(context.Context) error {
	// clear cache on restart
	d.cache.Reset()
	d.count.Store(0)
	return nil
}

// Receive handles messages
func (d *deadLetters) Receive(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *goaktpb.PostStart:
		d.init(ctx)
	case *goaktpb.Deadletter:
		d.handleDeadletter(msg)
	default:
		// simply ignore anyhing else
	}
}

// PostStop handles post procedures
func (d *deadLetters) PostStop(context.Context) error {
	d.logger.Infof("%s stopped successfully", d.pid.Name())
	// clear cache before on stop
	d.cache.Reset()
	d.count.Store(0)
	return nil
}

func (d *deadLetters) init(ctx *ReceiveContext) {
	d.eventsStream = ctx.Self().eventsStream
	d.logger = ctx.Logger()
	d.pid = ctx.Self()
	d.logger.Infof("%s started successfully", d.pid.Name())
}

func (d *deadLetters) handleDeadletter(msg *goaktpb.Deadletter) {
	// increment the count
	d.count.Inc()
	// publish the deadletter message to the event stream
	d.eventsStream.Publish(eventsTopic, msg)
	// cache the message for future query
	// TODO: add a query mechanism
	actorID := address.From(msg.GetReceiver()).String()
	bytea, _ := proto.Marshal(msg)
	d.cache.Store(actorID, bytea)
}
