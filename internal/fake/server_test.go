// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package fake

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	nicosdk "github.com/NVIDIA/infra-controller/rest-api/sdk/standard"

	"github.com/NVIDIA/cluster-api-provider-nico/internal/nico"
)

const (
	testOrgID        = "org-1"
	testTenantID     = "tenant-1"
	testSiteID       = "site-1"
	testVPCID        = "vpc-1"
	testInstanceType = "type-1"
	testStaticToken  = "test-token"
)

func TestClientLifecycleThroughHTTPFake(t *testing.T) {
	server, client := newSeededClient(t)
	assertSeededResources(t, client)
	instance := assertInstanceProvisioning(t, client)
	assertInstanceUpdatesAndReads(t, client, instance)
	assertInstanceDeletion(t, server, client, instance)
	assertDeterministicDump(t, server)
}

func assertSeededResources(t *testing.T, client *nico.Client) {
	t.Helper()

	tenantID, err := client.ResolveTenantID(t.Context())
	if err != nil {
		t.Fatalf("resolve tenant: %v", err)
	}
	if tenantID != testTenantID {
		t.Fatalf("tenant ID = %q, want %q", tenantID, testTenantID)
	}

	instanceType, err := client.GetInstanceTypeWithAllocationStats(t.Context(), testInstanceType)
	if err != nil {
		t.Fatalf("get instance type: %v", err)
	}
	allocationStats := instanceType.GetAllocationStats()
	if allocationStats.GetUnusedUsable() != 1 {
		t.Fatalf("unused usable capacity = %d, want 1", allocationStats.GetUnusedUsable())
	}
}

func assertInstanceProvisioning(t *testing.T, client *nico.Client) *nicosdk.Instance {
	t.Helper()

	request := testCreateRequest()
	instance, err := client.CreateInstance(t.Context(), request)
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	if instance.GetId() != "instance-00000001" {
		t.Fatalf("instance ID = %q, want %q", instance.GetId(), "instance-00000001")
	}
	if instance.GetStatus() != nicosdk.INSTANCESTATUS_PENDING {
		t.Fatalf("create status = %q, want %q", instance.GetStatus(), nicosdk.INSTANCESTATUS_PENDING)
	}

	instance, err = client.GetInstance(t.Context(), instance.GetId())
	if err != nil {
		t.Fatalf("first instance poll: %v", err)
	}
	if instance.GetStatus() != nicosdk.INSTANCESTATUS_PENDING {
		t.Fatalf("first poll status = %q, want %q", instance.GetStatus(), nicosdk.INSTANCESTATUS_PENDING)
	}

	instance, err = client.GetInstance(t.Context(), instance.GetId())
	if err != nil {
		t.Fatalf("second instance poll: %v", err)
	}
	if !nico.IsReady(instance) {
		t.Fatalf("second poll status = %q, want Ready", instance.GetStatus())
	}
	if got := instance.GetInterfaces()[0].GetIpAddresses(); len(got) != 1 || got[0] != defaultIPAddress {
		t.Fatalf("instance addresses = %v, want [%s]", got, defaultIPAddress)
	}
	return instance
}

func assertInstanceUpdatesAndReads(t *testing.T, client *nico.Client, instance *nicosdk.Instance) {
	t.Helper()

	labels := map[string]string{"cluster.x-k8s.io/cluster-name": "cluster-1", "topology.nvidia.com/site": testSiteID}
	instance, err := client.ApplyInstanceLabels(t.Context(), instance.GetId(), labels)
	if err != nil {
		t.Fatalf("apply labels: %v", err)
	}
	if len(instance.GetLabels()) != len(labels) || instance.GetLabels()["topology.nvidia.com/site"] != testSiteID {
		t.Fatalf("instance labels = %v, want %v", instance.GetLabels(), labels)
	}

	if _, err := client.TriggerInstanceReboot(t.Context(), instance.GetId()); err != nil {
		t.Fatalf("trigger reboot: %v", err)
	}

	site, err := client.GetSite(t.Context(), testSiteID)
	if err != nil {
		t.Fatalf("get site: %v", err)
	}
	if site.GetName() != "fake-site" {
		t.Fatalf("site name = %q, want %q", site.GetName(), "fake-site")
	}
	vpc, err := client.GetVPC(t.Context(), testVPCID)
	if err != nil {
		t.Fatalf("get VPC: %v", err)
	}
	if vpc.GetName() != "fake-vpc" {
		t.Fatalf("VPC name = %q, want %q", vpc.GetName(), "fake-vpc")
	}
}

