/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tochemey/goakt/log"
	testkit "github.com/tochemey/goakt/testkit/hash"
)

func TestOptions(t *testing.T) {
	mockHasher := new(testkit.Hasher)
	testCases := []struct {
		name     string
		option   Option
		expected Node
	}{
		{
			name:     "WithPartitionsCount",
			option:   WithPartitionsCount(2),
			expected: Node{partitionsCount: 2},
		},
		{
			name:     "WithLogger",
			option:   WithLogger(log.DefaultLogger),
			expected: Node{logger: log.DefaultLogger},
		},
		{
			name:     "WithWriteTimeout",
			option:   WithWriteTimeout(2 * time.Minute),
			expected: Node{writeTimeout: 2 * time.Minute},
		},
		{
			name:     "WithReadTimeout",
			option:   WithReadTimeout(2 * time.Minute),
			expected: Node{readTimeout: 2 * time.Minute},
		},
		{
			name:     "WithShutdownTimeout",
			option:   WithShutdownTimeout(2 * time.Minute),
			expected: Node{shutdownTimeout: 2 * time.Minute},
		},
		{
			name:     "WithHasher",
			option:   WithHasher(mockHasher),
			expected: Node{hasher: mockHasher},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var cl Node
			tc.option.Apply(&cl)
			assert.Equal(t, tc.expected, cl)
		})
	}
}
