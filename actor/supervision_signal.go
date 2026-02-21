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

type supervisionSignal struct {
	err       error
	msg       any
	timestamp time.Time
}

func newSupervisionSignal(err error, msg any) *supervisionSignal {
	return &supervisionSignal{
		err:       err,
		msg:       msg,
		timestamp: time.Now().UTC(),
	}
}

func (s *supervisionSignal) Err() error {
	return s.err
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
	if rtype.Kind() == reflect.Ptr {
		rtype = rtype.Elem()
	}
	return rtype.String()
}
