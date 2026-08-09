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

package remoteclient

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestProtocolCacheGetSetClear(t *testing.T) {
	var cache protocolCache
	assert.Equal(t, peerProtocolUnknown, cache.get())
	assert.False(t, cache.isLegacy())
	assert.False(t, cache.isDuplex())

	cache.set(peerProtocolLegacy)
	assert.Equal(t, peerProtocolLegacy, cache.get())
	assert.True(t, cache.isLegacy())
	assert.False(t, cache.isDuplex())
	assert.False(t, cache.legacyExpired(time.Now()))

	cache.set(peerProtocolDuplex)
	assert.Equal(t, peerProtocolDuplex, cache.get())
	assert.True(t, cache.isDuplex())
	assert.False(t, cache.isLegacy())

	cache.clear()
	assert.Equal(t, peerProtocolUnknown, cache.get())
	assert.False(t, cache.isLegacy())
	assert.False(t, cache.isDuplex())
}

func TestProtocolCacheLegacyExpired(t *testing.T) {
	var cache protocolCache
	cache.set(peerProtocolLegacy)
	cache.markedAt = time.Now().Add(-peerLegacyReprobeInterval - time.Second)
	assert.True(t, cache.legacyExpired(time.Now()))
}
