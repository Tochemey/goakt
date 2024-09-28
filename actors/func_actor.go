/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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

	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/goaktpb"
)

// ReceiveFunc is a message handling placeholder
type ReceiveFunc = func(ctx context.Context, message proto.Message) error

// PreStartFunc defines the PreStartFunc hook for an actor creation
type PreStartFunc = func(ctx context.Context) error

// PostStopFunc defines the PostStopFunc hook for an actor creation
type PostStopFunc = func(ctx context.Context) error

// FuncOption is the interface that applies a SpawnHook option.
type FuncOption interface {
	// Apply sets the Option value of a config.
	Apply(actor *funcConfig)
}

var _ FuncOption = funcOption(nil)

// funcOption implements the FuncOption interface.
type funcOption func(config *funcConfig)

// Apply implementation
func (f funcOption) Apply(c *funcConfig) {
	f(c)
}

type funcConfig struct {
	spawnConfig
	preStart PreStartFunc
	postStop PostStopFunc
}

func newFuncConfig(opts ...FuncOption) *funcConfig {
	config := &funcConfig{}
	for _, opt := range opts {
		opt.Apply(config)
	}
	return config
}

// WithPreStart defines the PreStartFunc hook
func WithPreStart(fn PreStartFunc) FuncOption {
	return funcOption(func(actor *funcConfig) {
		actor.preStart = fn
	})
}

// WithPostStop defines the PostStopFunc hook
func WithPostStop(fn PostStopFunc) FuncOption {
	return funcOption(func(actor *funcConfig) {
		actor.postStop = fn
	})
}

// WithFuncMailbox sets the mailbox to use when starting the func-based actor
func WithFuncMailbox(mailbox Mailbox) FuncOption {
	return funcOption(func(actor *funcConfig) {
		actor.mailbox = mailbox
	})
}

// funcActor is an actor that only handles messages
type funcActor struct {
	pid         *PID
	id          string
	receiveFunc ReceiveFunc
	config      *funcConfig
}

// newFuncActor creates an instance of funcActor
func newFuncActor(id string, receiveFunc ReceiveFunc, config *funcConfig) *funcActor {
	// create the actor instance
	actor := &funcActor{
		receiveFunc: receiveFunc,
		id:          id,
		config:      config,
	}
	return actor
}

// enforce compilation error
var _ Actor = (*funcActor)(nil)

// PreStart pre-starts the actor.
func (x *funcActor) PreStart(ctx context.Context) error {
	// check whether the pre-start hook is set and call it
	preStart := x.config.preStart
	if preStart != nil {
		return preStart(ctx)
	}
	return nil
}

// Receive processes any message dropped into the actor mailbox.
func (x *funcActor) Receive(ctx *ReceiveContext) {
	switch m := ctx.Message().(type) {
	case *goaktpb.PostStart:
		x.pid = ctx.Self()
	default:
		// handle the message and return the error
		if err := x.receiveFunc(ctx.Context(), m); err != nil {
			ctx.Err(err)
		}
	}
}

// PostStop is executed when the actor is shutting down.
func (x *funcActor) PostStop(ctx context.Context) error {
	// check whether the post-stop hook is set and call it
	postStop := x.config.postStop
	if postStop != nil {
		return postStop(ctx)
	}
	return nil
}
