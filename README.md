# cluster-api-provider-nico

`cluster-api-provider-nico` is a Cluster API infrastructure provider that provisions bare metal instances in [NCX Infra Controller (NICo)](https://github.com/NVIDIA/ncx-infra-controller-core) through its [REST API](https://github.com/NVIDIA/ncx-infra-controller-rest).

* `NicoCluster` holds shared NICo configuration such as site, VPC, and a Secret reference for API credentials.
* `NicoMachine` represents one NICo instance managed by Cluster API.
* `NicoMachineTemplate` supports `KubeadmControlPlane` and `MachineDeployment`.

## How It Works

The provider uses a namespaced Secret for NICo connection details and authentication.
The Secret supports either a static bearer token or OAuth2 client-credentials.

Each `NicoCluster` supplies the target site and VPC.
Each `NicoMachine` becomes one NICo instance, and `NicoMachineTemplate` is intended for use from `KubeadmControlPlane` and `MachineDeployment`.

The controller discovers the current tenant through the NICo API and caches it in the client.
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

## NICo credentials Secret

Each `NicoCluster` references a Secret in the same namespace.

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

A full kubeadm-based example lives in `examples/kubeadm/cluster.yaml`.

Before applying it, replace the placeholder values for:

* API endpoint and either token or OAuth2 client-credentials in the Secret
* NICo org in the Secret, plus required site ID and VPC ID in `NicoCluster`
* instance type, subnet, SSH key group IDs, and iPXE script content
* Kubernetes version

Apply the example:

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