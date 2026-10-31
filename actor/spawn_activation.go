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

import (
	"context"
	"fmt"
	"sync"
)

// spawnActivationGroup coalesces concurrent spawns of one actor identity. The
// first caller for a key runs the spawn on its own goroutine, so a spawn that
// nobody races costs a map entry and starts no goroutine; every later caller
// for the same key, until that spawn returns, waits for its result or for its
// own context, whichever comes first. It is the part of singleflight that
// runSpawnActivation needs, without the goroutine and the channel that DoChan
// adds to every call.
type spawnActivationGroup struct {
	// mu guards calls.
	mu sync.Mutex
	// calls holds the spawn in flight for each key, nil until the first run.
	calls map[string]*spawnActivationCall
}

// spawnActivationCall is one spawn in flight. pid and err are set by the
// caller running it before done is closed, and read by the others only after
// done is closed.
type spawnActivationCall struct {
	// done is closed once pid and err are set.
	done chan struct{}
	// pid is the spawn's result.
	pid *PID
	// err is the spawn's error.
	err error
	// waiters counts the callers that joined the call after it started. It is
	// read under the group's mutex, where it is written.
	waiters int
}

// run runs fn for key, or joins the fn already running for key. It returns
// the result and whether the result was shared between callers, which is what
// runSpawnActivation needs to tell a context error it inherited from one of
// its own. A caller that joins returns ctx.Err() when ctx is done before the
// running fn returns; that fn keeps running for the others.
//
// A panic in fn completes the call with an error, so that no waiter blocks on
// a call that never finishes, and then goes on through the caller as before.
func (x *spawnActivationGroup) run(ctx context.Context, key string, fn func() (*PID, error)) (pid *PID, shared bool, err error) {
	x.mu.Lock()
	if x.calls == nil {
		x.calls = make(map[string]*spawnActivationCall)
	}

	if call, ok := x.calls[key]; ok {
		call.waiters++
		x.mu.Unlock()

		select {
		case <-call.done:
			return call.pid, true, call.err
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}

	call := &spawnActivationCall{done: make(chan struct{})}
	x.calls[key] = call
	x.mu.Unlock()

	completed := false
	defer func() {
		if !completed {
			call.err = fmt.Errorf("spawn activation of %s panicked", key)
		}

		x.mu.Lock()
		delete(x.calls, key)
		shared = call.waiters > 0
		x.mu.Unlock()
		close(call.done)
	}()

	call.pid, call.err = fn()
	completed = true
	return call.pid, shared, call.err
}
