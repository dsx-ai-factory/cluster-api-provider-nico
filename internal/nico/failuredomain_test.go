// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nico

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	nicosdk "github.com/NVIDIA/infra-controller/rest-api/sdk/standard"
)

func TestClientListFailureDomainsExtractsUniqueSortedLabels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v2/org/test-org/nico/machine", r.URL.Path)
		assert.Equal(t, "site-1", r.URL.Query().Get("siteId"))
		assert.Equal(t, "1", r.URL.Query().Get("pageNumber"))
		assert.Equal(t, "100", r.URL.Query().Get("pageSize"))
		// A machine with no Instance Type can never satisfy an instanceTypeId
		// placement, and a total order keeps a machine from moving between pages.
		assert.Equal(t, "true", r.URL.Query().Get("hasInstanceType"))
		assert.Equal(t, "ID_ASC", r.URL.Query().Get("orderBy"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"id":"machine-1","labels":{"failure_domain":"fd-b"}},
			{"id":"machine-2","labels":{"failure_domain":"fd-a"}},
			{"id":"machine-3","labels":{"failure_domain":"fd-b"}},
			{"id":"machine-4","labels":{"failure_domain":""}},
			{"id":"machine-5"}
		]`))
	}))
	defer server.Close()

	domains, err := newStaticTokenClient(t, server.URL).ListFailureDomains(context.Background(), "site-1", "failure_domain")

	require.NoError(t, err)
	assert.Equal(t, []FailureDomain{{Name: "fd-a"}, {Name: "fd-b"}}, domains)
}

func TestClientListFailureDomainsWithoutLabelKeyQueriesNothing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
	}))
	defer server.Close()

	domains, err := newStaticTokenClient(t, server.URL).ListFailureDomains(context.Background(), "site-1", "")

	require.NoError(t, err)
	assert.Empty(t, domains)
}

func TestClientListFailureDomainsQueriesEveryMachinePage(t *testing.T) {
	var pages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("pageNumber")
		pages = append(pages, page)
		w.Header().Set("Content-Type", "application/json")
		switch page {
		case "1":
			body := "[" + strings.TrimSuffix(strings.Repeat(`{"labels":{"failure_domain":"fd-b"}},`, pageSize), ",") + "]"
			_, _ = w.Write([]byte(body))
		case "2":
			_, _ = w.Write([]byte(`[{"labels":{"failure_domain":"fd-a"}}]`))
		default:
			t.Fatalf("unexpected pageNumber %q", page)
		}
	}))
	defer server.Close()

	domains, err := newStaticTokenClient(t, server.URL).ListFailureDomains(context.Background(), "site-1", "failure_domain")

	require.NoError(t, err)
	assert.Equal(t, []string{"1", "2"}, pages)
	assert.Equal(t, []FailureDomain{{Name: "fd-a"}, {Name: "fd-b"}}, domains)
}

func TestClientCreateInstanceUsesServerSideFailureDomainFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || r.URL.Path != "/v2/org/test-org/nico/instance" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "instance-type-1", body["instanceTypeId"])
		assert.Equal(t, map[string]any{"failure_domain": "fd-a"}, body["machineLabelSelector"])
		assert.NotContains(t, body, "machineId")
		_, _ = w.Write([]byte(`{"id":"instance-1","machineId":"machine-a","siteId":"site-1","vpcId":"vpc-1"}`))
	}))
	defer server.Close()

	req := nicosdk.NewInstanceCreateRequest("machine-1", "tenant-1", "vpc-1")
	req.SetInstanceTypeId("instance-type-1")

	instance, err := newStaticTokenClient(t, server.URL).CreateInstance(context.Background(), *req, InstancePlacement{
		FailureDomain: "fd-a",
		LabelKey:      "failure_domain",
	})

	require.NoError(t, err)
	assert.Equal(t, "instance-1", instance.GetId())
	assert.Equal(t, "machine-a", instance.GetMachineId())
}

func TestClientCreateInstanceFiltersExplicitMachinePlacementServerSide(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || r.URL.Path != "/v2/org/test-org/nico/instance" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "machine-1", body["machineId"])
		assert.Equal(t, map[string]any{"failure_domain": "fd-a"}, body["machineLabelSelector"])
		assert.NotContains(t, body, "instanceTypeId")
		_, _ = w.Write([]byte(`{"id":"instance-1","machineId":"machine-1","siteId":"site-1","vpcId":"vpc-1"}`))
	}))
	defer server.Close()

	req := nicosdk.NewInstanceCreateRequest("machine-1", "tenant-1", "vpc-1")
	req.SetMachineId("machine-1")

	instance, err := newStaticTokenClient(t, server.URL).CreateInstance(context.Background(), *req, InstancePlacement{
		FailureDomain: "fd-a",
		LabelKey:      "failure_domain",
	})

	require.NoError(t, err)
	assert.Equal(t, "machine-1", instance.GetMachineId())
}

func TestClientCreateInstanceDoesNotMapAutomaticTargetedMachineRaceResponses(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
	}{
		{
			name:       "machine assigned after selection",
			statusCode: http.StatusBadRequest,
			body:       `{"message":"Machine: machine-1 is assigned to an Instance, cannot be used for new Instance"}`,
		},
		{
			name:       "machine allocation lock contention",
			statusCode: http.StatusInternalServerError,
			body:       `{"message":"Failed to lock Machine: machine-1 for Instance creation. It is likely being considered for another Instance creation request"}`,
		},
		{
			name:       "conflict response",
			statusCode: http.StatusConflict,
			body:       `{"message":"machine allocation conflicted"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method != http.MethodPost || r.URL.Path != "/v2/org/test-org/nico/instance" {
					t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
				}
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			req := nicosdk.NewInstanceCreateRequest("machine-1", "tenant-1", "vpc-1")
			req.SetInstanceTypeId("instance-type-1")

			_, err := newStaticTokenClient(t, server.URL).CreateInstance(context.Background(), *req, InstancePlacement{
				FailureDomain: "fd-a",
				LabelKey:      "failure_domain",
			})

			require.Error(t, err)
			assert.NotErrorIs(t, err, ErrFailureDomainUnavailable)
		})
	}
}

