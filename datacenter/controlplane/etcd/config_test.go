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

package etcd

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConfigValidate(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		config := &Config{
			Endpoints:   []string{"http://127.0.0.1:2379"},
			Namespace:   defaultNamespace,
			TTL:         5 * time.Second,
			DialTimeout: 5 * time.Second,
			Timeout:     5 * time.Second,
		}

		require.NoError(t, config.Validate())
	})

	t.Run("invalid config", func(t *testing.T) {
		config := &Config{}
		require.Error(t, config.Validate())
	})
}

func TestConfigSanitize(t *testing.T) {
	config := &Config{}
	config.Sanitize()

	require.NotNil(t, config.Context)
	require.Equal(t, defaultNamespace, config.Namespace)
	require.Equal(t, 5*time.Second, config.DialTimeout)
	require.Equal(t, 5*time.Second, config.Timeout)

	config = &Config{
		Context:     context.Background(),
		Namespace:   "/custom",
		DialTimeout: 2 * time.Second,
		Timeout:     3 * time.Second,
	}
	config.Sanitize()

	require.Equal(t, "/custom", config.Namespace)
	require.Equal(t, 2*time.Second, config.DialTimeout)
	require.Equal(t, 3*time.Second, config.Timeout)
}
