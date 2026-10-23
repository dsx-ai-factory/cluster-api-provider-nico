// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nico

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	nicosdk "github.com/NVIDIA/infra-controller/rest-api/sdk/standard"

	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/fake"
)

func TestUnit_ClientGetMachine(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/v2/org/test-org/nico/machine/machine-1"; got != want {
			t.Fatalf("request path = %q, want %q", got, want)
		}
		if got, want := r.URL.Query().Get("includeMetadata"), "true"; got != want {
			t.Fatalf("includeMetadata = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("Authorization"), testBearerToken; got != want {
			t.Fatalf("Authorization = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"machine-1","machineInterfaces":[{"isPrimary":true,"attachedDpuMachineID":"dpu-1"}]}`))
	}))
	defer server.Close()

	client := newStaticTokenClient(t, server.URL)
	machine, err := client.GetMachine(context.Background(), "machine-1")

	if err != nil {
		t.Fatalf("GetMachine() error = %v", err)
	}
	if got, want := machine.GetId(), "machine-1"; got != want {
		t.Fatalf("machine ID = %q, want %q", got, want)
	}
	machineInterfaces := machine.GetMachineInterfaces()
	if got, want := len(machineInterfaces), 1; got != want {
		t.Fatalf("machine interface count = %d, want %d", got, want)
	}
	if got, want := machineInterfaces[0].GetAttachedDpuMachineID(), "dpu-1"; got != want {
		t.Fatalf("attached DPU machine ID = %q, want %q", got, want)
	}
}

func TestClientGetSite(t *testing.T) {
	api := fake.New()
	api.SeedToken("static-token")
	api.SeedTenant("test-org", testTenant(), 0)
	siteResource := nicosdk.NewSite()
	siteResource.SetId("site-1")
	siteResource.SetName("Site / West")
	api.SeedSite("test-org", siteResource, 0)
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
	api.SeedVPC("test-org", vpcResource, 0)
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
	api.SeedTenant("test-org", testTenant(), 0)
	for i := range 101 {
		siteResource := nicosdk.NewSite()
		siteResource.SetId(fmt.Sprintf("site-%03d", i))
		siteResource.SetName(fmt.Sprintf("Site %03d", i))
		api.SeedSite("test-org", siteResource, 0)
	}
	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)

	client := newStaticTokenClient(t, server.URL)
	reg := isolatedMetrics(client)
	site, err := client.GetSite(context.Background(), "site-100")
	if err != nil {
		t.Fatalf("GetSite() error = %v", err)
	}
	if got, want := site.GetName(), "Site 100"; got != want {
		t.Fatalf("site name = %q, want raw name %q", got, want)
	}
	requests, _ := requestSamples(t, reg, "list_sites", "200")
	assert.InDelta(t, 2, requests, 0, "each requested page is one observation")
}

func TestClientRecordsAPIRequestMetrics(t *testing.T) {
	for _, tc := range []struct {
		name       string
		start      func() *httptest.Server
		statusCode string
	}{
		{
			name: "success",
			start: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"id":"vpc-1"}`))
				}))
			},
			statusCode: "200",
		},
		{
			name: "error response",
			start: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					http.Error(w, "nope", http.StatusNotFound)
				}))
			},
			statusCode: "404",
		},
		{
			name: "no response",
			start: func() *httptest.Server {
				server := httptest.NewServer(http.NotFoundHandler())
				server.Close()
				return server
			},
			statusCode: "none",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := tc.start()
			t.Cleanup(server.Close)
			client := newStaticTokenClient(t, server.URL)
			reg := isolatedMetrics(client)

			_, _ = client.GetVPC(context.Background(), "vpc-1")

			requests, observations := requestSamples(t, reg, "get_vpc", tc.statusCode)
			assert.InDelta(t, 1, requests, 0, "the request counter records the outcome")
			assert.Equal(t, uint64(1), observations, "the duration histogram records the same outcome")
		})
	}
}

// isolatedMetrics gives the client its own registry so a test sees only its own requests.
func isolatedMetrics(client *Client) prometheus.Gatherer {
	m := newAPIRequestMetrics()
	reg := prometheus.NewRegistry()
	reg.MustRegister(m.requests, m.duration)
	client.metrics = m
	return reg
}

func requestSamples(t *testing.T, reg prometheus.Gatherer, operation, statusCode string) (requests float64, observations uint64) {
	t.Helper()
	families, err := reg.Gather()
	require.NoError(t, err)
	for _, family := range families {
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["operation"] != operation || labels["status_code"] != statusCode {
				continue
			}
			switch family.GetName() {
			case "capnico_api_requests_total":
				requests = metric.GetCounter().GetValue()
			case "capnico_api_request_duration_seconds":
				observations = metric.GetHistogram().GetSampleCount()
			}
		}
	}
	return requests, observations
}

func testTenant() *nicosdk.Tenant {
	tenant := nicosdk.NewTenant()
	tenant.SetId(testTenantID)
	tenant.SetOrg("test-org")
	return tenant
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

func TestClientFindInstanceByNameRejectsPartialNameMatches(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"id":"instance-10","name":"machine-10"},
			{"id":"instance-1","name":"machine-1"}
		]`))
	}))
	defer server.Close()

	instance, err := newStaticTokenClient(t, server.URL).FindInstanceByName(context.Background(), InstanceLookup{Name: "machine-1"})

	require.NoError(t, err)
	assert.Equal(t, "instance-1", instance.GetId())
}

func TestClientFindInstanceByNameSkipsTerminatedRecordsWhenExcluded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"id":"instance-old","name":"machine-1","status":"Terminated"},
			{"id":"instance-live","name":"machine-1","status":"Ready"}
		]`))
	}))
	defer server.Close()

	client := newStaticTokenClient(t, server.URL)

	adopted, err := client.FindInstanceByName(context.Background(), InstanceLookup{Name: "machine-1", ExcludeTerminated: true})
	require.NoError(t, err)
	assert.Equal(t, "instance-live", adopted.GetId())

	// The already-exists recovery path must still see whatever holds the name.
	any, err := client.FindInstanceByName(context.Background(), InstanceLookup{Name: "machine-1"})
	require.NoError(t, err)
	assert.Equal(t, "instance-old", any.GetId())
}

func TestClientFindInstanceByNameReturnsNotFoundWhenOnlyTerminatedRemains(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"instance-old","name":"machine-1","status":"Terminated"}]`))
	}))
	defer server.Close()

	_, err := newStaticTokenClient(t, server.URL).FindInstanceByName(context.Background(), InstanceLookup{Name: "machine-1", ExcludeTerminated: true})

	assert.ErrorIs(t, err, ErrNotFound)
}