func TestClientCreateInstanceTreatsExplicitMachineContentionAsUnavailable(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
	}{
		{
			name:       "machine assigned",
			statusCode: http.StatusBadRequest,
			body:       `{"message":"Machine: machine-1 is assigned to an Instance, cannot be used for new Instance"}`,
		},
		{
			name:       "machine lock contention",
			statusCode: http.StatusInternalServerError,
			body:       `{"message":"Failed to lock Machine: machine-1 for Instance creation. It is likely being considered for another Instance creation request"}`,
		},
		{
			name:       "conflict response",
			statusCode: http.StatusConflict,
			body:       `{"message":"machine allocation conflicted"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method != http.MethodPost || r.URL.Path != "/v2/org/test-org/nico/instance" {
					t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
				}
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			req := nicosdk.NewInstanceCreateRequest("machine-1", "tenant-1", "vpc-1")
			req.SetMachineId("machine-1")

			_, err := newStaticTokenClient(t, server.URL).CreateInstance(context.Background(), *req, InstancePlacement{
				FailureDomain: "fd-a",
				LabelKey:      "failure_domain",
			})

			require.Error(t, err)
			assert.ErrorIs(t, err, ErrFailureDomainUnavailable)
		})
	}
}

func TestClientCreateInstanceKeepsTargetedAlreadyExistsDistinct(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || r.URL.Path != "/v2/org/test-org/nico/instance" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"message":"Instance machine-1 already exists"}`))
	}))
	defer server.Close()

	req := nicosdk.NewInstanceCreateRequest("machine-1", "tenant-1", "vpc-1")
	req.SetInstanceTypeId("instance-type-1")

	_, err := newStaticTokenClient(t, server.URL).CreateInstance(context.Background(), *req, InstancePlacement{
		FailureDomain: "fd-a",
		LabelKey:      "failure_domain",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAlreadyExists)
	assert.NotErrorIs(t, err, ErrFailureDomainUnavailable)
}

