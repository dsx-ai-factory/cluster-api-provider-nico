// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nico

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputePublicKeyHash(t *testing.T) {
	t.Run("valid EK cert", func(t *testing.T) {
		ekCert := generateTestEKCert(t, 789)
		certBundleBase64 := base64.StdEncoding.EncodeToString(ekCert.Raw)

		hash, err := ComputePublicKeyHash(certBundleBase64)
		require.NoError(t, err)

		require.NotNil(t, hash)
		assert.Len(t, hash, 64)
	})

	t.Run("RSA public key", func(t *testing.T) {
		ekCert := generateRSACert(t, 999)
		certBundleBase64 := base64.StdEncoding.EncodeToString(ekCert.Raw)

		hash, err := ComputePublicKeyHash(certBundleBase64)
		require.NoError(t, err)

		require.NotNil(t, hash)
		assert.Len(t, hash, 64)
	})

	t.Run("cert bundle with malformed first cert serial number", func(t *testing.T) {
		// This is a synthetically generated bad cert similar to one that we saw in the field.
		// It has a RSA serial number that is not minimally encoded, making it technically
		// invalid and causing the strict parsing in x509.ParseCertificates to fail.
		const syntheticMalformedSerialCertBundleBase64 = `
			MIIDBTCCAe2gAwIBAgIDAATSMA0GCSqGSIb3DQEBCwUAMDQxETAPBgNVBAoTCFRlc3Qg
			T3JnMR8wHQYDVQQDExZSU0EgVFBNIEVLIENlcnRpZmljYXRlMB4XDTI2MDQyMjE5MTk0
			MVoXDTI2MDQyMzIwMTk0MVowNDERMA8GA1UEChMIVGVzdCBPcmcxHzAdBgNVBAMTFlJT
			QSBUUE0gRUsgQ2VydGlmaWNhdGUwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIB
			AQDYtDzkuG4xgV3jXqjAX8RObDkuGQwNgH9ycmsCiQ7GJzEoX8+kYJqZNM1Ai4TLLL+d
			FjmY2Iihdw/OpMSF0nBsFZ6DlgdGiRXcyGn5NtF4+k5JE52ykq2gV0fifUGQY0KVD6+F
			XVyOF9tavwwjtLlE4HCyYleGPA3THFnriBawQf3QI/RHZyjshArIkNF0RGxa0hkggmia
			j+s5t7/Orwo65ruULz4SqnH5XA+RWpwuOfPR1F2QbBZ/bHcxDxf8z6C50gFqiqbVuZwZ
			Zu8tB3opPUnE0AimqHKdsjXxEy85PnsnYhKl1s8V/IjJIwTZyIeXpDyR9g4m0AJn50Aj
			OfmhAgMBAAGjIDAeMA4GA1UdDwEB/wQEAwIFoDAMBgNVHRMBAf8EAjAAMA0GCSqGSIb3
			DQEBCwUAA4IBAQCUEbUBGia8wnywyRp04S7KsRDjR12GvmrimCS1eKpAefFmS9LVqLvP
			fZJpWo4rPrhzZnb07W/z99tAkAwiE7+d9OQAAHFcya7DuO4rb54IjDMay9yzAwvOj9pC
			siNIIv+jeGShhAjD4CFKbN755/laAasgyaRAWRCKRH7VIu1Cf9gUZ0bo0MWPqZ38t05/
			m1qThiJyhi/cwkM8SiA3pVvz4wlW+SchVwX5CkuaXj/ATRY5cQd7Z+gSdl8lLEoYMhHK
			juear5vF3NRNIRGH9fOALo6ZAJw2GnhaAl8h1cRn2hk70ouwxTM99OYYzZT92KWSH2ia
			1+HbwbdmXqJ/saEeMIIBnjCCAUSgAwIBAgICFi4wCgYIKoZIzj0EAwIwKDEUMBIGA1UE
			ChMLVGVzdCBDQSBPcmcxEDAOBgNVBAMTB1Rlc3QgQ0EwHhcNMjYwNDIyMTkxOTQxWhcN
			MjYwNDIzMjAxOTQxWjAwMREwDwYDVQQKEwhUZXN0IE9yZzEbMBkGA1UEAxMSVFBNIEVL
			IENlcnRpZmljYXRlMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAExPsiyS+YWd3Tpcf6
			gBJZMX8YimC7mhXDd4zka89rJF0Fs/pOK+0i8/gbNUpSKT3CJAVdP2VrOpiMiK7nBKaB
			qqNWMFQwDgYDVR0PAQH/BAQDAgWgMBMGA1UdJQQMMAoGCCsGAQUFBwMBMAwGA1UdEwEB
			/wQCMAAwHwYDVR0jBBgwFoAURL8PMxtrSggUCD97EkTs0jjAErcwCgYIKoZIzj0EAwID
			SAAwRQIhALCqEXWNfNsvZHc7qmm2gviDFP+yRTn+S9Fy/5DbWmV2AiAzA+ytuevK5ata
			Uguc/p7Oo2wMyVmkiA8jWnxREhWtUg==
		`
		const syntheticMalformedSerialCertBundleFirstHash = "df0300fdd50d499482abe2c3e4cabf0d8055c8d3131e1c221f1ab1d01ad59159"
		hash, err := ComputePublicKeyHash(syntheticMalformedSerialCertBundleBase64)
		require.NoError(t, err)

		// The first cert in this synthetic bundle intentionally uses a non-minimal
		// serialNumber encoding. ComputePublicKeyHash should still hash its
		// subjectPublicKeyInfo instead of failing or skipping to the second cert.
		assert.Equal(t, syntheticMalformedSerialCertBundleFirstHash, hash)
	})

	t.Run("multi-cert bundle uses first cert", func(t *testing.T) {
		rsaCert := generateRSACert(t, 4321)
		eccCert := generateTestEKCert(t, 8765)
		firstCertBase64 := base64.StdEncoding.EncodeToString(rsaCert.Raw)
		certBundleBase64 := base64.StdEncoding.EncodeToString(append(rsaCert.Raw, eccCert.Raw...))

		firstCertHash, err := ComputePublicKeyHash(firstCertBase64)
		require.NoError(t, err)

		hash, err := ComputePublicKeyHash(certBundleBase64)
		require.NoError(t, err)

		assert.Equal(t, firstCertHash, hash)
	})

	t.Run("invalid base64", func(t *testing.T) {
		_, err := ComputePublicKeyHash("not-valid-base64!!!")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to base64 decode")
	})

	t.Run("invalid certificate data", func(t *testing.T) {
		invalidData := base64.StdEncoding.EncodeToString([]byte("not a certificate"))
		_, err := ComputePublicKeyHash(invalidData)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse TPM EK certificate")
	})

	t.Run("empty certificate data", func(t *testing.T) {
		emptyData := base64.StdEncoding.EncodeToString([]byte{})
		_, err := ComputePublicKeyHash(emptyData)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no certificates found")
	})

	t.Run("deterministic hash", func(t *testing.T) {
		ekCert := generateTestEKCert(t, 54321)
		certBundleBase64 := base64.StdEncoding.EncodeToString(ekCert.Raw)

		hash1, err := ComputePublicKeyHash(certBundleBase64)
		require.NoError(t, err)

		hash2, err := ComputePublicKeyHash(certBundleBase64)
		require.NoError(t, err)

		assert.Equal(t, hash1, hash2)
	})

	t.Run("different certs produce different hashes", func(t *testing.T) {
		ekCert1 := generateTestEKCert(t, 111)
		ekCert2 := generateTestEKCert(t, 222)

		hash1, err := ComputePublicKeyHash(base64.StdEncoding.EncodeToString(ekCert1.Raw))
		require.NoError(t, err)

		hash2, err := ComputePublicKeyHash(base64.StdEncoding.EncodeToString(ekCert2.Raw))
		require.NoError(t, err)

		assert.NotEqual(t, hash1, hash2)
	})

	t.Run("invalid input", func(t *testing.T) {
		_, err := ComputePublicKeyHash("invalid-base64")
		require.Error(t, err)
	})
}

