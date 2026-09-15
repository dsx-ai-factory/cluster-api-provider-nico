# API reference

The following sections document every field of the four CRDs in
`infrastructure.cluster.x-k8s.io/v1alpha1`, one section per kind. Read
[architecture.md](architecture) for why these fields exist and how they
interact. Read [getting-started.md](getting-started) for how to use them.

The field descriptions mirror the Go doc comments in `api/v1alpha1/`. If they
ever drift, the Go source is authoritative.

## NicoCluster

Namespaced. Short name `nicoc`. `kubectl get` columns: Available, Synced,
Provisioned, Site, Age.

### spec

| Field | Type | Meaning |
|---|---|---|
| `siteID` | string, required | The site where this provider creates and looks up instances. |
| `identityRef` | object, optional | Points to the Secret with NICo endpoint and authentication settings. Unset falls back to the provider-level credentials Secret in the manager's namespace. |
| `failureDomainLabelKey` | string, optional (1-255 chars) | The NICo Machine label key that carries the failure domain name. Read to publish `status.failureDomains`, and sent as the machine label selector key when placing an instance. Unset disables failure domain support for this cluster: no domains are published, and a Machine that requests one fails rather than being placed anywhere. Sites that follow the NICo convention use `failure_domain`. |

### status

| Field | Type | Meaning |
|---|---|---|
| `initialization.provisioned` | bool | True once the provider validates NICo access for this cluster. |
| `conditions` | list, max 32 | The current cluster state. See "Condition types and reasons" below. |
| `ready` | bool | True once the provider can reach NICo and resolve tenant context for this cluster. |
| `failureDomains` | list, 1-100 items | The placement domains NICo exposes for this cluster's site. |

Sample manifest: [`config/samples/infrastructure_v1alpha1_nicocluster.yaml`](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/config/samples/infrastructure_v1alpha1_nicocluster.yaml).

## NicoClusterTemplate

> **Scaffolded but not consumed.** The type exists and generates a CRD.
> Nothing in this repository consumes it yet. There is no ClusterClass or
> topology handling, and no example references it. Do not treat it as a
> supported templating path.

Namespaced. Short name `nicoct`. No status subresource, no controller.

### spec

| Field | Type | Meaning |
|---|---|---|
| `template.metadata` | object | Standard object metadata for the cluster the template would produce. |
| `template.spec` | object | Same fields as `NicoCluster.spec` above. |

Sample manifest: [`config/samples/infrastructure_v1alpha1_nicoclustertemplate.yaml`](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/config/samples/infrastructure_v1alpha1_nicoclustertemplate.yaml).

## NicoMachine

Namespaced. Short name `nicom`. `kubectl get` columns: Available, Synced,
Provisioned, Instance, Age.

**Immutability.** Once the controller assigns `spec.providerID`, the entire
spec is immutable. CEL validation rules reject any other change. The
controller uses these fields only to build the NICo instance create request,
and it never reapplies them to a live instance. See architecture.md's
"Machine reconciliation" section for why.

### spec

| Field | Type | Meaning |
|---|---|---|
| `vpcID` | string, required | The VPC where this provider creates the instance. |
| `instanceTypeID` | string | The instance type ID to provision. Exactly one of `instanceTypeID` or `machineID` must be set. |
| `machineID` | string | The machine ID for targeted instance creation. Exactly one of `instanceTypeID` or `machineID` must be set. |
| `interfaces` | list, required, min 1 | Interface attachment requests. Use subnet-based attachments for Ethernet network virtualization, VPC-prefix-based attachments for next-generation networking. Each entry sets exactly one of `subnetID` or `vpcPrefixID`. `ipAddress` is only valid with `vpcPrefixID`, and its least-significant host bit must be `1`. |
| `infinibandInterfaces` | list | One or more InfiniBand Partitions to attach. Mutually exclusive with `infinibandPartitionID`. |
| `infinibandPartitionID` | string | Attaches this partition to every active InfiniBand device the instance type exposes. Use instead of `infinibandInterfaces` when every device should share one partition. Requires `instanceTypeID`. |
| `nvLinkInterfaces` | list | One or more NVLink Logical Partitions to attach. Mutually exclusive with `nvLinkLogicalPartitionID`. |
| `nvLinkLogicalPartitionID` | string | Attaches this logical partition to every active NVLink device the instance type exposes. Use instead of `nvLinkInterfaces` when every device should share one partition. Requires `instanceTypeID`. |
| `sshKeyGroupIDs` | list | Allowed SSH key group IDs for Serial-over-LAN access. |
| `ipxeScript` | string | The iPXE script used to boot this instance. |
| `cloudInitInjectHostname` | bool | Prepends hostname directives to the bootstrap cloud-config. |
| `labels` | map | Applied to the instance. |
| `allowUnhealthyMachine` | bool | Allows targeted instance creation on a machine in Error status. |
| `providerID` | string | Set by the controller to `nico://<instance-id>` once the NICo instance is created. |

### status

