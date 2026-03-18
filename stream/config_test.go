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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDefaultStageConfig(t *testing.T) {
	cfg := defaultStageConfig()

	assert.Equal(t, int64(defaultInitialDemand), cfg.InitialDemand)
	assert.Equal(t, int64(defaultRefillThreshold), cfg.RefillThreshold)
	assert.Equal(t, FailFast, cfg.ErrorStrategy)
	assert.Equal(t, DropTail, cfg.OverflowStrategy)
	assert.Equal(t, defaultPullTimeout, cfg.PullTimeout)
	assert.Equal(t, 256, cfg.BufferSize)
	assert.True(t, cfg.Fusion)
}

func TestStageConfigDefaults_Values(t *testing.T) {
	assert.Equal(t, 256, defaultBufferSize)
	assert.Equal(t, 224, defaultInitialDemand)
	assert.Equal(t, 64, defaultRefillThreshold)
	assert.Equal(t, 5*time.Second, defaultPullTimeout)
}
