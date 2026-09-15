# API Reference

The following sections document every field of the four CRDs in
`infrastructure.cluster.x-k8s.io/v1alpha1`, one section per kind. Read
[Architecture](architecture.md) for why these fields exist and how they interact.
Read [Getting Started](getting-started.md) for how to use them.

The field descriptions mirror the Go doc comments in `api/v1alpha1/`. If they
ever drift, the Go source is authoritative.

## NicoCluster

`NicoCluster` is namespaced and uses the short name `nicoc`. Its `kubectl get`
output prints the Available, Synced, Provisioned, Site, and Age columns.

### Spec

The spec carries three fields, and only the site is required.

| Field | Type | Meaning |
|---|---|---|
| `siteID` | string, required | The site where this provider creates and looks up instances. |
| `identityRef` | object, optional | Points to the Secret that holds the NICo endpoint and the authentication settings. An unset value falls back to the provider-level credentials Secret in the manager's namespace. |
| `failureDomainLabelKey` | string, optional, 1 to 255 characters | The NICo Machine label key that carries the failure-domain name. The provider reads it to publish `status.failureDomains` and sends it as the machine label selector key when placing an instance. An unset value disables failure-domain support for this cluster, so no domains are published, and a Machine that requests one fails rather than being placed anywhere. Sites that follow the NICo convention use `failure_domain`. |

### Status

The status reports whether the provider can reach NICo and which placement
domains the cluster's site exposes.

