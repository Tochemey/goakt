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

package tlstest

import (
	"context"
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const fixtureDir = "../../test/data/certs"

// TestLoad asserts the configurations Load builds out of the fixture directory.
func TestLoad(t *testing.T) {
	info, err := Load(fixtureDir)
	require.NoError(t, err)
	require.NotNil(t, info)

	serverConfig := info.ServerConfig
	require.NotNil(t, serverConfig)
	require.Len(t, serverConfig.Certificates, 1)
	require.Equal(t, tls.RequireAndVerifyClientCert, serverConfig.ClientAuth)
	require.NotNil(t, serverConfig.ClientCAs)
	require.NotNil(t, serverConfig.RootCAs)
	require.Equal(t, uint16(tls.VersionTLS13), serverConfig.MinVersion)
	require.Equal(t, []string{"h2", "http/1.1"}, serverConfig.NextProtos)

	clientConfig := info.ClientConfig
	require.NotNil(t, clientConfig)
	require.Len(t, clientConfig.Certificates, 1)
	require.NotNil(t, clientConfig.RootCAs)
	require.False(t, clientConfig.InsecureSkipVerify)
	require.Equal(t, uint16(tls.VersionTLS13), clientConfig.MinVersion)
}

// TestLoadHandshake runs a mutual TLS handshake over a local listener to prove both sides of the
// fixtures verify each other.
func TestLoadHandshake(t *testing.T) {
	info, err := Load(fixtureDir)
	require.NoError(t, err)

	listener, err := tls.Listen("tcp", "127.0.0.1:0", info.ServerConfig)
	require.NoError(t, err)

	defer func() {
		require.NoError(t, listener.Close())
	}()

	serverErrs := make(chan error, 1)

	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErrs <- acceptErr
			return
		}

		defer func() {
			_ = conn.Close()
		}()

		serverErrs <- conn.(*tls.Conn).Handshake()
	}()

	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 10 * time.Second},
		Config:    info.ClientConfig,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := dialer.DialContext(ctx, "tcp", listener.Addr().String())
	require.NoError(t, err)
	require.NoError(t, conn.(*tls.Conn).HandshakeContext(ctx))
	require.NoError(t, conn.Close())

	select {
	case serverErr := <-serverErrs:
		require.NoError(t, serverErr)
	case <-time.After(10 * time.Second):
		require.Fail(t, "timed out waiting for the server handshake")
	}
}

// TestLoadMissingDirectory asserts Load fails when the fixture directory holds no certificate.
func TestLoadMissingDirectory(t *testing.T) {
	info, err := Load(t.TempDir())
	require.Error(t, err)
	require.Nil(t, info)
}

// TestLoadInvalidCA asserts Load fails, naming the file, when the server CA holds no certificate.
func TestLoadInvalidCA(t *testing.T) {
	dir := copyFixtures(t, caCertFile)
	require.NoError(t, os.WriteFile(filepath.Join(dir, caCertFile), []byte("not a certificate"), 0o600))

	info, err := Load(dir)
	require.Error(t, err)
	require.Nil(t, info)
	require.Contains(t, err.Error(), filepath.Join(dir, caCertFile))
}

// TestLoadInvalidKeyPair asserts Load fails when the server private key is not readable as a key.
func TestLoadInvalidKeyPair(t *testing.T) {
	dir := copyFixtures(t, serverKeyFile)
	require.NoError(t, os.WriteFile(filepath.Join(dir, serverKeyFile), []byte("not a key"), 0o600))

	info, err := Load(dir)
	require.Error(t, err)
	require.Nil(t, info)
}

// copyFixtures copies every fixture file but the excluded one into a temporary directory and
// returns that directory, so a test can supply its own broken version of the excluded file.
func copyFixtures(t *testing.T, excluded string) string {
	t.Helper()

	dir := t.TempDir()

	for _, name := range []string{caCertFile, serverCertFile, serverKeyFile, clientCACertFile, clientCertFile, clientKeyFile} {
		if name == excluded {
			continue
		}

		bs, err := os.ReadFile(filepath.Join(fixtureDir, name))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), bs, 0o600))
	}

	return dir
}
