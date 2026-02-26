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

package quorum

import (
	"errors"

	"github.com/tochemey/olric"

	gerrors "github.com/tochemey/goakt/v4/errors"
)

// IsQuorumError returns true when the error indicates a quorum-related failure.
func IsQuorumError(err error) bool {
	return errors.Is(err, gerrors.ErrWriteQuorum) ||
		errors.Is(err, gerrors.ErrReadQuorum) ||
		errors.Is(err, gerrors.ErrClusterQuorum) ||
		errors.Is(err, olric.ErrWriteQuorum) ||
		errors.Is(err, olric.ErrReadQuorum) ||
		errors.Is(err, olric.ErrClusterQuorum)
}

// NormalizeQuorumError maps backend quorum errors to the exported Go-Akt errors.
// It returns the original error when it does not represent a quorum failure.
func NormalizeQuorumError(err error) error {
	switch {
	case errors.Is(err, gerrors.ErrWriteQuorum) || errors.Is(err, olric.ErrWriteQuorum):
		return gerrors.ErrWriteQuorum
	case errors.Is(err, gerrors.ErrReadQuorum) || errors.Is(err, olric.ErrReadQuorum):
		return gerrors.ErrReadQuorum
	case errors.Is(err, gerrors.ErrClusterQuorum) || errors.Is(err, olric.ErrClusterQuorum):
		return gerrors.ErrClusterQuorum
	default:
		return err
	}
}
