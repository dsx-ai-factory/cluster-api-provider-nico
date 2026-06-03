# AGENTS.md

## Overview

`cluster-api-provider-nico` is a Cluster API infrastructure provider for NICo.
It currently exposes:

* `NicoCluster`
* `NicoMachine`
* `NicoClusterTemplate`
* `NicoMachineTemplate`

The provider uses the published NICo SDK under the alias `nicosdk`.

## Important Conventions

* Provider API version is `infrastructure.cluster.x-k8s.io/v1alpha1`.
* The provider implements the Cluster API `v1beta2` contract.
* NICo connection details live in a namespaced Secret. Each reconcile resolves
  credentials in this order:
  1. The Secret named by `NicoCluster.spec.identityRef.name` in the
     `NicoCluster`'s own namespace, when set.
  2. The provider-level Secret in the manager's namespace, configured via the
     `--provider-credentials-namespace` and `--provider-credentials-secret-name`
     manager flags (defaults: `$POD_NAMESPACE` or `capnico-system`, and
     `nico-credentials`).
* `NicoCluster.spec.identityRef` is optional; omit it to use the provider-level
  Secret.
* The Secret must include `endpoint` and `orgID`, plus either:
  * `token`, or
  * `tokenURL`, `clientID`, and `clientSecret`
* NICo clients are cached by Secret revision. External rotations of the
  credentials Secret (for example by ESO) are picked up on the next reconcile
  via the cache's `resourceVersion` key. Keep auth and connection state in the
  client layer, not CR status.
* `NicoMachine` owns NICo instance cleanup. Do not remove its finalizer while
  `status.instanceID` may still refer to an existing NICo instance.
* `NicoCluster` uses `ClusterFinalizer` to block deletion until all
  `NicoMachine` objects in the same namespace with the matching
  `cluster.x-k8s.io/cluster-name` label are gone. Do not add identity Secret
  finalizers unless explicitly implementing a stronger lifecycle option.

## Common Commands

```bash
make generate
make manifests
make fmt
make test
make build
make run
```

`controller-gen` is intentionally invoked via `go run` in the `Makefile`; do not run via a globally installed `controller-gen` binary.

## Editing Guidance

* When changing API types in `api/v1alpha1`, regenerate deepcopies and CRDs.
* Keep examples in `examples/kubeadm/` aligned with the current API.
* Prefer updating generated YAML via the source Go types and `make generate manifests`, not by hand.
* Keep docs generic and kubeadm-focused.
