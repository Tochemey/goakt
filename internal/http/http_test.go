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

package http

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/net/http2"

	"github.com/tochemey/goakt/v3/internal/size"
)

func TestNewClient(t *testing.T) {
	cl := NewHTTPClient(16 * size.KB)
	assert.IsType(t, new(http.Client), cl)
	assert.IsType(t, new(http2.Transport), cl.Transport)
	tr := cl.Transport.(*http2.Transport)
	assert.True(t, tr.AllowHTTP)
	assert.Equal(t, 10*time.Second, tr.PingTimeout)
	assert.Equal(t, 20*time.Second, tr.ReadIdleTimeout)
}

func TestURL(t *testing.T) {
	host := "127.0.0.1"
	port := 123

	url := URL(host, port)
	assert.Equal(t, "http://127.0.0.1:123", url)
}

func TestURLs(t *testing.T) {
	host := "127.0.0.1"
	port := 123

	url := URLs(host, port)
	assert.Equal(t, "https://127.0.0.1:123", url)
}
