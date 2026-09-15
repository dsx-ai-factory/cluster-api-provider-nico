# Troubleshooting

For a stuck first cluster, start with the stall-reason table in
[Getting Started](getting-started.md#7-when-it-stalls). The sections below cover
what that table does not: `NicoCluster` problems, failure-domain placement, and
the annotation-driven operations. Each entry gives a symptom, a cause, and a
fix.

When the relevant section names a condition reason, start by reading
`status.conditions` on the object that is not converging. Other cases below use
the checks described in their sections.

## NicoCluster Problems

### The Cluster Never Becomes Ready

Run `kubectl get nicocluster <name> -o yaml` and read `status.conditions`.
Three reasons account for this symptom.

- `WaitingForIdentitySecret` means the Secret is missing or misnamed. Check the
  name and namespace against the
  [NICo credentials Secret](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/README.md#nico-credentials-secret)
  section of the README.
- `IdentityConfigurationFailed` means the Secret exists, but its configuration
  is invalid. For example, required keys are missing, or the authentication
  mode is incomplete. Recheck its keys against the same credentials reference.
- `TenantResolutionFailed` means the tenant lookup failed. Causes include a
  wrong org or `apiName`, an expired or rejected token, an unroutable endpoint,
  or a CA mismatch. All four land on this one reason.

### The Failure Domain List Stays Empty

When `status.failureDomains` stays empty, check for
`FailureDomainDiscoveryFailed`. Three cases are possible.

- NICo returns 403. The identity lacks the `targetedInstanceCreation`
  capability for the site. This is not fatal. The cluster publishes no domains
  and provisions normally, without failure-domain placement.
- NICo returns 401. This is a credential problem, not a missing capability.
  Treat it like `TenantResolutionFailed` above.
- No error appears at all. Check whether `NicoCluster.spec.failureDomainLabelKey`
  is set. An unset key is expected to produce an empty list, since it
  deliberately disables the feature. Refer to the
  [API Reference](api-reference.md#nicocluster).

### The Cluster Is Stuck Deleting

The reason `WaitingForNicoMachinesDeletion` means one or more `NicoMachine`
objects in the same namespace still carry a matching
`cluster.x-k8s.io/cluster-name` label. The finalizer does not release until
they are gone. Delete the `Cluster`, not the namespace, and let Cluster API
delete the Machines first. Refer to the
[deletion lifecycle](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/README.md#deletion-lifecycle)
section of the README.

## NicoMachine Problems

### Failure-Domain Placement Fails

Three reasons report placement problems, and only one of them is about
capacity.

| Reason | Cause | Fix |
|---|---|---|
| `FailureDomainUnavailable` | No free machine of the requested instance type exists in that domain. The reconcile requeues, and this counts as an instance-type capacity wait. Refer to `ControlPlanePriorityDeferred` in the [stall table](getting-started.md#7-when-it-stalls). | Wait for capacity, or add machines to the domain. |
| `FailureDomainPlacementFailed` | This is not a capacity problem. Either the identity lacks the capability to send a machine label selector, an explicit `spec.machineID` does not match the requested domain, or a domain was requested on a cluster with no `failureDomainLabelKey`. | Check the identity's capability, the `machineID`, or the cluster's `failureDomainLabelKey`. The capability case retries on a long interval. NICo grants it out of band, and nothing here watches for the grant. |
| `FailureDomainDrifted` | This is not fatal. The label on the already-assigned machine changed after a correct placement. | Treat it as informational. The instance is still running. Do not let a `MachineHealthCheck` delete a healthy node over this alone. |

### CEL Rejects an Edit as Immutable

The message reads "is immutable after providerID is set," which means the spec
has already frozen. Refer to the immutability note in the
[API Reference](api-reference.md#nicomachine). To fix it, create a new
`NicoMachineTemplate` and repoint the `KubeadmControlPlane` or
`MachineDeployment` rather than editing in place.

### The Instance Is Created, but the Node Never Registers

At this point, CAPNICo's job is done: `status.instanceID` is set and the
instance has reached Ready. The problem sits one layer up, in the iPXE image,
cloud-init, or network reachability to the control-plane endpoint. Refer to the
[OS image contract](getting-started.md#what-has-to-be-in-the-os-image) and work
through its
[image proving checklist](getting-started.md#proving-an-image-before-you-trust-it).

### A Machine Is Stuck Deleting, or an Instance Looks Leaked

The controller reads the instance before deleting it, holds the finalizer, and
requeues until the instance reaches a terminal state. Both `Terminated` and
not-found count as released, so the finalizer guards completion, not just the
delete request. Check `status.instanceID` against NICo directly. Refer to
[machine reconciliation](architecture.md#machine-reconciliation) for the full
mechanism.

## Reboot and Repair Annotations Not Taking Effect

Both are annotations on the owning CAPI `Machine`, not fields on either CRD.
Check three things.

- The annotation key matches the configured flag, which is
  `--reboot-annotation` (default `nico.nvidia.com/reboot`) or
  `--repair-annotation` (default `nico.nvidia.com/machine-health-issue`).
- The value is non-empty. Both operations ignore an empty value.
- The controller logs record that the trigger reached NICo, so read them.

CAPNICo triggers at most one reboot per observed annotation application, and
removes the reboot annotation after NICo accepts it. A reboot that already
fired does not fire again until you reapply the annotation. Refer to the
[machine repair](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/README.md#machine-repair)
and
[machine reboot](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/README.md#machine-reboot)
sections of the README for the full contract.

## Credential Rotation Not Picked Up

Check which Secret actually changed: the provider-level Secret or the
per-cluster Secret named by `NicoCluster.spec.identityRef`. Clients are cached
per Secret `resourceVersion`. If the Secret that changed is not the one this
`NicoCluster` resolves to, or if its `resourceVersion` stayed the same, the
controller keeps using the old client. Refer to
[credential resolution](architecture.md#credential-resolution).

## Related Information

- The [README](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/README.md)
  has the credentials Secret layout and the install steps.
- [Architecture](architecture.md) explains the mechanism behind every fix above.
- [Getting Started](getting-started.md) has the `NicoMachine` create-path stall
  table and the OS-image contract.
- The [API Reference](api-reference.md) documents every condition and reason,
  field by field.
