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

package pendingasks

import (
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/internal/commands"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

func TestDelivery(t *testing.T) {
	t.Run("completes a waiting caller", func(t *testing.T) {
		table := New()
		slot := table.Register("corr")

		response := &commands.AsyncResponse{
			CorrelationID: "corr",
			Message:       testpb.Reply_builder{Content: "ok"}.Build(),
		}
		require.True(t, table.Complete(response))
		require.Zero(t, table.Len())

		require.Same(t, response, <-slot)
	})

	t.Run("reports an unknown correlation", func(t *testing.T) {
		table := New()
		require.False(t, table.Complete(&commands.AsyncResponse{CorrelationID: "missing"}))
	})

	t.Run("rejects an unusable response", func(t *testing.T) {
		table := New()
		table.Register("corr")

		require.False(t, table.Complete(nil))
		require.False(t, table.Complete(&commands.AsyncResponse{}))
		require.Equal(t, 1, table.Len())
	})

	t.Run("completes only once", func(t *testing.T) {
		table := New()
		table.Register("corr")

		require.True(t, table.Complete(&commands.AsyncResponse{CorrelationID: "corr"}))
		require.False(t, table.Complete(&commands.AsyncResponse{CorrelationID: "corr"}))
	})
}

func TestAbandon(t *testing.T) {
	t.Run("a reply after abandon is discarded", func(t *testing.T) {
		table := New()
		slot := table.Register("corr")

		table.Abandon("corr")
		require.Zero(t, table.Len())

		require.False(t, table.Complete(&commands.AsyncResponse{CorrelationID: "corr"}))
		require.Empty(t, slot)
	})

	t.Run("abandon after complete leaves the response readable", func(t *testing.T) {
		table := New()
		slot := table.Register("corr")

		require.True(t, table.Complete(&commands.AsyncResponse{CorrelationID: "corr"}))
		table.Abandon("corr")

		require.NotNil(t, <-slot)
	})

	t.Run("abandoning an unknown correlation is a no-op", func(t *testing.T) {
		table := New()
		table.Abandon("missing")
		require.Zero(t, table.Len())
	})
}

// TestCompleteAbandonRace drives the race the table exists to resolve: a reply
// landing at the same moment the caller times out. Exactly one side must win,
// and the slot must never be left holding a response the caller reported as
// abandoned.
func TestCompleteAbandonRace(t *testing.T) {
	for range 500 {
		table := New()
		slot := table.Register("corr")

		var wg sync.WaitGroup
		completed := make(chan bool, 1)

		wg.Add(2)
		go func() {
			defer wg.Done()
			completed <- table.Complete(&commands.AsyncResponse{CorrelationID: "corr"})
		}()
		go func() {
			defer wg.Done()
			table.Abandon("corr")
		}()
		wg.Wait()

		// The slot holds a response exactly when Complete claimed the entry.
		require.Equal(t, len(slot), boolToInt(<-completed))
		require.Zero(t, table.Len())
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TestConcurrentCorrelations verifies that independent asks do not interfere:
// every caller receives the response carrying its own correlation ID.
func TestConcurrentCorrelations(t *testing.T) {
	const callers = 64

	table := New()
	slots := make([]<-chan *commands.AsyncResponse, callers)
	ids := make([]string, callers)

	for i := range callers {
		ids[i] = strconv.Itoa(i)
		slots[i] = table.Register(ids[i])
	}

	var wg sync.WaitGroup
	wg.Add(callers)
	for i := range callers {
		go func() {
			defer wg.Done()
			table.Complete(&commands.AsyncResponse{CorrelationID: ids[i]})
		}()
	}
	wg.Wait()

	require.Zero(t, table.Len())
	for i := range callers {
		require.Equal(t, ids[i], (<-slots[i]).CorrelationID)
	}
}
