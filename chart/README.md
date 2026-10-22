# CAPI Provider NICo Helm Chart

This is the native Helm distribution of the Cluster API infrastructure
provider for NICo. It installs the controller, ServiceAccount, RBAC, metrics
Service, and five CAPNICo CRDs without embedding a pre-rendered Kustomize
manifest.

## Install from a checkout

Install cert-manager in the management cluster before installing this chart.
The chart creates the certificate resources for its validating webhook but
does not install cert-manager. `clusterctl init` installs cert-manager when
required.

Keep `webhook.enabled` and `certManager.enabled` set to `true`. The chart rejects
disabled settings because template validation and webhook certificates are required.
External webhook certificates are not supported.

```bash
helm upgrade --install capi-provider-nico ./chart \
  --namespace capnico-system \
  --create-namespace \
  --set manager.image.repository=ghcr.io/dsx-ai-factory/cluster-api-provider-nico/controller \
  --set manager.image.tag=v0.0.45 \
  --wait
```

The controller expects its NICo credentials Secret to exist independently of
the chart. See the repository [README](../README.md) for the Secret layout and
provider-level credential flags.

## Published variants

Releases publish the same chart with registry-specific image defaults:

| Variant | Chart location | Controller image |
|---|---|---|
| GHCR | `oci://ghcr.io/dsx-ai-factory/cluster-api-provider-nico/charts/capi-provider-nico` | Promoted GHCR image |
| DSX NGC | `<DSX_NGC_ORG>/<DSX_NGC_TEAM>/capi-provider-nico` | DSX NVCR image |
| NKE | `oci://nvcr.io/j7sbcjl3qgta/capi-provider-nico` | NKE NVCR image |

For example:

```bash
helm upgrade --install capi-provider-nico \
  oci://ghcr.io/dsx-ai-factory/cluster-api-provider-nico/charts/capi-provider-nico \
  --version <VERSION> \
  --namespace capnico-system \
  --create-namespace \
  --wait
```

The NKE package defaults `imagePullSecrets` to
`svc-nke-ngc-imagepull`. Other variants leave pull secrets empty.

If the GHCR packages are not anonymously readable, authenticate both clients
involved: use `helm registry login` for the workstation pulling the chart and
create a Kubernetes registry Secret for kubelet to pull the controller image.

## Configuration

| Value | Default | Purpose |
|---|---|---|
| `namespaceOverride` | `""` | Namespace the chart installs into. Empty means the release namespace |
| `createNamespace` | `true` | Create the Namespace object |
| `manager.replicas` | `1` | Controller Deployment replicas |
| `manager.image.repository` | `controller` | Controller image repository |
| `manager.image.tag` | chart `appVersion` | Controller image tag |
| `manager.image.pullPolicy` | `IfNotPresent` | Image pull policy |
| `manager.args` | `--leader-elect` | Controller arguments |
| `manager.env` | `POD_NAMESPACE` | Controller environment |
| `manager.envOverrides` | `{}` | Named environment overrides |
| `manager.resources` | requests and limits | Controller resources |
| `manager.imagePullSecrets` | `[]` | Manager pull secrets |
| `imagePullSecrets` | `[]` | Backward-compatible pull-secret alias |
| `manager.affinity` | `{}` | Pod affinity |
| `manager.nodeSelector` | `{}` | Pod node selector |
| `manager.tolerations` | `[]` | Pod tolerations |
| `manager.topologySpreadConstraints` | unset | Topology spreading |
| `manager.priorityClassName` | unset | Pod priority class |
| `manager.strategy` | unset | Deployment update strategy |
| `serviceAccount.enabled` | `true` | Create the controller ServiceAccount |
| `serviceAccount.name` | unset | Existing ServiceAccount when creation is disabled |
| `rbac.helpers.enabled` | `true` | Install CRD admin/editor/viewer helper roles |
| `crd.enabled` | `true` | Install provider CRDs |
| `crd.keep` | `true` | Retain CRDs during Helm uninstall |
| `metrics.enabled` | `true` | Install the HTTP metrics Service |
| `metrics.port` | `8080` | Metrics Service port |
| `webhook.enabled` | `true` | Required template validation webhook. |
| `webhook.port` | `9443` | Webhook server port. |
| `certManager.enabled` | `true` | Required webhook certificate resources. |
| `prometheus.enabled` | `false` | Install a ServiceMonitor |
| `networkPolicy.enabled` | `false` | Restrict metrics ingress |

`manager.imagePullSecrets` takes precedence when both pull-secret values are
set. The top-level value remains supported for upgrades from the former
manifest-wrapper chart.

CAPNICo watches infrastructure resources cluster-wide, so leave
`rbac.namespaced` set to `false`. CAPNICo currently serves metrics over HTTP;
leave the generator-provided `metrics.secure` setting at `false`.

## Namespace

By default the chart installs into the release namespace and creates the
Namespace object itself. Both defaults are unchanged, and a default install
renders byte-identically to earlier versions.

### Installing with Helm 3

The chart declares the namespace it installs into. Helm 3 cannot reconcile that
with `--create-namespace`: the namespace Helm creates carries no release
ownership metadata, and the chart's own object then fails to adopt it. Without
`--create-namespace`, Helm 3 cannot write its release record into a namespace
that does not exist yet. Helm 4 adopts the namespace and needs neither change.

Set `createNamespace=false` to install with Helm 3:

```bash
helm install capi-provider-nico <chart> \
  --namespace capnico-system --create-namespace \
  --set createNamespace=false \
  --wait
```

### Installing as a dependency of another chart

A subchart has no namespace of its own: every object it renders lands in the
parent release's namespace. `namespaceOverride` decouples the two, so an
umbrella chart released into one namespace can still place CAPNICo in another.
Every namespaced object, and every namespace reference inside a
ClusterRoleBinding, a webhook configuration or a certificate DNS name, follows
the value.

The namespace must already exist, because Helm stores the parent release
elsewhere and nothing in this chart runs before the parent's own objects:

```yaml
capi-provider-nico:
  namespaceOverride: capnico-system
  createNamespace: false
```

Setting `namespaceOverride` to a namespace other than the release namespace
while `createNamespace` is true is refused at render time: the chart would
create one namespace while Helm required another to already exist.

## Upgrade compatibility

Upgrade an existing wrapper-chart release with the same Helm release name and
namespace. The native chart deliberately preserves the historical Deployment,
ServiceAccount, Service, RBAC, Namespace, and selector identities.

```bash
helm upgrade capi-provider-nico <native-chart> \
  --namespace capnico-system \
  --wait
```

CRDs and the provider Namespace carry `helm.sh/resource-policy: keep`. Helm
uninstall removes the controller and RBAC resources but leaves CRDs and their
custom resources intact. Remove retained CRDs explicitly only when their data
is no longer needed.

The chart retains fixed cluster-scoped RBAC names for upgrade compatibility;
install only one CAPNICo release in a management cluster.
