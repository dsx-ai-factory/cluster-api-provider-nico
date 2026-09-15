# Overview

`cluster-api-provider-nico` (CAPNICo) is a
[Cluster API](https://cluster-api.sigs.k8s.io/) infrastructure provider. It
turns a `Cluster` and a `MachineDeployment` into bare-metal machines on
[NVIDIA Infra Controller (NICo)](https://github.com/NVIDIA/infra-controller).
It does not install Kubernetes or join nodes because the kubeadm bootstrap and
control-plane providers do that.

The provider exposes four custom resources. The three below carry the fields
you set day to day, and `NicoClusterTemplate` is scaffolded but not yet
consumed.

- `NicoCluster` holds the target site. It can also reference a per-cluster
  credentials Secret and name the NICo label key that carries failure domains.
- `NicoMachine` represents one NICo instance managed by Cluster API. It carries
  the VPC.
- `NicoMachineTemplate` supports `KubeadmControlPlane` and `MachineDeployment`.

These pages cover the CAPNICo documentation topics beyond the
[repository README](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/README.md).
The following table shows what you find on each one.

| Document | Contents |
|---|---|
| [Getting Started](getting-started) | Your first cluster, two ways: the in-repo fake NICo and a real NICo deployment. It also sets out what must exist on the NICo side first, the credentials Secret, and common pitfalls. |
| [Architecture](architecture) | Where this provider sits in Cluster API, the four CRDs and which fields belong to each, how credentials resolve and cache, machine reconciliation and what the finalizer guards, provider ID and node matching, teardown order, and the annotation-driven repair and reboot contracts. |
| [API Reference](api-reference) | A field-by-field reference for all four CRDs, including the one that is scaffolded but unconsumed, and every condition and reason. |
| [Troubleshooting](troubleshooting) | The symptom, cause, and fix for problems beyond the stall-reason table in Getting Started. |
| [Development](development) | The local kind, Tilt, Helm, and fake-NICo development loop. |
| [Writing Tests](writing-tests) | The controller envtest boundary, fixture inputs, Kubernetes and NICo goldens, and how to extend the HTTP fake. |

If this is your first visit, start with [Getting Started](getting-started). The
[README](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/README.md)
has the installation reference, the credentials Secret layout, and the full
worked `clusterctl` examples. Before you change controller behavior, read
[Architecture](architecture), and before you add or change controller envtests,
read [Writing Tests](writing-tests). When something is not converging, check
[Troubleshooting](troubleshooting), and for the full field list of each CRD,
check [API Reference](api-reference).

Other reference material lives next to the code it describes.

- The [contributing guide](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/CONTRIBUTING.md)
  describes the development environment and the pull request flow.
- The [release process](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/RELEASE.md)
  explains versioning and the Cluster API contract.
- The [security policy](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/SECURITY.md)
  tells you how to report a vulnerability.
- The [agent conventions](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/AGENTS.md)
  set out what coding agents must follow.
- The [Kubebuilder layout plugin](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/hack/kubebuilder/plugins/capnico-layout/v1/README.md)
  documents what the plugin does and the two commands for scaffolding a new API
  or controller.
- The [cluster examples](https://github.com/dsx-ai-factory/cluster-api-provider-nico/tree/main/examples)
  hold the worked manifests.
