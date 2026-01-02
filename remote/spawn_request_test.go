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

package remote

import (
	"encoding"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/extension"
)

type mockDependency struct {
	id string
}

func (s mockDependency) ID() string { return s.id }

func (mockDependency) MarshalBinary() ([]byte, error) { return nil, nil }

func (mockDependency) UnmarshalBinary([]byte) error { return nil }

// ensure stub implements the interfaces
var _ encoding.BinaryMarshaler = (*mockDependency)(nil)
var _ encoding.BinaryUnmarshaler = (*mockDependency)(nil)
var _ extension.Dependency = (*mockDependency)(nil)

func TestSpawnRequestValidateAndSanitize(t *testing.T) {
	t.Run("valid request", func(t *testing.T) {
		req := &SpawnRequest{
			Name: " actor ",
			Kind: " kind ",
			Singleton: &SingletonSpec{
				SpawnTimeout: time.Second,
				WaitInterval: 300 * time.Millisecond,
				MaxRetries:   3,
			},
			Relocatable:    false,
			Dependencies:   []extension.Dependency{},
			EnableStashing: true,
		}
		require.NoError(t, req.Validate())
		req.Sanitize()
		assert.Equal(t, "actor", req.Name)
		assert.Equal(t, "kind", req.Kind)
		assert.True(t, req.Relocatable)
	})

	t.Run("invalid dependency id", func(t *testing.T) {
		req := &SpawnRequest{
			Name:         "actor",
			Kind:         "kind",
			Dependencies: []extension.Dependency{mockDependency{id: ""}},
		}
		err := req.Validate()
		require.Error(t, err)
	})

	t.Run("invalid reentrancy mode", func(t *testing.T) {
		req := &SpawnRequest{
			Name: "actor",
			Kind: "kind",
			Reentrancy: &ReentrancyConfig{
				Mode: ReentrancyMode(99),
			},
		}
		err := req.Validate()
		require.Error(t, err)
		require.ErrorIs(t, err, gerrors.ErrInvalidReentrancyMode)
	})

	t.Run("sanitize clamps reentrancy max in flight", func(t *testing.T) {
		req := &SpawnRequest{
			Name: "actor",
			Kind: "kind",
			Reentrancy: &ReentrancyConfig{
				Mode:        ReentrancyAllowAll,
				MaxInFlight: -10,
			},
		}
		req.Sanitize()
		require.NotNil(t, req.Reentrancy)
		assert.Equal(t, 0, req.Reentrancy.MaxInFlight)
	})
}
