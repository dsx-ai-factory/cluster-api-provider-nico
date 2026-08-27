# docs

Documentation for `cluster-api-provider-nico` beyond the
[README](../README.md).

| Document | What it covers |
|---|---|
| [architecture.md](architecture.md) | Where this provider sits in Cluster API, the four CRDs and which fields live on which, how credentials resolve and cache, machine reconciliation and what the finalizer actually guards, provider ID and node matching, teardown order, and the annotation-driven repair and reboot contracts |
| [writing-tests.md](writing-tests.md) | The controller envtest boundary, fixture inputs, Kubernetes and NICo goldens, and how to extend the HTTP fake |

Start with the [README](../README.md) for installation, the credentials Secret
layout, and the worked `clusterctl` examples. Read
[architecture.md](architecture.md) before changing controller behaviour. Read
[writing-tests.md](writing-tests.md) before adding or changing controller
envtests.

Other reference material lives next to the code it describes:

- [../CONTRIBUTING.md](../CONTRIBUTING.md) — development environment and the
  pull-request flow
- [../RELEASE.md](../RELEASE.md) — versioning and the Cluster API contract
- [../SECURITY.md](../SECURITY.md) — vulnerability reporting
- [../AGENTS.md](../AGENTS.md) — conventions for coding agents
- [../hack/kubebuilder/plugins/capnico-layout/v1/README.md](../hack/kubebuilder/plugins/capnico-layout/v1/README.md)
  — the Kubebuilder layout plugin: what it does, and the two commands for
  scaffolding a new API or controller
- [../examples/](../examples/) — worked cluster manifests
