/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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

package breaker

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOptionsValidate(t *testing.T) {
	t.Run("default options valid", func(t *testing.T) {
		opts := defaultOptions()
		if err := opts.Validate(); err != nil {
			t.Fatalf("expected no error for default options, got: %v", err)
		}
	})

	tests := []struct {
		name       string
		modify     func(*options)
		wantErrSub string
		expectErr  bool
	}{
		{
			name: "failureRate negative",
			modify: func(o *options) {
				o.failureRate = -0.1
			},
			wantErrSub: "failureRate",
			expectErr:  true,
		},
		{
			name: "failureRate greater than 1",
			modify: func(o *options) {
				o.failureRate = 1.5
			},
			wantErrSub: "failureRate",
			expectErr:  true,
		},
		{
			name: "minRequests less than 1",
			modify: func(o *options) {
				o.minRequests = 0
			},
			wantErrSub: "minRequests",
			expectErr:  true,
		},
		{
			name: "openTimeout non-positive",
			modify: func(o *options) {
				o.openTimeout = 0
			},
			wantErrSub: "openTimeout",
			expectErr:  true,
		},
		{
			name: "window non-positive",
			modify: func(o *options) {
				o.window = 0
			},
			wantErrSub: "window",
			expectErr:  true,
		},
		{
			name: "buckets less than 1",
			modify: func(o *options) {
				o.buckets = 0
			},
			wantErrSub: "buckets",
			expectErr:  true,
		},
		{
			name: "halfOpenMaxCalls less than 1",
			modify: func(o *options) {
				o.halfOpenMaxCalls = 0
			},
			wantErrSub: "halfOpenMaxCalls",
			expectErr:  true,
		},
		{
			name: "clock nil",
			modify: func(o *options) {
				o.clock = nil
			},
			wantErrSub: "clock",
			expectErr:  true,
		},
		{
			name: "bucket duration too small",
			modify: func(o *options) {
				// choose window 1ms and buckets 2 -> bucket duration 500µs < 1ms
				o.window = 1 * time.Millisecond
				o.buckets = 2
			},
			wantErrSub: "bucket duration",
			expectErr:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := defaultOptions()
			tc.modify(opts)
			err := opts.Validate()
			if tc.expectErr {
				require.Error(t, err)
				if tc.wantErrSub != "" && !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("expected error containing %q, got %q", tc.wantErrSub, err.Error())
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestOptionsSanitize(t *testing.T) {
	opts := &options{}
	opts.Sanitize()
	require.NoError(t, opts.Validate())
}

// nolint
func TestOptionsSanitizeClampsFailureRate(t *testing.T) {
	t.Run("below zero", func(t *testing.T) {
		opts := &options{failureRate: -0.3}
		opts.Sanitize()
		require.Equal(t, 0.5, opts.failureRate)
	})
	t.Run("above one", func(t *testing.T) {
		opts := &options{failureRate: 1.3}
		opts.Sanitize()
		require.Equal(t, 0.5, opts.failureRate)
	})
}
