// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nico

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"

	nicosdk "github.com/NVIDIA/ncx-infra-controller-rest/sdk/standard"

	"github.com/NVIDIA/cluster-api-provider-nico/internal/fake"
)

func TestClientGetSite(t *testing.T) {
	api := fake.New()
	api.SeedToken("static-token")
	api.SeedTenant("test-org", testTenant())
	siteResource := nicosdk.NewSite()
	siteResource.SetId("site-1")
	siteResource.SetName("Site / West")
	api.SeedSite("test-org", *siteResource)
	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)

	client := newStaticTokenClient(t, server.URL)
	site, err := client.GetSite(context.Background(), "site-1")
	if err != nil {
		t.Fatalf("GetSite() error = %v", err)
	}
	if got, want := site.GetName(), "Site / West"; got != want {
		t.Fatalf("site name = %q, want raw name %q", got, want)
	}
}

func TestClientGetVPC(t *testing.T) {
	api := fake.New()
	api.SeedToken("static-token")
	vpcResource := nicosdk.NewVPC()
	vpcResource.SetId("vpc-1")
	vpcResource.SetName("VPC / Production")
	api.SeedVPC("test-org", *vpcResource)
	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)

	client := newStaticTokenClient(t, server.URL)
	vpc, err := client.GetVPC(context.Background(), "vpc-1")
	if err != nil {
		t.Fatalf("GetVPC() error = %v", err)
	}
	if got, want := vpc.GetName(), "VPC / Production"; got != want {
		t.Fatalf("VPC name = %q, want raw name %q", got, want)
	}
}

func TestClientGetSiteQueriesAdditionalPagesUntilFound(t *testing.T) {
	api := fake.New()
	api.SeedToken("static-token")
	api.SeedTenant("test-org", testTenant())
	for i := range 101 {
		siteResource := nicosdk.NewSite()
		siteResource.SetId(fmt.Sprintf("site-%03d", i))
		siteResource.SetName(fmt.Sprintf("Site %03d", i))
		api.SeedSite("test-org", *siteResource)
	}
	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)

	client := newStaticTokenClient(t, server.URL)
	site, err := client.GetSite(context.Background(), "site-100")
	if err != nil {
		t.Fatalf("GetSite() error = %v", err)
	}
	if got, want := site.GetName(), "Site 100"; got != want {
		t.Fatalf("site name = %q, want raw name %q", got, want)
	}
}

func testTenant() nicosdk.Tenant {
	tenant := nicosdk.NewTenant()
	tenant.SetId(testTenantID)
	tenant.SetOrg("test-org")
	return *tenant
}

func newStaticTokenClient(t *testing.T, endpoint string) *Client {
	t.Helper()
	client, err := NewClient(context.Background(), SecretConfig{
		Endpoint: endpoint,
		OrgID:    "test-org",
		Token:    "static-token",
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}
