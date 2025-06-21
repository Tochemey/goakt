/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package actor

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/internal/types"
)

func TestIdentity(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		grain := NewMockGrain()
		name := "testGrain"
		identity := newIdentity(grain, name)
		require.NotNil(t, identity)
		expectedKind := types.Name(grain)
		expectedStr := fmt.Sprintf("%s%s%s", expectedKind, identitySeparator, name)
		require.Equal(t, expectedKind, identity.Kind(), "expected kind to match grain type name")
		require.Equal(t, "testGrain", identity.Name(), "expected name to match provided name")
		require.Equal(t, expectedStr, identity.String(), "expected string representation to match format")
	})
	t.Run("With empty name", func(t *testing.T) {
		grain := NewMockGrain()
		name := ""
		identity := newIdentity(grain, name)
		err := identity.Validate()
		require.ErrorContains(t, err, "the [name] is required", "expected validation error for empty name")
	})
	t.Run("With name more than 255", func(t *testing.T) {
		grain := NewMockGrain()
		name := strings.Repeat("a", 300)
		identity := newIdentity(grain, name)
		err := identity.Validate()
		require.ErrorContains(t, err, "grain name is too long. Maximum length is 255", "expected validation error for empty name")
	})
	t.Run("With invalid name", func(t *testing.T) {
		grain := NewMockGrain()
		name := "$omeN@me"
		identity := newIdentity(grain, name)
		err := identity.Validate()
		require.ErrorContains(t, err, "must contain only word characters (i.e. [a-zA-Z0-9] plus non-leading '-' or '_')", "expected validation error for empty name")
	})
	t.Run("With valid parsing", func(t *testing.T) {
		grain := NewMockGrain()
		name := "testGrain"
		identity := newIdentity(grain, name)
		actual, err := toIdentity(identity.String())
		require.NoError(t, err, "expected no error when parsing identity string")
		require.True(t, identity.Equal(actual), "expected parsed identity to match original")
	})
	t.Run("With no identity separator", func(t *testing.T) {
		actual, err := toIdentity("invalid-identity-string")
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidGrainIdentity)
		require.Nil(t, actual)
	})
	t.Run("With invalid", func(t *testing.T) {
		identity := fmt.Sprintf("%s%s%s", "kind", identitySeparator, strings.Repeat("a", 300))
		actual, err := toIdentity(identity)
		require.Error(t, err)
		require.Nil(t, actual)
	})
	t.Run("With inequal identity", func(t *testing.T) {
		grain := NewMockGrain()
		name1 := "testGrain1"
		name2 := "testGrain2"
		identity1 := newIdentity(grain, name1)
		identity2 := newIdentity(grain, name2)
		require.False(t, identity1.Equal(identity2), "expected identities to be unequal")
	})
}
