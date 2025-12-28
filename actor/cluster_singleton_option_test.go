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

package actor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClusterSingletonOption(t *testing.T) {
	cfg := newClusterSingletonConfig()
	require.Nil(t, cfg.Role())

	role := "payments"
	WithSingletonRole(role)(cfg)
	WithSingletonSpawnTimeout(time.Second)(cfg)
	WithSingletonSpawnWaitInterval(100 * time.Millisecond)(cfg)
	WithSingletonSpawnRetries(10)(cfg)

	require.Equal(t, 10, cfg.numberOfRetries)
	require.Equal(t, 100*time.Millisecond, cfg.waitInterval)
	require.Equal(t, time.Second, cfg.spawnTimeout)
	require.NotNil(t, cfg.Role())
	require.Equal(t, role, *cfg.Role())
}
