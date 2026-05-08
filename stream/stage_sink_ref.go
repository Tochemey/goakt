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

package stream

import (
	"errors"
	"fmt"

	"github.com/tochemey/goakt/v4/actor"
)

// sinkRefInitialCredit is the wire-level demand granted on subscribe and
// refilled per ack from the local sink sub-pipeline. The value matches the
// default actor mailbox window so a typical sink keeps a full mailbox of
// wire-arrived elements in flight before a refill is required.
const sinkRefInitialCredit int64 = 256

// sinkRefAckThreshold is the consumed-count at which the head feedSourceActor
// emits a refill ack to this endpoint, mirroring the existing flowActor
// watermark refill pattern at quarter-window.
const sinkRefAckThreshold int64 = sinkRefInitialCredit / 4

// sinkRefEndpointActor is the consumer-side endpoint of a SinkRef. It owns
// a Sink[T] stage descriptor and, on receipt of streamSubscribeWire,
// materialises a sub-pipeline of [feedSourceActor] -> [user sink] whose
// head this actor pushes incoming user values into. Elements arrive as
// ordinary remote messages — the remoting layer handles their serialization.
// Wire-level credit is granted up front and refilled on each subFeedAck
// from the head.
//
// One subscription per endpoint: subsequent subscribes receive
// streamErrorWire and are otherwise ignored.
type sinkRefEndpointActor[T any] struct {
	sinkDesc   *stageDesc
	system     actor.ActorSystem
	subscriber *actor.PID
	streamID   string
	feedHead   *actor.PID
	feedKey    string
}

func newSinkRefEndpointActor[T any](sinkDesc *stageDesc) *sinkRefEndpointActor[T] {
	return &sinkRefEndpointActor[T]{sinkDesc: sinkDesc}
}

func (a *sinkRefEndpointActor[T]) PreStart(ctx *actor.Context) error {
	a.system = ctx.ActorSystem()
	return nil
}

// Receive handles streamSubscribeWire, streamElementWire, streamCompleteWire,
// streamErrorWire, and subFeedAck.
func (a *sinkRefEndpointActor[T]) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *streamSubscribeWire:
		if a.subscriber != nil {
			rctx.Tell(rctx.Sender(), &streamErrorWire{
				StreamID: msg.StreamID,
				Err:      "stream: sink ref already subscribed",
			})
			return
		}
		a.subscriber = rctx.Sender()
		a.streamID = msg.StreamID
		a.feedKey = a.streamID

		// Spawn [feedSourceActor] -> [user sink] sub-pipeline. The feed source's
		// splitter is this actor — it will receive subFeedAck for refill.
		feedDesc := makeFeedSourceDesc(rctx.Self(), a.feedKey, sinkRefAckThreshold)
		stages := []*stageDesc{feedDesc, a.sinkDesc}
		_, head, err := materializeWithHead(rctx.Context(), a.system, stages)
		if err != nil {
			rctx.Tell(a.subscriber, &streamErrorWire{
				StreamID: a.streamID,
				Err:      fmt.Sprintf("stream: sink ref materialize: %v", err),
			})
			rctx.Shutdown()
			return
		}
		a.feedHead = head

		// Grant initial wire credit so the producer can begin shipping.
		rctx.Tell(a.subscriber, &streamRequestWire{
			StreamID: a.streamID,
			N:        sinkRefInitialCredit,
		})

	case *streamCompleteWire:
		if msg.StreamID != a.streamID {
			return
		}
		if a.feedHead != nil {
			rctx.Tell(a.feedHead, &subFeedDone{})
		}
		rctx.Shutdown()

	case *streamErrorWire:
		if msg.StreamID != a.streamID {
			return
		}
		// Producer-side error — close the local sink and shut down. The user's
		// sink sees a normal completion; the failure is observable at the
		// producer's StreamHandle.
		if a.feedHead != nil {
			rctx.Tell(a.feedHead, &subFeedDone{})
		}
		rctx.Shutdown()

	case *subFeedAck:
		// Local sink consumed n elements — refill the wire credit window.
		if a.subscriber != nil {
			rctx.Tell(a.subscriber, &streamRequestWire{
				StreamID: a.streamID,
				N:        msg.n,
			})
		}

	default:
		// Anything that isn't a known control wire is a user element pushed
		// from the producer-side bridge. fromWire handles both T-direct
		// (primitive types deserialized by CBOR as values) and *T (struct
		// pointers); reject everything else so a stray remote message can't
		// silently corrupt the sink.
		if a.feedHead == nil {
			return
		}
		v, ok := fromWire[T](rctx.Message())
		if !ok {
			rctx.Unhandled()
			return
		}
		rctx.Tell(a.feedHead, &subPush{value: v})
	}
}

