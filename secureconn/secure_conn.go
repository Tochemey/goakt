/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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

package secureconn

import (
	"crypto/tls"
	"crypto/x509"
)

// SecureConn defines the mTLS configuration
type SecureConn struct {
	rootCA *x509.CertPool
	cert   *tls.Certificate
}

// NewSecureConn creates an instance of mTLS configuration
func NewSecureConn(rootCA *x509.CertPool, cert *tls.Certificate) *SecureConn {
	return &SecureConn{
		rootCA: rootCA,
		cert:   cert,
	}
}

// NewSecureConnFromPEMBlocks create an instance of mTLS configuration from binary representations
// of the root certificate, the private key and the certificate file
func NewSecureConnFromPEMBlocks(rootCAsPEMBlock, keyPEMBlock, certPEMBlock []byte) (*SecureConn, error) {
	certpool := x509.NewCertPool()
	certpool.AppendCertsFromPEM(rootCAsPEMBlock)
	x509KeyPair, err := tls.X509KeyPair(certPEMBlock, keyPEMBlock)
	if err != nil {
		return nil, err
	}
	return &SecureConn{
		rootCA: certpool,
		cert:   &x509KeyPair,
	}, nil
}

// SecureClient returns the TLS client configuration
// that is required to make secured connection to a secured server
// on the remote node
func (conn *SecureConn) SecureClient() *tls.Config {
	return &tls.Config{
		RootCAs:      conn.rootCA,
		Certificates: []tls.Certificate{*conn.cert},
		NextProtos:   []string{"h2", "http/1.1"},
		MinVersion:   tls.VersionTLS13,
		CurvePreferences: []tls.CurveID{
			tls.CurveP521,
			tls.CurveP384,
			tls.CurveP256,
		},
	}
}

// SecureServer return the TLS server configuration
// required to handle secured connection from a remote node
func (conn *SecureConn) SecureServer() *tls.Config {
	return &tls.Config{
		ClientCAs:    conn.rootCA,
		Certificates: []tls.Certificate{*conn.cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		NextProtos:   []string{"h2", "http/1.1"},
		MinVersion:   tls.VersionTLS13,
		CurvePreferences: []tls.CurveID{
			tls.CurveP521,
			tls.CurveP384,
			tls.CurveP256,
		},
	}
}
