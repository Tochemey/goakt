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

// Package stream internal tests for the splitter behind MergeSubstreams.
package stream

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSubFlowSourceActor_HandleAck_IgnoresAnotherFeedSource verifies that an
// acknowledgement lowers the in-flight count of a key only when it comes from
// the feed source of the substream that holds the key. Under
// SubstreamRestart the feed source of a failed substream can acknowledge
// after a new substream has taken its key.
func TestSubFlowSourceActor_HandleAck_IgnoresAnotherFeedSource(t *testing.T) {
	sys := newInternalTestSystem(t)
	ctx := context.Background()

	failedFeed, err := sys.Spawn(ctx, "failed-feed", &dummyStageActor{})
	require.NoError(t, err)
	currentFeed, err := sys.Spawn(ctx, "current-feed", &dummyStageActor{})
	require.NoError(t, err)

	splitter := newSubFlowSourceActor[int](nil, nil, splitModeGroupBy, nil, nil, 0, 4, BackpressureSource, SubstreamRestart, defaultStageConfig())
	splitter.children[7] = &substreamState{head: currentFeed, slot: 1, inFlight: 4}

	splitter.handleAck(failedFeed, 7, 3)
	require.EqualValues(t, 4, splitter.children[7].inFlight, "an acknowledgement of the failed feed source was counted")

	splitter.handleAck(currentFeed, 7, 3)
	require.EqualValues(t, 1, splitter.children[7].inFlight)
}
