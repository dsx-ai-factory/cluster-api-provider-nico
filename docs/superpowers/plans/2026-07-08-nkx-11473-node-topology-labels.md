# NKX-11473 CAPI Node Topology Labels Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Populate raw Forge topology in `NicoMachine.status` and apply the corresponding normalized labels to CAPI tenant Nodes.

**Architecture:** CAPNICo reads the backing instance, Site, and VPC using its tenant-visible NICo client and writes raw observed values to status. CWE's NicoMachine-driven controller retains provider-ID reconciliation and additionally reconciles only the five NKE topology labels, normalizing names at the Kubernetes-label boundary.

**Tech Stack:** Go, controller-runtime, Cluster API v1beta2, Kubernetes API, generated NICo SDK, testify.

---

## File structure

- `api/v1alpha1/nicomachine_types.go` — raw observed topology status fields.
- `internal/nico/client.go` and `internal/nico/client_test.go` — authenticated Site/VPC lookups.
- `controllers/nicomachine_controller.go` and `controllers/nicomachine_controller_test.go` — provider topology observation.
- `config/crd/bases/infrastructure.cluster.x-k8s.io_nicomachines.yaml` and `api/v1alpha1/zz_generated.deepcopy.go` — generated API artifacts.
- `cloud-workflow-engine/internal/controllers/capi/nico_provider_id_controller.go` and test — tenant Node metadata reconciliation.

### Task 1: Define and generate CAPNICo status topology

**Files:**
- Modify: `api/v1alpha1/nicomachine_types.go`
- Create: `api/v1alpha1/nicomachine_types_test.go`
- Modify: `api/v1alpha1/zz_generated.deepcopy.go`
- Modify: `config/crd/bases/infrastructure.cluster.x-k8s.io_nicomachines.yaml`

- [ ] **Step 1: Write the failing API contract test**

```go
func TestNicoMachineStatusTopologyFieldsRoundTrip(t *testing.T) {
	status := NicoMachineStatus{
		MachineID: "machine-1", SiteID: "site-1", SiteName: "New York / A",
		VPCID: "vpc-1", VPCName: "Tenant VPC",
	}
	b, err := json.Marshal(status)
	require.NoError(t, err)
	var got NicoMachineStatus
	require.NoError(t, json.Unmarshal(b, &got))
	require.Equal(t, status, got)
}
```

- [ ] **Step 2: Verify the test is red**

Run: `go test ./api/v1alpha1 -run TestNicoMachineStatusTopologyFieldsRoundTrip -count=1`

Expected: compile failure because the four topology fields do not yet exist.

- [ ] **Step 3: Add the fields and regenerate artifacts**

Add optional raw `SiteID`, `SiteName`, `VPCID`, and `VPCName` string fields after `MachineID`, each documented as an observed Forge/Carbide value. Run `make generate manifests`.

- [ ] **Step 4: Verify the contract and schema**

Run: `go test ./api/v1alpha1 -run TestNicoMachineStatusTopologyFieldsRoundTrip -count=1 && git diff --check`

Expected: PASS; the CRD exposes four optional status strings.

- [ ] **Step 5: Commit**

```bash
git add api/v1alpha1 config/crd/bases
git commit -m "feat: expose NicoMachine topology status"
```

### Task 2: Add tenant-visible NICo Site and VPC lookups

**Files:**
- Modify: `internal/nico/client.go`
- Modify: `internal/nico/client_test.go`

- [ ] **Step 1: Write failing HTTP-client tests**

Using an `httptest.Server` and static-token `SecretConfig`, add:

```go
func TestClientGetSite(t *testing.T) {
	// Assert GET /v2/org/test-org/carbide/site/site-1 and bearer auth.
	// Return {"id":"site-1","name":"New York / A"}.
	site, err := client.GetSite(context.Background(), "site-1")
	require.NoError(t, err)
	require.Equal(t, "New York / A", site.GetName())
}

func TestClientGetVPC(t *testing.T) {
	// Assert GET /v2/org/test-org/carbide/vpc/vpc-1 and bearer auth.
	// Return {"id":"vpc-1","name":"Tenant VPC"}.
	vpc, err := client.GetVPC(context.Background(), "vpc-1")
	require.NoError(t, err)
	require.Equal(t, "Tenant VPC", vpc.GetName())
}
```

- [ ] **Step 2: Verify tests are red**

Run: `go test ./internal/nico -run 'TestClientGet(Site|VPC)$' -count=1`

Expected: compile failure because the two client methods are absent.

- [ ] **Step 3: Implement the minimal authenticated wrappers**

```go
func (c *Client) GetSite(ctx context.Context, siteID string) (*nicosdk.Site, error) {
	authCtx, err := c.authCtx(ctx)
	if err != nil { return nil, err }
	site, resp, err := c.api.SiteAPI.GetSite(authCtx, c.orgID, siteID).Execute()
	if err != nil { return nil, normalizeError(resp, err) }
	return site, nil
}

func (c *Client) GetVPC(ctx context.Context, vpcID string) (*nicosdk.VPC, error) {
	authCtx, err := c.authCtx(ctx)
	if err != nil { return nil, err }
	vpc, resp, err := c.api.VPCAPI.GetVpc(authCtx, c.orgID, vpcID).Execute()
	if err != nil { return nil, normalizeError(resp, err) }
	return vpc, nil
}
```

- [ ] **Step 4: Verify green**

Run: `go test ./internal/nico -run 'TestClientGet(Site|VPC)$' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/nico/client.go internal/nico/client_test.go
git commit -m "feat: look up Nico site and VPC topology"
```

### Task 3: Snapshot raw topology in CAPNICo

**Files:**
- Modify: `controllers/nicomachine_controller.go`
- Modify: `controllers/nicomachine_controller_test.go`

