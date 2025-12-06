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

// ContextPropagator injects and extracts metadata from remoting requests.
//
// It defines how context-scoped values (e.g., trace IDs, auth tokens, correlation IDs)
// are carried across RPC boundaries by reading/writing HTTP headers.
//
// Implementations should:
//   - Be side-effect free and stateless (safe for concurrent use).
//   - Prefer stable, vendor-neutral header keys when possible.
//   - Validate and sanitize inputs to avoid header injection or oversized headers.
//   - Avoid propagating sensitive values unless explicitly required and securely handled.
//
// Usage:
//
//	// Server-side: extract values from incoming headers into request context.
//	func handler(w http.ResponseWriter, r *nethttp.Request, p ContextPropagator) {
//	    ctx, err := p.Extract(r.Context(), r.Header)
//	    if err != nil {
//	        // decide policy: log, mask, or fail the request
//	    }
//	    // use ctx for request-scoped operations
//	}
//
//	// Client-side: inject values from context into outgoing request headers.
//	func call(ctx context.Context, req *nethttp.Request, p ContextPropagator) error {
//	    if err := p.Inject(ctx, req.Header); err != nil {
//	        return err
//	    }
//	    // perform the HTTP call
//	    return nil
//	}
//
// Common patterns:
//   - Correlation: propagate "X-Request-ID" or similar across services.
//   - Tracing: integrate with OpenTelemetry/B3/W3C Trace-Context by mapping context fields.
//   - Security: propagate JWT/bearer tokens via "Authorization" only when appropriate.
//
// Error handling recommendations:
//   - Inject should return an error only for unrecoverable conditions (e.g., invalid header write).
//   - Extract may return a non-nil ctx with a best-effort parse and an error describing issues.
//   - Callers should define clear policies for partial failures (log-and-continue vs. fail-fast).
type ContextPropagator interface {
	// Inject writes context values into headers for an outgoing request.
	// Implementations should not mutate ctx and must be safe for concurrent use.
	Inject(ctx context.Context, headers nethttp.Header) error

	// Extract reads headers from an incoming request and returns a new context
	// containing any propagated values. The returned context should derive from
	// the provided ctx to preserve cancellations and deadlines.
	Extract(ctx context.Context, headers nethttp.Header) (context.Context, error)
}
