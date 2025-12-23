/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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

package remote

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/extension"
)

func TestGrainActivationRequestValidateAndSanitize(t *testing.T) {
	t.Run("valid request", func(t *testing.T) {
		req := &GrainRequest{
			Name:              " grain ",
			Kind:              " kind ",
			Dependencies:      []extension.Dependency{mockDependency{id: "dep"}},
			ActivationTimeout: 0,
			ActivationRetries: 0,
		}
		require.NoError(t, req.Validate())
		req.Sanitize()
		assert.Equal(t, "grain", req.Name)
		assert.Equal(t, "kind", req.Kind)
		assert.Equal(t, time.Second, req.ActivationTimeout)
		assert.Equal(t, 5, req.ActivationRetries)
	})

	t.Run("invalid dependency id", func(t *testing.T) {
		req := &GrainRequest{
			Name:         "grain",
			Kind:         "kind",
			Dependencies: []extension.Dependency{mockDependency{id: ""}},
		}
		err := req.Validate()
		require.Error(t, err)
	})
}
