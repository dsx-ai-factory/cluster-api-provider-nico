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
* NICo connection details live in a Secret referenced by `NicoCluster.spec.identityRef`.
* The Secret must include `endpoint` and `orgID`, plus either:
  * `token`, or
  * `tokenURL`, `clientID`, and `clientSecret`
* NICo clients are cached by Secret revision. Keep auth and connection state in the client layer, not CR status.

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
* Keep examples in `examples/kubeadm/cluster.yaml` aligned with the current API.
* Prefer updating generated YAML via the source Go types and `make generate manifests`, not by hand.
* Keep docs generic and kubeadm-focused.
