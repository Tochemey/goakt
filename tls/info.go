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

package tls

import "crypto/tls"

// Info encapsulates the TLS configuration for both client and server
// sides of a secure connection.
//
// Both the ServerConfig and ClientConfig configurations should be set up
// using the same root Certificate Authority (CA) in order to establish
// trust during the TLS handshake. This is especially important when
// mutual TLS (mTLS) is required, as both parties must authenticate each
// other’s certificates against the same CA.
//
// Typical usage includes:
//
//   - ServerConfig: Used by the server to present its certificate and
//     validate client certificates when mTLS is enabled.
//   - ClientConfig: Used by the client to validate the server certificate
//     and optionally present its own certificate for mTLS.
//
// Note: The tls.Config values should be constructed carefully, for
// example by setting fields such as RootCAs, Certificates, and
// InsecureSkipVerify (only for testing). Reuse of a single tls.Config
// across multiple connections is recommended for performance.
type Info struct {
	// ClientConfig holds the TLS configuration used by clients to connect
	// securely to a server. This includes validation of the server’s
	// certificate and, if required, providing a client certificate for
	// mutual TLS authentication.
	ClientConfig *tls.Config

	// ServerConfig holds the TLS configuration used by servers to accept
	// secure client connections. This includes presenting the server’s
	// own certificate and optionally validating client certificates
	// when mutual TLS authentication is enabled.
	ServerConfig *tls.Config
}
