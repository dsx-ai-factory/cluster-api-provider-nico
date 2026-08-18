# CAPI Provider NICo Helm Chart

This chart packages the CAPNICo `config/default` Kustomize bundle for
environments that consume providers from a Helm registry instead of directly
from Git.

The generated file at `files/infrastructure-components.yaml` is copied from the
CAPI-native provider artifact generated from the local `config/default` bundle.
It is intentionally not checked into git. Regenerate it before packaging the
chart:

```bash
make helm-chart-manifests
```

The chart intentionally keeps the provider manifest mostly unchanged. It only
supports injecting `imagePullSecrets` for environments that require registry
credentials at install time.

This Helm chart is a temporary stop-gap for DSX deployments while CAPNICo lives
in a private repository. After this repository is open sourced, `dsx-sbom` should
reference the provider's Kustomize manifests directly instead of relying on a
published Helm chart artifact.

The chart goes to two registries, and which one you want depends on which image
you can pull. The two copies are the same chart with a different image named in
the manifest.

```bash
# GHCR -- names the GHCR controller image, so no NGC login is needed
helm pull oci://ghcr.io/nvidia/cluster-api-provider-nico/charts/capi-provider-nico \
  --version <VERSION>

# NGC -- names the DSX image, for internal consumers
ngc registry chart pull <DSX_NGC_ORG>/<DSX_NGC_TEAM>/capi-provider-nico:<VERSION>
```

⚠️ **The GHCR packages are not anonymously pullable yet.** This repository is
`internal`, and a GHCR package inherits that visibility when it is created —
repository visibility does not change it. Until each package is made public in
its own settings, both the chart and the image need a GitHub token with
`read:packages`.

**Two separate credentials, because two different clients do the pulling.**
`helm registry login` authorises *your workstation* to fetch the chart. It does
nothing for the cluster: kubelet pulls the controller image itself and cannot
see your Helm config, so it needs a Kubernetes pull secret as well. Doing only
the first gets you a successful `helm install` followed by `ImagePullBackOff`.

```bash
# 1. your workstation, to pull the chart
echo "$GITHUB_TOKEN" | helm registry login ghcr.io -u <your-user> --password-stdin

# 2. the cluster, to pull the image
kubectl create secret docker-registry ghcr-imagepull \
  --namespace <namespace> \
  --docker-server=ghcr.io \
  --docker-username=<your-user> \
  --docker-password="$GITHUB_TOKEN"

helm install capi-provider-nico \
  oci://ghcr.io/nvidia/cluster-api-provider-nico/charts/capi-provider-nico \
  --version <VERSION> \
  --set imagePullSecrets[0].name=ghcr-imagepull
```
