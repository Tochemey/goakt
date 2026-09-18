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

// Package tlstest loads the TLS fixtures the test suites share.
package tlstest

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"

	gtls "github.com/tochemey/goakt/v4/tls"
)

const (
	caCertFile       = "ca.cert"            // CA that signed the server certificate
	serverCertFile   = "auto.pem"           // server certificate, valid for localhost, 127.0.0.1 and ::1
	serverKeyFile    = "auto.key"           // private key of the server certificate
	clientCACertFile = "client-auth-ca.pem" // CA that signed the client certificate
	clientCertFile   = "client-auth.pem"    // client certificate presented during mutual TLS
	clientKeyFile    = "client-auth.key"    // private key of the client certificate
)

// Load reads the TLS fixtures in dir and returns the matching server and client configurations.
//
// dir is the fixture directory, test/data/certs in the repository, given relative to the package
// of the calling test.
//
// The server configuration presents the server certificate, requires and verifies a client
// certificate against the client CA, speaks TLS 1.3 at minimum and advertises h2 and http/1.1.
//
// The client configuration presents the client certificate and verifies the server against the
// server CA, so callers dialing localhost or 127.0.0.1 get a fully verified handshake.
func Load(dir string) (*gtls.Info, error) {
	serverCert, err := tls.LoadX509KeyPair(filepath.Join(dir, serverCertFile), filepath.Join(dir, serverKeyFile))
	if err != nil {
		return nil, fmt.Errorf("failed to load the server key pair: %w", err)
	}

	clientCert, err := tls.LoadX509KeyPair(filepath.Join(dir, clientCertFile), filepath.Join(dir, clientKeyFile))
	if err != nil {
		return nil, fmt.Errorf("failed to load the client key pair: %w", err)
	}

	rootCAs, err := loadCertPool(filepath.Join(dir, caCertFile))
	if err != nil {
		return nil, err
	}

	clientCAs, err := loadCertPool(filepath.Join(dir, clientCACertFile))
	if err != nil {
		return nil, err
	}

	return &gtls.Info{
		ServerConfig: &tls.Config{
			Certificates: []tls.Certificate{serverCert},
			ClientAuth:   tls.RequireAndVerifyClientCert,
			ClientCAs:    clientCAs,
			RootCAs:      rootCAs,
			MinVersion:   tls.VersionTLS13,
			NextProtos:   []string{"h2", "http/1.1"},
		},
		ClientConfig: &tls.Config{
			Certificates: []tls.Certificate{clientCert},
			RootCAs:      rootCAs,
			MinVersion:   tls.VersionTLS13,
		},
	}, nil
}

// loadCertPool reads the PEM encoded certificate authority at path and returns it as a pool.
func loadCertPool(path string) (*x509.CertPool, error) {
	bs, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("failed to read the certificate authority %q: %w", path, err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(bs) {
		return nil, fmt.Errorf("no certificate found in %q", path)
	}

	return pool, nil
}
