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

// Package refusal marks the error with which a node refused a grain message
// before any handler ran it, because the node is shutting down. The mark tells
// the sender that the message did not run, so sending it again elsewhere
// cannot run it twice.
//
// The mark is internal on purpose. An error returned to application code is
// always unmarked (Unmark), so a grain handler that passes on the error of a
// nested call can never make its own node look like it refused a message it
// ran.
package refusal

import "errors"

// markedError is the mark: it wraps the refusal and reads like it.
type markedError struct {
	err error
}

// unmarkedError hides the mark of the error it holds while it keeps matching
// everything else the error matches.
type unmarkedError struct {
	err error
}

// Mark returns err marked as the refusal of a grain message by the node.
func Mark(err error) error {
	return &markedError{err}
}

// Marked reports whether err carries the mark of a node refusal.
func Marked(err error) bool {
	_, marked := errors.AsType[*markedError](err)
	return marked
}

// Unmark returns err without the mark of a node refusal. An error that is not
// marked is returned as is. The result has the same text and matches the same
// errors through errors.Is and errors.As.
func Unmark(err error) error {
	if !Marked(err) {
		return err
	}

	if marked, ok := err.(*markedError); ok {
		return Unmark(marked.err)
	}

	return &unmarkedError{err}
}

// Error returns the text of the refusal unchanged, so the marked error reads,
// and travels over the wire, like the error it marks.
func (x *markedError) Error() string {
	return x.err.Error()
}

// Unwrap returns the refusal.
func (x *markedError) Unwrap() error {
	return x.err
}

// Error returns the text of the held error.
func (x *unmarkedError) Error() string {
	return x.err.Error()
}

// Is reports whether the held error matches target.
func (x *unmarkedError) Is(target error) bool {
	return errors.Is(x.err, target)
}

// As finds in the held error the first error that target can hold, except the
// mark, which it never reveals.
func (x *unmarkedError) As(target any) bool {
	if _, mark := target.(**markedError); mark {
		return false
	}

	return errors.As(x.err, target)
}
