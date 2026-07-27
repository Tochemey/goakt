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
	"context"
	"fmt"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/commands"
)

// routeAsyncReply delivers a response to whoever is awaiting the given
// correlation ID.
//
// The reply target carried on the request decides the route, which is why it
// travels with the request rather than being inferred at reply time: a nil
// target completes a caller blocked in an ask on this node, and an actor target
// is told the response, locally or across the network. The target arrives
// already typed, so routing never parses an address.
//
// from is the replying actor, used as the sender of the response so that
// actor-to-actor replies keep the identity they have today.
func (x *actorSystem) routeAsyncReply(ctx context.Context, from *PID, replyTo *commands.AsyncReplyTo, correlationID string, message any, failure error) error {
	if correlationID == "" {
		return gerrors.ErrInvalidMessage
	}

	// A response carrying neither payload nor failure is the wire form of a
	// successful reply without a payload (NoErr): the requester observes a nil
	// result and a nil error.
	response := &commands.AsyncResponse{CorrelationID: correlationID}
	if failure != nil {
		response.Error = failure.Error()
	} else {
		response.Message = message
	}

	// A nil target means the awaiting caller is blocked in an ask on this node.
	// The table is always local: a caller registers its slot on the node that
	// runs the callee, including when the ask arrived over the network.
	if replyTo == nil {
		if !x.pendingAsks.Complete(response) {
			x.logger.Debugf("async response dropped: no caller awaits correlation id=%s", correlationID)
		}
		return nil
	}

	if !replyTo.Valid() {
		return gerrors.ErrInvalidMessage
	}

	switch replyTo.Kind {
	case commands.ReplyToActor:
		return x.tellAsyncResponse(ctx, from, replyTo.Actor, response)
	case commands.ReplyToGrain:
		identity, err := toIdentity(replyTo.Grain)
		if err != nil {
			return err
		}
		return x.deliverAsyncEnvelope(ctx, identity, response)
	default:
		return gerrors.NewErrInvalidMessage(fmt.Errorf("async reply: unroutable target kind %d", replyTo.Kind))
	}
}

// tellAsyncResponse hands a response to the actor that issued the request.
//
// An off-node requester is answered at the exact address it recorded on the
// request, not by resolving its name through the cluster registry. The
// correlation ID being answered lives in that one process's memory, so a
// different incarnation of the same name would only discard the response, and
// routing by address keeps replies working wherever remoting works rather than
// only where the registry can resolve.
func (x *actorSystem) tellAsyncResponse(ctx context.Context, from *PID, to *address.Address, response *commands.AsyncResponse) error {
	// Grain repliers and deferred handles have no PID; the system's NoSender
	// is the honest sender identity for their responses.
	if from == nil {
		from = x.NoSender()
	}

	pid, err := x.pidOf(ctx, to)
	if err != nil {
		return err
	}

	// Tell routes to the network itself when the target is a remote handle.
	return from.Tell(ctx, pid, response)
}
