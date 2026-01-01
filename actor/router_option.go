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

package actor

import "time"

// RouterOption is the interface that applies a configuration option.
type RouterOption interface {
	// Apply sets the Option value of a config.
	Apply(r *router)
}

// enforce compilation error
var _ RouterOption = RouterOptionFunc(nil)

// RouterOptionFunc implements the Option interface.
type RouterOptionFunc func(router *router)

func (f RouterOptionFunc) Apply(r *router) {
	f(r)
}

// WithRoutingStrategy sets the routing strategy to use by the router
// to forward messages to its routees. By default, FanOutRouting is used.
// ⚠️ Note: Fan-out, round-robin, and random strategies are fire-and-forget; they never wait for replies.
// When you need the router to observe replies consider Scatter-Gather First or Tail-Chopping,
// but remember that routers always operate asynchronously—the outcome is delivered back to the sender
// as a new message rather than through Ask.
func WithRoutingStrategy(strategy RoutingStrategy) RouterOption {
	return RouterOptionFunc(func(r *router) {
		r.routingStrategy = strategy
	})
}

// AsTailChopping configures the router to use the Tail-Chopping routing strategy.
//
// Tail-Chopping prioritizes predictable latency over full concurrency by probing one
// routee at a time (in random order) and only moving on when the current attempt does
// not respond fast enough. This avoids thundering herds while still racing to find
// any healthy worker within a bounded deadline. Replies are forwarded asynchronously
// back to the Broadcast sender; the router itself never blocks waiting for results.
//
// Sequence (conceptual):
//  1. Shuffle live routees to avoid bias.
//  2. Send the message to the first routee.
//  3. If no response arrives before interval, send to the next routee.
//  4. Repeat until:
//     - A routee replies (first success wins and the rest are cancelled).
//     - All routees have been attempted.
//     - The cumulative time exceeds within (global deadline).
//  5. If the deadline expires without success, the sender receives a StatusFailure.
//
// Parameters:
//
//	within   - Total time budget allowed for obtaining any successful response.
//	           Must be > 0. Starts counting when the first routee receives the message.
//	interval - Delay between successive attempts. Must be > 0 and < within for multiple routees
//	           to be attempted. If interval >= within only the first routee is tried.
//
// Recommended tuning guidelines:
//   - interval ≈ p95 of expected fast response time so that healthy actors reply before the next probe fires.
//   - within  ≈ (number_of_routees * interval) or a business SLO bound to cap worst-case latency.
//   - Ensure interval << within to allow multiple attempts; otherwise only the first probe runs.
//
// Example:
//
//	// Create a tail-chopping router that will try for up to 2s,
//	// issuing a new attempt every 200ms until a reply is obtained.
//	r := newRouter(5, &MyWorker{}, logger, AsTailChopping(2*time.Second, 200*time.Millisecond))
//
// Use Scatter-Gather when you need the absolute fastest responder; use Tail-Chopping
// when you prefer controlled sequential probing with upper-bounded fan-out.
//
// Returns a RouterOption that sets internal state used by the router.
func AsTailChopping(within, interval time.Duration) RouterOption {
	return RouterOptionFunc(func(r *router) {
		r.kind = tailChoppingRouter
		r.within = within
		r.interval = interval
	})
}

// AsScatterGatherFirst configures the router to use the Scatter‑Gather First routing strategy.
//
// Pattern summary:
//   - Sends the same request to all currently live routees concurrently.
//   - Returns the first successful reply within the time budget (within).
//   - Late replies are ignored once the first result is returned or the deadline elapses.
//   - Replies arrive asynchronously to the Broadcast sender; the router never blocks.
//
// Behavioral contract (first-response-wins):
//   - Every routee may work on the request (duplicate work is expected).
//   - The first successful reply is forwarded to the original sender; remaining replies are dropped.
//   - If no routee replies before within, the sender receives a StatusFailure.
//   - If no live routees exist at send time, fail fast to avoid wasting time.
//
// Parameters:
//
//	within: Total time budget for obtaining any successful response.
//	        Must be > 0. Choose based on SLOs or empirical latency.
//
// Tuning guidelines:
//   - within ≈ p95/p99 of expected fast response or a business SLO so healthy routees usually win.
//   - For large pools, consider sampling a subset (e.g., nearest N) or capping concurrency to avoid storms.
//   - Add a small safety margin for serialization and scheduling overheads before declaring a timeout.
//
// Example:
//
//	// Create a scatter‑gather router that waits up to 500ms for any reply.
//	r := newRouter(10, &MyWorker{}, logger, AsScatterGatherFirst(500*time.Millisecond))
//
//	payload, _ := anypb.New(&MyRequest{})
//	ctx.Tell(rPID, &goaktpb.Broadcast{Message: payload})
//
//	// Later, handle the first successful reply (or StatusFailure) asynchronously
//	// in the sender's Receive.
//
// Errors and edge cases:
//   - within <= 0 should be validated by the caller before passing.
//   - Expect a timeout error after within elapses without any reply.
//   - Decide what constitutes “successful reply” (e.g., non‑error response type).
//
// Returns a RouterOption that sets internal state used by the router.
func AsScatterGatherFirst(within time.Duration) RouterOption {
	return RouterOptionFunc(func(r *router) {
		r.kind = scatterGatherFirstRouter
		r.within = within
	})
}

// WithResumeRouteeOnFailure sets the router's supervision directive for its routees to Resume.
//
// Behavior:
//   - The failing routee keeps its current in-memory state and continues processing the next messages.
//   - The failing message is not retried by the router.
//   - Best for transient, non-corrupting failures (e.g., timeouts, temporary downstream issues).
//
// Notes:
//   - Mutually exclusive with WithRestartRouteeOnFailure and WithStopRouteeOnFailure; the last applied wins.
//   - If no directive option is provided, the default is Restart.
func WithResumeRouteeOnFailure() RouterOption {
	return RouterOptionFunc(func(r *router) {
		r.supervisorDirective = resumeRoutee
	})
}

// WithRestartRouteeOnFailure sets the router's supervision directive for its routees to Restart.
//
// Behavior:
//   - The failing routee is restarted (stopped and started anew), resetting its internal state.
//   - The failing message is not retried by the router.
//   - Subsequent messages may be processed after the restart.
//   - Use when local state may be corrupted or requires re-initialization.
//
// Notes:
//   - Mutually exclusive with WithResumeRouteeOnFailure and WithStopRouteeOnFailure; the last applied wins.
func WithRestartRouteeOnFailure(maxRetries uint32, timeout time.Duration) RouterOption {
	return RouterOptionFunc(func(r *router) {
		r.supervisorDirective = restartRoutee
		r.restartRouteeAttempts = maxRetries
		r.restartRouteeWithin = timeout
	})
}

// WithStopRouteeOnFailure sets the router's supervision directive for its routees to Stop.
//
// Behavior:
//   - The failing routee is terminated and removed from the routing pool.
//   - The failing message is not retried by the router.
//   - Use when the routee cannot continue safely or should be quarantined.
//
// Notes:
//   - Mutually exclusive with WithResumeRouteeOnFailure and WithRestartRouteeOnFailure; the last applied wins.
//   - The router will stop sending messages to the stopped routee; it is not automatically replaced by this option.
//   - This is the default directive if none is specified.
func WithStopRouteeOnFailure() RouterOption {
	return RouterOptionFunc(func(r *router) {
		r.supervisorDirective = stopRoutee
	})
}
