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

package crdt

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateBridge(t *testing.T) {
	key := PNCounterKey("counter")
	u := &Update[*PNCounter]{
		Key:     key,
		Initial: NewPNCounter(),
		Modify: func(current *PNCounter) *PNCounter {
			return current.Increment("node-1", 1)
		},
	}

	t.Run("KeyID", func(t *testing.T) {
		assert.Equal(t, "counter", u.KeyID())
	})

	t.Run("CRDTDataType", func(t *testing.T) {
		assert.Equal(t, PNCounterType, u.CRDTDataType())
	})

	t.Run("InitialValue", func(t *testing.T) {
		initial := u.InitialValue()
		require.NotNil(t, initial)
		assert.Equal(t, int64(0), initial.(*PNCounter).Value())
	})

	t.Run("Apply with existing value", func(t *testing.T) {
		existing := NewPNCounter().Increment("node-1", 5)
		result := u.Apply(existing)
		assert.Equal(t, int64(6), result.(*PNCounter).Value())
	})

	t.Run("Apply with nil value uses initial", func(t *testing.T) {
		result := u.Apply(nil)
		assert.Equal(t, int64(1), result.(*PNCounter).Value())
	})

	t.Run("Apply with type mismatch falls back to initial", func(t *testing.T) {
		wrongType := NewGCounter().Increment("node-1", 99)
		result := u.Apply(wrongType)
		assert.Equal(t, int64(1), result.(*PNCounter).Value())
	})
}

func TestGetBridge(t *testing.T) {
	key := PNCounterKey("counter")
	g := &Get[*PNCounter]{Key: key}

	t.Run("KeyID", func(t *testing.T) {
		assert.Equal(t, "counter", g.KeyID())
	})

	t.Run("Response with data", func(t *testing.T) {
		data := NewPNCounter().Increment("node-1", 5)
		resp := g.Response(data)
		require.NotNil(t, resp)
		typed := resp.(*GetResponse[*PNCounter])
		assert.Equal(t, "counter", typed.Key.ID())
		assert.Equal(t, int64(5), typed.Data.Value())
	})

	t.Run("Response with nil data", func(t *testing.T) {
		resp := g.Response(nil)
		require.NotNil(t, resp)
		typed := resp.(*GetResponse[*PNCounter])
		assert.Nil(t, typed.Data)
	})

	t.Run("Response with type mismatch returns zero value", func(t *testing.T) {
		wrongType := NewGCounter().Increment("node-1", 99)
		resp := g.Response(wrongType)
		require.NotNil(t, resp)
		typed := resp.(*GetResponse[*PNCounter])
		assert.Nil(t, typed.Data)
	})
}

func TestSubscribeBridge(t *testing.T) {
	key := ORSetKey[string]("sessions")
	s := &Subscribe[*ORSet[string]]{Key: key}

	t.Run("KeyID", func(t *testing.T) {
		assert.Equal(t, "sessions", s.KeyID())
	})

	t.Run("IsSubscribe marker", func(t *testing.T) {
		s.IsSubscribe() // should not panic
	})
}

func TestUnsubscribeBridge(t *testing.T) {
	key := ORSetKey[string]("sessions")
	u := &Unsubscribe[*ORSet[string]]{Key: key}

	t.Run("KeyID", func(t *testing.T) {
		assert.Equal(t, "sessions", u.KeyID())
	})

	t.Run("IsUnsubscribe marker", func(t *testing.T) {
		u.IsUnsubscribe() // should not panic
	})
}

func TestDeleteBridge(t *testing.T) {
	key := GCounterKey("counter")
	d := &Delete[*GCounter]{Key: key}

	t.Run("KeyID", func(t *testing.T) {
		assert.Equal(t, "counter", d.KeyID())
	})

	t.Run("IsDelete marker", func(t *testing.T) {
		d.IsDelete() // should not panic
	})
}
