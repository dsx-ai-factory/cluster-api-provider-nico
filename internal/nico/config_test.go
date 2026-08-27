// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nico

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/NVIDIA/cluster-api-provider-nico/internal/fake"
)

func TestLoadSecretConfigStaticToken(t *testing.T) {
	secret := &corev1.Secret{
		Data: map[string][]byte{
			SecretKeyEndpoint: []byte("https://nico.example.com"),
			SecretKeyOrgID:    []byte("test-org"),
			SecretKeyToken:    []byte("static-token"),
		},
	}

	cfg, err := LoadSecretConfig(secret)
	if err != nil {
		t.Fatalf("LoadSecretConfig() error = %v", err)
	}
	if cfg.Token != "static-token" {
		t.Fatalf("expected static token, got %q", cfg.Token)
	}
	if cfg.TokenURL != "" {
		t.Fatalf("expected empty tokenURL for static token config, got %q", cfg.TokenURL)
	}
}

func TestLoadSecretConfigOAuthClientCredentials(t *testing.T) {
	secret := &corev1.Secret{
		Data: map[string][]byte{
			SecretKeyEndpoint:     []byte("https://nico.example.com"),
			SecretKeyOrgID:        []byte("test-org"),
			SecretKeyTokenURL:     []byte("https://issuer.example.com/token"),
			SecretKeyClientID:     []byte("client-id"),
			SecretKeyClientSecret: []byte("client-secret"),
			SecretKeyScope:        []byte("carbide extra"),
		},
	}

	cfg, err := LoadSecretConfig(secret)
	if err != nil {
		t.Fatalf("LoadSecretConfig() error = %v", err)
	}
	if cfg.TokenURL != "https://issuer.example.com/token" {
		t.Fatalf("expected tokenURL to be loaded, got %q", cfg.TokenURL)
	}
	if got, want := strings.Join(cfg.Scopes, " "), "carbide extra"; got != want {
		t.Fatalf("expected scopes %q, got %q", want, got)
	}
}

func TestLoadSecretConfigRejectsMixedAuthModes(t *testing.T) {
	secret := &corev1.Secret{
		Data: map[string][]byte{
			SecretKeyEndpoint:     []byte("https://nico.example.com"),
			SecretKeyOrgID:        []byte("test-org"),
			SecretKeyToken:        []byte("static-token"),
			SecretKeyTokenURL:     []byte("https://issuer.example.com/token"),
			SecretKeyClientID:     []byte("client-id"),
			SecretKeyClientSecret: []byte("client-secret"),
		},
	}

	if _, err := LoadSecretConfig(secret); err == nil {
		t.Fatalf("expected mixed auth modes to be rejected")
	}
}

func TestTokenSourceStaticToken(t *testing.T) {
	cfg := SecretConfig{
		Endpoint: "https://nico.example.com",
		OrgID:    "test-org",
		Token:    "static-token",
	}

	httpClient, err := cfg.HTTPClient()
	if err != nil {
		t.Fatalf("HTTPClient() error = %v", err)
	}

	source, err := cfg.TokenSource(context.Background(), httpClient)
	if err != nil {
		t.Fatalf("TokenSource() error = %v", err)
	}

	token, err := source.Token()
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	if token.AccessToken != "static-token" {
		t.Fatalf("expected static token, got %q", token.AccessToken)
	}
}

func TestTokenSourceOAuthClientCredentials(t *testing.T) {
	api := fake.New()
	api.SeedClient("client-id", "client-secret")
	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)

	cfg := SecretConfig{
		Endpoint:     "https://nico.example.com",
		OrgID:        "test-org",
		TokenURL:     server.URL + "/token",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		Scopes:       []string{"carbide"},
	}

	httpClient, err := cfg.HTTPClient()
	if err != nil {
		t.Fatalf("HTTPClient() error = %v", err)
	}

	source, err := cfg.TokenSource(context.Background(), httpClient)
	if err != nil {
		t.Fatalf("TokenSource() error = %v", err)
	}

	token, err := source.Token()
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	if api.TokenRequestCount() != 1 {
		t.Fatalf("expected token endpoint to be called")
	}
	if token.AccessToken == "" {
		t.Fatalf("expected a non-empty access token")
	}
}
