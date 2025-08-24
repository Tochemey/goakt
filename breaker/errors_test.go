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
	"errors"
	"fmt"
	"testing"
)

func TestError_Error(t *testing.T) {
	cause := errors.New("root cause")

	tests := []struct {
		name     string
		err      *Error
		expected string
	}{
		{
			name: "with cause",
			err: &Error{
				Type:    ErrorTypeTimeout,
				State:   Open,
				Message: "operation timed out",
				Cause:   cause,
			},
			expected: fmt.Sprintf("circuit-breaker [open]: operation timed out: %v", cause),
		},
		{
			name: "without cause",
			err: &Error{
				Type:    ErrorTypeOpen,
				State:   Closed,
				Message: "breaker is open",
				Cause:   nil,
			},
			expected: "circuit-breaker [closed]: breaker is open",
		},
		{
			name: "half-open with custom message",
			err: &Error{
				Type:    ErrorTypePanic,
				State:   HalfOpen,
				Message: "panic recovered",
			},
			expected: "circuit-breaker [half-open]: panic recovered",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			if got != tt.expected {
				t.Errorf("Error() = %q; want %q", got, tt.expected)
			}
		})
	}
}

func TestError_Unwrap(t *testing.T) {
	cause := errors.New("underlying issue")
	err := &Error{
		Type:    ErrorTypeTimeout,
		State:   Open,
		Message: "operation failed",
		Cause:   cause,
	}

	if !errors.Is(err, cause) {
		t.Errorf("expected errors.Is to match cause, but it did not")
	}

	if got := err.Unwrap(); !errors.Is(got, cause) {
		t.Errorf("Unwrap() = %v; want %v", got, cause)
	}
}
