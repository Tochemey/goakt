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

// LinearGraph[T] is a fluent builder for linear (single-source, single-sink)
// stream pipelines. It wraps an in-progress Source[T] and provides a chainable
// API that makes the pipeline-assembly intent explicit in the type system.
//
// Use From to start a LinearGraph, chain Via calls for type-preserving
// transformations, then call To to obtain a RunnableGraph:
//
//	handle, err := stream.From(stream.Of(1, 2, 3)).
//	    Via(stream.Map(double)).
//	    To(stream.Collect[int]()).
//	    Run(ctx, sys)
//
// For type-changing transformations, call Source() to obtain the underlying
// Source[T] and apply the package-level Via free function:
//
//	intSrc  := stream.From(stream.Of("a", "b")).Via(stream.Filter(...)).Source()
//	stringSrc := stream.Via(intSrc, stream.Map(parse))  // Source[int]
type LinearGraph[T any] struct {
	src Source[T]
}

// Via applies a type-preserving flow and returns a new *LinearGraph[T],
// keeping the fluent chain alive.
func (g *LinearGraph[T]) Via(flow Flow[T, T]) *LinearGraph[T] {
	return &LinearGraph[T]{src: Via(g.src, flow)}
}

// To attaches a Sink[T] and returns a RunnableGraph ready for execution.
func (g *LinearGraph[T]) To(sink Sink[T]) RunnableGraph {
	return g.src.To(sink)
}

// Source returns the underlying Source[T] accumulated so far.
// Use this to apply type-changing flows via the package-level Via free function
// when the transformation changes the element type.
func (g *LinearGraph[T]) Source() Source[T] {
	return g.src
}

// ViaLinear pipes a LinearGraph[In] through a type-changing Flow[In, Out],
// returning a new LinearGraph[Out]. This free function is the LinearGraph
// equivalent of the package-level Via function for Source.
func ViaLinear[In, Out any](g *LinearGraph[In], flow Flow[In, Out]) *LinearGraph[Out] {
	return &LinearGraph[Out]{src: Via(g.src, flow)}
}
