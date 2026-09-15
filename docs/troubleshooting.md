# Troubleshooting

For a stuck first cluster, start with
[getting-started.md's stall-reason table](getting-started) ("7. When it
stalls"). The sections below cover what that table does not: `NicoCluster`
problems, failure-domain placement, and the annotation-driven operations.
Each entry gives a symptom, a cause, and a fix.

Read `status.conditions` first, on the object that is not converging. Every
problem below names a specific, distinct reason, not a generic timeout.

## NicoCluster problems

**Never becomes `Ready`.** Check `kubectl get nicocluster <name> -o yaml`,
then `status.conditions`:

- `WaitingForIdentitySecret`: the Secret is missing or misnamed. Check the
  name and namespace against README's "NICo credentials Secret" section.
- `IdentityConfigurationFailed`: the Secret exists, but its configuration is
  invalid (for example, missing required keys, or an incomplete
  authentication mode). Recheck its keys against README's credentials
  reference.
- `TenantResolutionFailed`: the tenant lookup failed. Causes include a wrong
  org or `apiName`, an expired or rejected token, an unroutable endpoint, or
  a CA mismatch. All four land on this one reason.

**`status.failureDomains` stays empty.** Check for
`FailureDomainDiscoveryFailed`:

- NICo returns 403: the identity lacks the `targetedInstanceCreation`
  capability for the site, which is not fatal. The cluster publishes no
  domains and provisions normally, without failure-domain placement.
- NICo returns 401: a credential problem, not a missing capability. Treat it
  like `TenantResolutionFailed` above.
- No error at all: check whether `NicoCluster.spec.failureDomainLabelKey` is
  set. An unset key is expected to produce an empty list, since it
  deliberately disables the feature. See api-reference.md.

**Stuck deleting.** Reason `WaitingForNicoMachinesDeletion` means one or
more `NicoMachine`s in the same namespace still carry a matching
`cluster.x-k8s.io/cluster-name` label. The finalizer will not release until
they are gone. Delete the `Cluster`, not the namespace, and let Cluster API
delete the Machines first. See README's "Deletion lifecycle".

## NicoMachine problems

**Failure-domain placement:**

| Reason | Cause | Fix |
|---|---|---|
| `FailureDomainUnavailable` | No free machine of the requested instance type in that domain. Requeues, and counts as an instance-type capacity wait. See `ControlPlanePriorityDeferred` in getting-started.md's stall table. | Wait for capacity, or add machines to the domain. |
| `FailureDomainPlacementFailed` | Not a capacity problem: the identity lacks the capability to send a machine label selector, an explicit `spec.machineID` does not match the requested domain, or a domain was requested on a cluster with no `failureDomainLabelKey`. | Check the identity's capability, the `machineID`, or the cluster's `failureDomainLabelKey`. The capability case retries on a long interval. NICo grants it out of band, and nothing here watches for the grant. |
| `FailureDomainDrifted` | Non-fatal. The label on the already-assigned machine changed after a correct placement. | Informational only. The instance is still running. Do not let a `MachineHealthCheck` delete a healthy node over this alone. |

**CEL rejection, "...is immutable after providerID is set."** The spec
already froze. See api-reference.md's Immutability note. Fix: create a new
`NicoMachineTemplate` and repoint the `KubeadmControlPlane` or
`MachineDeployment`, rather than editing in place.

**Instance created, but the node never registers.** At this point,
CAPNICo's job is done: `status.instanceID` is set and the instance reached
Ready. The problem sits one layer up: the iPXE image, cloud-init, or network
reachability to the control-plane endpoint. Go to getting-started.md's "What
has to be in the OS image" section and its "Proving an image before you
trust it" checklist.

**Stuck deleting, or a suspected leaked instance.** The controller reads the
instance before deleting it, holds the finalizer, and requeues until the
instance reaches a terminal state. Both `Terminated` and not-found count as
released, so the finalizer guards completion, not just the delete request.
Check `status.instanceID` against NICo directly. See architecture.md's
"Machine reconciliation" section for the full mechanism.

## Reboot and repair annotations not taking effect

Both are annotations on the owning CAPI `Machine`, not fields on either CRD.
Check:

- The annotation key matches the configured flag: `--reboot-annotation`
  (default `nico.nvidia.com/reboot`) or `--repair-annotation` (default
  `nico.nvidia.com/machine-health-issue`).
- The value is non-empty. Both operations ignore an empty value.
- Controller logs, for whether the trigger reached NICo.

CAPNICo triggers at most one reboot per observed annotation application,
and removes the reboot annotation once NICo accepts it. A reboot that
already fired will not fire again until the annotation is re-applied. See
README's "Machine Repair" and "Machine Reboot" sections for the full
contract.

## Credential rotation not picked up

Check which Secret actually changed: the provider-level Secret, or the
per-cluster Secret named by `NicoCluster.spec.identityRef`. Clients are
cached per Secret `resourceVersion`. If the Secret that changed is not the
one this `NicoCluster` resolves to, or its `resourceVersion` did not
actually change, the controller keeps using the old client. See
architecture.md's "Credential resolution" section.

## See also

- [README.md](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/README.md): the credentials Secret layout and install
- [architecture.md](architecture): the mechanism behind every fix above
- [getting-started.md](getting-started): the `NicoMachine` create-path
  stall table and the OS-image contract
- [api-reference.md](api-reference): every condition and reason, field by
  field
