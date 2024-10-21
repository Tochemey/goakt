/*
 * MIT License
 *
 * Copyright (c) 2024 Tochemey
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

package lib

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"time"

	"github.com/tochemey/goakt/v2/internal/types"
)

// Pause pauses the running process for some time period
func Pause(duration time.Duration) {
	stopCh := make(chan types.Unit, 1)
	timer := time.AfterFunc(duration, func() {
		stopCh <- types.Unit{}
	})
	<-stopCh
	timer.Stop()
}

// FileExists tries to report whether a local physical file of "filename" exists.
func FileExists(filename string) bool {
	info, err := os.Stat(filename)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// GetServerTLSConfig returns a server TLS config
func GetServerTLSConfig(certifcate *tls.Certificate) *tls.Config {
	config := getTLSConfig()
	if certifcate != nil {
		config.ClientAuth = tls.RequireAndVerifyClientCert
		config.Certificates = []tls.Certificate{*certifcate}
		config.GetCertificate = func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return certifcate, nil
		}
	}
	return config
}

// GetClientTLSConfig returns the client TLS config
func GetClientTLSConfig(rootCA []byte) *tls.Config {
	config := getTLSConfig()
	if len(rootCA) == 0 {
		config.InsecureSkipVerify = true
	}
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(rootCA)
	config.RootCAs = caCertPool
	return config
}

func getTLSConfig() *tls.Config {
	return &tls.Config{
		NextProtos: []string{"h2", "http/1.1"},
		CurvePreferences: []tls.CurveID{
			tls.CurveP521,
			tls.CurveP384,
			tls.CurveP256,
		},
		CipherSuites: []uint16{
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_CHACHA20_POLY1305_SHA256,
			tls.TLS_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			0xC028, /* TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA384 */
		},
		// TLS versions below 1.2 are considered insecure - see https://www.rfc-editor.org/rfc/rfc7525.txt for details
		MinVersion: tls.VersionTLS12,
	}
}
