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

// Wire protocol used by SourceRef and SinkRef to carry the demand-driven
// stream protocol across nodes. The types are unexported so callers cannot
// construct or inspect them; they reach the wire only through the endpoint
// and bridge actors. Field names are exported because the CBOR / proto
// serializers reflect on struct fields.
//
// Sender identity is implicit: the bridge sends streamSubscribeWire via
// rctx.Tell, and the endpoint captures rctx.Sender() to address subsequent
// outbound messages back through the bridge. GoAkt's remoting layer
// preserves Sender across node boundaries, so no path field appears here.
//
// StreamID disambiguates concurrent subscriptions on a single endpoint when
// the design is later extended; in this phase the endpoint accepts only one
// subscriber, but every wire message still carries the StreamID so that late
// or stale messages from a prior subscription are recognisable.

// streamSubscribeWire is sent by the consumer-side bridge to the endpoint to
// open a subscription. The endpoint records rctx.Sender() as the subscriber
// and materialises the underlying source / sink on first receipt.
type streamSubscribeWire struct {
	StreamID string
}

// streamRequestWire signals demand from the consumer to the producer. N is the
// number of additional elements the consumer is willing to accept.
type streamRequestWire struct {
	StreamID string
	N        int64
}

// User elements are sent directly via rctx.Tell(remotePID, value) — the
// remoting layer serializes them using the same registry as any other remote
// message. There is no streamElementWire envelope: it would double-encode
// (wrap the value in []byte payload, then wrap the envelope again over the
// wire) and force every endpoint to carry a redundant remote.Serializer.
// One subscription per endpoint means any message that isn't a control wire
// (subscribe/request/complete/error/cancel) is unambiguously a user element.

// streamCompleteWire indicates the producer has emitted its final element
// and the subscription is closing normally.
type streamCompleteWire struct {
	StreamID string
}

// streamErrorWire reports a producer-side failure. The subscription is
// closing and no further messages will arrive.
type streamErrorWire struct {
	StreamID string
	Err      string
}

// streamCancelWire is sent by the consumer to abort an active subscription
// before natural completion. The endpoint tears down its sub-pipeline.
type streamCancelWire struct {
	StreamID string
}
