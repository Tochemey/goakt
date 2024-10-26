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
 *
 */

package actors

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMTLS(t *testing.T) {
	t.Run("With new instance", func(t *testing.T) {
		m := NewTLS(&x509.CertPool{}, &tls.Certificate{})
		assert.NotNil(t, m)
	})
	t.Run("With new instance from PEM bocks", func(t *testing.T) {
		// create a CA certicate
		ca := &x509.Certificate{
			SerialNumber: big.NewInt(2019),
			Subject: pkix.Name{
				Organization: []string{"Contoso, INC."},
			},
			NotBefore:             time.Now(),
			NotAfter:              time.Now().AddDate(10, 0, 0),
			IsCA:                  true, // this specifies it is a root certificate
			ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
			KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
			BasicConstraintsValid: true,
		}
		// generate a public and private key for the CA
		caPrivateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)
		// sign the CA
		cabytes, err := x509.CreateCertificate(rand.Reader, ca, ca, &caPrivateKey.PublicKey, caPrivateKey)
		require.NoError(t, err)
		// create the CA PEM
		caPEM := bytes.NewBuffer(nil)
		err = pem.Encode(caPEM, &pem.Block{Type: "CERTIFICATE", Bytes: cabytes})
		require.NoError(t, err)

		// create the private key PEM
		caPrivateKeyPEM := bytes.NewBuffer(nil)
		caPrivateKeyBytes, err := x509.MarshalPKCS8PrivateKey(caPrivateKey)
		require.NoError(t, err)
		err = pem.Encode(caPrivateKeyPEM, &pem.Block{Type: "PRIVATE KEY", Bytes: caPrivateKeyBytes})
		require.NoError(t, err)

		// create the actual certificate
		cert := &x509.Certificate{
			SerialNumber: big.NewInt(1658),
			Subject: pkix.Name{
				Organization: []string{"\"Contoso, INC."},
			},
			IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
			NotBefore:    time.Now(),
			NotAfter:     time.Now().AddDate(10, 0, 0),
			SubjectKeyId: []byte{1, 2, 3, 4, 6},
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
			KeyUsage:     x509.KeyUsageDigitalSignature,
		}

		// generate a public and private key for the certificate
		certPrivateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)

		// sign the certificate with the root CA
		certBytes, err := x509.CreateCertificate(rand.Reader, cert, ca, &certPrivateKey.PublicKey, caPrivateKey)
		require.NoError(t, err)
		certPEM := bytes.NewBuffer(nil)
		err = pem.Encode(certPEM, &pem.Block{Type: "CERTIFICATE", Bytes: certBytes})
		require.NoError(t, err)

		certPrivKeyPEM := new(bytes.Buffer)
		certPrivateKeyBytes, err := x509.MarshalPKCS8PrivateKey(certPrivateKey)
		require.NoError(t, err)
		err = pem.Encode(certPrivKeyPEM, &pem.Block{
			Type:  "PRIVATE KEY",
			Bytes: certPrivateKeyBytes,
		})
		require.NoError(t, err)

		m, err := NewTLSFromPEMBlocks(caPEM.Bytes(), certPrivKeyPEM.Bytes(), certPEM.Bytes())
		require.NoError(t, err)
		assert.NotNil(t, m)

		clientConf := m.ClientConfig()
		serverConf := m.ServerConfig()
		assert.NotNil(t, clientConf)
		assert.NotNil(t, serverConf)
	})
	t.Run("With new instance from PEM bocks with invalid PEM blocks", func(t *testing.T) {
		m, err := NewTLSFromPEMBlocks(nil, nil, nil)
		require.Error(t, err)
		assert.Nil(t, m)
	})
}
