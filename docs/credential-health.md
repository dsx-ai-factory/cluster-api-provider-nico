# Credential Health

A `NicoIdentity` reports whether a credentials Secret works before any cluster
depends on it. This page covers installing, enabling, upgrading, rolling back
and disabling that observation. The fields, check timing and status reasons are
in the [API reference](api-reference.md#nicoidentity).

## Install

Both supported installations include the NicoIdentity CRD, the controller and
its permissions, and neither needs a manager flag:

* **Helm.** The chart installs the CRD by default (`crd.enabled=true`) and the
  `nicoidentity-admin-role`, `nicoidentity-editor-role` and
  `nicoidentity-viewer-role` helpers by default (`rbac.helpers.enabled=true`).
* **clusterctl.** `clusterctl init --infrastructure nico:<version>` installs the
  same CRD and roles from the release bundle.

The only setting is optional: `--provider-identity-validation-timeout`
(default `30s`) bounds one check. Add it to the chart's `manager.args` list if
you need a different value.

If you install CRDs separately and set `crd.enabled=false`, apply the release's
CRDs, including `nicoidentities.infrastructure.cluster.x-k8s.io`, before
installing or upgrading the chart. When the NicoIdentity CRD is missing, the
manager logs that credential observation is off and keeps reconciling clusters
and machines. Installing the CRD later takes effect after a manager restart.

## Enable the Observation

Create an Identity in the namespace of the Secret it should check:

```yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: NicoIdentity
metadata:
  name: nico-default
  namespace: capnico-system
spec:
  credentialsRef:
    name: nico-credentials
```

* **Provider-level credentials** live in the manager's credentials namespace
  (`--provider-credentials-namespace`, by default the manager's own namespace)
  under the name `--provider-credentials-secret-name` (by default
  `nico-credentials`). The example above names them for a default installation
  in `capnico-system`.
* **Per-cluster credentials** are the Secrets that `NicoCluster.spec.identityRef`
  names. Create an Identity in the NicoCluster's namespace that names the same
  Secret.
* **Scope.** The Identity and its Secret must share a namespace, and the
  manager must watch that namespace: every namespace by default, or only the
  one given by `--namespace`. With `--watch-filter`, label the Identity
  `cluster.x-k8s.io/watch-filter=<value>`. An Identity outside the scope keeps
  no status.

CAPNICo only reads the Secret. Whatever delivers it today, such as Vault
Secrets Operator, keeps creating and rotating it, and a Secret change triggers
a check within seconds.

## Read the Status

```bash
kubectl get nicoidentities -A
```

`Ready` describes the last completed check of the Secret the Identity names.
Treat it as current only when its `observedGeneration` matches
`metadata.generation` and `LastChecked` is recent: checks repeat every four to
five minutes. A stopped manager leaves the last result in place, and
`kubectl wait --for=condition=Ready` does not consider its age. A failed check
changes status only. It raises no Event or alert, so nothing notifies an
operator on its own.

The `nicoidentity-viewer-role` helper lets a user read Identities and their
status without reading Secrets or writing Identities.

## Disable the Observation

Delete the Identity. Its Secret, clusters and machines are unaffected, because
nothing selects credentials through an Identity. To pause one Identity without
deleting it, take it out of the manager's scope, for example by removing its
watch-filter label. Its status then stops updating and ages.

## Upgrade

A release that adds NicoIdentity also adds a CRD, so it is a minor-version
release. To upgrade from an earlier release, upgrade the CRDs and the manager
together with `helm upgrade` or `clusterctl upgrade apply`, then create
Identities. Existing NicoClusters, NicoMachines and their Secret references keep
working unchanged, and credential selection does not change.

## Roll Back

The chart keeps its CRDs when a release is rolled back or uninstalled, so
`helm rollback` to a release without NicoIdentity leaves the CRD and every
Identity in place. The older manager ignores them: their status stops updating
and ages, and clusters keep reconciling as before. If you set
`--provider-identity-validation-timeout`, remove it from `manager.args` before
rolling back, because older managers reject unknown flags.

## Limits

* `NicoCluster.spec.identityRef` still names a Secret. Selecting a cluster's
  credentials through a NicoIdentity is not supported yet.
* `Ready=True` means authentication and NICo's current-tenant lookup succeeded.
  It does not prove every provisioning permission, capacity or the intended
  principal.
* Checks run one at a time. During a NICo outage, when each check can take the
  full timeout, a round over many Identities takes proportionally longer.
