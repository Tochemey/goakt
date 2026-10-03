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

package actor

import (
	"reflect"
	"time"
)

// supervisionSignal is one failure handed to supervision: the error to report,
// the message being handled when it happened, and when it happened.
type supervisionSignal struct {
	err error
	// cause is the error the handler panicked with, when it panicked with one.
	// err then wraps it in a PanicError that adds the panic's location, and
	// cause is what the supervisor's rules are matched against first: a rule
	// for the panicked error's type applies as it would to ctx.Err.
	cause     error
	msg       any
	timestamp time.Time
}

// newSupervisionSignal records a failure that happened while handling msg.
func newSupervisionSignal(err error, msg any) *supervisionSignal {
	return &supervisionSignal{
		err:       err,
		msg:       msg,
		timestamp: time.Now().UTC(),
	}
}

// newPanicSupervisionSignal records a panic whose value was the error cause,
// reported as err (the PanicError wrapping it).
func newPanicSupervisionSignal(err error, cause error, msg any) *supervisionSignal {
	signal := newSupervisionSignal(err, msg)
	signal.cause = cause
	return signal
}

// Err returns the error to report: for a panic, the PanicError.
func (s *supervisionSignal) Err() error {
	return s.err
}

// Cause returns the error the handler panicked with, or nil.
func (s *supervisionSignal) Cause() error {
	return s.cause
}

func (s *supervisionSignal) Msg() any {
	return s.msg
}

func (s *supervisionSignal) Timestamp() time.Time {
	return s.timestamp
}

func errorType(err error) string {
	if err == nil {
		return "nil"
	}
	rtype := reflect.TypeOf(err)
	if rtype.Kind() == reflect.Pointer {
		rtype = rtype.Elem()
	}
	return rtype.String()
}
