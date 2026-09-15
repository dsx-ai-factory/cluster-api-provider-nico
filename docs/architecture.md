# Architecture

How `cluster-api-provider-nico` (CAPNICo) turns Cluster API objects into bare
metal running in NICo.

Claims here are stated against the code. Where behaviour is subtle, the relevant
package is named so it can be checked.

## Where this provider sits

CAPNICo is a Cluster API **infrastructure provider**. It provisions machines and
carries bootstrap data to them. It does not install Kubernetes and it does not
run the join itself — the kubeadm bootstrap and control-plane providers generate
the cloud-init, and CAPNICo hands it to NICo as instance user data.

```
  Cluster API core
        │
        │  Machine.spec.bootstrap.configRef ──► kubeadm bootstrap provider
        │                                              │
        │                                     writes a bootstrap data Secret
        │                                              │
        │  Machine.spec.infrastructureRef               │
        ▼                                              ▼
  NicoCluster / NicoMachine  ◄──── reads the Secret, extracts the cloud-config
        │
        │  NICo REST API: CreateInstance, user data attached
        ▼
  NVIDIA Infra Controller (NICo)  ──►  an instance boots from its iPXE script
```

The bootstrap payload travels **through** this provider. The controller reads the
bootstrap data Secret, extracts the cloud-config, and sets it as the instance's
user data on the create request.

The management cluster must be able to reach the NICo API, and the target VPC and
its subnet or VPC prefix must already exist.

## The resources

| Kind | Holds |
|---|---|
| `NicoCluster` | `spec.siteID`, and optionally `spec.identityRef` naming a per-cluster credentials Secret. **Those are its only two fields.** |
| `NicoMachine` | One NICo instance. `spec.vpcID` is required and lives **here**, not on `NicoCluster`. |
| `NicoMachineTemplate` | Consumed by `KubeadmControlPlane` and `MachineDeployment` |
| `NicoClusterTemplate` | The type exists and generates a CRD. Nothing in this repository consumes it yet — there is no ClusterClass or topology handling, and no example uses it. |

`NicoMachine.spec` remains editable until the controller assigns
`spec.providerID`. After that assignment, the entire spec is immutable because
CAPNICO currently uses these fields to build the NICo instance create request
but does not apply later changes to the existing instance. Metadata and status
updates remain allowed. The controller's initial provider ID assignment is the
transition that freezes the spec.

`NicoMachineTemplate.spec.template.spec` is immutable. To change machine
infrastructure, create a new `NicoMachineTemplate` and update the
`KubeadmControlPlane` or `MachineDeployment` reference so Cluster API performs a
replacement rollout. Template metadata remains mutable.

The API group is `infrastructure.cluster.x-k8s.io/v1alpha1`. `metadata.yaml`
records the Cluster API contract each release series implements; it is currently
`v1beta2`.

**Site and VPC come from different objects.** The site is cluster-scoped, the VPC
is per machine. A reconcile reads `nicoCluster.Spec.SiteID` and
`nicoMachine.Spec.VPCID` separately, and the create request carries the machine's
VPC.

## Credential resolution

Every reconcile resolves credentials in a fixed order:

1. The Secret named by `NicoCluster.spec.identityRef.name`, in the
   `NicoCluster`'s own namespace, when that field is set.
2. Otherwise the provider-level Secret in the manager's namespace, named by
   `--provider-credentials-namespace` (default `$POD_NAMESPACE`, falling back to
   `capnico-system`) and `--provider-credentials-secret-name` (default
   `nico-credentials`).
3. Otherwise the reconcile fails with a not-found error.

**Clients are cached per Secret revision.** The cache key is
`namespace/name@resourceVersion`, and inserting a new revision evicts earlier
entries sharing the `namespace/name@` prefix. That is what makes external
credential rotation work: when something like External Secrets Operator rewrites
the Secret, the `resourceVersion` changes, the old client is evicted, and the next
reconcile builds a new one. No manager restart, and no auth state in CR status.

The provider performs the OAuth2 client-credentials exchange itself, wrapping it
in a reusing token source and injecting the token into the SDK per request. The
published NICo SDK accepts a bearer token in the request context but has no
helper for acquiring one.

See the README for the Secret's key layout and both authentication modes.

