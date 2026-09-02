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
controller issues the delete request and removes the finalizer once that request
succeeds — including when NICo reports the instance is already gone. It does not
poll for a terminal state, so **the finalizer guards the request, not the
completion**. Do not remove it while `status.instanceID` may still refer to a live
instance.

`NicoCluster` holds its own finalizer and refuses to release it while any
`NicoMachine` remains in the same namespace carrying a matching
`cluster.x-k8s.io/cluster-name` label.

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

- [../README.md](../README.md) — installation, the credentials Secret, and the
  worked `clusterctl` examples
- [../CONTRIBUTING.md](../CONTRIBUTING.md) — development setup and the
  pull-request flow
- [../AGENTS.md](../AGENTS.md) — the conventions this codebase holds to
- [../RELEASE.md](../RELEASE.md) — versioning and the Cluster API contract
