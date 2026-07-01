# cluster-api-provider-nico

Kubernetes Cluster API (CAPI) infrastructure provider to provision bare metal nodes in [NCX Infra Controller (NICo)](https://github.com/NVIDIA/ncx-infra-controller-core).

* `NicoCluster` holds shared NICo configuration such as site and VPC, and may optionally reference a per-cluster credentials Secret.
* `NicoMachine` represents one NICo instance managed by Cluster API.
* `NicoMachineTemplate` supports `KubeadmControlPlane` and `MachineDeployment`.

## How It Works

The provider uses a Secret for NICo connection details and authentication.


The Secret supports either a static bearer token or OAuth2 client-credentials.

Each `NicoCluster` supplies the target site and VPC.
Each `NicoMachine` becomes one NICo instance, and `NicoMachineTemplate` is intended for use from `KubeadmControlPlane` and `MachineDeployment`.

The controller discovers the current tenant through the NICo API and caches it in the client.
NICo clients themselves are cached per Secret `resourceVersion`, so external
rotations of the credentials Secret (for example by External Secrets Operator)
are picked up on the next reconcile without restarting the manager.
Machine reconciliation treats instance creation and instance readiness separately, and create conflicts are handled idempotently by looking up an existing instance by name.

The provider expects the NICo API to be reachable from the management cluster, and it assumes the target VPC and subnet or VPC prefix are already defined for the machines you want to provision.

The `NicoMachine`'s iPXE script must boot an OS image that can consume kubeadm cloud-init user data.

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

## Provider release artifacts

CAPNICo publishes Cluster API provider artifacts in the same shape consumed by
`clusterctl`: `metadata.yaml` and `infrastructure-components.yaml`.

Generate the local artifacts with the controller image you want to publish:

```bash
CONTROLLER_IMG=registry.gitlab-master.nvidia.com/nke/cluster-api-provider-nico:v0.0.8 \
make release-manifests
```

This writes the clusterctl artifacts to `out/`. Release pipelines publish those
files to GitLab Generic Packages under a versioned package path:

```text
${CI_API_V4_URL}/projects/${CI_PROJECT_ID}/packages/generic/cluster-api-provider-nico/<version>/
```

Update `metadata.yaml` only when introducing a new major/minor release series or
changing the Cluster API contract supported by a release series. Patch releases
within the same series should keep the existing metadata entry.

To consume a released CAPNICo provider from `clusterctl`, add the GitLab Generic
Package URL to your clusterctl configuration. Use the `gitlab.nvidia.com` alias instead of
`gitlab-master.nvidia.com`; `clusterctl` only recognizes self-hosted GitLab
provider URLs when the hostname starts with `gitlab.`.

Create or update the default `clusterctl` config file:

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
    url: https://gitlab.nvidia.com/api/v4/projects/263631/packages/generic/cluster-api-provider-nico/v0.0.10/infrastructure-components.yaml
```

Then initialize the provider:

```bash
clusterctl init --infrastructure nico:v0.0.10
```

This release does not publish workload cluster templates yet. Use
`clusterctl generate cluster --from <template-file-or-url>` with a local
template when generating clusters.

The generated provider manifest references the controller image you pass through
`CONTROLLER_IMG`. Management clusters must be able to pull that image. For local
or private deployments, either grant access to the GitLab registry image or
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
`Machine`. A consumer requests that a NiCo instance be flagged for repair
before deletion by setting the configured repair annotation on the `Machine`.
CAPNICo treats annotation presence as the repair request; the annotation value
is used as the health-issue summary forwarded to NiCo. When the annotation is
present, CAPNICo forwards the health issue to the NiCo delete request as
machine health context for the repair workflow.

The feature can be disabled by setting the flag to an empty string.

The default annotation key is:

* `nico.nvidia.com/machine-health-issue`

The key is configurable with a manager flag:

* `--repair-annotation`

## Machine Reboot

CAPNICo exposes reboot as an annotation-driven contract on the owning CAPI
`Machine`. A consumer requests a reboot by setting the configured reboot
annotation on the `Machine`. CAPNICo treats annotation presence as the reboot
request; the annotation value is consumer-owned metadata and is not interpreted.
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

The full scaffold command log and plugin validation notes are recorded in
`docs/kubebuilder-setup.md`.
