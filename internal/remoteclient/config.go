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
	"reflect"

	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/remote"
)

// ClientSerializerOptions converts the user-defined serializer entries from cfg
// into a slice of [ClientOption] values ready to pass to [NewClient].
//
// The default proto.Message entry is intentionally excluded because [NewClient]
// seeds itself with the same default independently. Only entries added via
// [remote.WithSerializers] are returned, so callers never register the proto
// default twice.
//
// Returns nil when no user-defined serializers have been registered.
func ClientSerializerOptions(cfg *remote.Config) []ClientOption {
	serializers := cfg.Serializers()
	if len(serializers) == 0 {
		return nil
	}

	// The proto.Message interface entry is always pre-registered by NewConfig /
	// DefaultConfig. NewClient seeds itself with the same default, so skip it
	// here to avoid a redundant entry in the client's serializer slice.
	protoMsgType := reflect.TypeOf((*proto.Message)(nil)).Elem()

	opts := make([]ClientOption, 0, len(serializers))
	for typ, ser := range serializers {
		if typ == protoMsgType {
			continue
		}
		t := typ
		s := ser
		opts = append(opts, func(r *client) {
			r.serializers = append(r.serializers, ifaceEntry{iface: t, serializer: s})
		})
	}
	if len(opts) == 0 {
		return nil
	}
	return opts
}
