// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package fake

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	nicosdk "github.com/NVIDIA/infra-controller/rest-api/sdk/standard"

	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/nico"
)

const (
	testOrgID        = "org-1"
	testTenantID     = "tenant-1"
	testSiteID       = "site-1"
	testVPCID        = "vpc-1"
	testInstanceType = "type-1"
	testStaticToken  = "test-token"
)

type testBackend struct {
	createErr error
	deleteErr error
	ready     func(context.Context, string) (bool, error)
	create    func(context.Context, string, string, string, map[string]string) error
}

func (b *testBackend) Create(ctx context.Context, instanceID, machineName, userData string, labels map[string]string) error {
	if b.create != nil {
		return b.create(ctx, instanceID, machineName, userData, labels)
	}
	return b.createErr
}

func (b *testBackend) Ready(ctx context.Context, instanceID string) (bool, error) {
	if b.ready == nil {
		return false, nil
	}
	return b.ready(ctx, instanceID)
}

func (b *testBackend) Delete(context.Context, string) error {
	return b.deleteErr
}

func newSeededBackendClient(t *testing.T, backend Backend) (*Server, *nico.Client) {
	t.Helper()

	server := New()
	server.SeedToken(testStaticToken)
	server.SeedTenant(testOrgID, testTenant(), 0)
	server.SeedInstanceType(testOrgID, testInstanceTypeResource(1), 0)
	server.SeedSite(testOrgID, testSite(), 0)
	server.SeedVPC(testOrgID, testVPC(), 0)
	server.SetBackend(backend)
	endpoint := httptest.NewServer(server.Handler())
	t.Cleanup(endpoint.Close)
	return server, newStaticClient(t, endpoint.URL, testStaticToken)
}

func newStaticClient(t *testing.T, endpoint, token string) *nico.Client {
	t.Helper()

	client, err := nico.NewClient(t.Context(), nico.SecretConfig{
		Endpoint: endpoint,
		OrgID:    testOrgID,
		Token:    token,
	})
	if err != nil {
		t.Fatalf("build static-token client: %v", err)
	}
	return client
}

func testTenant() *nicosdk.Tenant {
	tenant := nicosdk.NewTenant()
	tenant.SetId(testTenantID)
	tenant.SetOrg(testOrgID)
	return tenant
}

func testInstanceTypeResource(unusedUsable int32) *nicosdk.InstanceType {
	instanceType := nicosdk.NewInstanceType()
	instanceType.SetId(testInstanceType)
	stats := nicosdk.NewInstanceTypeAllocationStats()
	stats.SetTotal(unusedUsable)
	stats.SetUsed(0)
	stats.SetUnused(unusedUsable)
	stats.SetUnusedUsable(unusedUsable)
	instanceType.SetAllocationStats(*stats)
	return instanceType
}

func testSite() *nicosdk.Site {
	site := nicosdk.NewSite()
	site.SetId(testSiteID)
	site.SetName("fake-site")
	site.SetOrg(testOrgID)
	return site
}

func testVPC() *nicosdk.VPC {
	vpc := nicosdk.NewVPC()
	vpc.SetId(testVPCID)
	vpc.SetName("fake-vpc")
	vpc.SetOrg(testOrgID)
	vpc.SetTenantId(testTenantID)
	vpc.SetSiteId(testSiteID)
	return vpc
}

func testCreateRequest() nicosdk.InstanceCreateRequest {
	interfaceRequest := nicosdk.NewInterfaceCreateRequest()
	interfaceRequest.SetSubnetId("subnet-1")
	request := nicosdk.NewInstanceCreateRequest(
		"machine-1",
		testTenantID,
		testVPCID,
	)
	request.SetInterfaces([]nicosdk.InterfaceCreateRequest{*interfaceRequest})
	request.SetMachineId("machine-id-1")
	request.SetLabels(map[string]string{"cluster.x-k8s.io/cluster-name": "cluster-1"})
	request.SetUserData("#cloud-config\n")
	return *request
}

func TestBackendLifecycleFailuresAreReturned(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		server, client := newSeededBackendClient(t, &testBackend{createErr: errors.New("create failed")})
		if _, err := client.CreateInstance(t.Context(), testCreateRequest(), nico.InstancePlacement{}); err == nil {
			t.Fatal("create instance succeeded when backend creation failed")
		}
		if got := server.InstanceCount(); got != 0 {
			t.Fatalf("live instance count = %d, want 0", got)
		}
	})

	t.Run("delete", func(t *testing.T) {
		server, client := newSeededBackendClient(t, &testBackend{deleteErr: errors.New("delete failed")})
		instance, err := client.CreateInstance(t.Context(), testCreateRequest(), nico.InstancePlacement{})
		if err != nil {
			t.Fatalf("create instance: %v", err)
		}
		if err := client.DeleteInstance(t.Context(), instance.GetId(), nil); err == nil {
			t.Fatal("delete instance succeeded when backend deletion failed")
		}

		server.mu.Lock()
		record := server.instances.Resources[resourceKey(testOrgID, instance.GetId())]
		requestCount := len(server.requests)
		server.mu.Unlock()
		if record == nil || record.instance.GetStatus() != nicosdk.INSTANCESTATUS_PENDING {
			t.Fatalf("instance status after failed deletion = %v, want Pending", record)
		}
		if requestCount != 1 {
			t.Fatalf("recorded request count = %d, want only the successful create", requestCount)
		}
	})
}

func TestBackendReceivesMachineName(t *testing.T) {
	var got string
	backend := &testBackend{create: func(_ context.Context, _, machineName, _ string, _ map[string]string) error {
		got = machineName
		return nil
	}}
	_, client := newSeededBackendClient(t, backend)
	request := testCreateRequest()
	if _, err := client.CreateInstance(t.Context(), request, nico.InstancePlacement{}); err != nil {
		t.Fatalf("create instance: %v", err)
	}
	if got != request.GetName() {
		t.Fatalf("backend machine name = %q, want %q", got, request.GetName())
	}
}

func TestBackendReadDoesNotHoldServerLock(t *testing.T) {
	started := make(chan struct{})
	continueReady := make(chan struct{})
	release := sync.OnceFunc(func() { close(continueReady) })
	defer release()
	backend := &testBackend{ready: func(ctx context.Context, _ string) (bool, error) {
		close(started)
		select {
		case <-continueReady:
			return false, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}}
	_, client := newSeededBackendClient(t, backend)
	instance, err := client.CreateInstance(t.Context(), testCreateRequest(), nico.InstancePlacement{})
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}

	getDone := make(chan error, 1)
	go func() {
		_, err := client.GetInstance(t.Context(), instance.GetId())
		getDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("backend readiness check did not start")
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := client.GetVPC(ctx, testVPCID); err != nil {
		t.Fatalf("concurrent VPC read was blocked by backend readiness: %v", err)
	}
	release()
	select {
	case err := <-getDone:
		if err != nil {
			t.Fatalf("get instance: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("instance read did not finish")
	}
}