func TestClientCreateInstanceMapsNoCandidateResponseToFailureDomainUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || r.URL.Path != "/v2/org/test-org/nico/instance" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"No Machines are available for specified Instance Type"}`))
	}))
	defer server.Close()

	req := nicosdk.NewInstanceCreateRequest("machine-1", "tenant-1", "vpc-1")
	req.SetInstanceTypeId("instance-type-1")

	_, err := newStaticTokenClient(t, server.URL).CreateInstance(context.Background(), *req, InstancePlacement{
		FailureDomain: "fd-a",
		LabelKey:      "failure_domain",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrFailureDomainUnavailable)
}

func TestClientCreateInstanceMapsExplicitFilterMismatchResponseToFailureDomainMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || r.URL.Path != "/v2/org/test-org/nico/instance" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"Machine specified in request does not match machineLabelSelector"}`))
	}))
	defer server.Close()

	req := nicosdk.NewInstanceCreateRequest("machine-1", "tenant-1", "vpc-1")
	req.SetMachineId("machine-1")

	_, err := newStaticTokenClient(t, server.URL).CreateInstance(context.Background(), *req, InstancePlacement{
		FailureDomain: "fd-a",
		LabelKey:      "failure_domain",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrFailureDomainMismatch)
}

func TestClientCreateInstanceMapsMissingPlacementCapability(t *testing.T) {
	tests := []struct {
		name           string
		machineID      string
		instanceTypeID string
		message        string
	}{
		{
			name:           "automatic placement",
			instanceTypeID: "instance-type-1",
			message:        "Tenant does not have capability to create Instances using Machine label selector",
		},
		{
			name:      "explicit machine",
			machineID: "machine-1",
			message:   "Tenant does not have capability to create Instances using specific Machine ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method != http.MethodPost || r.URL.Path != "/v2/org/test-org/nico/instance" {
					t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
				}
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"source":  "nico",
					"message": tt.message,
					"data":    nil,
				})
			}))
			defer server.Close()

			req := nicosdk.NewInstanceCreateRequest("machine-1", "tenant-1", "vpc-1")
			if tt.machineID != "" {
				req.SetMachineId(tt.machineID)
			} else {
				req.SetInstanceTypeId(tt.instanceTypeID)
			}

			_, err := newStaticTokenClient(t, server.URL).CreateInstance(context.Background(), *req, InstancePlacement{
				FailureDomain: "fd-a",
				LabelKey:      "failure_domain",
			})

			require.Error(t, err)
			assert.ErrorIs(t, err, ErrFailureDomainCapabilityRequired)
			assert.ErrorIs(t, err, ErrUnauthorized)
		})
	}
}

func TestClientCreateInstanceWithoutFailureDomainKeepsInstanceTypeAllocation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || r.URL.Path != "/v2/org/test-org/nico/instance" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "instance-type-1", body["instanceTypeId"])
		assert.NotContains(t, body, "machineId")
		assert.NotContains(t, body, "machineLabelSelector")
		_, _ = w.Write([]byte(`{"id":"instance-1","instanceTypeId":"instance-type-1"}`))
	}))
	defer server.Close()

	req := nicosdk.NewInstanceCreateRequest("machine-1", "tenant-1", "vpc-1")
	req.SetInstanceTypeId("instance-type-1")

	instance, err := newStaticTokenClient(t, server.URL).CreateInstance(context.Background(), *req, InstancePlacement{})

	require.NoError(t, err)
	assert.Equal(t, "instance-type-1", instance.GetInstanceTypeId())
}

func TestClientListFailureDomainsUsesConfiguredLabelKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"id":"machine-1","labels":{"failure_domain":"fd-default","failure-domain":"fd-custom"}}
		]`))
	}))
	defer server.Close()

	domains, err := newStaticTokenClient(t, server.URL).ListFailureDomains(context.Background(), "site-1", "failure-domain")

	require.NoError(t, err)
	assert.Equal(t, []FailureDomain{{Name: "fd-custom"}}, domains)
}

func TestClientCreateInstanceUsesConfiguredLabelKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, map[string]any{"failure-domain": "fd-a"}, body["machineLabelSelector"])
		_, _ = w.Write([]byte(`{"id":"instance-1","machineId":"machine-a"}`))
	}))
	defer server.Close()

	req := nicosdk.NewInstanceCreateRequest("machine-1", "tenant-1", "vpc-1")
	req.SetInstanceTypeId("instance-type-1")

	_, err := newStaticTokenClient(t, server.URL).CreateInstance(context.Background(), *req, InstancePlacement{
		FailureDomain: "fd-a",
		LabelKey:      "failure-domain",
	})

	require.NoError(t, err)
}