## Machine reconciliation

A create-path reconcile is **not** a single API call. One pass may resolve the
tenant, read the instance type, create the instance, read it back, look up the
site (itself paginated) and the VPC, apply labels, and trigger a reboot. What it
does not do is block waiting for the instance to become ready — it evaluates
readiness in the same pass and requeues if the instance is not there yet.

**Create is idempotent only for the already-exists case.** If `CreateInstance`
fails with an already-exists error, the controller looks the instance up by name
and adopts it. That error is produced from an HTTP 409 whose body says the
instance already exists; any other 409 becomes a plain conflict and fails the
reconcile without adoption.

`status.instanceID` is the link back to the real instance. On delete, the
controller reads the instance before deleting it, so one already released or
mid-teardown is not deleted twice. It then holds the finalizer and requeues
until the instance reaches a terminal state, so **the finalizer guards the
completion, not just the request**. NICo retains terminated instance records, so
both `Terminated` and not-found count as released. Do not remove the finalizer
while `status.instanceID` may still refer to a live instance.

`NicoCluster` holds its own finalizer and refuses to release it while any
`NicoMachine` remains in the same namespace carrying a matching
`cluster.x-k8s.io/cluster-name` label.

## Failure domains

Topology comes from NICo Machine labels. Discovery, placement, and verification
all read the same `machines.labels` records. The label key is
`NicoCluster.spec.failureDomainLabelKey`. It has no default: leaving it unset
disables failure domains for the cluster, so an existing cluster does not start
constraining placement the moment its site's Machines happen to carry a
`failure_domain` label. Sites following the NICo convention set it to
`failure_domain`.

```mermaid
sequenceDiagram
    participant NICo
    participant NCC as NicoCluster controller
    participant CAPI as Cluster API core
    participant CP as Control-plane provider
    participant NMC as NicoMachine controller

    NCC->>NICo: List Machines(siteID)
    NICo-->>NCC: machines with failure_domain labels
    NCC->>NCC: deduplicate and sort labels
    NCC->>NCC: set NicoCluster.status.failureDomains
    CAPI->>CAPI: copy to Cluster.status.failureDomains
    CP->>CAPI: create Machine with spec.failureDomain=fd-b
    NMC->>NICo: CreateInstance(instanceTypeId, machineLabelSelector.failure_domain=fd-b)
    NICo->>NICo: select and lock an exact label match atomically
    NICo-->>NMC: created instance with assigned machineId
    NMC->>NICo: Get Machine(instance.machineId)
    NICo-->>NMC: assigned Machine and labels
    NMC->>NMC: set NicoMachine.status.failureDomain
```

CapNICo never chooses the *domain*: it publishes the label-derived choices
and honors the domain selected by Cluster API. It expresses that choice on the
create request as JSON
`machineLabelSelector: {"failure_domain":"fd-b"}`. The generated Go SDK names
the `map[string]string` field `MachineLabelSelector` and provides
`SetMachineLabelSelector`.

With `instanceTypeId`, NICo chooses a matching free Machine and locks it as part
of the create operation. CapNICo does not list or choose a concrete Machine, so
there is no client-side list/create race. A non-empty selector requires NICo's
effective `targetedInstanceCreation` capability for the selected site, including
automatic `instanceTypeId` placement. An explicitly configured
`NicoMachine.spec.machineID` is retained as `machineId` and validated atomically
against the same selector; explicit placement also requires that capability.
CapNICo never falls back to an unconstrained create.

