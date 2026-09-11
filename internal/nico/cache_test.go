// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nico

import (
	"context"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/fake"
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
	api := fake.New()
	api.SeedClient("client-id", "client-secret")
	api.SeedTenant("test-org", testTenant())
	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)

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

	replacement := testTenant()
	replacement.SetId("tenant-2")
	api.SeedTenant("test-org", replacement)

	tenantID, err = client.ResolveTenantID(context.Background())
	if err != nil {
		t.Fatalf("ResolveTenantID() second call error = %v", err)
	}
	if tenantID != testTenantID {
		t.Fatalf("expected tenant-1 on second call, got %q", tenantID)
	}

	if got := api.TokenRequestCount(); got != 1 {
		t.Fatalf("expected token endpoint to be called once, got %d", got)
	}
}
