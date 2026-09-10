# cluster-api-provider-nico

[![Test](https://github.com/NVIDIA/cluster-api-provider-nico/actions/workflows/test.yml/badge.svg)](https://github.com/NVIDIA/cluster-api-provider-nico/actions/workflows/test.yml)
[![Lint](https://github.com/NVIDIA/cluster-api-provider-nico/actions/workflows/lint.yml/badge.svg)](https://github.com/NVIDIA/cluster-api-provider-nico/actions/workflows/lint.yml)
[![License](https://img.shields.io/github/license/NVIDIA/cluster-api-provider-nico)](LICENSE)
[![Latest Release](https://img.shields.io/github/v/release/NVIDIA/cluster-api-provider-nico?include_prereleases)](https://github.com/NVIDIA/cluster-api-provider-nico/releases)

Kubernetes Cluster API (CAPI) infrastructure provider to provision bare metal nodes in [NVIDIA Infra Controller (NICo)](https://github.com/NVIDIA/infra-controller).

CAPNICo is a Cluster API **infrastructure provider** and does one job: give
Cluster API the machines it asks for. It does not install Kubernetes and it does
not join nodes — the kubeadm bootstrap and control-plane providers do that. You
declare a `Cluster` and a `MachineDeployment` as usual, and this provider turns
each requested machine into a NICo instance on real hardware.

* `NicoCluster` holds the target site, and may optionally reference a per-cluster credentials Secret.
* `NicoMachine` represents one NICo instance managed by Cluster API, and carries the VPC.
* `NicoMachineTemplate` supports `KubeadmControlPlane` and `MachineDeployment`.

## Features

- **Bare-metal machines through the Cluster API contract.** A `MachineDeployment`
  or `KubeadmControlPlane` provisions real hardware with no separate workflow.
- **Two credential styles.** A static bearer token, or OAuth2 client
  credentials, supplied through a Secret.
- **Per-cluster credentials.** `NicoCluster` may reference its own Secret, so one
  management cluster can drive several sites.
- **Credential rotation without a restart.** Clients are cached per Secret
  `resourceVersion`, so an external rotation is picked up on the next reconcile.
- **Idempotent instance creation.** A create conflict resolves by looking up the
  existing instance by name rather than failing or duplicating.
- **External control-plane endpoints**, including a kube-vip template and a
  developer flow for a single requested IP.
- **Machine repair and reboot**, both driven by annotations.
- **An ordered deletion lifecycle**, guarded by a finalizer.

## How this fits with other tools

This provider does one job and leaves the rest to the standard Cluster API
components:

| Component | What it does | Relationship |
|---|---|---|
| Cluster API core | Decides which machines should exist | Asks this provider for them |
| Kubeadm bootstrap and control-plane providers | Install Kubernetes and join nodes | **This provider does neither** |
| [NVIDIA Infra Controller (NICo)](https://github.com/NVIDIA/infra-controller) | Provisions the physical instances | This provider drives its API |
| Other infrastructure providers (CAPD, CAPA, and so on) | The same role on a different substrate | Interchangeable — swap the provider, keep the workflow |

If you already run Cluster API, your workflow does not change. You declare the
same objects, and this provider satisfies them with NICo hardware.

## Documentation

- [docs/getting-started.md](docs/getting-started.md) — **start here.** First
  cluster three ways: the in-repo fake, a local NICo, and a production site.
  What must exist on the NICo side first, the credentials Secret, what the OS
  image has to contain, and the traps.
- [docs/architecture.md](docs/architecture.md) — how the pieces fit: the CRDs and
  which fields live on which, credential resolution and client caching, machine
  reconciliation and what the finalizer guards, provider ID and node matching,
  teardown order, and the annotation-driven repair and reboot contracts.
  **Read this before changing controller behaviour.**
- [CONTRIBUTING.md](CONTRIBUTING.md) — development setup and the pull-request flow
- [RELEASE.md](RELEASE.md) — versioning and the Cluster API contract
- [SECURITY.md](SECURITY.md) — vulnerability reporting
- [NICo documentation](https://docs.nvidia.com/infra-controller/) — the
  infrastructure this provider drives

## Community and support

- **Questions and discussion** — ask in
  [GitHub Discussions](https://github.com/NVIDIA/cluster-api-provider-nico/discussions/categories/q-a),
  where an answer can be marked as the accepted one.
- **Bug reports and feature requests** — open a
  [GitHub issue](https://github.com/NVIDIA/cluster-api-provider-nico/issues).
- **Code of Conduct** — everyone taking part is expected to follow the
  [Code of Conduct](CODE_OF_CONDUCT.md).
- **Security** — report vulnerabilities as described in [SECURITY.md](SECURITY.md).
  **Do not open a public issue for a security report.**
- **Cluster API itself** — for questions about Cluster API rather than this
  provider, the upstream project runs `#cluster-api` on
  [Kubernetes Slack](https://slack.k8s.io/), and the
  [Cluster API book](https://cluster-api.sigs.k8s.io/) covers the core concepts.
- **Support level** — Experimental.

## How It Works

### Architecture

One controller, three CRDs, and one outbound API. Everything runs in the
management cluster; nothing is installed on the provisioned nodes.

```
   Cluster API core                    management cluster
   ├── Cluster          ──────────►  NicoCluster      (target site, credentials)
   ├── MachineDeployment ─────────►  NicoMachineTemplate
   └── Machine          ──────────►  NicoMachine      (one instance, one VPC)
                                          │
                                          │  CAPNICo controller
                                          ▼
                                     NICo REST API
                                          │
                                          ▼
                                     bare-metal instance
                                     (iPXE boot, kubeadm cloud-init)
```

Cluster API owns the desired state. This controller reconciles each
`NicoMachine` into one NICo instance and reports the provider ID back, which is
how the node is matched. The kubeadm providers take over from there.

[docs/architecture.md](docs/architecture.md) has the detail: field ownership,
credential resolution and client caching, what the finalizer guards, teardown
order, and the repair and reboot contracts.

### Credentials and reconciliation

The provider uses a Secret for NICo connection details and authentication. The
Secret supports either a static bearer token or OAuth2 client-credentials.

Each `NicoCluster` supplies the target site. The VPC is set per machine, on
`NicoMachine.spec.vpcID`.
Each `NicoMachine` becomes one NICo instance, and `NicoMachineTemplate` is intended for use from `KubeadmControlPlane` and `MachineDeployment`.

The controller discovers the current tenant through the NICo API and caches it in the client.
NICo clients themselves are cached per Secret `resourceVersion`, so external
rotations of the credentials Secret (for example by External Secrets Operator)
are picked up on the next reconcile without restarting the manager.
Machine reconciliation treats instance creation and instance readiness separately, and create conflicts are handled idempotently by looking up an existing instance by name.

The provider expects the NICo API to be reachable from the management cluster, and it assumes the target VPC and subnet or VPC prefix are already defined for the machines you want to provision.

The `NicoMachine`'s iPXE script must boot an OS image that can consume kubeadm cloud-init user data.

## Local development (no NICo access needed)

`make tilt-up` creates a local kind cluster, brings up Cluster API core, installs
CAPNICo through its native Helm chart, and runs it against the fake NICo
endpoint shipped in this repository. No hardware and no access to a real NICo
deployment is required.

```bash
# Create the kind cluster and start Tilt; the web UI is on localhost:10352
make tilt-up
```

The Tilt UI deliberately avoids Tilt's default port of 10350, so this loop runs
alongside another Cluster API development environment on the same machine.
Override `CAPNICO_TILT_PORT` if 10352 is taken too.

In a second terminal, point kubectl at the local cluster and apply the worked
example:

```bash
export KUBECONFIG=~/.kube/capnico.kubeconfig   # or: eval "$(make kubeconfig)"

kubectl apply -f examples/cluster-fake.yaml
kubectl get nicoclusters,nicomachines
```

Tear everything down with `make tilt-down`.

To run just the fake endpoint on its own, without Kubernetes:

```bash
make run-fake   # serves the NICo API surface on :8090
```

See [docs/development.md](docs/development.md) for the complete development
workflow and test commands.

### Failure domains

CapNICo implements the Cluster API failure domain contract so control-plane
machines can be spread across correlated-failure boundaries such as rack groups.

* `NicoCluster.status.failureDomains` lists the domains NICo offers for the
  cluster's site. Cluster API copies this to `Cluster.status.failureDomains`.
* The control-plane provider selects a domain and sets `spec.failureDomain` on
  each `Machine`.
* CapNICo discovers domain names from the labels on the site's Machines that
  have an Instance Type. For a requested domain, it sends
  `machineLabelSelector: {"failure_domain":"<domain>"}` with the instance create
  request. NICo selects and locks a matching Machine atomically. Automatic
  placement retains `instanceTypeId`; an explicitly configured
  `NicoMachine.spec.machineID` retains `machineId`, and NICo validates that
  Machine against the same selector.
* `NicoCluster.spec.failureDomainLabelKey` selects the Machine label key. It is
  unset by default, which disables failure domains for the cluster; NICo defines
  no canonical key, so set it to whatever the site stamps on its Machines.
* CapNICo reads the assigned Machine after creation and reports its label as
  `NicoMachine.status.failureDomain`; it never copies the requested value into
  observed status without verification. A label that changes after placement is
  reported on the `FailureDomainDrifted` condition and does not fail an
  already-provisioned machine.

Every published domain needs Machines of every Instance Type the cluster
provisions. A control-plane machine assigned to a domain with none retries
placement under `FailureDomainUnavailable` and never progresses, because Cluster
API does not reassign an existing Machine's failure domain.

Failure-domain placement requires a NICo server containing
[NVIDIA/infra-controller#5484](https://github.com/NVIDIA/infra-controller/pull/5484),
merged to `main` on August 28, 2026. The SDK module
`rest-api/sdk/standard` carries no semver tags, so CapNICo pins the
pseudo-version `v0.0.0-20260901235154-eafb6b962baf`. Older servers silently
ignore unknown JSON fields and are not compatible with this CapNICo behavior.
The JSON field is `machineLabelSelector`; the generated Go SDK exposes
`MachineLabelSelector` (`map[string]string`) and `SetMachineLabelSelector`.

A non-empty selector requires NICo's effective `targetedInstanceCreation`
capability for the selected site, including automatic `instanceTypeId`
placement. Explicit `machineId` placement also requires that capability. If no
matching Machine is available, the `NicoMachine` reports
`FailureDomainPlacementFailed` and retries without unconstrained placement.
Missing capability and explicit-selector mismatch are reported as non-capacity
placement failures. There is no client-side Machine-list/create race because
matching and allocation happen in the create operation.

The same capability gates listing Machines, so an identity without it publishes
no failure domains and the cluster provisions as it did before failure-domain
support. Failure domains are therefore opt-in per tenant and site, with no
feature flag.

Because CapNICo reconciles and creates each Cluster API Machine independently,
it does not use NICo's batch allocation API, even though
`machineLabelSelector` is supported there too. CapNICo still creates one
`NicoMachine` at a time and does not yet provide grouped NVLink co-placement.

## Install

Initialize the core Cluster API controllers and the kubeadm providers:

```bash
export KUBECONFIG=/path/to/management-cluster.kubeconfig
clusterctl init --bootstrap kubeadm --control-plane kubeadm
```

Build and push the controller image you want to run:

```bash
make docker-build IMG=ghcr.io/your-org/cluster-api-provider-nico:latest
```

Update `config/manager/manager.yaml` to use that image, then install the provider:

```bash
kubectl apply -k config/default
```

Alternatively, install the native Helm chart and supply the same controller
image without editing the Kustomize source:

```bash
helm upgrade --install capi-provider-nico ./chart \
  --namespace capnico-system \
  --create-namespace \
  --set manager.image.repository=ghcr.io/your-org/cluster-api-provider-nico \
  --set manager.image.tag=latest \
  --wait
```

See the [chart documentation](chart/README.md) for published chart locations,
configuration values, and compatibility notes for upgrading an existing Helm
release.

## Provider release artifacts

CAPNICo publishes Cluster API provider artifacts in the same shape consumed by
`clusterctl`: `metadata.yaml` and `infrastructure-components.yaml`.

Generate the local artifacts with the controller image you want to publish:

```bash
CONTROLLER_IMG=ghcr.io/nvidia/cluster-api-provider-nico/controller:v0.0.43 \
make release-manifests
```

This writes the clusterctl artifacts to `out/`. Tagging a release attaches the
same two files to the GitHub Release.

Update `metadata.yaml` only when introducing a new major/minor release series or
changing the Cluster API contract supported by a release series. Patch releases
within the same series should keep the existing metadata entry.

To consume a released CAPNICo provider from `clusterctl`, add the release asset
URL to your clusterctl configuration. Create or update the default config file:

```bash
mkdir -p ~/.config/cluster-api
$EDITOR ~/.config/cluster-api/clusterctl.yaml
```

If the file already exists, merge the `nico` entry into the existing `providers`
list. The version in the URL is the default used when a `clusterctl` command
does not specify one explicitly:

```yaml
providers:
  - name: nico
    type: InfrastructureProvider
    url: https://github.com/NVIDIA/cluster-api-provider-nico/releases/download/v0.0.43/infrastructure-components.yaml
```

Then initialize the provider:

```bash
clusterctl init --infrastructure nico:v0.0.43
```

### Controller scope

The CAPNICo manager accepts the standard Cluster API provider scope flags:

* `--namespace` limits reconciliation to Cluster API objects in one namespace.
  The empty default watches all namespaces, which is the mode used by
  `clusterctl` installations.
* `--watch-filter` limits reconciliation to objects labeled
  `cluster.x-k8s.io/watch-filter=<value>`. The empty default reconciles all
  objects.

Use both flags when running multiple CAPNICo manager instances in one
management cluster.

This release does not publish workload cluster templates yet. Use
`clusterctl generate cluster --from <template-file-or-url>` with a local
template when generating clusters.

The generated provider manifest references the controller image you pass through
`CONTROLLER_IMG`. Management clusters must be able to pull that image. For local
or private deployments, either grant the cluster access to that registry or
regenerate the artifacts with an image mirrored to a registry the cluster can
reach.


## NICo credentials Secret

The provider supports two sources for the NICo credentials Secret. Each is
optional on its own, but at least one must be configured for the provider to
reconcile a `NicoCluster`. They are not mutually exclusive — both may be
present at the same time:
1. A provider-level Secret in the manager's own namespace, used by every
   `NicoCluster` that does not set its own `spec.identityRef`.
2. A per-cluster Secret referenced by `NicoCluster.spec.identityRef`, in the
   same namespace as the `NicoCluster`. When set, it overrides the
   provider-level Secret for that `NicoCluster`.

The provider-level Secret's name and namespace are configurable via two manager flags:

* `--provider-credentials-namespace` (default: `$POD_NAMESPACE`, falling back
  to `capnico-system` when unset, e.g. under `make run`)
* `--provider-credentials-secret-name` (default: `nico-credentials`)

The same keys are used for both the provider-level Secret or per-cluster
override(s).

Required keys:

* `endpoint`: base URL for the NICo API, for example `https://nico.example.com`
* `orgID`: NICo org identifier used in API paths

Choose one authentication mode.

Static bearer token:

* `token`: bearer token used for API calls

OAuth2 client credentials:

* `tokenURL`: OAuth2 token endpoint
* `clientID`: OAuth2 client ID
* `clientSecret`: OAuth2 client secret
* `scope`: optional space-separated scopes, for example `carbide`

Optional keys:

* `ca.crt`: PEM-encoded CA bundle
* `insecureSkipTLSVerify`: `true` or `false`
* `apiName`: NICo API name, NICo allows customers to optionally specify a custom API name for their deployment.

The published NICo SDK does not include a helper for token acquisition beyond accepting a bearer token in request context, so this provider performs the OAuth2 client-credentials exchange itself and refreshes access tokens automatically.

Example using OAuth2 client credentials:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: nico-credentials
  namespace: default
type: Opaque
stringData:
  endpoint: https://nico.example.com
  orgID: your-org
  tokenURL: https://issuer.example.com/token
  clientID: <client-id>
  clientSecret: <client-secret>
  scope: carbide
  ca.crt: |
    -----BEGIN CERTIFICATE-----
    ...
    -----END CERTIFICATE-----
```

## Kubeadm examples

The kubeadm examples are local `clusterctl` templates. They assume the
provider-level credentials Secret already exists:

```bash
kubectl create secret generic -n capnico-system nico-credentials \
  --from-literal=endpoint=https://nico.example.com \
  --from-literal=orgID=your-org \
  --from-literal=token=<bearer-token>

# Or use OAuth2 client credentials instead of a static token:
kubectl create secret generic -n capnico-system nico-credentials \
  --from-literal=endpoint=https://nico.example.com \
  --from-literal=orgID=your-org \
  --from-literal=tokenURL=<token-url> \
  --from-literal=clientID=<client-id> \
  --from-literal=clientSecret=<client-secret> \
  --from-literal=scope=<scope>
```

The kubeadm templates patch kubelet with `providerID: nico://<instance-id>` by reading
the NICo metadata service at `169.254.169.254:7777`. This lets Cluster API match
workload-cluster Nodes back to their `Machine` objects.

The NICo architecture documentation describes The Metadata Service (FMDS) as the local HTTP metadata API provided by the DPU agent:
[Overview and Components](https://docs.nvidia.com/infra-controller/documentation/architecture/overview-and-components).

List the variables required by a template with:

```bash
clusterctl generate cluster demo \
  --from examples/kubeadm/cluster.yaml \
  --list-variables
```

### External control-plane endpoint

Use `examples/kubeadm/cluster.yaml` when a stable Kubernetes API endpoint
already exists, for example through DNS, an external load balancer, or kube-vip
managed outside this template.

`NICO_NETWORK_METHOD` selects the NICo network attachment field and defaults to
`vpcPrefixID`, which is preferred for new clusters. Set
`NICO_NETWORK_METHOD=subnetID` for legacy subnet networking. `NICO_NETWORK_ID`
is the corresponding VPC prefix or subnet ID.

Set `CONTROL_PLANE_ENDPOINT_HOST` to the stable API endpoint IP or DNS name.

```bash
kubectl create namespace demo

NICO_SITE_ID=your-site \
NICO_VPC_ID=your-vpc \
NICO_CONTROL_PLANE_INSTANCE_TYPE_ID=your-control-plane-instance-type \
NICO_WORKER_INSTANCE_TYPE_ID=your-worker-instance-type \
NICO_NETWORK_METHOD=vpcPrefixID \
NICO_NETWORK_ID=your-vpc-prefix \
NICO_CONTROL_PLANE_IPXE_SCRIPT='chain https://boot.example.com/ipxe/control-plane.ipxe' \
NICO_WORKER_IPXE_SCRIPT='chain https://boot.example.com/ipxe/worker.ipxe' \
CONTROL_PLANE_ENDPOINT_HOST=10.0.0.100 \
clusterctl generate cluster demo \
  --from examples/kubeadm/cluster.yaml \
  --target-namespace demo \
  --kubernetes-version v1.36.0 \
  --control-plane-machine-count 1 \
  --worker-machine-count 1 \
  | kubectl apply -f -
```

### Kube-vip convenience template

Use `examples/kubeadm/cluster-kube-vip.yaml` when a stable API endpoint does not
already exist and you want the template to bootstrap kube-vip as a static pod.
The network variables are the same as `cluster.yaml`.

Set `KUBE_VIP_ADDRESS` to the stable API endpoint IP. Set
`CONTROL_PLANE_ENDPOINT_HOST` to that IP or to a DNS name that resolves to it.
Joining nodes require this endpoint to be reachable after kube-vip starts. By
default, `KUBE_VIP_BGP_PEER_AS=auto` reads the peer ASN from the NICo instance
metadata service at `/latest/meta-data/asn`. 

```bash
kubectl create namespace demo

NICO_SITE_ID=your-site \
NICO_VPC_ID=your-vpc \
NICO_CONTROL_PLANE_INSTANCE_TYPE_ID=your-control-plane-instance-type \
NICO_WORKER_INSTANCE_TYPE_ID=your-worker-instance-type \
NICO_NETWORK_METHOD=vpcPrefixID \
NICO_NETWORK_ID=your-vpc-prefix \
NICO_CONTROL_PLANE_IPXE_SCRIPT='chain https://boot.example.com/ipxe/control-plane.ipxe' \
NICO_WORKER_IPXE_SCRIPT='chain https://boot.example.com/ipxe/worker.ipxe' \
KUBE_VIP_ADDRESS=10.0.0.100 \
CONTROL_PLANE_ENDPOINT_HOST=10.0.0.100 \
clusterctl generate cluster demo \
  --from examples/kubeadm/cluster-kube-vip.yaml \
  --target-namespace demo \
  --kubernetes-version v1.36.0 \
  --control-plane-machine-count 1 \
  --worker-machine-count 1 \
  | kubectl apply -f -
```

### Developer: single control-plane requested IP

`examples/kubeadm/cluster-single-control-plane-static-ip.yaml` exists for
developer bring-up and requested-IP testing. It skips kube-vip and requests
`CONTROL_PLANE_ENDPOINT_IP` directly as the control-plane NICo interface IP,
then uses the same address as the Kubernetes API server endpoint.

`CONTROL_PLANE_ENDPOINT_IP` must be an available IP in the VPC prefix and must
have its least-significant host bit set to `1`, which is required by NICo's
VPC-prefix linknet allocation. Do not use this template for normal clusters or
HA control planes; use `cluster-kube-vip.yaml` instead. This is only intended 
for development with minimal hardware requirements.

```bash
kubectl create namespace demo

NICO_SITE_ID=your-site \
NICO_VPC_ID=your-vpc \
NICO_CONTROL_PLANE_INSTANCE_TYPE_ID=your-control-plane-instance-type \
NICO_WORKER_INSTANCE_TYPE_ID=your-worker-instance-type \
NICO_VPC_PREFIX_ID=your-vpc-prefix \
NICO_CONTROL_PLANE_IPXE_SCRIPT='chain https://boot.example.com/ipxe/control-plane.ipxe' \
NICO_WORKER_IPXE_SCRIPT='chain https://boot.example.com/ipxe/worker.ipxe' \
CONTROL_PLANE_ENDPOINT_IP=10.0.0.11 \
clusterctl generate cluster demo \
  --from examples/kubeadm/cluster-single-control-plane-static-ip.yaml \
  --target-namespace demo \
  --kubernetes-version v1.36.0 \
  --worker-machine-count 1 \
  | kubectl apply -f -
```

## Deletion lifecycle

The supported teardown path is to delete the CAPI `Cluster` and wait for the
`Cluster` deletion to complete before deleting the namespace, if it is still
needed. This lets Cluster API delete machines before the shared infrastructure
object and gives each `NicoMachine` a chance to delete its backing NICo instance
while credentials are still available.

Do not use namespace deletion as the normal teardown mechanism while NICo
instances may still exist. A namespace delete can remove the per-cluster
credentials Secret before `NicoMachine` finalizers finish, which can leave
machines stuck in deletion and require manual NICo cleanup.

## Machine Repair

CAPNICo exposes repair as an annotation-driven contract on the owning CAPI
`Machine`. A consumer requests that a NICo instance be flagged for repair before
deletion by setting the configured repair annotation on the `Machine`. CAPNICo
treats a non-empty annotation value as the repair request and forwards the
health issue to the NICo delete request as machine health context for the repair
workflow.

The value is parsed as JSON first:

```json
{"category": "Thermal", "summary": "over temperature", "details": "optional"}
```

A value that does not parse as JSON is treated as a legacy plain-string summary
and assigned the category `Other`.

The feature can be disabled by setting the flag to an empty string.

The default annotation key is:

* `nico.nvidia.com/machine-health-issue`

The key is configurable with a manager flag:

* `--repair-annotation`

## Machine Reboot

CAPNICo exposes reboot as an annotation-driven contract on the owning CAPI
`Machine`. A consumer requests a reboot by setting the configured reboot
annotation on the `Machine`. CAPNICo treats a non-empty annotation value as the
reboot request; the value itself is consumer-owned metadata and is not
interpreted.
CAPNICo triggers at most one NICo instance reboot for each observed annotation
application. After NICo accepts the reboot trigger, CAPNICo removes the
configured reboot annotation from the `Machine`.

The default annotation key is:

* `nico.nvidia.com/reboot`

The key is configurable with a manager flag:

* `--reboot-annotation`

## Development

CAPNICo uses Kubebuilder project metadata and Makefile conventions. Start with
the Makefile help output when looking for common development tasks:

```bash
make help
```

Common local checks:

```bash
make generate
make manifests
make test
make build
```

CAPNICo keeps controllers in the top-level `controllers` package to stay close
to CAPA and CAPG. Kubebuilder `go/v4` scaffolds controllers under
`internal/controller` by default, so this repository includes a small external
Kubebuilder plugin that adapts generated controller files into the CAPNICo
layout.

Use the plugin Makefile when initializing the project scaffold or adding a new
API/controller:

```bash
make -C hack/kubebuilder/plugins/capnico-layout/v1 init-project
make -C hack/kubebuilder/plugins/capnico-layout/v1 create-api KIND=NicoCluster
```

For template resources that do not need reconcilers:

```bash
make -C hack/kubebuilder/plugins/capnico-layout/v1 create-api KIND=NicoClusterTemplate CONTROLLER=false
```

The plugin's own
[README](hack/kubebuilder/plugins/capnico-layout/v1/README.md) describes what it
does and how to install it.

## Contributing

- Start here: [CONTRIBUTING.md](CONTRIBUTING.md)
- Code of Conduct: [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
- Coding agents: [AGENTS.md](AGENTS.md)

See [Community and support](#community-and-support) above for where to ask
questions and how to report a vulnerability.

## License

Apache License 2.0. See [LICENSE](LICENSE).
