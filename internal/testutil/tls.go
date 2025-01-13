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

package testutil

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" // nolint
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// CertRoot defines the CA root
// This include the CA key and CA certificate
type CertRoot struct {
	key  crypto.Signer
	cert *x509.Certificate
}

// Key return the root certificate key
func (x CertRoot) Key() crypto.Signer {
	return x.key
}

// Cert returns the root certificate
func (x CertRoot) Cert() *x509.Certificate {
	return x.cert
}

// NewCertRoot creates an instance of CertRoot
// useful for unit tests
func NewCertRoot(t *testing.T) *CertRoot {
	key, _ := generateKey(t)
	cert, _ := generateRootCert(t, key)
	return &CertRoot{
		key:  key,
		cert: cert,
	}
}

// GetClientTLSConfig creates a client TLS configuration for unit tests
func GetClientTLSConfig(t *testing.T, root *CertRoot) *tls.Config {
	keyPEM, csrPEM := generateKeyAndCSR(t)
	signCSR := signCSR(t, csrPEM, root.Key(), root.Cert())
	certificate, err := tls.X509KeyPair(signCSR, keyPEM)
	require.NoError(t, err)

	certPool := x509.NewCertPool()
	certPool.AddCert(root.Cert())

	return &tls.Config{
		Certificates: []tls.Certificate{certificate},
		RootCAs:      certPool,
		MinVersion:   tls.VersionTLS13,
		CurvePreferences: []tls.CurveID{
			tls.CurveP521,
			tls.CurveP384,
			tls.CurveP256,
		},
	}
}

// GetServerTLSConfig creates a server TLS configuration for unit tests
func GetServerTLSConfig(t *testing.T, root *CertRoot) *tls.Config {
	keyPEM, csrPEM := generateKeyAndCSR(t)
	signCSR := signCSR(t, csrPEM, root.Key(), root.Cert())
	certificate, err := tls.X509KeyPair(signCSR, keyPEM)
	require.NoError(t, err)

	certPool := x509.NewCertPool()
	certPool.AddCert(root.Cert())

	return &tls.Config{
		Certificates: []tls.Certificate{certificate},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    certPool,
		MinVersion:   tls.VersionTLS13,
		CurvePreferences: []tls.CurveID{
			tls.CurveP521,
			tls.CurveP384,
			tls.CurveP256,
		},
	}
}

// generateRootCert generates a root certificate for unit tests
func generateRootCert(t *testing.T, key crypto.Signer) (*x509.Certificate, []byte) {
	subjectKeyIdentifier := calculateSubjectKeyIdentifier(t, key.Public())

	template := &x509.Certificate{
		SerialNumber:          generateSerial(t),
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		SubjectKeyId:          subjectKeyIdentifier,
		AuthorityKeyId:        subjectKeyIdentifier,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}

	rootCert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}

	rootCertPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: der,
	})

	return rootCert, rootCertPEM
}

// signCSR signs a certificate signing request with the given CA certificate and private key
func signCSR(t *testing.T, csr []byte, caKey crypto.Signer, caCert *x509.Certificate) []byte {
	block, _ := pem.Decode(csr)
	if block == nil {
		t.Fatal("failed to decode csr")
	}

	certificateRequest, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}

	if err := certificateRequest.CheckSignature(); err != nil {
		t.Fatal(err)
	}

	template := x509.Certificate{
		Subject:               certificateRequest.Subject,
		PublicKeyAlgorithm:    certificateRequest.PublicKeyAlgorithm,
		PublicKey:             certificateRequest.PublicKey,
		SignatureAlgorithm:    certificateRequest.SignatureAlgorithm,
		Signature:             certificateRequest.Signature,
		SerialNumber:          generateSerial(t),
		Issuer:                caCert.Issuer,
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		SubjectKeyId:          calculateSubjectKeyIdentifier(t, certificateRequest.PublicKey),
		BasicConstraintsValid: true,
		IPAddresses:           certificateRequest.IPAddresses,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, caCert, certificateRequest.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
}

// generateKey generates a 2048-bit RSA private key
func generateKey(t *testing.T) (crypto.Signer, []byte) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	return key, keyPEM
}

func generateKeyAndCSR(t *testing.T) ([]byte, []byte) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	key := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(rsaKey),
	})

	template := &x509.CertificateRequest{
		SignatureAlgorithm: x509.SHA256WithRSA,
		IPAddresses:        []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}

	req, err := x509.CreateCertificateRequest(rand.Reader, template, rsaKey)
	if err != nil {
		t.Fatal(err)
	}

	csr := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE REQUEST",
		Bytes: req,
	})

	return key, csr
}

// generateSerial generates a serial number using the maximum number of octets (20) allowed by RFC 5280 4.1.2.2
// (Adapted from https://github.com/cloudflare/cfssl/blob/828c23c22cbca1f7632b9ba85174aaa26e745340/signer/local/local.go#L407-L418)
func generateSerial(t *testing.T) *big.Int {
	serialNumber := make([]byte, 20)
	_, err := io.ReadFull(rand.Reader, serialNumber)
	if err != nil {
		t.Fatal(err)
	}

	return new(big.Int).SetBytes(serialNumber)
}

// calculateSubjectKeyIdentifier implements a common method to generate a key identifier
// from a public key, namely, by composing it from the 160-bit SHA-1 hash of the bit string
// of the public key (cf. https://tools.ietf.org/html/rfc5280#section-4.2.1.2).
// (Adapted from https://github.com/jsha/minica/blob/master/main.go).
func calculateSubjectKeyIdentifier(t *testing.T, pubKey crypto.PublicKey) []byte {
	spkiASN1, err := x509.MarshalPKIXPublicKey(pubKey)
	if err != nil {
		t.Fatal(err)
	}

	var spki struct {
		Algorithm        pkix.AlgorithmIdentifier
		SubjectPublicKey asn1.BitString
	}
	_, err = asn1.Unmarshal(spkiASN1, &spki)
	if err != nil {
		t.Fatal(err)
	}

	// nolint
	skid := sha1.Sum(spki.SubjectPublicKey.Bytes)
	return skid[:]
}
