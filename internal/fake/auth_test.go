// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package fake

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	nicosdk "github.com/NVIDIA/infra-controller/rest-api/sdk/standard"

	"github.com/NVIDIA/cluster-api-provider-nico/internal/nico"
)

func TestStaticTokenAuthentication(t *testing.T) {
	server := New()
	server.SeedToken("expected-token")
	server.SeedTenant(testOrgID, testTenant())
	endpoint := httptest.NewServer(server.Handler())
	t.Cleanup(endpoint.Close)

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint.URL+tenantPath(testOrgID), nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	response, err := endpoint.Client().Do(request)
	if err != nil {
		t.Fatalf("request without token: %v", err)
	}
	closeResponseBody(t, response)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("request without token status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}

	request, err = http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint.URL+tenantPath(testOrgID), nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer wrong-token")
	response, err = endpoint.Client().Do(request)
	if err != nil {
		t.Fatalf("request with wrong token: %v", err)
	}
	closeResponseBody(t, response)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("request with wrong token status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}

	client := newStaticClient(t, endpoint.URL, "expected-token")
	tenantID, err := client.ResolveTenantID(t.Context())
	if err != nil {
		t.Fatalf("resolve tenant with configured token: %v", err)
	}
	if tenantID != testTenantID {
		t.Fatalf("tenant ID = %q, want %q", tenantID, testTenantID)
	}
}

func TestOAuthClientCredentialsAndTokenReuse(t *testing.T) {
	server := New()
	server.SeedClient("client+id", "s/e=cret+")
	server.SeedTenant(testOrgID, testTenant())
	server.SeedVPC(testOrgID, testVPC())
	endpoint := httptest.NewServer(server.Handler())
	t.Cleanup(endpoint.Close)

	client, err := nico.NewClient(t.Context(), nico.SecretConfig{
		Endpoint:     endpoint.URL,
		OrgID:        testOrgID,
		TokenURL:     endpoint.URL + tokenPath,
		ClientID:     "client+id",
		ClientSecret: "s/e=cret+",
	})
	if err != nil {
		t.Fatalf("build OAuth client: %v", err)
	}

	if _, err := client.ResolveTenantID(t.Context()); err != nil {
		t.Fatalf("resolve tenant: %v", err)
	}
	if _, err := client.GetVPC(t.Context(), testVPCID); err != nil {
		t.Fatalf("get VPC: %v", err)
	}
	if got := server.TokenRequestCount(); got != 1 {
		t.Fatalf("token request count = %d, want 1", got)
	}
}

func TestOAuthAcceptsClientSecretPost(t *testing.T) {
	server := New()
	server.SeedClient("client-id", "client-secret")
	endpoint := httptest.NewServer(server.Handler())
	t.Cleanup(endpoint.Close)

	form := url.Values{
		"grant_type":    {grantTypeClientCredentials},
		"client_id":     {"client-id"},
		"client_secret": {"client-secret"},
		"scope":         {"carbide"},
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint.URL+tokenPath, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build token request: %v", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := endpoint.Client().Do(request)
	if err != nil {
		t.Fatalf("send token request: %v", err)
	}
	closeResponseBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("token request status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if got := server.TokenRequestCount(); got != 1 {
		t.Fatalf("token request count = %d, want 1", got)
	}
}

func TestOAuthRejectsInvalidClientCredentials(t *testing.T) {
	server := New()
	server.SeedClient("expected-client", "expected-secret")
	server.SeedTenant(testOrgID, testTenant())
	endpoint := httptest.NewServer(server.Handler())
	t.Cleanup(endpoint.Close)

	client, err := nico.NewClient(context.Background(), nico.SecretConfig{
		Endpoint:     endpoint.URL,
		OrgID:        testOrgID,
		TokenURL:     endpoint.URL + tokenPath,
		ClientID:     "wrong-client",
		ClientSecret: "wrong-secret",
	})
	if err != nil {
		t.Fatalf("build OAuth client: %v", err)
	}
	if _, err := client.ResolveTenantID(t.Context()); err == nil {
		t.Fatal("resolve tenant succeeded with invalid client credentials")
	}
	if got := server.TokenRequestCount(); got != 0 {
		t.Fatalf("token request count = %d, want 0", got)
	}
}

func TestTokenEndpointRejectsUnsupportedOrUnauthenticatedGrant(t *testing.T) {
	server := New()
	server.SeedClient("expected-client", "expected-secret")
	endpoint := httptest.NewServer(server.Handler())
	t.Cleanup(endpoint.Close)

	request, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		endpoint.URL+tokenPath,
		strings.NewReader(url.Values{"grant_type": {"password"}}.Encode()),
	)
	if err != nil {
		t.Fatalf("build unsupported grant request: %v", err)
	}
	request.SetBasicAuth("expected-client", "expected-secret")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := endpoint.Client().Do(request)
	if err != nil {
		t.Fatalf("send unsupported grant request: %v", err)
	}
	closeResponseBody(t, response)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unsupported grant status = %d, want %d", response.StatusCode, http.StatusBadRequest)
	}

	request, err = http.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		endpoint.URL+tokenPath,
		strings.NewReader(url.Values{"grant_type": {grantTypeClientCredentials}}.Encode()),
	)
	if err != nil {
		t.Fatalf("build unauthenticated grant request: %v", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err = endpoint.Client().Do(request)
	if err != nil {
		t.Fatalf("send unauthenticated grant request: %v", err)
	}
	closeResponseBody(t, response)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated grant status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
	if got := server.TokenRequestCount(); got != 0 {
		t.Fatalf("token request count = %d, want 0", got)
	}
}

func testTenant() nicosdk.Tenant {
	tenant := nicosdk.NewTenant()
	tenant.SetId(testTenantID)
	tenant.SetOrg(testOrgID)
	return *tenant
}

func tenantPath(org string) string {
	return "/v2/org/" + org + "/nico/tenant/current"
}
