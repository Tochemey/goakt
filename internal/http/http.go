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
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/net/http2"
)

// NewHTTPClient creates an HTTP/2 client configured for high-performance
// remoting with optimized connection pooling, timeouts, and multiplexing.
//
// The client uses HTTP/2 over cleartext (h2c) for efficient multiplexing of
// multiple requests over a single TCP connection. This significantly reduces
// connection overhead and improves throughput, especially for high-frequency
// RPC calls typical in actor system remoting.
//
// Key optimizations:
//   - Reduced ping and read idle timeouts for faster failure detection
//   - Connection keep-alive via net.Dialer for connection reuse
//   - Optimized dialer settings for low-latency scenarios
//   - HTTP/2 multiplexing for efficient concurrent request handling
//
// Parameters:
//   - maxReadFrameSize: Maximum frame size in bytes for HTTP/2 frames.
//     Valid range is typically 16KB to 16MB. Larger frames reduce overhead
//     but increase memory usage per connection.
func NewHTTPClient(maxReadFrameSize uint32) *http.Client {
	// Configure dialer with connection keep-alive for connection reuse.
	// KeepAlive duration should be longer than ReadIdleTimeout to ensure
	// connections remain active between requests. A 30-second keep-alive
	// balances connection reuse with resource cleanup.
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,  // Connection establishment timeout
		KeepAlive: 30 * time.Second, // TCP keep-alive probe interval
	}

	return &http.Client{
		// Most RPC servers don't use HTTP redirects. Return the last response
		// immediately to avoid unnecessary round trips.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		// Set overall request timeout to prevent indefinite hangs. This serves
		// as a safety net for requests that don't honor context cancellation.
		Timeout: 30 * time.Second,
		Transport: &http2.Transport{
			TLSClientConfig:  nil,  // No TLS for h2c (HTTP/2 cleartext)
			AllowHTTP:        true, // Permit HTTP/2 over cleartext connections
			MaxReadFrameSize: maxReadFrameSize,
			// Enable HTTP-level compression for bandwidth efficiency. The actual
			// compression used depends on server negotiation and client preferences.
			DisableCompression: false,
			// Custom dialer with keep-alive for connection reuse. The dialer
			// maintains TCP connections alive between requests, reducing the
			// overhead of connection establishment for subsequent calls.
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				// Use the configured dialer to establish TCP connection with keep-alive.
				// The connection will be reused by HTTP/2 multiplexing for multiple
				// concurrent streams, providing significant performance improvements
				// for high-frequency RPC traffic.
				return dialer.DialContext(ctx, network, addr)
			},
			// PingTimeout controls how long to wait for a ping response from the server.
			// Reduced from 30s to 10s to detect dead connections faster and fail
			// requests more quickly, improving overall system responsiveness.
			PingTimeout: 10 * time.Second,
			// ReadIdleTimeout triggers a health check ping if no frames are received
			// within this duration. Reduced from 30s to 20s to detect idle connections
			// sooner and maintain connection health more proactively.
			ReadIdleTimeout: 20 * time.Second,
			// Note: MaxConcurrentStreams is negotiated during HTTP/2 connection
			// establishment and is controlled by the server, not the client. The client
			// will respect the server's advertised limit, which is typically 100 by default.
		},
	}
}

// NewHTTPSClient creates an HTTP/2 client with TLS support, configured for
// high-performance secure remoting with optimized connection pooling, timeouts,
// and multiplexing.
//
// This client is functionally equivalent to NewHTTPClient but uses TLS encryption
// for secure communication over the wire. All performance optimizations from
// NewHTTPClient are applied here as well.
//
// Key optimizations:
//   - Reduced ping and read idle timeouts for faster failure detection
//   - Connection keep-alive via net.Dialer for connection reuse
//   - Optimized TLS dialer with connection reuse support
//   - HTTP/2 multiplexing for efficient concurrent request handling
//
// Parameters:
//   - clientTLS: TLS configuration for secure connections. Must not be nil.
//     The configuration is used for all TLS connections established by this client.
//   - maxReadFrameSize: Maximum frame size in bytes for HTTP/2 frames.
//     Valid range is typically 16KB to 16MB. Larger frames reduce overhead
//     but increase memory usage per connection.
//
// nolint
func NewHTTPSClient(clientTLS *tls.Config, maxReadFrameSize uint32) *http.Client {
	// Configure dialer with connection keep-alive for connection reuse.
	// KeepAlive duration should be longer than ReadIdleTimeout to ensure
	// connections remain active between requests. A 30-second keep-alive
	// balances connection reuse with resource cleanup.
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,  // Connection establishment timeout
		KeepAlive: 30 * time.Second, // TCP keep-alive probe interval
	}

	// Create a custom HTTP/2 transport with optimized settings for secure remoting.
	h2Transport := &http2.Transport{
		// Enable HTTP-level compression for bandwidth efficiency. The actual
		// compression used depends on server negotiation and client preferences.
		DisableCompression: false,
		MaxReadFrameSize:   maxReadFrameSize,
		// Reuse the provided TLS configuration for all secure connections.
		// This ensures consistent security settings across all connections
		// established by this client.
		TLSClientConfig: clientTLS,
		// PingTimeout controls how long to wait for a ping response from the server.
		// Reduced from 30s to 10s to detect dead connections faster and fail
		// requests more quickly, improving overall system responsiveness.
		PingTimeout: 10 * time.Second,
		// ReadIdleTimeout triggers a health check ping if no frames are received
		// within this duration. Reduced from 30s to 20s to detect idle connections
		// sooner and maintain connection health more proactively.
		ReadIdleTimeout: 20 * time.Second,
		// Note: MaxConcurrentStreams is negotiated during HTTP/2 connection
		// establishment and is controlled by the server, not the client. The client
		// will respect the server's advertised limit, which is typically 100 by default.
		// Custom TLS dialer with keep-alive for connection reuse. The dialer
		// establishes TCP connections with keep-alive enabled, then upgrades
		// them to TLS. This maintains connection reuse benefits even with
		// encrypted connections.
		DialTLSContext: func(ctx context.Context, network, addr string, config *tls.Config) (net.Conn, error) {
			// Establish TCP connection with keep-alive using the configured dialer.
			conn, err := dialer.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			// Upgrade connection to TLS. The config parameter should match
			// TLSClientConfig, but we use the provided config for flexibility.
			return tls.Client(conn, config), nil
		},
	}

	return &http.Client{
		// Most RPC servers don't use HTTP redirects. Return the last response
		// immediately to avoid unnecessary round trips.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		// Set overall request timeout to prevent indefinite hangs. This serves
		// as a safety net for requests that don't honor context cancellation.
		Timeout:   30 * time.Second,
		Transport: h2Transport,
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
