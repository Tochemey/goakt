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

// Package stream provides reactive, backpressure-aware stream processing built
// on top of the GoAkt actor model.
//
// # Overview
//
// Pipelines are assembled from three building blocks:
//
//   - Source[T]     — an origin that produces elements of type T
//   - Flow[In, Out] — a transformation that consumes In and produces Out
//   - Sink[T]       — a terminal stage that consumes elements of type T
//
// Nothing executes until a complete pipeline (Source → Flow* → Sink) is
// converted to a RunnableGraph and Run() is called on it.
//
// # Backpressure
//
// Demand signals flow upstream as streamRequest(n) messages. Downstream stages
// only receive elements when they have signaled capacity. Upstream stages never
// push more than the signaled demand, ensuring that fast producers cannot
// overwhelm slow consumers.
//
// # Actor Source Protocol
//
// User-defined actors that act as stream sources must handle PullRequest
// messages and reply with PullResponse[T]. An empty Elements slice signals
// end-of-stream.
//
//	type mySource struct{}
//	func (s *mySource) Receive(ctx *actor.ReceiveContext) {
//	    switch msg := ctx.Message().(type) {
//	    case *stream.PullRequest:
//	        ctx.Response(&stream.PullResponse[int]{Elements: []int{1, 2, 3}})
//	    }
//	}
package stream

// PullRequest is sent by the stream runtime to an actor source to request
// up to N elements. The actor must reply with a *PullResponse[T].
// An empty PullResponse.Elements slice signals end-of-stream.
type PullRequest struct {
	N int64
}

// PullResponse is the expected reply to a PullRequest.
// Elements contains the produced values. An empty slice signals end-of-stream.
type PullResponse[T any] struct {
	Elements []T
}
