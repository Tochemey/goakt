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

package remoteclient

import (
	"errors"

	"github.com/tochemey/goakt/v4/remote"
)

var (
	errNoSerializerEncode = errors.New("remote: no serializer could encode the message")
	errNoSerializerDecode = errors.New("remote: no serializer could decode the message")
)

// serializerDispatch is a composite [Serializer] returned by
// [remoting.Serializer] when the caller passes nil (receive path).
// Built once per [remoting] instance and reused for every inbound message.
// It tries each registered serializer in registration order and returns
// the first successful result.
type serializerDispatch struct {
	entries []ifaceEntry
}

var _ remote.Serializer = (*serializerDispatch)(nil)

// Serialize tries each registered serializer in order and returns the first
// successful encoding.
func (x *serializerDispatch) Serialize(message any) ([]byte, error) {
	var lastErr error
	for i := range x.entries {
		data, err := x.entries[i].serializer.Serialize(message)
		if err == nil {
			return data, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errNoSerializerEncode
}

// Deserialize tries each registered serializer in order and returns the first
// successful decoding.
func (x *serializerDispatch) Deserialize(data []byte) (any, error) {
	var lastErr error
	for i := range x.entries {
		msg, err := x.entries[i].serializer.Deserialize(data)
		if err == nil {
			return msg, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errNoSerializerDecode
}
