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

The provider expects the NICo API to be reachable from the management cluster, and it assumes the target VPC, subnet (or VPC prefix), and SSH key groups are already defined for the machines you want to provision.

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

To consume a released CAPNICo provider from `clusterctl`, add it to your
clusterctl configuration. `clusterctl` reads this file from
`${XDG_CONFIG_HOME}/cluster-api/clusterctl.yaml` when `XDG_CONFIG_HOME` is set:

```bash
export XDG_CONFIG_HOME="${HOME}/.config"
mkdir -p "${XDG_CONFIG_HOME}/cluster-api"
```

Create or update `${XDG_CONFIG_HOME}/cluster-api/clusterctl.yaml`. If the file
already exists, merge the `nico` entry into the existing `providers` list:

```yaml
providers:
  - name: nico
    type: InfrastructureProvider
    url: https://gitlab-master.nvidia.com/api/v4/projects/263631/packages/generic/cluster-api-provider-nico/v0.0.8/infrastructure-components.yaml
```

Then initialize the provider:

```bash
clusterctl init --infrastructure nico:v0.0.8
```

For local testing before a release is published, generate artifacts with an image
the management cluster can pull. For example, with kind:

```bash
docker build -t docker.io/library/cluster-api-provider-nico:v0.0.8 .
kind load docker-image docker.io/library/cluster-api-provider-nico:v0.0.8 --name <kind-cluster-name>

CONTROLLER_IMG=docker.io/library/cluster-api-provider-nico:v0.0.8 make release-manifests
mkdir -p out/local-repository/infrastructure-nico/v0.0.8
cp out/infrastructure-components.yaml out/local-repository/infrastructure-nico/v0.0.8/
cp out/metadata.yaml out/local-repository/infrastructure-nico/v0.0.8/
```

Point `clusterctl.yaml` at the local components file, then run
`clusterctl init --infrastructure nico:v0.0.8`:

```yaml
providers:
  - name: nico
    type: InfrastructureProvider
    url: /path/to/cluster-api-provider-nico/out/local-repository/infrastructure-nico/v0.0.8/infrastructure-components.yaml
```

On macOS, if `XDG_CONFIG_HOME` is not set, `clusterctl` uses
`${HOME}/Library/Application Support/cluster-api/clusterctl.yaml`. Also note
that `${HOME}/.cluster-api/overrides/...` takes precedence over provider URLs.

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

## Kubeadm example

A full kubeadm-based example lives in `examples/kubeadm/cluster.yaml`. It
relies on the provider-level credentials Secret:

```bash
kubectl create secret generic -n capnico-system nico-credentials \
  --from-literal=endpoint=https://nico.example.com \
  --from-literal=orgID=your-org \
  # Add appropriate auth mode specific keys here
  # --from-literal=token=<bearer-token> \
  # --from-literal=tokenURL=<token-url> \
  # --from-literal=clientID=<client-id> \
  # --from-literal=clientSecret=<client-secret> \
  # --from-literal=scope=<scope>

```

Apply the example:

Before applying it, replace the placeholder values for:

* required site ID and VPC ID in `NicoCluster`
* instance type, subnet, SSH key group IDs, and iPXE script content
* Kubernetes version


```bash
kubectl apply -f examples/kubeadm/cluster.yaml
```

## Development

Generate API deepcopies, CRDs, and RBAC:

```bash
make generate manifests
```

Run tests:

```bash
make test
```

Run the manager locally against the current `KUBECONFIG`:

```bash
make run
```