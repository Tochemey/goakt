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

package memberlist

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAddr_Network(t *testing.T) {
	a := addr("127.0.0.1:7946")
	require.Equal(t, "tcp", a.Network())
}

func TestAddr_String(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{name: "ipv4 with port", in: "127.0.0.1:7946"},
		{name: "empty", in: ""},
		{name: "ipv6 with port", in: "[::1]:7946"},
		{name: "hostname with port", in: "example.local:9000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := addr(tt.in)
			require.Equal(t, tt.in, a.String())
		})
	}
}
