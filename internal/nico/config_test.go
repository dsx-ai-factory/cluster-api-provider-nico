// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nico

import (
	"context"
	"flag"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/fake"
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

func TestProviderConfigIdentityFlags(t *testing.T) {
	credentials := types.NamespacedName{Namespace: "capnico-system", Name: "nico-credentials"}
	tests := []struct {
		name        string
		credentials types.NamespacedName
		args        []string
		wantName    string
		wantTimeout time.Duration
		wantErr     string
	}{
		{
			name:        "defaults disable observation with a 30-second timeout",
			credentials: credentials,
			wantTimeout: 30 * time.Second,
		},
		{
			name:        "explicit selection and timeout",
			credentials: credentials,
			args:        []string{"--provider-identity-name=nico-default", "--provider-identity-validation-timeout=45s"},
			wantName:    "nico-default",
			wantTimeout: 45 * time.Second,
		},
		{
			name:        "shorter timeout",
			credentials: credentials,
			args:        []string{"--provider-identity-name=nico-default", "--provider-identity-validation-timeout=500ms"},
			wantName:    "nico-default",
			wantTimeout: 500 * time.Millisecond,
		},
		{
			name:        "zero timeout is rejected even when observation is disabled",
			credentials: credentials,
			args:        []string{"--provider-identity-validation-timeout=0s"},
			wantErr:     "--provider-identity-validation-timeout must be positive",
		},
		{
			name:        "negative timeout is rejected",
			credentials: credentials,
			args:        []string{"--provider-identity-name=nico-default", "--provider-identity-validation-timeout=-1s"},
			wantErr:     "--provider-identity-validation-timeout must be positive",
		},
		{
			name:        "invalid Identity name is rejected",
			credentials: credentials,
			args:        []string{"--provider-identity-name=Nico_Default"},
			wantErr:     `--provider-identity-name "Nico_Default"`,
		},
		{
			name:    "selection requires a default credentials Secret",
			args:    []string{"--provider-identity-name=nico-default"},
			wantErr: "requires a provider credentials namespace and Secret name",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ProviderConfig{Credentials: tc.credentials}
			fs := flag.NewFlagSet("manager", flag.ContinueOnError)
			cfg.BindFlags(fs)
			if err := fs.Parse(tc.args); err != nil {
				t.Fatalf("Parse() error = %v", err)
			}

			err := cfg.Validate()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Validate() error = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if cfg.IdentityName != tc.wantName {
				t.Fatalf("IdentityName = %q, want %q", cfg.IdentityName, tc.wantName)
			}
			if cfg.IdentityValidationTimeout != tc.wantTimeout {
				t.Fatalf("IdentityValidationTimeout = %s, want %s", cfg.IdentityValidationTimeout, tc.wantTimeout)
			}
		})
	}
}
