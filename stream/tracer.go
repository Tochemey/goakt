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

// Tracer is an optional hook for distributed tracing integration.
// Attach a tracer to a flow or sink via WithTracer(t).
type Tracer interface {
	// OnElement is called for every element passing through a stage.
	// stageName identifies the stage; seqNo is the element sequence number;
	// latencyNs is the processing latency in nanoseconds.
	OnElement(stageName string, seqNo uint64, latencyNs int64)
	// OnDemand is called when a stage sends a demand signal upstream.
	OnDemand(stageName string, n int64)
	// OnError is called when a stage encounters an element-level error.
	OnError(stageName string, err error)
	// OnComplete is called when a stage receives a completion signal.
	OnComplete(stageName string)
}

// MetricsReporter is an optional hook for external metrics systems.
// Implementations can forward StreamMetrics to Prometheus, OpenTelemetry, etc.
type MetricsReporter interface {
	// Report is called periodically with the current metrics snapshot.
	// id is the stream's unique identifier.
	Report(id string, metrics StreamMetrics)
}
