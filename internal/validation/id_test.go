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

package validation

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIDValidator(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		id := "valid-id"
		validator := NewIDValidator(id)
		err := validator.Validate()
		require.NoError(t, err)
	})
	t.Run("With invalid length", func(t *testing.T) {
		id := strings.Repeat("a", 300)
		validator := NewIDValidator(id)
		err := validator.Validate()
		require.Error(t, err)
	})
	t.Run("With invalid characters", func(t *testing.T) {
		id := "$omeN@me"
		validator := NewIDValidator(id)
		err := validator.Validate()
		require.Error(t, err)
	})
}