| Field | Type | Meaning |
|---|---|---|
| `initialization.provisioned` | bool | True once CAPNICo creates the backing instance. |
| `conditions` | list, max 32 | The current machine state. See "Condition types and reasons" below. |
| `ready` | bool | True once the backing instance reaches Ready. |
| `instanceID` | string | The instance identifier backing this machine. |
| `machineID` | string | The NICo machine ID for this machine. |
| `siteID`, `siteName` | string | The observed site for this machine. |
| `vpcID`, `vpcName` | string | The observed VPC for this machine. |
| `tpmEkPubHash` | string | The TPM EK public hash for this machine. |
| `addresses` | list | Addresses observed on the backing instance. |
| `failureDomain` | string | The domain NICo reports the backing instance was placed in. |

Sample manifest: [`config/samples/infrastructure_v1alpha1_nicomachine.yaml`](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/config/samples/infrastructure_v1alpha1_nicomachine.yaml).

## NicoMachineTemplate

Namespaced. Short name `nicomt`. No status subresource, no controller.
Consumed by `KubeadmControlPlane` and `MachineDeployment`.

**Immutability, stricter than `NicoMachine`.** A CEL rule freezes the entire
`spec.template.spec` on every update, from creation, not gated on
`providerID` the way `NicoMachine.spec` is. To change machine
infrastructure, create a new `NicoMachineTemplate` and repoint the
`KubeadmControlPlane` or `MachineDeployment`. Cluster API then performs a
replacement rollout.

### spec

| Field | Type | Meaning |
|---|---|---|
| `template.metadata` | object | Standard object metadata for the machine the template would produce. |
| `template.spec` | object | Same fields as `NicoMachine.spec` above. |

Sample manifest: [`config/samples/infrastructure_v1alpha1_nicomachinetemplate.yaml`](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/config/samples/infrastructure_v1alpha1_nicomachinetemplate.yaml).

## Condition types and reasons

Four condition types, published by the two reconcilers:

| Type | Meaning |
|---|---|
| `NicoReady` | Whether NICo is ready for use by the cluster. |
| `Synced` | Whether the desired infrastructure was reconciled. |
| `MachineProvisioned` | Whether the backing instance for a machine is provisioned. |
| `FailureDomainDrifted` | Whether the machine NICo assigned is still in the failure domain the owner `Machine` requested. CAPNICo reports it as its own condition rather than folding it into `Synced`, so drift discovered after a correct placement does not mark a running machine unavailable. |

Reasons, grouped by theme. Rows marked ★ also appear in getting-started.md's
stall-reason table ("7. When it stalls"), with what to do about them. The
rest are covered in [troubleshooting.md](troubleshooting).

| Reason | Meaning |
|---|---|
| `Unknown` | The condition has not been reported yet. |
| `Deleting` | The resource is being deleted. |
| ★ `WaitingForIdentitySecret` | The configured identity Secret is not available yet. |
| `IdentityConfigurationFailed` | The identity configuration is invalid. |
| ★ `TenantResolutionFailed` | NICo tenant resolution failed. |
| `InfrastructureReady` | The infrastructure resource is ready. |
| `Synced` / `NotSynced` / `SyncUnknown` | The desired infrastructure was reconciled, was not reconciled, or its reconciliation state is unknown. |
| `WaitingForClusterInfrastructure` | The owning cluster's infrastructure is not ready yet. |
| ★ `WaitingForBootstrapData` | Bootstrap data is not available yet. |
| `BootstrapDataInvalid` | Bootstrap data could not be used. |
| `InstanceCreateRequestInvalid` | The NICo instance create request is invalid: CAPNICo could not build it, or NICo rejected it with HTTP 400. |
| `AvailabilityCheckFailed` | The instance type availability check failed. |
| ★ `InstanceTypeNotFound` | The configured instance type does not exist. |
| ★ `InstanceTypeUnavailable` | The configured instance type has no available capacity. |
| ★ `InstanceCreateFailed` | NICo instance creation failed. |
| `InstanceMissing` | The backing NICo instance could not be found. |
| ★ `InstanceNotReady` | The backing NICo instance is not ready yet. |
| `InstanceReady` | The backing NICo instance is ready. |
| `InstanceNotFound` | The requested import instance is not available in NICo. |
| `InstanceAlreadyClaimed` | Another `NicoMachine` already references the backing instance. |
| `WaitingForNicoMachinesDeletion` | The cluster is waiting for all `NicoMachine`s in its namespace to finish deleting. |
| ★ `ControlPlanePriorityDeferred` | A worker's instance create is held back so a control-plane machine waiting on the same instance type can claim scarce capacity first. |
| `FailureDomainUnavailable` | The requested failure domain has no machine of the configured instance type free to place on. |
| `FailureDomainPlacementFailed` | NICo refused placement in the requested failure domain for a reason free capacity would not resolve. |
| `FailureDomainVerificationFailed` | The failure domain of the assigned machine could not be read back from NICo. |
| `FailureDomainDiscoveryFailed` | NICo failure domains could not be listed. |
| `FailureDomainDrifted` | The assigned machine's failure domain no longer matches the one requested at create. |
| `FailureDomainStable` | The assigned machine is still in the requested failure domain, or none was requested. |

## See also

- [architecture.md](architecture): how these fields interact, credential
  resolution, and the failure-domain mechanism
- [getting-started.md](getting-started): using these CRDs to stand up a
  cluster
- [troubleshooting.md](troubleshooting): what to do about a given
  condition reason
- [README.md](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/README.md): installation and the credentials Secret
- [`config/samples/`](https://github.com/dsx-ai-factory/cluster-api-provider-nico/tree/main/config/samples): minimal worked manifests for all
  four CRDs
