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

package commands

import (
	"github.com/tochemey/goakt/v4/internal/address"
)

// ReplyToKind discriminates the kind of process awaiting an AsyncResponse.
type ReplyToKind uint8

const (
	// ReplyToActor routes the response to an actor address.
	ReplyToActor ReplyToKind = iota
	// ReplyToGrain routes the response to a grain identity.
	ReplyToGrain
)

// AsyncReplyTo identifies the requester awaiting an AsyncResponse.
//
// An actor target keeps its parsed address so that replying never re-parses a
// string. A grain target is carried in its string form: the grain registry is
// keyed by that exact string, so keeping it avoids rebuilding it on the reply
// path, and holding it as a string keeps this package free of any dependency
// on the actor package.
//
// A nil *AsyncReplyTo means the response completes a pending ask registered on
// the node that receives it rather than addressing a process.
type AsyncReplyTo struct {
	// Kind selects which of the fields below carries the target.
	Kind ReplyToKind
	// Actor is set when Kind is ReplyToActor.
	Actor *address.Address
	// Grain holds the grain identity string when Kind is ReplyToGrain.
	Grain string
}

// Valid reports whether the target carries the payload its kind requires.
func (a *AsyncReplyTo) Valid() bool {
	if a == nil {
		return false
	}

	switch a.Kind {
	case ReplyToActor:
		return a.Actor != nil
	case ReplyToGrain:
		return a.Grain != ""
	default:
		return false
	}
}

// AsyncRequest wraps a user message with correlation and reply metadata.
type AsyncRequest struct {
	// CorrelationID links requests to responses.
	CorrelationID string
	// ReplyTo identifies the requester awaiting the response.
	ReplyTo *AsyncReplyTo
	// Message is the original user payload.
	Message any
}

// AsyncResponse delivers a response or an error for an AsyncRequest.
type AsyncResponse struct {
	// CorrelationID matches the originating AsyncRequest.
	CorrelationID string
	// Message is the successful response payload.
	Message any
	// Error carries the failure reason as a string to keep the envelope stable
	// across versions and nodes.
	Error string
}
