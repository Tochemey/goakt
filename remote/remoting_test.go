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
	"crypto/tls"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRemotingOptionsAndDefaults(t *testing.T) {
	r := NewRemoting().(*remoting)

	assert.NotNil(t, r.HTTPClient())
	assert.Equal(t, DefaultMaxReadFrameSize, r.MaxReadFrameSize())
	assert.Equal(t, NoCompression, r.Compression())
	assert.Nil(t, r.TLSConfig())

	// ensure close does not panic
	r.Close()
}

// nolint
func TestRemotingOptionApplication(t *testing.T) {
	tlsCfg := &tls.Config{}
	r := NewRemoting(
		WithRemotingTLS(tlsCfg),
		WithRemotingMaxReadFameSize(1024),
		WithRemotingCompression(GzipCompression),
	).(*remoting)

	assert.Equal(t, tlsCfg, r.TLSConfig())
	assert.Equal(t, 1024, r.MaxReadFrameSize())
	assert.Equal(t, GzipCompression, r.Compression())

	// RemotingServiceClient should build without hitting network.
	client := r.RemotingServiceClient("localhost", 8080)
	assert.NotNil(t, client)
}
