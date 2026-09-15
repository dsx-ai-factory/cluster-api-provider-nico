# docs

Documentation for `cluster-api-provider-nico` beyond the
[README](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/README.md).

| Document | What it covers |
|---|---|
| [getting-started.md](getting-started) | First cluster, two ways: the in-repo fake NICo, and a real NICo deployment. What must exist on the NICo side first, the credentials Secret, and the traps |
| [architecture.md](architecture) | Where this provider sits in Cluster API, the four CRDs and which fields live on which, how credentials resolve and cache, machine reconciliation and what the finalizer actually guards, provider ID and node matching, teardown order, and the annotation-driven repair and reboot contracts |
| [development.md](development) | The local Kind, Tilt, Helm, and fake-NICo development loop |
| [writing-tests.md](writing-tests) | The controller envtest boundary, fixture inputs, Kubernetes and NICo goldens, and how to extend the HTTP fake |

New here? Start with [getting-started.md](getting-started). The
[README](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/README.md)
has the installation reference, the credentials Secret layout, and the full
worked `clusterctl` examples. Read [architecture.md](architecture) before
changing controller behaviour. Read [writing-tests.md](writing-tests) before
adding or changing controller envtests.

Other reference material lives next to the code it describes:

- [CONTRIBUTING.md](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/CONTRIBUTING.md)
  — development environment and the pull-request flow
- [RELEASE.md](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/RELEASE.md)
  — versioning and the Cluster API contract
- [SECURITY.md](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/SECURITY.md)
  — vulnerability reporting
- [AGENTS.md](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/AGENTS.md)
  — conventions for coding agents
- [hack/kubebuilder/plugins/capnico-layout/v1/README.md](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/hack/kubebuilder/plugins/capnico-layout/v1/README.md)
  — the Kubebuilder layout plugin: what it does, and the two commands for
  scaffolding a new API or controller
- [examples/](https://github.com/dsx-ai-factory/cluster-api-provider-nico/tree/main/examples)
  — worked cluster manifests
