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

import "errors"

// ErrorStrategy controls how a stage responds to element-level processing errors.
type ErrorStrategy int

const (
	// FailFast stops the entire stream on the first element-level error.
	// This is the default strategy.
	FailFast ErrorStrategy = iota
	// Resume logs the error, skips the offending element, and continues processing.
	Resume
	// Retry reprocesses the element up to a configured number of times before failing.
	Retry
	// Supervise delegates error handling to the stream's supervision strategy.
	Supervise
)

// Sentinel errors returned by the stream runtime.
var (
	// ErrStreamCanceled is returned when the stream was stopped before completion.
	ErrStreamCanceled = errors.New("stream: canceled")
	// ErrPullTimeout is returned when an actor source does not respond within the pull timeout.
	ErrPullTimeout = errors.New("stream: pull from actor source timed out")
	// ErrInvalidGraph is returned when a RunnableGraph has fewer than 2 stages (source + sink).
	ErrInvalidGraph = errors.New("stream: graph must have at least a source and a sink")
)
