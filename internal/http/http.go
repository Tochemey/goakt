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

package http

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/net/http2"
)

// NewClient creates a http client use h2c
func NewClient() *http.Client {
	return &http.Client{
		// Most RPC servers don't use HTTP redirects
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http2.Transport{
			TLSClientConfig:    nil,
			AllowHTTP:          true,
			DisableCompression: false,
			DialTLSContext: func(_ context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				// If you're also using this client for non-h2c traffic, you may want to
				// delegate to tls.Dial if the network isn't TCP or the addr isn't in an
				// allow-list.
				return net.Dial(network, addr)
			},
			PingTimeout:     30 * time.Second,
			ReadIdleTimeout: 30 * time.Second,
		},
	}
}

// NewTLSClient creates a secured http client
func NewTLSClient(tlsConfig *tls.Config) *http.Client {
	return &http.Client{
		// Most RPC servers don't use HTTP redirects
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http2.Transport{
			TLSClientConfig:    tlsConfig,
			DisableCompression: false,
			DialTLSContext: func(_ context.Context, network, addr string, config *tls.Config) (net.Conn, error) {
				return tls.Dial(network, addr, config)
			},
			PingTimeout:     30 * time.Second,
			ReadIdleTimeout: 30 * time.Second,
		},
	}
}

// URL create a http connection address
func URL(host string, port int) string {
	return fmt.Sprintf("http://%s", net.JoinHostPort(host, strconv.Itoa(port)))
}

// URLs create a secured http connection address
func URLs(host string, port int) string {
	return fmt.Sprintf("https://%s", net.JoinHostPort(host, strconv.Itoa(port)))
}
