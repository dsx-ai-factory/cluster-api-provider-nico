// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nico

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestClientCacheReusesClientForSameSecretRevision(t *testing.T) {
	cache := NewClientCache()
	secret := &corev1.Secret{}
	secret.Namespace = "default"
	secret.Name = "nico-credentials"
	secret.ResourceVersion = "1"

	cfg := SecretConfig{
		Endpoint: "https://nico.example.com",
		OrgID:    "test-org",
		Token:    "static-token",
	}

	first, err := cache.GetOrCreate(context.Background(), secret, cfg)
	if err != nil {
		t.Fatalf("GetOrCreate() error = %v", err)
	}
	second, err := cache.GetOrCreate(context.Background(), secret, cfg)
	if err != nil {
		t.Fatalf("GetOrCreate() error = %v", err)
	}

	if first != second {
		t.Fatalf("expected cached client instance to be reused")
	}
}

func TestClientCacheInvalidatesOnSecretRevisionChange(t *testing.T) {
	cache := NewClientCache()
	cfg := SecretConfig{
		Endpoint: "https://nico.example.com",
		OrgID:    "test-org",
		Token:    "static-token",
	}

	oldSecret := &corev1.Secret{}
	oldSecret.Namespace = "default"
	oldSecret.Name = "nico-credentials"
	oldSecret.ResourceVersion = "1"

	newSecret := oldSecret.DeepCopy()
	newSecret.ResourceVersion = "2"

	first, err := cache.GetOrCreate(context.Background(), oldSecret, cfg)
	if err != nil {
		t.Fatalf("GetOrCreate(old) error = %v", err)
	}
	second, err := cache.GetOrCreate(context.Background(), newSecret, cfg)
	if err != nil {
		t.Fatalf("GetOrCreate(new) error = %v", err)
	}

	if first == second {
		t.Fatalf("expected a new client for a new secret resourceVersion")
	}
}

func TestClientResolveTenantIDCachesDiscovery(t *testing.T) {
	var tokenCalls int
	var tenantCalls int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			tokenCalls++
			expectedAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("client-id:client-secret"))
			if got := r.Header.Get("Authorization"); got != expectedAuth {
				t.Fatalf("expected Authorization header %q, got %q", expectedAuth, got)
			}
			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("ReadAll() error = %v", err)
			}
			values, err := url.ParseQuery(string(bodyBytes))
			if err != nil {
				t.Fatalf("ParseQuery() error = %v", err)
			}
			if got := values.Get("grant_type"); got != "client_credentials" {
				t.Fatalf("expected client_credentials grant type, got %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"dynamic-token","token_type":"Bearer","expires_in":3600}`))
		case testTenantPath:
			tenantCalls++
			if got := r.Header.Get("Authorization"); got != "Bearer dynamic-token" {
				t.Fatalf("expected bearer token header, got %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"tenant-1"}`))
		default:
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	cfg := SecretConfig{
		Endpoint:     server.URL,
		OrgID:        "test-org",
		TokenURL:     server.URL + "/token",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		Scopes:       []string{"carbide"},
	}

	client, err := NewClient(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	tenantID, err := client.ResolveTenantID(context.Background())
	if err != nil {
		t.Fatalf("ResolveTenantID() error = %v", err)
	}
	if tenantID != testTenantID {
		t.Fatalf("expected tenant-1, got %q", tenantID)
	}

	tenantID, err = client.ResolveTenantID(context.Background())
	if err != nil {
		t.Fatalf("ResolveTenantID() second call error = %v", err)
	}
	if tenantID != testTenantID {
		t.Fatalf("expected tenant-1 on second call, got %q", tenantID)
	}

	if tokenCalls != 1 {
		t.Fatalf("expected token endpoint to be called once, got %d", tokenCalls)
	}
	if tenantCalls != 1 {
		t.Fatalf("expected tenant API to be called once, got %d", tenantCalls)
	}
}
