/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package remote

import (
	"context"
	nethttp "net/http"
)

// ContextPropagator defines how Go context values travel across remoting and cluster
// boundaries by injecting them into outbound HTTP headers and extracting them on the
// receiving side (trace IDs, auth tokens, correlation IDs, and similar metadata).
//
// Implementations should be stateless, safe for concurrent use, favor stable header keys,
// and avoid leaking sensitive data unless explicitly required. Validate inputs to guard
// against header injection or oversized header sets. Go-Akt relies on a ContextPropagator
// so that context-derived metadata survives hops to remote actors or cluster peers and
// can be read safely during messages handling via ReceiveContext.Context() or GrainContext.Context().
//
// Error handling:
//   - Inject should fail only when headers cannot be written.
//   - Extract should return a derived context and report parse issues via the error,
//     letting callers choose log-and-continue vs fail-fast policies.
type ContextPropagator interface {
	// Inject writes context values into headers for an outgoing request.
	// Implementations should not mutate ctx and must be safe for concurrent use.
	Inject(ctx context.Context, headers nethttp.Header) error

	// Extract reads headers from an incoming request and returns a new context
	// containing any propagated values. The returned context should derive from
	// the provided ctx to preserve cancellations and deadlines.
	Extract(ctx context.Context, headers nethttp.Header) (context.Context, error)
}
