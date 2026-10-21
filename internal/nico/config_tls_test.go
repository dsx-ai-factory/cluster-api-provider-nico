// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nico

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Exercise the custom ca.crt path with a key blocked by the Go fix for
// CVE-2026-111943. All certificates are synthetic and valid at test time.
func TestHTTPClientRejectsDistrustedCA(t *testing.T) {
	for _, mode := range []string{"trusted", "distrusted-root", "distrusted-intermediate"} {
		t.Run(mode, func(t *testing.T) {
			newKey := func(blocked bool) *ecdsa.PrivateKey {
				t.Helper()
				if !blocked {
					key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
					if err != nil {
						t.Fatal(err)
					}
					return key
				}
				// A deliberately trivial test key reproduces one of the eight SPKI
				// hashes blocked in Go 1.26.10. Do not use this key outside this test.
				scalar := make([]byte, 48)
				scalar[len(scalar)-1] = 4
				key, err := ecdsa.ParseRawPrivateKey(elliptic.P384(), scalar)
				if err != nil {
					t.Fatal(err)
				}
				spki, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
				if err != nil {
					t.Fatal(err)
				}
				const want = "63ecefee431d49523bc1136c4e72d0206a477086caf606cd5cf025686c109cf8"
				if got := fmt.Sprintf("%x", sha256.Sum256(spki)); got != want {
					t.Fatalf("test key SPKI hash = %s, want upstream distrusted hash %s", got, want)
				}
				return key
			}
			issue := func(serial int64, key *ecdsa.PrivateKey, parent *x509.Certificate, signer *ecdsa.PrivateKey, ca bool) *x509.Certificate {
				t.Helper()
				template := &x509.Certificate{
					SerialNumber: big.NewInt(serial),
					Subject:      pkix.Name{CommonName: fmt.Sprintf("synthetic-test-%d", serial)},
					NotBefore:    time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
					BasicConstraintsValid: true, IsCA: ca, KeyUsage: x509.KeyUsageDigitalSignature,
				}
				if ca {
					template.KeyUsage |= x509.KeyUsageCertSign
				} else {
					template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
					template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
				}
				if parent == nil {
					parent, signer = template, key
				}
				der, err := x509.CreateCertificate(rand.Reader, template, parent, &key.PublicKey, signer)
				if err != nil {
					t.Fatal(err)
				}
				cert, err := x509.ParseCertificate(der)
				if err != nil {
					t.Fatal(err)
				}
				return cert
			}
			rootKey := newKey(mode == "distrusted-root")
			root := issue(1, rootKey, nil, nil, true)
			issuer, issuerKey := root, rootKey
			var intermediate *x509.Certificate
			if mode == "distrusted-intermediate" {
				issuerKey = newKey(true)
				intermediate = issue(2, issuerKey, root, rootKey, true)
				issuer = intermediate
			}
			leafKey := newKey(false)
			leaf := issue(3, leafKey, issuer, issuerKey, false)
			chain := [][]byte{leaf.Raw}
			if intermediate != nil {
				chain = append(chain, intermediate.Raw)
			}
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
			server.Config.ErrorLog = log.New(io.Discard, "", 0)
			server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: chain, PrivateKey: leafKey}}}
			server.StartTLS()
			t.Cleanup(server.Close)

			client, err := (SecretConfig{CA: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: root.Raw})}).HTTPClient()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(client.CloseIdleConnections)
			resp, err := client.Get(server.URL)
			if resp != nil {
				if err := resp.Body.Close(); err != nil {
					t.Fatalf("closing response body: %v", err)
				}
			}
			if mode == "trusted" {
				if err != nil {
					t.Fatalf("valid custom CA rejected: %v", err)
				}
				if resp.StatusCode != http.StatusNoContent {
					t.Fatalf("status = %d", resp.StatusCode)
				}
				return
			}
			if err == nil {
				t.Fatal("TLS accepted a chain containing a distrusted CA key")
			}
			var invalid x509.CertificateInvalidError
			if !errors.As(err, &invalid) || invalid.Reason != x509.NoValidChains {
				t.Fatalf("expected distrust rejection (NoValidChains), got %v", err)
			}
		})
	}
}
