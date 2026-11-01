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
	"hash/maphash"
	"sync"
)

// spawnActivationShards is the number of shards a spawnActivationGroup
// spreads its keys over. Every spawn in the process, top-level or child, goes
// through the group twice, so one mutex would be the lock every core waits on
// when many actors are spawned at once; the tree index is sharded the same
// way, and to the same count.
const spawnActivationShards = 64

// spawnActivationSeed is the seed spawnActivationGroup hashes keys with.
var spawnActivationSeed = maphash.MakeSeed()

// spawnActivationGroup coalesces concurrent spawns of one actor identity. The
// first caller for a key runs the spawn on its own goroutine, so a spawn that
// nobody races costs a map entry and starts no goroutine; every later caller
// for the same key, until that spawn returns, waits for its result or for its
// own context, whichever comes first. It is the part of singleflight that
// runSpawnActivation needs, without the goroutine and the channel that DoChan
// adds to every call.
type spawnActivationGroup struct {
	// shards holds the calls in flight, each key in the shard its hash
	// selects (see shard).
	shards [spawnActivationShards]spawnActivationShard
}

// spawnActivationShard holds the calls in flight for the keys that hash to
// it.
type spawnActivationShard struct {
	// mu guards calls.
	mu sync.Mutex
	// calls holds the spawn in flight for each key, nil until the first run.
	calls map[string]*spawnActivationCall
}

// spawnActivationCall is one spawn in flight. pid and err are set by the
// caller running it before done is closed, and read by the others only after
// done is closed.
type spawnActivationCall struct {
	// done is closed once pid and err are set. It is created by the first
	// caller that joins the call, under the shard's mutex, so a spawn nobody
	// races allocates no channel; nil when no caller joined.
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
	s := x.shard(key)
	s.mu.Lock()
	if s.calls == nil {
		s.calls = make(map[string]*spawnActivationCall)
	}

	if call, ok := s.calls[key]; ok {
		call.waiters++
		if call.done == nil {
			call.done = make(chan struct{})
		}

		done := call.done
		s.mu.Unlock()

		select {
		case <-done:
			return call.pid, true, call.err
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}

	call := &spawnActivationCall{}
	s.calls[key] = call
	s.mu.Unlock()

	completed := false
	defer func() {
		if !completed {
			call.err = fmt.Errorf("spawn activation of %s panicked", key)
		}

		s.mu.Lock()
		delete(s.calls, key)
		shared = call.waiters > 0
		done := call.done
		s.mu.Unlock()

		if done != nil {
			close(done)
		}
	}()

	call.pid, call.err = fn()
	completed = true
	return call.pid, shared, call.err
}

// shard returns the shard that holds key.
func (x *spawnActivationGroup) shard(key string) *spawnActivationShard {
	return &x.shards[maphash.String(spawnActivationSeed, key)%spawnActivationShards]
}
