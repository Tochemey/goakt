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

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStreamHandle_Done_SignalDone(t *testing.T) {
	h := newStreamHandle("sub1", nil, nil)

	select {
	case <-h.Done():
		t.Fatal("Done should not be closed yet")
	default:
	}

	h.signalDone(nil)

	select {
	case <-h.Done():
	default:
		t.Fatal("Done should be closed after signalDone")
	}
}

func TestStreamHandle_Err_NilOnSuccess(t *testing.T) {
	h := newStreamHandle("sub1", nil, nil)
	h.signalDone(nil)
	assert.Nil(t, h.Err())
}

func TestStreamHandle_Err_WithError(t *testing.T) {
	h := newStreamHandle("sub1", nil, nil)
	sentinel := errors.New("oops")
	h.signalDone(sentinel)
	assert.ErrorIs(t, h.Err(), sentinel)
}

func TestStreamHandle_SignalDone_Idempotent(t *testing.T) {
	h := newStreamHandle("sub1", nil, nil)
	err1 := errors.New("first")
	h.signalDone(err1)
	h.signalDone(errors.New("second")) // second call must be ignored

	// Error should still be the first one.
	assert.ErrorIs(t, h.Err(), err1)
}

func TestStreamHandle_Stop_NilSource(t *testing.T) {
	h := newStreamHandle("sub1", nil, nil)
	// Stop with nil sourcePID should not panic.
	require.NoError(t, h.Stop(context.Background()))
}

func TestStreamHandle_Abort(t *testing.T) {
	h := newStreamHandle("sub1", nil, nil)
	h.Abort() // with no PIDs, must not panic and must close Done.

	select {
	case <-h.Done():
	default:
		t.Fatal("Abort should close Done")
	}
	assert.ErrorIs(t, h.Err(), ErrStreamCanceled)
}

func TestStreamHandle_Metrics(t *testing.T) {
	h := newStreamHandle("sub1", nil, nil)
	h.sourceMetrics.elementsIn.Add(5)
	h.sinkMetrics.elementsOut.Add(3)

	m := h.Metrics()
	assert.Equal(t, uint64(5), m.ElementsIn)
	assert.Equal(t, uint64(3), m.ElementsOut)
}

func TestStreamHandleImpl_ID(t *testing.T) {
	h := newStreamHandle("my-stream-id", nil, nil)
	assert.Equal(t, "my-stream-id", h.ID())
}

func TestMultiHandle_ID_IsNonEmpty(t *testing.T) {
	h1 := newStreamHandle("a", nil, nil)
	h2 := newStreamHandle("b", nil, nil)
	mh := newMultiHandle([]StreamHandle{h1, h2})
	assert.NotEmpty(t, mh.ID())
}

func TestMultiHandle_Done_ClosesWhenAllSubsDone(t *testing.T) {
	h1 := newStreamHandle("a", nil, nil)
	h2 := newStreamHandle("b", nil, nil)
	mh := newMultiHandle([]StreamHandle{h1, h2})

	select {
	case <-mh.Done():
		t.Fatal("multiHandle.Done should not be closed before any sub is done")
	default:
	}

	h1.signalDone(nil)

	select {
	case <-mh.Done():
		t.Fatal("multiHandle.Done should not close until all subs are done")
	default:
	}

	h2.signalDone(nil)

	select {
	case <-mh.Done():
	case <-time.After(time.Second):
		t.Fatal("multiHandle.Done did not close after all subs signaled done")
	}
	assert.NoError(t, mh.Err())
}

func TestMultiHandle_Err_PropagatesFirstSubError(t *testing.T) {
	h1 := newStreamHandle("a", nil, nil)
	h2 := newStreamHandle("b", nil, nil)
	mh := newMultiHandle([]StreamHandle{h1, h2})

	sentinel := errors.New("sub-stream failure")
	h1.signalDone(sentinel)
	h2.signalDone(nil)

	select {
	case <-mh.Done():
	case <-time.After(time.Second):
		t.Fatal("multiHandle.Done did not close")
	}
	assert.ErrorIs(t, mh.Err(), sentinel)
}

func TestMultiHandle_Stop_WhenAllSubsAlreadyDone(t *testing.T) {
	h1 := newStreamHandle("a", nil, nil)
	h2 := newStreamHandle("b", nil, nil)
	mh := newMultiHandle([]StreamHandle{h1, h2})

	h1.signalDone(nil)
	h2.signalDone(nil)

	select {
	case <-mh.Done():
	case <-time.After(time.Second):
		t.Fatal("multiHandle did not close after both subs done")
	}

	require.NoError(t, mh.Stop(context.Background()))
}

func TestMultiHandle_Stop_ContextCanceled(t *testing.T) {
	h1 := newStreamHandle("a", nil, nil)
	h2 := newStreamHandle("b", nil, nil)
	mh := newMultiHandle([]StreamHandle{h1, h2})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := mh.Stop(ctx)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestMultiHandle_Abort_ClosesAllSubs(t *testing.T) {
	h1 := newStreamHandle("a", nil, nil)
	h2 := newStreamHandle("b", nil, nil)
	mh := newMultiHandle([]StreamHandle{h1, h2})

	mh.Abort()

	select {
	case <-h1.Done():
	case <-time.After(time.Second):
		t.Fatal("h1 should be done after Abort")
	}
	assert.ErrorIs(t, h1.Err(), ErrStreamCanceled)

	select {
	case <-h2.Done():
	case <-time.After(time.Second):
		t.Fatal("h2 should be done after Abort")
	}
	assert.ErrorIs(t, h2.Err(), ErrStreamCanceled)

	select {
	case <-mh.Done():
	case <-time.After(time.Second):
		t.Fatal("multiHandle should be done after Abort")
	}
}

func TestMultiHandle_Metrics_AggregatesBothHandles(t *testing.T) {
	h1 := newStreamHandle("a", nil, nil)
	h1.sourceMetrics.elementsIn.Add(5)
	h1.sinkMetrics.elementsOut.Add(4)
	h1.sinkMetrics.errors.Add(1)

	h2 := newStreamHandle("b", nil, nil)
	h2.sourceMetrics.elementsIn.Add(3)
	h2.sinkMetrics.elementsOut.Add(3)

	mh := newMultiHandle([]StreamHandle{h1, h2})
	m := mh.Metrics()

	// h1.Metrics() = aggregate({sourceIn=5}, {out=4, errors=1}) = {In=5, Out=4, Errors=1}
	// h2.Metrics() = aggregate({sourceIn=3}, {out=3}) = {In=3, Out=3}
	// multiHandle.Metrics() = aggregate(h1, h2) = {In=8, Out=7, Errors=1}
	assert.Equal(t, uint64(8), m.ElementsIn)
	assert.Equal(t, uint64(7), m.ElementsOut)
	assert.Equal(t, uint64(1), m.Errors)
}