func assertInstanceDeletion(t *testing.T, server *Server, client *nico.Client, instance *nicosdk.Instance) {
	t.Helper()

	summary := "GPU health alert"
	details := ""
	healthIssue := nicosdk.NewMachineHealthIssue(
		"Hardware",
		*nicosdk.NewNullableString(&summary),
		*nicosdk.NewNullableString(&details),
	)
	if err := client.DeleteInstance(t.Context(), instance.GetId(), healthIssue); err != nil {
		t.Fatalf("delete instance: %v", err)
	}

	instance, err := client.GetInstance(t.Context(), instance.GetId())
	if err != nil {
		t.Fatalf("first deletion poll: %v", err)
	}
	if !nico.IsTerminating(instance) {
		t.Fatalf("first deletion poll status = %q, want Terminating", instance.GetStatus())
	}
	instance, err = client.GetInstance(t.Context(), instance.GetId())
	if err != nil {
		t.Fatalf("terminal deletion poll: %v", err)
	}
	if !nico.IsTerminated(instance) {
		t.Fatalf("terminal deletion poll status = %q, want Terminated", instance.GetStatus())
	}
	if got := server.InstanceCount(); got != 0 {
		t.Fatalf("live instance count = %d, want 0", got)
	}
}

func assertDeterministicDump(t *testing.T, server *Server) {
	t.Helper()

	dump, err := server.Dump()
	if err != nil {
		t.Fatalf("dump fake state: %v", err)
	}
	secondDump, err := server.Dump()
	if err != nil {
		t.Fatalf("dump fake state again: %v", err)
	}
	if dump != secondDump {
		t.Fatal("fake state dump is not deterministic")
	}
	for _, expected := range []string{
		"status: Terminated",
		"reboot_count: 1",
		"operation: create-instance",
		"operation: delete-instance",
		"machineHealthIssue:",
		"summary: GPU health alert",
	} {
		if !strings.Contains(dump, expected) {
			t.Fatalf("fake state dump does not contain %q:\n%s", expected, dump)
		}
	}
	if got := strings.Count(dump, "operation: update-instance"); got != 2 {
		t.Fatalf("update records in dump = %d, want 2:\n%s", got, dump)
	}
	if strings.Contains(dump, testStaticToken) {
		t.Fatalf("fake state dump contains bearer token:\n%s", dump)
	}
}

func TestReadOnlyRoutesAndSitePagination(t *testing.T) {
	server := New()
	server.SeedToken(testStaticToken)
	server.SeedTenant(testOrgID, testTenant())
	server.SeedInstanceType(testOrgID, testInstanceTypeResource(3))
	server.SeedVPC(testOrgID, testVPC())
	for i := range 101 {
		site := nicosdk.NewSite()
		site.SetId(fmt.Sprintf("site-%03d", i))
		site.SetName(fmt.Sprintf("Site %03d", i))
		server.SeedSite(testOrgID, *site)
	}
	endpoint := httptest.NewServer(server.Handler())
	t.Cleanup(endpoint.Close)
	client := newStaticClient(t, endpoint.URL, testStaticToken)

	if _, err := client.ResolveTenantID(t.Context()); err != nil {
		t.Fatalf("resolve tenant: %v", err)
	}
	instanceType, err := client.GetInstanceTypeWithAllocationStats(t.Context(), testInstanceType)
	if err != nil {
		t.Fatalf("get instance type: %v", err)
	}
	allocationStats := instanceType.GetAllocationStats()
	if allocationStats.GetUnusedUsable() != 3 {
		t.Fatalf("unused usable capacity = %d, want 3", allocationStats.GetUnusedUsable())
	}
	if _, err := client.GetVPC(t.Context(), testVPCID); err != nil {
		t.Fatalf("get VPC: %v", err)
	}
	site, err := client.GetSite(t.Context(), "site-100")
	if err != nil {
		t.Fatalf("get site from second page: %v", err)
	}
	if site.GetName() != "Site 100" {
		t.Fatalf("site name = %q, want %q", site.GetName(), "Site 100")
	}
}