func (a *sinkRefEndpointActor[T]) PostStop(_ *actor.Context) error { return nil }

// remoteSinkBridgeActor is the producer-side sink stage that adapts a SinkRef
// back into a Sink[T] in the local graph. It resolves the endpoint by name
// (resolution is direct against the consumer node's remote server — no
// cluster registry on the critical path), sends streamSubscribeWire, ships
// incoming local elements as ordinary remote messages (the remoting layer
// serializes them), and translates wire credit into local upstream demand.
type remoteSinkBridgeActor[T any] struct {
	endpointName string
	endpointHost string
	endpointPort int
	system       actor.ActorSystem
	endpoint     *actor.PID
	upstream     *actor.PID
	subID        string
	streamID     string
	termErr      error
	config       StageConfig
}

func newRemoteSinkBridgeActor[T any](endpointName, endpointHost string, endpointPort int, config StageConfig) *remoteSinkBridgeActor[T] {
	return &remoteSinkBridgeActor[T]{
		endpointName: endpointName,
		endpointHost: endpointHost,
		endpointPort: endpointPort,
		system:       config.System,
		config:       config,
	}
}

func (a *remoteSinkBridgeActor[T]) PreStart(_ *actor.Context) error { return nil }

// TermErr exposes any terminal wire error so completionWrapper can surface it
// on StreamHandle.Err().
func (a *remoteSinkBridgeActor[T]) TermErr() error { return a.termErr }

// Receive handles stageWire, endpointResolved, streamRequestWire,
// streamElement, streamComplete, streamError, and streamErrorWire.
func (a *remoteSinkBridgeActor[T]) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *stageWire:
		a.upstream = msg.upstream
		a.subID = msg.subID

		endpoint, err := resolveEndpoint(rctx.Context(), a.system, rctx.Self(), a.endpointHost, a.endpointPort, a.endpointName)
		if err != nil {
			a.termErr = fmt.Errorf("stream: resolve sink ref %q: %w", a.endpointName, err)
			rctx.Shutdown()
			return
		}

		a.endpoint = endpoint
		a.streamID = newStageID()
		// Use Self().Tell so a remote-send failure surfaces here. rctx.Tell
		// silently records errors via rctx.Err which the bridge would never
		// observe — masking a transient remoting failure as a hung subscribe.
		if err := rctx.Self().Tell(rctx.Context(), a.endpoint, &streamSubscribeWire{StreamID: a.streamID}); err != nil {
			a.termErr = fmt.Errorf("stream: sink ref subscribe %q: %w", a.endpointName, err)
			rctx.Shutdown()
			return
		}

	case *streamRequestWire:
		// Wire credit from the consumer is forwarded as local demand upstream.
		if msg.StreamID != a.streamID || a.upstream == nil {
			return
		}

		rctx.Tell(a.upstream, &streamRequest{subID: a.subID, n: msg.N})

	case *streamElement:
		if a.endpoint == nil {
			return
		}
		// Send the element as a plain remote message; goakt's remoting
		// serializes it via the registry shared with the rest of the system.
		// wireForm normalises to a pointer so the registry's exact-type
		// lookup matches the registered *T entry.
		rctx.Tell(a.endpoint, wireForm(msg.value))

	case *streamComplete:
		if a.endpoint != nil {
			rctx.Tell(a.endpoint, &streamCompleteWire{StreamID: a.streamID})
		}
		rctx.Shutdown()

	case *streamError:
		a.termErr = msg.err
		if a.endpoint != nil {
			rctx.Tell(a.endpoint, &streamErrorWire{
				StreamID: a.streamID,
				Err:      msg.err.Error(),
			})
		}

		rctx.Shutdown()

	case *streamErrorWire:
		if msg.StreamID != a.streamID {
			return
		}
		a.termErr = errors.New(msg.Err)
		rctx.Shutdown()

	default:
		rctx.Unhandled()
	}
}

func (a *remoteSinkBridgeActor[T]) PostStop(_ *actor.Context) error { return nil }
