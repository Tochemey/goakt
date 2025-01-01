/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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

package members

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/tochemey/goakt/v2/log"
)

func TestOptions(t *testing.T) {
	testCases := []struct {
		name     string
		option   Option
		expected Server
	}{
		{
			name:     "WithLogger",
			option:   WithLogger(log.DefaultLogger),
			expected: Server{logger: log.DefaultLogger},
		},
		{
			name:     "WithMaxJoinAttempts",
			option:   WithMaxJoinAttempts(2),
			expected: Server{maxJoinAttempts: 2},
		},
		{
			name:     "WithShutdownTimeout",
			option:   WithShutdownTimeout(2 * time.Minute),
			expected: Server{shutdownTimeout: 2 * time.Minute},
		},
		{
			name:     "WithJoinTimeout",
			option:   WithJoinTimeout(2 * time.Minute),
			expected: Server{joinTimeout: 2 * time.Minute},
		},
		{
			name:     "WithJoinRetryInterval",
			option:   WithJoinRetryInterval(2 * time.Minute),
			expected: Server{joinRetryInterval: 2 * time.Minute},
		},
		{
			name:     "WithReplicationInterval",
			option:   WithReplicationInterval(2 * time.Minute),
			expected: Server{replicationInterval: 2 * time.Minute},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var server Server
			tc.option.Apply(&server)
			assert.Equal(t, tc.expected, server)
		})
	}
}