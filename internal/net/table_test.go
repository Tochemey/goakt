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

package net

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeParseTablePayloadRoundTrip(t *testing.T) {
	payload, err := encodeTablePayload(TableKindActorPath, 7, "goakt://sys@127.0.0.1:8080/user/a")
	require.NoError(t, err)

	kind, id, literal, err := parseTablePayload(payload)
	require.NoError(t, err)
	assert.Equal(t, TableKindActorPath, kind)
	assert.Equal(t, uint64(7), id)
	assert.Equal(t, "goakt://sys@127.0.0.1:8080/user/a", literal)
}

func TestEncodeTablePayloadRejectsOversizedLiteral(t *testing.T) {
	_, err := encodeTablePayload(TableKindActorPath, 7, strings.Repeat("a", maxTableLiteralLen+1))
	require.Error(t, err)
}

func TestParseTablePayloadRejects(t *testing.T) {
	_, _, _, err := parseTablePayload(nil)
	require.Error(t, err)

	_, _, _, err = parseTablePayload([]byte{TableKindTypeName, 0x00, 0x01, 'x'})
	require.Error(t, err)

	_, _, _, err = parseTablePayload([]byte{TableKindTypeName, 0x01, 0x00})
	require.Error(t, err)

	_, _, _, err = parseTablePayload([]byte{TableKindTypeName, 0x01, 0x02, 'a'})
	require.Error(t, err)
}

func TestSenderTableRegisterAndOverflow(t *testing.T) {
	table := newSenderTable(2)
	var emitted []uint64
	emit := func(id uint64) error {
		emitted = append(emitted, id)
		return nil
	}

	id, err := table.register("a", emit)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), id)

	id, err = table.register("a", emit)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), id)
	assert.Equal(t, []uint64{1}, emitted)

	id, err = table.register("b", emit)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), id)

	id, err = table.register("c", emit)
	require.NoError(t, err)
	assert.Equal(t, uint64(0), id)
	assert.Equal(t, []uint64{1, 2}, emitted)

	id, err = table.register("", emit)
	require.NoError(t, err)
	assert.Zero(t, id)
}

func TestSenderTableEmitFailureRollsBack(t *testing.T) {
	table := newSenderTable(4)
	boom := errors.New("admit failed")

	id, err := table.register("a", func(uint64) error { return boom })
	require.ErrorIs(t, err, boom)
	assert.Zero(t, id)

	var got uint64
	id, err = table.register("a", func(id uint64) error {
		got = id
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, uint64(2), id)
	assert.Equal(t, uint64(2), got)
}

// TestSenderTableEmitBackpressureFallsBackInline pins the transient-congestion
// contract: a TABLE emit refused with ErrDuplexBackpressure must not fail the
// message being encoded. register rolls the mapping back and returns (0, nil)
// so the caller encodes an inline ref, and the next first-use retries the
// registration with a fresh ID.
func TestSenderTableEmitBackpressureFallsBackInline(t *testing.T) {
	table := newSenderTable(4)

	id, err := table.register("a", func(uint64) error { return ErrDuplexBackpressure })
	require.NoError(t, err)
	assert.Zero(t, id)

	var got uint64
	id, err = table.register("a", func(id uint64) error {
		got = id
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, uint64(2), id)
	assert.Equal(t, uint64(2), got)
}

func TestSenderTableConcurrentFirstUseSingleEmit(t *testing.T) {
	table := newSenderTable(8)
	var emits atomic.Int32
	var wg sync.WaitGroup

	for range 32 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			id, err := table.register("shared", func(uint64) error {
				emits.Add(1)
				return nil
			})
			require.NoError(t, err)
			assert.Equal(t, uint64(1), id)
		}()
	}

	wg.Wait()
	assert.Equal(t, int32(1), emits.Load())
}

func TestReceiverTableInstallRules(t *testing.T) {
	table := newReceiverTable(2)

	out := table.install(1, "a")
	require.Empty(t, out.HardError)

	out = table.install(1, "a")
	assert.True(t, out.SoftIgnore)

	out = table.install(1, "b")
	require.Equal(t, "table id 1 literal conflict", out.HardError)

	out = table.install(2, "b")
	require.Empty(t, out.HardError)

	out = table.install(3, "c")
	require.Equal(t, "table capacity exceeded", out.HardError)

	out = table.install(0, "x")
	require.NotEmpty(t, out.HardError)

	out = table.install(4, "")
	require.NotEmpty(t, out.HardError)
}

func TestReceiverTableResolveRefLazy(t *testing.T) {
	table := newReceiverTable(4)
	require.Empty(t, table.install(1, "path").HardError)

	var calls int
	literal, handle, ok := table.resolveRef(1, func(lit string) any {
		calls++
		return lit + "-pid"
	})
	require.True(t, ok)
	assert.Equal(t, "path", literal)
	assert.Equal(t, "path-pid", handle)
	assert.Equal(t, 1, calls)

	literal, handle, ok = table.resolveRef(1, func(string) any {
		calls++
		return "other"
	})
	require.True(t, ok)
	assert.Equal(t, "path", literal)
	assert.Equal(t, "path-pid", handle)
	assert.Equal(t, 1, calls)

	_, _, ok = table.resolveRef(9, nil)
	assert.False(t, ok)
}

func TestReceiverTableResolveRefOutsideLock(t *testing.T) {
	table := newReceiverTable(4)
	require.Empty(t, table.install(1, "path").HardError)

	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32

	go func() {
		_, _, _ = table.resolveRef(1, func(string) any {
			calls.Add(1)
			close(started)
			<-release
			return "first"
		})
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("resolve did not start")
	}

	// A concurrent lookup must complete while resolve is blocked outside the mutex.
	done := make(chan struct{})
	go func() {
		defer close(done)

		lit, handle, ok := table.resolveRef(1, nil)
		require.True(t, ok)
		assert.Equal(t, "path", lit)
		assert.Nil(t, handle)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("lookup blocked behind resolve")
	}

	close(release)
	require.Eventually(t, func() bool {
		_, handle, ok := table.resolveRef(1, nil)
		return ok && handle == "first"
	}, 2*time.Second, 10*time.Millisecond)
	assert.Equal(t, int32(1), calls.Load())
}
