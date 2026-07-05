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

func TestClaimScheduleFireReturnsErrorForUnsupportedCluster(t *testing.T) {
	cl := &MockCluster{}
	err := ClaimScheduleFire(context.Background(), cl, "key", time.Minute)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrScheduleFireClaimed)
}

func TestSupportsScheduleFireClaim(t *testing.T) {
	require.True(t, SupportsScheduleFireClaim(&cluster{}))
	require.False(t, SupportsScheduleFireClaim(&MockCluster{}))
	require.False(t, SupportsScheduleFireClaim(nil))
}

func TestRemoveScheduleFireNoopWhenClusterOrKeyMissing(t *testing.T) {
	require.NoError(t, RemoveScheduleFire(context.Background(), nil, "key"))
	require.NoError(t, RemoveScheduleFire(context.Background(), &cluster{}, ""))
}

func TestRemoveScheduleFireNoopForUnsupportedCluster(t *testing.T) {
	require.NoError(t, RemoveScheduleFire(context.Background(), &MockCluster{}, "key"))
}

func TestRemoveScheduleFireReturnsErrorWhenNotRunning(t *testing.T) {
	cl := &cluster{running: atomic.NewBool(false)}
	err := RemoveScheduleFire(context.Background(), cl, "key")
	require.ErrorIs(t, err, ErrEngineNotRunning)
}

func TestRemoveScheduleFireDeletesTheComposedKey(t *testing.T) {
	var gotKeys []string
	cl := &cluster{
		running:      atomic.NewBool(true),
		writeTimeout: time.Second,
		dmap: &MockDMap{
			deleteFn: func(_ context.Context, keys ...string) (int, error) {
				gotKeys = keys
				return 1, nil
			},
		},
	}

	require.NoError(t, RemoveScheduleFire(context.Background(), cl, "key"))
	require.Equal(t, []string{composeKey(namespaceScheduleFire, "key")}, gotKeys)
}

func TestRemoveScheduleFirePropagatesDMapError(t *testing.T) {
	expectedErr := errors.New("delete failure")
	cl := &cluster{
		running:      atomic.NewBool(true),
		writeTimeout: time.Second,
		dmap: &MockDMap{
			deleteFn: func(context.Context, ...string) (int, error) {
				return 0, expectedErr
			},
		},
	}

	err := RemoveScheduleFire(context.Background(), cl, "key")
	require.ErrorIs(t, err, expectedErr)
}
