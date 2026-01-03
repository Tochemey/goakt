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

package reentrancy

import (
	"testing"

	"github.com/stretchr/testify/require"

	gerrors "github.com/tochemey/goakt/v3/errors"
)

func TestNewDefaults(t *testing.T) {
	cfg := New()
	require.Equal(t, Off, cfg.Mode())
	require.Equal(t, 0, cfg.MaxInFlight())
	require.NoError(t, cfg.Validate())
}

func TestWithMaxInFlight(t *testing.T) {
	tests := []struct {
		name  string
		value int
		want  int
	}{
		{
			name:  "negative clamps to zero",
			value: -1,
			want:  0,
		},
		{
			name:  "zero stays zero",
			value: 0,
			want:  0,
		},
		{
			name:  "positive value",
			value: 5,
			want:  5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := New(WithMaxInFlight(tt.value))
			require.Equal(t, tt.want, cfg.MaxInFlight())
		})
	}
}

func TestWithMode(t *testing.T) {
	tests := []struct {
		name string
		mode Mode
	}{
		{
			name: "off",
			mode: Off,
		},
		{
			name: "allow all",
			mode: AllowAll,
		},
		{
			name: "stash non reentrant",
			mode: StashNonReentrant,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := New(WithMode(tt.mode))
			require.Equal(t, tt.mode, cfg.Mode())
		})
	}
}

func TestValidateInvalidMode(t *testing.T) {
	cfg := New(WithMode(Mode(99)))
	require.ErrorIs(t, cfg.Validate(), gerrors.ErrInvalidReentrancyMode)
}

func TestIsValidReentrancyMode(t *testing.T) {
	require.True(t, IsValidReentrancyMode(Off))
	require.True(t, IsValidReentrancyMode(AllowAll))
	require.True(t, IsValidReentrancyMode(StashNonReentrant))
	require.False(t, IsValidReentrancyMode(Mode(99)))
}