// Test helpers

func generateTestEKCert(t *testing.T, serialNumber int64) *x509.Certificate {
	t.Helper()

	caCert, caPrivKey := generateRootCAWithKey(t)

	ekPrivKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	ekTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(serialNumber),
		Subject: pkix.Name{
			CommonName:   "TPM EK Certificate",
			Organization: []string{"Test Org"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
	}

	ekCertDER, err := x509.CreateCertificate(rand.Reader, ekTemplate, caCert, &ekPrivKey.PublicKey, caPrivKey)
	require.NoError(t, err)

	ekCert, err := x509.ParseCertificate(ekCertDER)
	require.NoError(t, err)

	return ekCert
}

func generateRootCAWithKey(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()

	caPrivKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   "Test CA",
			Organization: []string{"Test CA Org"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caPrivKey.PublicKey, caPrivKey)
	require.NoError(t, err)

	caCert, err := x509.ParseCertificate(caCertDER)
	require.NoError(t, err)

	return caCert, caPrivKey
}

func generateRSACert(t *testing.T, serialNumber int64) *x509.Certificate {
	t.Helper()

	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(serialNumber),
		Subject: pkix.Name{
			CommonName:   "RSA TPM EK Certificate",
			Organization: []string{"Test Org"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  false,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privKey.PublicKey, privKey)
	require.NoError(t, err)

	cert, err := x509.ParseCertificate(certDER)
	require.NoError(t, err)

	return cert
}
