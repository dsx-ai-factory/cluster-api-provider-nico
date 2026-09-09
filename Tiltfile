# SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

load("ext://helm_resource", "helm_resource")

# Local development loop: CAPNICo plus the fake NICo API, so the provisioning
# flow runs with no hardware and no access to a real deployment.
#
#   make tilt-up      create the kind cluster, install Cluster API, start Tilt
#   make tilt-down    stop Tilt and delete the cluster
#
# Cluster API itself is installed out of band by `clusterctl init` (see the
# capi-init target) from released images rather than built from source.

update_settings(k8s_upsert_timeout_secs=60 * 5)

CAPNICO_CLUSTER_NAME = "capnico"
CAPNICO_NAMESPACE = "capnico-system"

# Must match the chart's manager image so Tilt substitutes its build.
CAPNICO_MANAGER_IMAGE = "controller"

CAPNICO_FAKE_IMAGE = "fake-nico-api-server"
CAPNICO_FAKE_NAMESPACE = "fake-nico-api"

# Check the context before any shell command or apply can run.
capnico_context = k8s_context()
if not capnico_context:
    fail("No current kubecontext in KUBECONFIG=%s. The cluster is probably already gone — `make tilt-up` creates and selects kind-%s." % (
        os.getenv("KUBECONFIG", "~/.kube/config"), CAPNICO_CLUSTER_NAME))
if capnico_context != "kind-" + CAPNICO_CLUSTER_NAME:
    fail("Refusing to run against %s. Run `make tilt-up`, which creates and selects kind-%s." % (capnico_context, CAPNICO_CLUSTER_NAME))
allow_k8s_contexts(capnico_context)

# Cluster API must already be installed. `make tilt-up` does that before
# starting Tilt; fail clearly rather than deploying a provider with no core
# controllers to drive it.
capnico_capi_installed = str(local(
    "kubectl get deployment -n capi-system capi-controller-manager --ignore-not-found -o name || true",
    quiet=True, echo_off=True)).strip()
if capnico_capi_installed == "":
    fail("Cluster API is not installed in %s. Run `make tilt-up`, which runs clusterctl init first." % capnico_context)

capnico_arch = str(local("go env GOARCH", quiet=True, echo_off=True)).strip()

##
## Provider
##

# Compile on the host so edits reuse the local Go build cache; the development
# image below is then a single COPY layer.
local_resource(
    "capnico-manager-build",
    cmd="CGO_ENABLED=0 GOOS=linux GOARCH=%s go build -o .tiltbuild/bin/manager ./cmd" % capnico_arch,
    deps=["go.mod", "go.sum", "api", "cmd", "controllers", "internal"],
    labels=["capnico"],
)

docker_build(
    CAPNICO_MANAGER_IMAGE, ".",
    dockerfile="hack/tilt/Dockerfile.manager",
    only=[".tiltbuild/bin/manager"],
)

# The release name matches chart/Chart.yaml so the chart's fullname helper
# keeps the generated resource names stable.
helm_resource(
    "capi-provider-nico",
    chart="./chart",
    namespace=CAPNICO_NAMESPACE,
    image_deps=[CAPNICO_MANAGER_IMAGE],
    image_keys=[("manager.image.repository", "manager.image.tag")],
    flags=[
        "--create-namespace",
        "-f", "hack/tilt/values.yaml",
    ],
    resource_deps=["capnico-manager-build"],
    labels=["capnico"],
)

##
## Fake NICo API
##

docker_build(CAPNICO_FAKE_IMAGE, ".", dockerfile="Dockerfile.fake",
             only=["go.mod", "go.sum", "api", "cmd", "internal",
                   "LICENSE", "NOTICE", "THIRD_PARTY_NOTICES.md"])

k8s_yaml("hack/tilt/fake-nico-api.yaml")

k8s_resource(
    workload="fake-nico-api",
    objects=[
        CAPNICO_FAKE_NAMESPACE + ":namespace",
        "nico-credentials:secret",
        "fake-nico-seed:configmap",
    ],
    labels=["fake-nico-api"],
    port_forwards=["8090:8090"],
)
