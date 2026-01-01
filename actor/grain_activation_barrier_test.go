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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	gerrors "github.com/tochemey/goakt/v3/errors"
)

func TestNewGrainActivationBarrier(t *testing.T) {
	barrier := newGrainActivationBarrier(0, 5*time.Second)

	assert.True(t, barrier.enabled)
	assert.EqualValues(t, 1, barrier.minPeers)
	assert.Equal(t, 5*time.Second, barrier.timeout)
	select {
	case <-barrier.ready:
		t.Fatal("expected ready channel to remain open")
	default:
	}
}

func TestGrainActivationBarrierOpen(t *testing.T) {
	t.Run("nil barrier does nothing", func(t *testing.T) {
		var barrier *grainActivationBarrier
		assert.NotPanics(t, func() {
			barrier.open()
		})
	})

	t.Run("disabled barrier does not close", func(t *testing.T) {
		barrier := newGrainActivationBarrier(1, 0)
		barrier.enabled = false

		barrier.open()

		select {
		case <-barrier.ready:
			t.Fatal("expected ready channel to remain open")
		default:
		}
	})

	t.Run("enabled barrier closes once", func(t *testing.T) {
		barrier := newGrainActivationBarrier(1, 0)

		barrier.open()
		barrier.open()

		select {
		case <-barrier.ready:
		default:
			t.Fatal("expected ready channel to be closed")
		}
	})
}

func TestGrainActivationBarrierWait(t *testing.T) {
	t.Run("nil barrier returns nil", func(t *testing.T) {
		var barrier *grainActivationBarrier
		assert.NoError(t, barrier.wait(context.Background()))
	})

	t.Run("disabled barrier returns nil", func(t *testing.T) {
		barrier := newGrainActivationBarrier(1, 0)
		barrier.enabled = false

		assert.NoError(t, barrier.wait(context.Background()))
	})

	t.Run("ready already open returns nil", func(t *testing.T) {
		barrier := newGrainActivationBarrier(1, 0)
		barrier.open()

		assert.NoError(t, barrier.wait(context.Background()))
	})

	t.Run("timeout returns barrier error", func(t *testing.T) {
		barrier := newGrainActivationBarrier(1, 20*time.Millisecond)

		err := barrier.wait(context.Background())
		assert.ErrorIs(t, err, gerrors.ErrGrainActivationBarrierTimeout)
	})

	t.Run("opens while waiting with timeout", func(t *testing.T) {
		barrier := newGrainActivationBarrier(1, 100*time.Millisecond)

		go func() {
			time.Sleep(10 * time.Millisecond)
			barrier.open()
		}()

		assert.NoError(t, barrier.wait(context.Background()))
	})

	t.Run("context cancellation beats timeout", func(t *testing.T) {
		barrier := newGrainActivationBarrier(1, 50*time.Millisecond)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := barrier.wait(ctx)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("opens while waiting without timeout", func(t *testing.T) {
		barrier := newGrainActivationBarrier(1, 0)

		go func() {
			time.Sleep(10 * time.Millisecond)
			barrier.open()
		}()

		assert.NoError(t, barrier.wait(context.Background()))
	})

	t.Run("context cancellation without timeout", func(t *testing.T) {
		barrier := newGrainActivationBarrier(1, 0)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := barrier.wait(ctx)
		assert.ErrorIs(t, err, context.Canceled)
	})
}
