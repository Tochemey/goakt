/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package cluster

import (
	"crypto/tls"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/tochemey/goakt/v3/internal/size"
	"github.com/tochemey/goakt/v3/log"
	testkit "github.com/tochemey/goakt/v3/mocks/hash"
)

func TestOptions(t *testing.T) {
	mockHasher := new(testkit.Hasher)
	// nolint
	tlsConfig := &tls.Config{}
	size := uint64(1 * size.MB)

	testCases := []struct {
		name   string
		option Option
		check  func(t *testing.T, e *Engine)
	}{
		{
			name:   "WithPartitionsCount",
			option: WithPartitionsCount(2),
			check: func(t *testing.T, e *Engine) {
				assert.EqualValues(t, 2, e.partitionsCount)
			},
		},
		{
			name:   "WithLogger",
			option: WithLogger(log.DefaultLogger),
			check: func(t *testing.T, e *Engine) {
				assert.Equal(t, log.DefaultLogger, e.logger)
			},
		},
		{
			name:   "WithWriteTimeout",
			option: WithWriteTimeout(2 * time.Minute),
			check: func(t *testing.T, e *Engine) {
				assert.Equal(t, 2*time.Minute, e.writeTimeout)
			},
		},
		{
			name:   "WithReadTimeout",
			option: WithReadTimeout(2 * time.Minute),
			check: func(t *testing.T, e *Engine) {
				assert.Equal(t, 2*time.Minute, e.readTimeout)
			},
		},
		{
			name:   "WithShutdownTimeout",
			option: WithShutdownTimeout(2 * time.Minute),
			check: func(t *testing.T, e *Engine) {
				assert.Equal(t, 2*time.Minute, e.shutdownTimeout)
			},
		},
		{
			name:   "WithHasher",
			option: WithHasher(mockHasher),
			check: func(t *testing.T, e *Engine) {
				assert.Equal(t, mockHasher, e.hasher)
			},
		},
		{
			name:   "WithReplicaCount",
			option: WithReplicaCount(2),
			check: func(t *testing.T, e *Engine) {
				assert.EqualValues(t, 2, e.replicaCount)
			},
		},
		{
			name:   "WithMinimumPeersQuorum",
			option: WithMinimumPeersQuorum(3),
			check: func(t *testing.T, e *Engine) {
				assert.EqualValues(t, 3, e.minimumPeersQuorum)
			},
		},
		{
			name:   "WithWriteQuorum",
			option: WithWriteQuorum(3),
			check: func(t *testing.T, e *Engine) {
				assert.EqualValues(t, 3, e.writeQuorum)
			},
		},
		{
			name:   "WithReadQuorum",
			option: WithReadQuorum(3),
			check: func(t *testing.T, e *Engine) {
				assert.EqualValues(t, 3, e.readQuorum)
			},
		},
		{
			name:   "WithTLS",
			option: WithTLS(tlsConfig, tlsConfig),
			check: func(t *testing.T, e *Engine) {
				assert.Equal(t, tlsConfig, e.serverTLS)
				assert.Equal(t, tlsConfig, e.clientTLS)
			},
		},
		{
			name:   "WithStorageSize",
			option: WithTableSize(size),
			check: func(t *testing.T, e *Engine) {
				assert.Equal(t, size, e.tableSize)
			},
		},
		{
			name:   "WithBootstrapTimeout",
			option: WithBootstrapTimeout(time.Second),
			check: func(t *testing.T, e *Engine) {
				assert.Equal(t, time.Second, e.bootstrapTimeout)
			},
		},
		{
			name:   "WithCacheSyncInterval",
			option: WithCacheSyncInterval(time.Second),
			check: func(t *testing.T, e *Engine) {
				assert.Equal(t, time.Second, e.cacheSyncInterval)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var eng Engine
			tc.option.Apply(&eng)
			tc.check(t, &eng)
		})
	}
}
