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

package breaker

import (
	"context"
	"fmt"
)

// ErrorType represents different types of circuit breaker errors
type ErrorType int

const (
	ErrorTypeOpen ErrorType = iota
	ErrorTypeTimeout
	ErrorTypePanic
)

// Error provides detailed error information
type Error struct {
	Type    ErrorType
	State   State
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("circuit-breaker [%s]: %s: %v", e.State, e.Message, e.Cause)
	}
	return fmt.Sprintf("circuit-breaker [%s]: %s", e.State, e.Message)
}

func (e *Error) Unwrap() error {
	return e.Cause
}

var (
	// ErrOpen is returned when the breaker is open and rejects a call.
	ErrOpen = &Error{
		Type:    ErrorTypeOpen,
		State:   Open,
		Message: "circuit breaker is open",
	}
	// ErrTimeout indicates the context expired while executing the function.
	ErrTimeout = &Error{
		Type:    ErrorTypeTimeout,
		Message: "execution timeout",
		Cause:   context.DeadlineExceeded,
	}
)
