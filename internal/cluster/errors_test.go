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

package cluster

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tochemey/olric"

	gerrors "github.com/tochemey/goakt/v3/errors"
)

func TestIsQuorumError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "write quorum", err: olric.ErrWriteQuorum, want: true},
		{name: "read quorum", err: olric.ErrReadQuorum, want: true},
		{name: "cluster quorum", err: olric.ErrClusterQuorum, want: true},
		{name: "write quorum (goakt)", err: gerrors.ErrWriteQuorum, want: true},
		{name: "read quorum (goakt)", err: gerrors.ErrReadQuorum, want: true},
		{name: "cluster quorum (goakt)", err: gerrors.ErrClusterQuorum, want: true},
		{name: "non quorum", err: errors.New("boom"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, IsQuorumError(tt.err))
		})
	}
}

func TestNormalizeQuorumError(t *testing.T) {
	plain := errors.New("boom")
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "write quorum", err: olric.ErrWriteQuorum, want: gerrors.ErrWriteQuorum},
		{name: "read quorum", err: olric.ErrReadQuorum, want: gerrors.ErrReadQuorum},
		{name: "cluster quorum", err: olric.ErrClusterQuorum, want: gerrors.ErrClusterQuorum},
		{name: "write quorum (goakt)", err: gerrors.ErrWriteQuorum, want: gerrors.ErrWriteQuorum},
		{name: "read quorum (goakt)", err: gerrors.ErrReadQuorum, want: gerrors.ErrReadQuorum},
		{name: "cluster quorum (goakt)", err: gerrors.ErrClusterQuorum, want: gerrors.ErrClusterQuorum},
		{name: "non quorum", err: plain, want: plain},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "non quorum" {
				require.Same(t, tt.want, NormalizeQuorumError(tt.err))
				return
			}
			require.ErrorIs(t, NormalizeQuorumError(tt.err), tt.want)
		})
	}
}
