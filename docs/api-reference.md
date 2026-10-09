# API Reference

The following sections document every field of the five CRDs in
`infrastructure.cluster.x-k8s.io/v1alpha1`, one section per kind. Read
[Architecture](architecture.md) for why these fields exist and how they interact.
Read [Getting Started](getting-started.md) for how to use them.

The field descriptions mirror the Go doc comments in `api/v1alpha1/`. If they
ever drift, the Go source is authoritative.

## NicoIdentity

`NicoIdentity` is a namespaced credential-observation API. Standard CAPNICo
installations include its CRD. The NicoIdentity controller periodically checks
the Secret each Identity names and records the result in status, reconciling
every Identity in the manager's scope the same way it reconciles NicoClusters.
Creating an Identity enrolls its Secret, and deleting the Identity stops the
checks. Status stays absent until a check completes, and Identities outside the
manager's scope are not checked. An Identity that leaves the scope keeps its
last published status, which then ages. Absence is not success.

The Identity describes credential health independently of tenant clusters. It
does not select provisioning credentials, protect a Secret from deletion, or
gate cluster or machine reconciliation. Existing `NicoCluster.spec.identityRef`
values continue to reference Secrets. To observe one of those per-cluster
Secrets, create an Identity that names it in the same namespace.

### Spec

The required spec contains one reference. Connection and authentication settings
remain in the Secret.

| Field | Type | Meaning |
|---|---|---|
| `credentialsRef.name` | string, required | Name of a Secret in the Identity's namespace. Uses Kubernetes Secret-name syntax, with a maximum of 253 characters. You can change this reference. |

Admission validates the reference's shape, not whether the Secret exists or
contains usable credentials. The API has no cross-namespace reference field.

### Enabling the Observation

No flag is needed. The manager checks every Identity in its cache scope, as it
does NicoClusters: all namespaces, or only the namespace given by `--namespace`.
With `--watch-filter`, it checks only Identities whose
`cluster.x-k8s.io/watch-filter` label has that value. Changing or removing the
label stops the checks, and the last published status remains and ages. With
the chart's `rbac.namespaced=true`, set `--namespace` to the release namespace,
as NicoCluster and NicoMachine already require.

| Flag | Default | Meaning |
|---|---|---|
| `--provider-identity-validation-timeout` | `30s` | Maximum duration of one check, from token acquisition through the NICo read. Must be positive. |

The controller starts when the NicoIdentity CRD is installed. If the CRD is
missing, the manager logs that credential observation is off and starts
normally. After installing the CRD, restart the manager.

To observe the provider-level credentials, create an Identity that names the
default Secret in its namespace, for example from the sample below. `Ready`
describes only the Secret the Identity names, which need not be the provider's
default.

A check runs when the Identity is created, when its spec or labels change, and
when the manager starts. Another runs four to five minutes after each completed
check, including failed ones. The interval is fixed, and each Identity gets a
stable offset within the last minute, so Identities do not all recheck at once.
Creating, changing or deleting a Secret triggers a check of the Identities in
its namespace that name it within seconds, and the scheduled check still runs
if that event is missed. Checks run one at a
time, so during a NICo outage, when each check can take the full timeout, a
round over many Identities takes proportionally longer. Each check
acquires a new OAuth token, or uses the static token, and calls NICo's
current-tenant endpoint. That endpoint creates the organization's Tenant record
if none exists. Enabling the observation before any cluster exists can therefore
create the record that the first cluster would otherwise create.

### Status

The `/status` subresource holds the following observation fields. Only the
controller writes them.

| Field | Type | Meaning |
|---|---|---|
| `conditions` | list, up to 32 items | Standard Kubernetes conditions, keyed by `type`. `Ready` records the latest completed validation result. |
| `lastCheckedTime` | timestamp | Completion time of the reported attempt, including failures. Absent before a completed attempt. |

For `Ready`, `True` means the completed credential check passed the NICo
current-tenant lookup baseline. `False` means a known configuration, credential,
or permission failure; `Unknown` means the result is inconclusive. This baseline
does not establish all provisioning permissions, capacity, or the intended
principal. Reasons can be extended; messages are diagnostic text for people.

| Ready | Reason | Meaning |
|---|---|---|
| `True` | `ValidationSucceeded` | Authentication and the current-tenant lookup succeeded. The message says so if TLS certificate verification is disabled. |
| `False` | `CredentialsNotFound` | The referenced Secret does not exist. |
| `False` | `InvalidConfiguration` | The Secret lacks required keys or has invalid values. |
| `False` | `AuthenticationFailed` | The token issuer or NICo rejected the credentials. |
| `False` | `AccessDenied` | NICo forbade the current-tenant lookup. |
| `Unknown` | `SecretReadFailed` | The controller could not read the Secret, for example because of RBAC. |
| `Unknown` | `ValidationFailed` | The check timed out, could not connect, failed TLS verification, or got an unexpected response. |

Readers select `Ready` by type and compare its `observedGeneration` with
`metadata.generation`. They also inspect `lastCheckedTime` for freshness. The
controller writes `Ready` and `lastCheckedTime` in separate requests, so for a
moment after a check `lastCheckedTime` can still show the previous one.
Missing or outdated observations do not establish current health. Condition
`lastTransitionTime` records a change in truth value, not the last check.
Additional conditions must not change those existing meanings.

The `kubectl get nicoidentities` columns show Ready, LastChecked, and Age.
Compare LastChecked with the current time: a stopped manager cannot mark its
last result stale, and `kubectl wait --for=condition=Ready` does not check age.
A failed check updates status only; it raises no Event or alert. Deleting an
Identity removes only the observation object, not its Secret or any NICo
resource.

Use the desired-only
[`NicoIdentity sample`](../config/samples/infrastructure_v1alpha1_nicoidentity.yaml)
in the namespace containing the credentials Secret. Installation bundles do not
create an Identity or credential Secret. The `nicoidentity-viewer-role` helper
grants Identity reads without Secret access or permission to write spec/status.

## NicoCluster

`NicoCluster` is namespaced and uses the short name `nicoc`. Its `kubectl get`
output prints the Available, Synced, Provisioned, Site, and Age columns.

### Spec

The spec carries four fields, and only the site is required.

| Field | Type | Meaning |
|---|---|---|
| `siteID` | string, required | The site where this provider creates and looks up instances. |
| `identityRef` | object, optional | Points to the Secret that holds the NICo endpoint and the authentication settings. An unset value falls back to the provider-level credentials Secret in the manager's namespace. |
| `powerControlIdentityRef` | object, optional | Points to a Secret in the cluster namespace with provider credentials for NICo Machine power control. An unset value uses the regular `identityRef` credential. |
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
| `ipxeScript` | string | The iPXE script used to boot this instance. This field is mutually exclusive with `operatingSystemID`. |
| `operatingSystemID` | string | The ID of a registered NICo operating system used to boot this instance. This field is mutually exclusive with `ipxeScript`. |
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
| `reboot` | object, optional | Records the latest annotation-driven reboot. It contains `annotation`, the accepted `annotationValue`, `mode`, `phase`, `startedAt`, optional `deadline`, `bootID`, `completedAt`, and the final `path` and `message`. |
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

The cluster and machine reconcilers publish four condition types. `NicoIdentity` reports its own `Ready` condition, described in [NicoIdentity](#nicoidentity).

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
  holds minimal worked manifests for all five CRDs.
