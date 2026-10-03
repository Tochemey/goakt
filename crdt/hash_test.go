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
	"google.golang.org/protobuf/types/known/structpb"
)

// hashTestValue is a user value with a map inside, the case where a plain
// encoding would depend on iteration order.
type hashTestValue struct {
	Name   string
	Labels map[string]int
}

func TestHashValue(t *testing.T) {
	t.Run("equal values hash alike", func(t *testing.T) {
		assert.Equal(t, hashValue("a"), hashValue("a"))
		assert.Equal(t, hashValue(42), hashValue(42))
		assert.Equal(t, hashValue(nil), hashValue(nil))
	})

	t.Run("the type of a value is part of it", func(t *testing.T) {
		assert.NotEqual(t, hashValue(1), hashValue("1"))
		assert.NotEqual(t, hashValue(1), hashValue(int64(1)))
		assert.NotEqual(t, hashValue(1), hashValue(true))
		assert.NotEqual(t, hashValue(""), hashValue(nil))
	})

	t.Run("map order inside a value does not matter", func(t *testing.T) {
		labels := map[string]int{}
		reversed := map[string]int{}

		for i := range 64 {
			labels[string(rune('a'+i))] = i
			reversed[string(rune('a'+63-i))] = 63 - i
		}

		assert.Equal(t, hashValue(hashTestValue{Name: "n", Labels: labels}), hashValue(hashTestValue{Name: "n", Labels: reversed}))
		assert.NotEqual(t, hashValue(hashTestValue{Name: "n", Labels: labels}), hashValue(hashTestValue{Name: "m", Labels: labels}))
	})

	t.Run("a pointer hashes like the value it points to", func(t *testing.T) {
		value := hashTestValue{Name: "n", Labels: map[string]int{"a": 1}}
		assert.Equal(t, hashValue(value), hashValue(&value))
	})

	t.Run("protobuf messages hash by content", func(t *testing.T) {
		first, err := structpb.NewStruct(map[string]any{"a": 1, "b": "two", "c": true})
		assert.NoError(t, err)
		second, err := structpb.NewStruct(map[string]any{"c": true, "b": "two", "a": 1})
		assert.NoError(t, err)
		other, err := structpb.NewStruct(map[string]any{"a": 2})
		assert.NoError(t, err)

		assert.Equal(t, hashValue(first), hashValue(second))
		assert.NotEqual(t, hashValue(first), hashValue(other))
		assert.NotEqual(t, hashValue(structpb.NewStringValue("x")), hashValue(structpb.NewBoolValue(true)))
	})

	t.Run("a value that cannot be encoded still hashes", func(t *testing.T) {
		assert.NotPanics(t, func() { hashValue(make(chan int)) })
	})
}

func TestHashLiveDots(t *testing.T) {
	t.Run("dot order does not matter", func(t *testing.T) {
		forward := []dot{{nodeID: "a", counter: 1}, {nodeID: "b", counter: 2}, {nodeID: "c", counter: 3}}
		backward := []dot{{nodeID: "c", counter: 3}, {nodeID: "b", counter: 2}, {nodeID: "a", counter: 1}}
		assert.Equal(t, hashLiveDots(forward), hashLiveDots(backward))
	})

	t.Run("only the highest dot of a node counts", func(t *testing.T) {
		redundant := []dot{{nodeID: "a", counter: 1}, {nodeID: "a", counter: 4}, {nodeID: "a", counter: 4}, {nodeID: "b", counter: 2}}
		compacted := []dot{{nodeID: "b", counter: 2}, {nodeID: "a", counter: 4}}
		assert.Equal(t, hashLiveDots(compacted), hashLiveDots(redundant))
		assert.NotEqual(t, hashLiveDots(compacted), hashLiveDots([]dot{{nodeID: "a", counter: 1}, {nodeID: "b", counter: 2}}))
	})
}

func TestHashNodeCounters(t *testing.T) {
	assert.Equal(t, hashNodeCounters(nil), hashNodeCounters(map[string]uint64{"a": 0}))
	assert.NotEqual(t, hashNodeCounters(map[string]uint64{"a": 1, "b": 2}), hashNodeCounters(map[string]uint64{"a": 2, "b": 1}))
}