This behavior requires a NICo server containing
[NVIDIA/infra-controller#5484](https://github.com/NVIDIA/infra-controller/pull/5484),
merged to `main` on August 28, 2026. The SDK module `rest-api/sdk/standard`
carries no semver tags, so CapNICo pins the pseudo-version
`v0.0.0-20260901235154-eafb6b962baf`. Older servers silently ignore unknown JSON
fields and are not compatible.

NICo also supports `machineLabelSelector` on batch instance allocation. CapNICo
does not use that API: it reconciles one deterministically named `NicoMachine`
at a time and does not yet provide grouped NVLink co-placement. Supporting that
guarantee would require a separate group-reconciliation design.

Listing Machines is gated by the same `targetedInstanceCreation` capability as
the selector, so an identity that cannot list them cannot request a domain
either. NICo reports that as 403, and the cluster then publishes no domains and
provisions normally. A 401 is a credential problem rather than a missing
capability, and is handled as a discovery error instead.

Any discovery error before the first successful publication keeps the
`NicoCluster` unprovisioned, so control-plane machines are not created against
an unknown domain list. Once published, a later refresh error preserves the
previous list, because clearing it would read as *no spread required*.

Discovery considers only Machines that have an Instance Type, since placement
selects within one. It deliberately does not filter on Machine status or
assignment: the published list feeds `Cluster.status.failureDomains`, and a
domain that vanished while its Machines were busy would make Cluster API treat
the control-plane machines already placed there as out of their failure domain.
The consequence is an operational requirement -- every published domain needs
Machines of every Instance Type the cluster provisions, or a control-plane
machine assigned to a domain that has none will retry placement indefinitely
under `FailureDomainUnavailable`.

The published list is capped at the 100 domains the CRD allows; anything beyond
that is dropped in sort order and logged.

### Placement failures

`FailureDomainUnavailable` means the domain has no free Machine of the Instance
Type. It requeues, and it counts as an instance-type capacity wait, so a
control-plane machine blocked on it still defers workers of that type.

`FailureDomainPlacementFailed` covers the failures free capacity would not fix:
a Machine label selector the tenant lacks the capability to send, an explicit
`spec.machineID` that does not match the domain, or a requested domain on a
cluster with no `failureDomainLabelKey`. The capability case retries on a long
interval rather than terminally, because NICo grants that capability out of band
and nothing this controller watches changes when it does.

Drift found after a correct placement -- NICo enforces the selector inside the
create transaction, so this means a label changed afterwards -- is reported on
the `FailureDomainDrifted` condition and in `status.failureDomain`. It does not
fail provisioning: the instance is running, and marking it unavailable would
invite a `MachineHealthCheck` to delete a healthy node over a label edit.

## Provider ID and node matching

The kubeadm templates read the instance ID from the NICo metadata service at
`169.254.169.254:7777/latest/meta-data/instance-id` and patch kubelet with
`providerID: nico://<instance-id>`. That is how Cluster API matches a
workload-cluster `Node` back to its `Machine`. NICo's documentation describes this
as the Metadata Service (FMDS), a local HTTP metadata API served by the DPU agent
— see [Overview and Components](https://docs.nvidia.com/infra-controller/documentation/architecture/overview-and-components).

## Teardown

Delete the Cluster API `Cluster` and let that deletion finish before removing the
namespace. That order lets Cluster API delete machines before the shared
infrastructure object, and gives each `NicoMachine` a chance to issue its delete
**while credentials still exist**.

Deleting the namespace first can remove the per-cluster credentials Secret before
the finalizers run, which strands machines in deletion and leaves NICo instances
to clean up by hand.

## Annotation-driven operations

Two operations are contracts on the owning Cluster API `Machine` rather than
fields on the CRD, so a consumer can drive them without depending on this API.
**Both ignore an annotation whose value is empty** — the value carries meaning in
each case.

### Repair — `nico.nvidia.com/machine-health-issue`

The value is parsed as JSON first:

```json
{"category": "Thermal", "summary": "over temperature", "details": "optional"}
```

A value that does not parse as JSON is treated as a legacy plain-string summary
and assigned the category `Other`. Either way the health issue is forwarded to
NICo as context on the delete request. Configurable with `--repair-annotation`;
set it empty to disable the feature.

### Reboot — `nico.nvidia.com/reboot`

Presence with a non-empty value requests one reboot. The value itself is
consumer-owned and is not otherwise interpreted. After NICo accepts the trigger,
the provider removes the annotation, so one reboot happens per application.
Configurable with `--reboot-annotation`.

## See also

- [README.md](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/README.md) — installation, the credentials Secret, and the
  worked `clusterctl` examples
- [CONTRIBUTING.md](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/CONTRIBUTING.md) — development setup and the
  pull-request flow
- [AGENTS.md](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/AGENTS.md) — the conventions this codebase holds to
- [RELEASE.md](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/RELEASE.md) — versioning and the Cluster API contract
