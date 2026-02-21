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

	"github.com/tochemey/goakt/v4/internal/quorum"
)

var (
	// ErrActorNotFound is return when an actor is not found
	ErrActorNotFound = errors.New("actor not found")
	// ErrPeerSyncNotFound is returned when a peerSync record is not found
	ErrPeerSyncNotFound = errors.New("peerSync record not found")
	// ErrInvalidTLSConfiguration is returned whent the TLS configuration is not properly set
	ErrInvalidTLSConfiguration = errors.New("TLS configuration is invalid")
	// ErrEngineNotRunning is returned when the cluster engine is not running
	ErrEngineNotRunning = errors.New("cluster engine is not running")
	// ErrGrainNotFound is returned when a grain is not found
	ErrGrainNotFound = errors.New("grain not found")
	// ErrGrainAlreadyExists is returned when a grain entry already exists.
	ErrGrainAlreadyExists = errors.New("grain already exists")
	// ErrKindAlreadyExists is returned when an actor kind entry already exists.
	ErrKindAlreadyExists = errors.New("kind already exists")
)

// IsQuorumError returns true when a cluster operation failed due to quorum constraints.
func IsQuorumError(err error) bool {
	return quorum.IsQuorumError(err)
}

// NormalizeQuorumError maps backend quorum errors to the exported Go-Akt errors.
// It returns the original error when it does not represent a quorum failure.
func NormalizeQuorumError(err error) error {
	return quorum.NormalizeQuorumError(err)
}
