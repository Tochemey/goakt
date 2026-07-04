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
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tochemey/olric"
	"go.uber.org/atomic"
)

func TestClaimScheduleFireReturnsErrorWhenClusterNil(t *testing.T) {
	err := ClaimScheduleFire(context.Background(), nil, "key", time.Minute)
	require.EqualError(t, err, "cluster is nil")
}

func TestClaimScheduleFireReturnsErrorWhenKeyEmpty(t *testing.T) {
	cl := &cluster{running: atomic.NewBool(true)}
	err := ClaimScheduleFire(context.Background(), cl, "", time.Minute)
	require.EqualError(t, err, "schedule fire key is empty")
}

func TestClaimScheduleFireReturnsErrorWhenNotRunning(t *testing.T) {
	cl := &cluster{running: atomic.NewBool(false)}
	err := ClaimScheduleFire(context.Background(), cl, "key", time.Minute)
	require.ErrorIs(t, err, ErrEngineNotRunning)
}

func TestClaimScheduleFireReturnsClaimedWhenKeyExists(t *testing.T) {
	cl := &cluster{
		running:      atomic.NewBool(true),
		dmap:         &MockDMap{putErr: olric.ErrKeyFound},
		writeTimeout: time.Second,
	}

	err := ClaimScheduleFire(context.Background(), cl, "key", time.Minute)
	require.ErrorIs(t, err, ErrScheduleFireClaimed)
}

func TestClaimScheduleFirePropagatesDMapError(t *testing.T) {
	expectedErr := errors.New("put failure")
	cl := &cluster{
		running:      atomic.NewBool(true),
		dmap:         &MockDMap{putErr: expectedErr},
		writeTimeout: time.Second,
	}

	err := ClaimScheduleFire(context.Background(), cl, "key", time.Minute)
	require.ErrorIs(t, err, expectedErr)
}

func TestClaimScheduleFireSucceeds(t *testing.T) {
	var gotOptions []olric.PutOption
	cl := &cluster{
		running:      atomic.NewBool(true),
		writeTimeout: time.Second,
		dmap: &MockDMap{
			putFn: func(_ context.Context, _ string, _ any, options ...olric.PutOption) error { // nolint
				gotOptions = options
				return nil
			},
		},
	}

	err := ClaimScheduleFire(context.Background(), cl, "key", time.Minute)
	require.NoError(t, err)
	// NX (claim-once) and EX (TTL) must both be applied to the write.
	require.Len(t, gotOptions, 2)
}

func TestClaimScheduleFireFallbackReturnsClaimedWhenJobKeyExists(t *testing.T) {
	cl := &MockCluster{
		jobKeyFn: func(context.Context, string) ([]byte, error) {
			return []byte{1}, nil
		},
	}

	err := ClaimScheduleFire(context.Background(), cl, "key", time.Minute)
	require.ErrorIs(t, err, ErrScheduleFireClaimed)
	require.Equal(t, 1, cl.jobKeyCalls)
	require.Zero(t, cl.putJobKeyCalls)
}

func TestClaimScheduleFireFallbackClaimsWhenJobKeyAbsent(t *testing.T) {
	cl := &MockCluster{
		jobKeyFn: func(context.Context, string) ([]byte, error) {
			return nil, errors.New("not found")
		},
	}

	err := ClaimScheduleFire(context.Background(), cl, "key", time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, cl.jobKeyCalls)
	require.Equal(t, 1, cl.putJobKeyCalls)
}

func TestClaimScheduleFireFallbackPropagatesPutJobKeyError(t *testing.T) {
	expectedErr := errors.New("put job key failed")
	cl := &MockCluster{
		jobKeyFn: func(context.Context, string) ([]byte, error) {
			return nil, errors.New("not found")
		},
		putJobKeyFn: func(context.Context, string, []byte) error {
			return expectedErr
		},
	}

	err := ClaimScheduleFire(context.Background(), cl, "key", time.Minute)
	require.ErrorIs(t, err, expectedErr)
}