func TestSeedFromYAML(t *testing.T) {
	server := New()
	err := server.SeedFromYAML(`
instances:
- org: org-1
  resource:
    id: imported-1
    name: imported-machine
    status: Ready
    tenantId: tenant-1
`)
	if err != nil {
		t.Fatalf("SeedFromYAML() error = %v", err)
	}
	if got := server.InstanceCount(); got != 1 {
		t.Fatalf("live instance count = %d, want 1", got)
	}

	if err := server.SeedFromYAML("requests:\n- operation: create-instance\n"); err == nil {
		t.Fatal("SeedFromYAML() accepted request observations")
	}
	if err := server.SeedFromYAML("instances:\n- org: org-1\n  resource: {}\n"); err == nil {
		t.Fatal("SeedFromYAML() accepted an instance without an ID")
	}
}

func TestRejectsMalformedCreateRequest(t *testing.T) {
	server := New()
	server.SeedToken(testStaticToken)
	server.SeedTenant(testOrgID, testTenant())
	server.SeedInstanceType(testOrgID, testInstanceTypeResource(1))
	server.SeedVPC(testOrgID, testVPC())
	endpoint := httptest.NewServer(server.Handler())
	t.Cleanup(endpoint.Close)

	request, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		endpoint.URL+"/v2/org/"+testOrgID+"/nico/instance",
		bytes.NewBufferString(`{"name":"incomplete"}`),
	)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+testStaticToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := endpoint.Client().Do(request)
	if err != nil {
		t.Fatalf("send request: %v", err)
	}
	closeResponseBody(t, response)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed create status = %d, want %d", response.StatusCode, http.StatusBadRequest)
	}

	client := newStaticClient(t, endpoint.URL, testStaticToken)
	missingTarget := testCreateRequest()
	missingTarget.UnsetMachineId()
	if _, err := client.CreateInstance(t.Context(), missingTarget); err == nil {
		t.Fatal("create without instanceTypeId or machineId succeeded")
	}

	bothTargets := testCreateRequest()
	bothTargets.SetInstanceTypeId(testInstanceType)
	if _, err := client.CreateInstance(t.Context(), bothTargets); err == nil {
		t.Fatal("create with both instanceTypeId and machineId succeeded")
	}

	unknownType := testCreateRequest()
	unknownType.UnsetMachineId()
	unknownType.SetInstanceTypeId("missing-type")
	if _, err := client.CreateInstance(t.Context(), unknownType); err == nil {
		t.Fatal("create with an unknown instanceTypeId succeeded")
	}
}

func closeResponseBody(t *testing.T, response *http.Response) {
	t.Helper()
	if err := response.Body.Close(); err != nil {
		t.Fatalf("close response body: %v", err)
	}
}

func newSeededClient(t *testing.T) (*Server, *nico.Client) {
	t.Helper()

	server := New()
	server.SeedToken(testStaticToken)
	server.SeedTenant(testOrgID, testTenant())
	server.SeedInstanceType(testOrgID, testInstanceTypeResource(1))
	server.SeedSite(testOrgID, testSite())
	server.SeedVPC(testOrgID, testVPC())
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

func testInstanceTypeResource(unusedUsable int32) nicosdk.InstanceType {
	instanceType := nicosdk.NewInstanceType()
	instanceType.SetId(testInstanceType)
	stats := nicosdk.NewInstanceTypeAllocationStats()
	stats.SetTotal(unusedUsable)
	stats.SetUsed(0)
	stats.SetUnused(unusedUsable)
	stats.SetUnusedUsable(unusedUsable)
	instanceType.SetAllocationStats(*stats)
	return *instanceType
}

func testSite() nicosdk.Site {
	site := nicosdk.NewSite()
	site.SetId(testSiteID)
	site.SetName("fake-site")
	site.SetOrg(testOrgID)
	return *site
}

func testVPC() nicosdk.VPC {
	vpc := nicosdk.NewVPC()
	vpc.SetId(testVPCID)
	vpc.SetName("fake-vpc")
	vpc.SetOrg(testOrgID)
	vpc.SetTenantId(testTenantID)
	vpc.SetSiteId(testSiteID)
	return *vpc
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
