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
	"time"

	"github.com/tochemey/goakt/v3/supervisor"
)

// Deadletter defines the deadletter event.
// It is the Go equivalent of the goaktpb.Deadletter proto message,
// used internally to avoid a circular dependency on the actor package.
type Deadletter struct {
	// Specifies the sender's address
	Sender string
	// Specifies the actor address
	Receiver string
	// Specifies the message to send to the actor.
	// Any message is allowed to be sent.
	Message any
	// Specifies the message send time
	SendTime time.Time
	// Specifies the reason why the deadletter
	Reason string
}

// PublishDeadletters is used to trigger the publishing of all accumulated deadletters.
// This message does not carry any payload.
type PublishDeadletters struct{}

// SendDeadletter is used to emit a single deadletter event.
// The deadletter field contains the details of the failed message.
type SendDeadletter struct {
	// Specifies the deadletter to emit.
	Deadletter Deadletter
}

// DeadlettersCountRequest requests the count of deadletters.
// Optionally, the ActorID can be provided to filter deadletters for a specific actor.
type DeadlettersCountRequest struct {
	// Optional: actor ID to filter deadletters for a specific actor.
	ActorID *string
}

// DeadlettersCountResponse returns the total number of deadletters.
// The TotalCount field contains the count of deadletters found.
type DeadlettersCountResponse struct {
	// Specifies the total count of deadletters.
	TotalCount int64
}

// AsyncRequest wraps a user message with correlation and reply metadata.
type AsyncRequest struct {
	// CorrelationID links requests to responses.
	CorrelationID string
	// ReplyTo is the address of the actor that should receive the response.
	ReplyTo string
	// Message is the original user payload.
	Message any
}

// AsyncResponse delivers a response or error for an AsyncRequest.
type AsyncResponse struct {
	// CorrelationID matches the original AsyncRequest.
	CorrelationID string
	// Message is the successful response payload.
	Message any
	// Error is set when the request fails or times out.
	Error string
}

// HealthCheckRequest is sent internally to an actor (or actor system component) to verify that:
//  1. The mailbox is responsive.
//  2. The actor has completed its initialization.
//  3. The actor can accept user messages.
//
// Empty by design: presence of a valid response implies readiness.
type HealthCheckRequest struct{}

// HealthCheckResponse is returned when an actor (or component) is ready/healthy.
// If a timeout or failure occurs upstream, the absence of this response is
// interpreted as not-ready/unhealthy.
// Extend ONLY if there is a strong need to surface diagnostic details.
type HealthCheckResponse struct{}

type Panicking struct {
	// Specifies the actor id
	ActorID string
	// Specifies the error message
	Err error
	// Specifies the supervisor
	Supervisor *supervisor.Supervisor
	// Specifies the strategy
	Strategy supervisor.Strategy
	// Specifies the directive
	Directive supervisor.Directive
	// Specifies the message that triggered the failure
	Message any
	// Specifies when the error occurred
	Timestamp time.Time
}
