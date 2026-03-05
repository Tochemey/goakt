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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/hash"
)

func TestRouterOption(t *testing.T) {
	t.Run("WithRoutingStrategy", func(t *testing.T) {
		router := &router{}
		option := WithRoutingStrategy(RandomRouting)
		option.Apply(router)
		assert.Equal(t, RandomRouting, router.routingStrategy)
	})
	t.Run("AsTailChoppingRouter", func(t *testing.T) {
		router := &router{}
		option := AsTailChopping(time.Second, time.Second)
		option.Apply(router)
		require.Equal(t, tailChoppingRouter, router.kind)
		assert.Equal(t, time.Second, router.within)
		assert.Equal(t, time.Second, router.interval)
	})
	t.Run("AsScatterGatherFirst", func(t *testing.T) {
		router := &router{}
		option := AsScatterGatherFirst(time.Second)
		option.Apply(router)
		require.Equal(t, scatterGatherFirstRouter, router.kind)
		assert.Equal(t, time.Second, router.within)
	})
	t.Run("WithResumeRouteeOnFailure", func(t *testing.T) {
		router := &router{}
		option := WithResumeRouteeOnFailure()
		option.Apply(router)
		assert.Equal(t, resumeRoutee, router.supervisorDirective)
	})
	t.Run("WithRestartRouteeOnFailure", func(t *testing.T) {
		router := &router{}
		option := WithRestartRouteeOnFailure(2, time.Second)
		option.Apply(router)
		assert.EqualValues(t, 2, router.restartRouteeAttempts)
		assert.Equal(t, time.Second, router.restartRouteeWithin)
		assert.Equal(t, restartRoutee, router.supervisorDirective)
	})
	t.Run("WithStopRouteeOnFailure", func(t *testing.T) {
		router := &router{}
		option := WithStopRouteeOnFailure()
		option.Apply(router)
		assert.Equal(t, stopRoutee, router.supervisorDirective)
	})
	t.Run("WithConsistentHashRouter", func(t *testing.T) {
		router := &router{}
		extractor := func(msg any) string { return "key" }
		option := WithConsistentHashRouter(extractor)
		option.Apply(router)
		assert.Equal(t, ConsistentHashRouting, router.routingStrategy)
		require.NotNil(t, router.routingKeyExtractor)
		assert.Equal(t, "key", router.routingKeyExtractor("anything"))
	})
	t.Run("WithConsistentHashVirtualNodes", func(t *testing.T) {
		router := &router{}
		option := WithConsistentHashVirtualNodes(300)
		option.Apply(router)
		assert.Equal(t, 300, router.virtualNodes)
	})
	t.Run("WithConsistentHashHasher", func(t *testing.T) {
		router := &router{}
		h := hash.DefaultHasher()
		option := WithConsistentHashHasher(h)
		option.Apply(router)
		assert.Equal(t, h, router.hasher)
	})
}
