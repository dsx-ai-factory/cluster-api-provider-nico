# AGENTS.md

## See also

Read before your first change here:

* [README.md](README.md) — what the provider does, how to install it, the
  credentials Secret layout, and worked `clusterctl` examples
* [CONTRIBUTING.md](CONTRIBUTING.md) — development setup, branch naming, commit
  format, and pull request expectations

Read when the change calls for it:

* [RELEASE.md](RELEASE.md) — versioning and what counts as a breaking change.
  Read this before changing `api/v1alpha1/` or `metadata.yaml`.
* [SECURITY.md](SECURITY.md) — vulnerability reporting and the out-of-scope list
* [MAINTAINERS.md](MAINTAINERS.md) — the maintainer roster
* [CHANGELOG.md](CHANGELOG.md) — historical entries and generated-notes pointer
* [config/samples/](config/samples/) — one sample manifest per CRD
* [examples/kubeadm/](examples/kubeadm/) — the `clusterctl` cluster templates
* [.github/PULL_REQUEST_TEMPLATE.md](.github/PULL_REQUEST_TEMPLATE.md) — the
  checklist your pull request has to satisfy

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

## Permitted Work

Agents may make focused changes to:

- Controller logic in `controllers/` and `internal/`
- API types in `api/v1alpha1/` (subject to the regeneration note below)
- CRD and RBAC config under `config/` (prefer editing Go types, then regenerating)
- clusterctl templates under `examples/kubeadm/`
- Tests under `test/`
- CI workflows under `.github/workflows/`
- Documentation: `README.md`, `CONTRIBUTING.md`, `CHANGELOG.md`, `RELEASE.md`, `AGENTS.md`

Prefer small, focused changes. Do not bundle unrelated fixes in a single commit.

## Out of Scope

Do not:

- Edit generated files (`zz_generated.deepcopy.go`, `config/crd/bases/*.yaml`) by hand
  — regenerate with `make generate manifests`.
- Commit credentials, kubeconfigs, bearer tokens, or client secrets.
- Bypass CI (`--no-verify`, deleting test assertions, adding `if: false` guards to required checks).
- Push directly to `main` — all changes go through pull requests.
- Modify `metadata.yaml` for patch releases; only update it for new major/minor series or contract changes.

## Secrets and Credentials

The controller reads NICo credentials from Kubernetes Secrets. When writing tests or examples:

- Use `fake.NewClientBuilder()` for unit tests that only need a Kubernetes
  client. Controller envtests must use the stateful HTTP fake in
  `internal/fake`; do not inject a Go implementation of the NICo API.
- Use only synthetic credentials in fixtures.
- Never log `endpoint`, `token`, `clientSecret`, or `ca.crt` values.
- Redact secret data in error messages with `<redacted>` or similar.
- The credentials Secret keys are: `endpoint`, `orgID`, `token` (or `tokenURL`+`clientID`+`clientSecret`).

## Verification Commands

Run before opening a PR:

```bash
make generate manifests   # ensure generated files are current
make fmt                  # gofmt
make test                 # unit tests
make build                # confirm the binary compiles
```

Optionally:

```bash
make lint                 # golangci-lint (same as CI)
```

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

## Non-obvious Tools

- **controller-gen** — generates DeepCopy methods and CRD YAML from Go type annotations. Invoked via `go run` in the Makefile; version is pinned in `go.mod`.
- **Kubebuilder custom plugin** at `hack/kubebuilder/plugins/capnico-layout/v1` — adapts Kubebuilder-generated controller files into the CAPNICo `controllers/` layout. Use its Makefile when adding new API kinds.
- **setup-envtest** — downloads Kubernetes API server binaries for integration tests. Called automatically by `make test`.

## Editing Guidance

* When changing API types in `api/v1alpha1`, regenerate deepcopies and CRDs.
* Keep examples in `examples/kubeadm/` aligned with the current API.
* Prefer updating generated YAML via the source Go types and `make generate manifests`, not by hand.
* Keep docs generic and kubeadm-focused.

## Good/Bad Patterns

**Good — wrap errors with context:**
```go
if err := r.Client.Get(ctx, key, secret); err != nil {
    return ctrl.Result{}, fmt.Errorf("fetching credentials secret %s: %w", key, err)
}
```

**Bad — swallow or lose the error:**
```go
r.Client.Get(ctx, key, secret) // ignore error
```

**Good — use `ctrl.LoggerFrom(ctx)` for structured logging:**
```go
log := ctrl.LoggerFrom(ctx).WithValues("nicoCluster", req.NamespacedName)
log.Info("reconciling")
```

**Bad — use `fmt.Println` or unstructured logging:**
```go
fmt.Println("reconciling " + req.Name)
```
