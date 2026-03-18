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
