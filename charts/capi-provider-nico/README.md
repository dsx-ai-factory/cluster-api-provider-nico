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
reference the provider's Kustomize manifests directly instead of relying on an
NVCR-published Helm chart artifact.