| Field | Type | Meaning |
|---|---|---|
| `initialization.provisioned` | bool | True after the provider validates NICo access for this cluster. |
| `conditions` | list, up to 32 items | The current cluster state. Refer to [Condition Types and Reasons](#condition-types-and-reasons) below. |
| `ready` | bool | True after the provider can reach NICo and resolve tenant context for this cluster. |
| `failureDomains` | list, 1 to 100 items | The placement domains NICo exposes for this cluster's site. |

For a minimal manifest, refer to
[`infrastructure_v1alpha1_nicocluster.yaml`](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/config/samples/infrastructure_v1alpha1_nicocluster.yaml).

## NicoClusterTemplate

<Note>
`NicoClusterTemplate` is scaffolded but not consumed. The type exists and
generates a CRD. Nothing in this repository consumes it yet. There is no
ClusterClass or topology handling, and no example references it. Do not treat
it as a supported templating path.
</Note>

`NicoClusterTemplate` is namespaced and uses the short name `nicoct`. It has no
status subresource and no controller.

### Spec

The spec wraps the `NicoCluster` spec in a template.

| Field | Type | Meaning |
|---|---|---|
| `template.metadata` | object | Standard object metadata for the cluster the template would produce. |
| `template.spec` | object | The same fields as `NicoCluster.spec` above. |

For a minimal manifest, refer to
[`infrastructure_v1alpha1_nicoclustertemplate.yaml`](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/config/samples/infrastructure_v1alpha1_nicoclustertemplate.yaml).

## NicoMachine

`NicoMachine` is namespaced and uses the short name `nicom`. Its `kubectl get`
output prints the Available, Synced, Provisioned, Instance, and Age columns.

### Immutability

After the controller assigns `spec.providerID`, the entire spec is immutable,
and CEL validation rules reject any edit to it. Metadata and status updates
remain allowed. The controller uses these fields only to build the NICo instance
create request, and it never reapplies them to a live instance. Refer to
[machine reconciliation](architecture.md#machine-reconciliation) for why.

### Spec

The spec describes one NICo instance, from its placement to its boot script.

| Field | Type | Meaning |
|---|---|---|
| `vpcID` | string, required | The VPC where this provider creates the instance. |
| `instanceTypeID` | string | The instance type ID to provision. Set exactly one of `instanceTypeID` and `machineID`. |
| `machineID` | string | The machine ID for targeted instance creation. Set exactly one of `instanceTypeID` and `machineID`. |
| `interfaces` | list, required, 1 or more items | Interface attachment requests. Use subnet-based attachments for Ethernet network virtualization, and VPC-prefix-based attachments for next-generation networking. Each entry sets exactly one of `subnetID` or `vpcPrefixID`. The `ipAddress` field is only valid with `vpcPrefixID`, and its least-significant host bit must be `1`. |
| `infinibandInterfaces` | list | One or more InfiniBand Partitions to attach. This field is mutually exclusive with `infinibandPartitionID`. |
| `infinibandPartitionID` | string | Attaches this partition to every active InfiniBand device the instance type exposes. Use it instead of `infinibandInterfaces` when every device should share one partition. It requires `instanceTypeID`. |
| `nvLinkInterfaces` | list | One or more NVLink Logical Partitions to attach. This field is mutually exclusive with `nvLinkLogicalPartitionID`. |
| `nvLinkLogicalPartitionID` | string | Attaches this logical partition to every active NVLink device the instance type exposes. Use it instead of `nvLinkInterfaces` when every device should share one partition. It requires `instanceTypeID`. |
| `sshKeyGroupIDs` | list | The allowed SSH key group IDs for Serial-over-LAN access. |
| `ipxeScript` | string | The iPXE script used to boot this instance. |
| `cloudInitInjectHostname` | bool | Prepends hostname directives to the bootstrap cloud-config. |
| `labels` | map | Labels applied to the instance. |
| `allowUnhealthyMachine` | bool | Allows targeted instance creation on a machine in Error status. |
| `providerID` | string | Set by the controller to `nico://<instance-id>` after the NICo instance is created. |

### Status

The status records what the provider observed about the backing instance.

| Field | Type | Meaning |
|---|---|---|
| `initialization.provisioned` | bool | True after CAPNICo creates the backing instance. |
| `conditions` | list, up to 32 items | The current machine state. Refer to [Condition Types and Reasons](#condition-types-and-reasons) below. |
| `ready` | bool | True after the backing instance reaches Ready. |
| `instanceID` | string | The instance identifier backing this machine. |
| `machineID` | string | The NICo machine ID for this machine. |
| `siteID`, `siteName` | string | The observed site for this machine. |
| `vpcID`, `vpcName` | string | The observed VPC for this machine. |
| `tpmEkPubHash` | string | The TPM EK public hash for this machine. |
| `addresses` | list | The addresses observed on the backing instance. |
| `failureDomain` | string | The domain NICo reports the backing instance was placed in. |

For a minimal manifest, refer to
[`infrastructure_v1alpha1_nicomachine.yaml`](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/config/samples/infrastructure_v1alpha1_nicomachine.yaml).

## NicoMachineTemplate

`NicoMachineTemplate` is namespaced and uses the short name `nicomt`. It has no
status subresource and no controller. Both `KubeadmControlPlane` and
`MachineDeployment` consume it.

### Immutability

This kind is stricter than `NicoMachine`. A CEL rule freezes the entire
`spec.template.spec` on every update from creation onward, rather than gating
on `providerID` the way `NicoMachine.spec` does. To change machine
infrastructure, create a new `NicoMachineTemplate` and repoint the
`KubeadmControlPlane` or `MachineDeployment`. Cluster API then performs a
replacement rollout.

### Spec

The spec wraps the `NicoMachine` spec in a template.

| Field | Type | Meaning |
|---|---|---|
| `template.metadata` | object | Standard object metadata for the machine the template would produce. |
| `template.spec` | object | The same fields as `NicoMachine.spec` above. |

For a minimal manifest, refer to
[`infrastructure_v1alpha1_nicomachinetemplate.yaml`](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/config/samples/infrastructure_v1alpha1_nicomachinetemplate.yaml).

## Condition Types and Reasons

The two reconcilers publish four condition types.

| Type | Meaning |
|---|---|
| `NicoReady` | Whether NICo is ready for use by the cluster. |
| `Synced` | Whether the desired infrastructure was reconciled. |
| `MachineProvisioned` | Whether the backing instance for a machine is provisioned. |
| `FailureDomainDrifted` | Whether the machine NICo assigned is still in the failure domain the owner `Machine` requested. CAPNICo reports drift as its own condition rather than folding it into `Synced`. Drift discovered after a correct placement therefore does not mark a running machine unavailable. |

The following table lists every reason, grouped by theme. A "Yes" in the stall
table column means the reason also appears in
[Getting Started](getting-started.md#7-when-it-stalls), which tells you what to
do about it. [Troubleshooting](troubleshooting.md) covers the rest.

| Reason | In stall table | Meaning |
|---|---|---|
| `Unknown` | | The condition has not been reported yet. |
| `Deleting` | | The resource is being deleted. |
| `WaitingForIdentitySecret` | Yes | The configured identity Secret is not available yet. |
| `IdentityConfigurationFailed` | | The identity configuration is invalid. |
| `TenantResolutionFailed` | Yes | NICo tenant resolution failed. |
| `InfrastructureReady` | | The infrastructure resource is ready. |
| `Synced`, `NotSynced`, `SyncUnknown` | | The desired infrastructure was reconciled, was not reconciled, or its reconciliation state is unknown. |
| `WaitingForClusterInfrastructure` | | The owning cluster's infrastructure is not ready yet. |
| `WaitingForBootstrapData` | Yes | Bootstrap data is not available yet. |
| `BootstrapDataInvalid` | | Bootstrap data could not be used. |
| `InstanceCreateRequestInvalid` | | The NICo instance create request is invalid. Either CAPNICo could not build it, or NICo rejected it with HTTP 400. |
| `AvailabilityCheckFailed` | | The instance type availability check failed. |
| `InstanceTypeNotFound` | Yes | The configured instance type does not exist. |
| `InstanceTypeUnavailable` | Yes | The configured instance type has no available capacity. |
| `InstanceCreateFailed` | Yes | NICo instance creation failed. |
| `InstanceMissing` | | The backing NICo instance could not be found. |
| `InstanceNotReady` | Yes | The backing NICo instance is not ready yet. |
| `InstanceReady` | | The backing NICo instance is ready. |
| `InstanceNotFound` | | The requested import instance is not available in NICo. |
| `InstanceAlreadyClaimed` | | Another `NicoMachine` already references the backing instance. |
| `WaitingForNicoMachinesDeletion` | | The cluster is waiting for all `NicoMachine` objects in its namespace to finish deleting. |
| `ControlPlanePriorityDeferred` | Yes | A worker's instance create is held back so a control-plane machine waiting on the same instance type can claim scarce capacity first. |
| `FailureDomainUnavailable` | | The requested failure domain has no machine of the configured instance type free to place on. |
| `FailureDomainPlacementFailed` | | NICo refused placement in the requested failure domain for a reason free capacity would not resolve. |
| `FailureDomainVerificationFailed` | | The failure domain of the assigned machine could not be read back from NICo. |
| `FailureDomainDiscoveryFailed` | | NICo failure domains could not be listed. |
| `FailureDomainDrifted` | | The assigned machine's failure domain no longer matches the one requested at create. |
| `FailureDomainStable` | | The assigned machine is still in the requested failure domain, or none was requested. |

## Related Information

- [Architecture](architecture.md) explains how these fields interact, how
  credentials resolve, and how the failure-domain mechanism works.
- [Getting Started](getting-started.md) walks through using these CRDs to stand up
  a cluster.
- [Troubleshooting](troubleshooting.md) tells you what to do about a given
  condition reason.
- The [README](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/README.md)
  has the installation steps and the credentials Secret.
- [`config/samples/`](https://github.com/dsx-ai-factory/cluster-api-provider-nico/tree/main/config/samples)
  holds minimal worked manifests for all four CRDs.
