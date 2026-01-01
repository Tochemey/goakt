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

package cluster

import (
	"crypto/tls"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/tochemey/goakt/v3/internal/size"
	"github.com/tochemey/goakt/v3/log"
	hashmock "github.com/tochemey/goakt/v3/mocks/hash"
	gtls "github.com/tochemey/goakt/v3/tls"
)

// nolint
func TestConfigOptions(t *testing.T) {
	mockHasher := new(hashmock.Hasher)
	tlsCfg := &tls.Config{}
	tlsInfo := &gtls.Info{ClientConfig: tlsCfg, ServerConfig: tlsCfg}
	sizeVal := uint64(8 * size.MB)

	testCases := []struct {
		name   string
		option ConfigOption
		assert func(*testing.T, *config)
	}{
		{
			name:   "WithLogger",
			option: WithLogger(log.DebugLogger),
			assert: func(t *testing.T, cfg *config) {
				assert.Equal(t, log.DebugLogger, cfg.logger)
			},
		},
		{
			name:   "WithShardHasher",
			option: WithPartitioner(mockHasher),
			assert: func(t *testing.T, cfg *config) {
				assert.Equal(t, mockHasher, cfg.shardHasher)
			},
		},
		{
			name:   "WithShardCount",
			option: WithShardCount(17),
			assert: func(t *testing.T, cfg *config) {
				assert.EqualValues(t, 17, cfg.shardCount)
			},
		},
		{
			name:   "WithReplicasCount",
			option: WithReplicasCount(3),
			assert: func(t *testing.T, cfg *config) {
				assert.EqualValues(t, 3, cfg.replicasCount)
			},
		},
		{
			name:   "WithMinimumMembersQuorum",
			option: WithMinimumMembersQuorum(4),
			assert: func(t *testing.T, cfg *config) {
				assert.EqualValues(t, 4, cfg.minimumMembersQuorum)
			},
		},
		{
			name:   "WithMembersWriteQuorum",
			option: WithMembersWriteQuorum(5),
			assert: func(t *testing.T, cfg *config) {
				assert.EqualValues(t, 5, cfg.membersWriteQuorum)
			},
		},
		{
			name:   "WithMembersReadQuorum",
			option: WithMembersReadQuorum(6),
			assert: func(t *testing.T, cfg *config) {
				assert.EqualValues(t, 6, cfg.membersReadQuorum)
			},
		},
		{
			name:   "WithDataTableSize",
			option: WithDataTableSize(sizeVal),
			assert: func(t *testing.T, cfg *config) {
				assert.Equal(t, sizeVal, cfg.tableSize)
			},
		},
		{
			name:   "WithWriteTimeout",
			option: WithWriteTimeout(45 * time.Second),
			assert: func(t *testing.T, cfg *config) {
				assert.Equal(t, 45*time.Second, cfg.writeTimeout)
			},
		},
		{
			name:   "WithReadTimeout",
			option: WithReadTimeout(30 * time.Second),
			assert: func(t *testing.T, cfg *config) {
				assert.Equal(t, 30*time.Second, cfg.readTimeout)
			},
		},
		{
			name:   "WithShutdownTimeout",
			option: WithShutdownTimeout(3 * time.Minute),
			assert: func(t *testing.T, cfg *config) {
				assert.Equal(t, 3*time.Minute, cfg.shutdownTimeout)
			},
		},
		{
			name:   "WithBootstrapTimeout",
			option: WithBootstrapTimeout(12 * time.Second),
			assert: func(t *testing.T, cfg *config) {
				assert.Equal(t, 12*time.Second, cfg.bootstrapTimeout)
			},
		},
		{
			name:   "WithRoutingTableInterval",
			option: WithRoutingTableInterval(5 * time.Second),
			assert: func(t *testing.T, cfg *config) {
				assert.Equal(t, 5*time.Second, cfg.routingTableInterval)
			},
		},
		{
			name:   "WithBalancerInterval",
			option: WithBalancerInterval(2 * time.Second),
			assert: func(t *testing.T, cfg *config) {
				assert.Equal(t, 2*time.Second, cfg.triggerBalancerInterval)
			},
		},
		{
			name:   "WithTLS",
			option: WithTLS(tlsInfo),
			assert: func(t *testing.T, cfg *config) {
				assert.Equal(t, tlsInfo, cfg.tlsInfo)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultConfig()
			tc.option(cfg)
			tc.assert(t, cfg)
		})
	}
}

func TestConfigOptionsIgnoreZeroOrNil(t *testing.T) {
	cfg := defaultConfig()
	original := *cfg

	WithLogger(nil)(cfg)
	WithPartitioner(nil)(cfg)
	WithShardCount(0)(cfg)
	WithReplicasCount(0)(cfg)
	WithMinimumMembersQuorum(0)(cfg)
	WithMembersWriteQuorum(0)(cfg)
	WithMembersReadQuorum(0)(cfg)
	WithDataTableSize(0)(cfg)
	WithWriteTimeout(0)(cfg)
	WithReadTimeout(0)(cfg)
	WithShutdownTimeout(0)(cfg)
	WithBootstrapTimeout(0)(cfg)
	WithRoutingTableInterval(0)(cfg)
	WithBalancerInterval(0)(cfg)

	assert.Equal(t, original.logger, cfg.logger)
	assert.Equal(t, original.shardHasher, cfg.shardHasher)
	assert.Equal(t, original.shardCount, cfg.shardCount)
	assert.Equal(t, original.replicasCount, cfg.replicasCount)
	assert.Equal(t, original.minimumMembersQuorum, cfg.minimumMembersQuorum)
	assert.Equal(t, original.membersWriteQuorum, cfg.membersWriteQuorum)
	assert.Equal(t, original.membersReadQuorum, cfg.membersReadQuorum)
	assert.Equal(t, original.tableSize, cfg.tableSize)
	assert.Equal(t, original.writeTimeout, cfg.writeTimeout)
	assert.Equal(t, original.readTimeout, cfg.readTimeout)
	assert.Equal(t, original.shutdownTimeout, cfg.shutdownTimeout)
	assert.Equal(t, original.bootstrapTimeout, cfg.bootstrapTimeout)
	assert.Equal(t, original.routingTableInterval, cfg.routingTableInterval)
	assert.Equal(t, original.triggerBalancerInterval, cfg.triggerBalancerInterval)
	assert.Nil(t, cfg.tlsInfo)
}