- [ ] **Step 1: Write failing observation tests**

Introduce a small unexported status-assignment helper and test raw preservation plus missing names:

```go
func TestSetObservedTopologyPreservesRawForgeValues(t *testing.T) {
	machine := &infrav1.NicoMachine{}
	setObservedTopology(machine, instance("machine-1", "site-1", "vpc-1"),
		site("New York / A"), vpc("Tenant VPC"))
	require.Equal(t, "New York / A", machine.Status.SiteName)
	require.Equal(t, "Tenant VPC", machine.Status.VPCName)
}

func TestSetObservedTopologyLeavesUnavailableNamesAbsent(t *testing.T) {
	machine := &infrav1.NicoMachine{}
	setObservedTopology(machine, instance("machine-1", "site-1", "vpc-1"), nil, nil)
	require.Empty(t, machine.Status.SiteName)
	require.Empty(t, machine.Status.VPCName)
}
```

- [ ] **Step 2: Verify tests are red**

Run: `go test ./controllers -run TestSetObservedTopology -count=1`

Expected: compile failure because `setObservedTopology` is absent.

- [ ] **Step 3: Implement observation and non-fatal name lookup**

After `GetInstance`, write the raw machine/site/VPC IDs to status. Lookup Site and VPC only when their IDs are non-empty; log lookup errors and leave that name empty without returning an error. Assign returned `GetName()` values verbatim before readiness evaluation.

- [ ] **Step 4: Verify green**

Run: `go test ./controllers -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add controllers/nicomachine_controller.go controllers/nicomachine_controller_test.go
git commit -m "feat: snapshot Nico topology on machines"
```

### Task 4: Prepare an isolated CWE feature branch

**Files:**
- Modify later: `internal/controllers/capi/nico_provider_id_controller.go`
- Modify later: `internal/controllers/capi/nico_provider_id_controller_test.go`

- [ ] **Step 1: Create and verify the CWE worktree**

From a clean `cloud-workflow-engine` checkout:

```bash
git worktree add .worktrees/NKX-11473-node-topology-labels -b NKX-11473-node-topology-labels
cd .worktrees/NKX-11473-node-topology-labels
go mod download
go test ./internal/controllers/capi -count=1
```

Expected: clean worktree and passing baseline test suite.

### Task 5: Reconcile CAPI tenant-node topology labels

**Files:**
- Modify: `internal/controllers/capi/nico_provider_id_controller.go`
- Modify: `internal/controllers/capi/nico_provider_id_controller_test.go`

- [ ] **Step 1: Write failing controller tests**

Seed raw status values and a tenant Node with an unrelated label and a stale managed value:

```go
func TestNicoProviderIDControllerReconcilePatchesTopologyLabels(t *testing.T) {
	// NicoMachine status: machine-1, site-1, "New York / A", vpc-1, "Tenant VPC".
	// Node labels: {"keep":"value", "nke.nvidia.com/vpc-name":"stale"}.
	_, err := controller.Reconcile(context.Background(), providerIDReconcileRequest())
	require.NoError(t, err)
	node := getTenantNodeForProviderIDTest(t, tenantClient)
	require.Equal(t, "new-york-a", node.Labels[constants.LabelKeyNodeSiteName])
	require.Equal(t, "tenant-vpc", node.Labels[constants.LabelKeyNodeVPCName])
	require.Equal(t, "value", node.Labels["keep"])
}

func TestNicoProviderIDControllerReconcileRemovesAbsentManagedLabels(t *testing.T) {
	// Empty siteName and vpcName remove only those two keys.
}
```

- [ ] **Step 2: Verify tests are red**

Run: `go test ./internal/controllers/capi -run 'TestNicoProviderIDControllerReconcile(PatchesTopologyLabels|RemovesAbsentManagedLabels)' -count=1`

Expected: assertion failures because the controller only writes `spec.providerID`.

- [ ] **Step 3: Implement desired-label reconciliation**

Create a helper that maps non-empty raw status values to the existing five label-key constants. Reuse the direct-path normalization algorithm for the two names (or move that exact helper to a shared CWE internal package and update both callers). Build a merge patch from the tenant Node; set or delete only the five managed keys; preserve all unrelated labels; and retain the existing provider-ID conflict rule. Patch only when provider ID or managed labels differ.

- [ ] **Step 4: Verify green**

Run: `go test ./internal/controllers/capi -count=1`

Expected: PASS, including all existing provider-ID tests.

- [ ] **Step 5: Commit**

```bash
git add internal/controllers/capi/nico_provider_id_controller.go internal/controllers/capi/nico_provider_id_controller_test.go
git commit -m "feat: label CAPI nodes with Nico topology"
```

### Task 6: Verify both repositories

**Files:** Verify only.

- [ ] **Step 1: Format and test CAPNICo**

```bash
make fmt
go test ./...
git diff --check
```

Expected: all tests pass and no whitespace errors.

- [ ] **Step 2: Format and test CWE**

```bash
gofmt -w internal/controllers/capi/nico_provider_id_controller.go internal/controllers/capi/nico_provider_id_controller_test.go
go test ./internal/controllers/capi
git diff --check
```

Expected: all targeted tests pass and no whitespace errors.

- [ ] **Step 3: Inspect final scope**

Run in each worktree:

```bash
git status --short
git log --oneline origin/main..HEAD
```

Expected: only planned feature commits are present.

## Self-review

- Tasks 1–3 cover raw status values, tenant-visible API reads, and non-fatal missing names.
- Task 5 owns exactly five labels, normalizes at CWE, omits empty values, removes stale managed labels, and preserves unrelated labels.
- No task changes `node.kubernetes.io/instance-type` or adds `sku-id`.
- Every production-code task starts with an explicit failing test and red command.

