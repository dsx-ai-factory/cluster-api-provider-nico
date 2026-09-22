# Getting Started

CAPNICo is a Cluster API infrastructure provider. It turns each Cluster API
`Machine` into one NICo instance on bare metal. It does not install Kubernetes
and it does not join nodes. The kubeadm bootstrap and control-plane providers do
that, and CAPNICo carries their cloud-init to the instance as user data.

This page covers three paths. Start with Path A even if you have hardware,
because it is the fastest way to learn what a healthy reconcile looks like, and
you want that baseline when Path C misbehaves.

| Path | Needs | Gets you |
|---|---|---|
| [Path A, the in-repo fake](#path-a-the-built-in-fake) | Docker and kind. | The full reconcile loop in about 5 minutes, with no NICo at all. |
| [Path B, a local NICo](#path-b-against-a-real-nico) | Docker, kind, and the infra-controller repository. | The real NICo REST API, against mock hosts. |
| [Path C, production](#path-c-production-against-an-existing-nico-site) | An existing NICo site with real hardware. | Machines that actually boot. |

## Before You Start

Clone this repository, because every path below runs `make` and reads files from
the checkout.

```bash
git clone https://github.com/dsx-ai-factory/cluster-api-provider-nico
cd cluster-api-provider-nico
```

Install the tools in the following table. `make tilt-up` calls `ctlptl`, `tilt`,
and `helm` directly and does not install them for you.

| Tool | Used for |
|---|---|
| [Docker](https://docs.docker.com/engine/install/) | Builds and the kind node. |
| [ctlptl](https://github.com/tilt-dev/ctlptl) | Creates the kind cluster and its local registry. |
| [tilt](https://docs.tilt.dev/install.html) | Runs the development loop. |
| [helm](https://helm.sh/docs/intro/install/) | Installs the provider chart. |
| `kubectl` | Applies manifests and inspects cluster resources. |
| [Go](https://go.dev/dl/) | Builds the manager and the codegen tools. |
| [clusterctl](https://cluster-api.sigs.k8s.io/user/quick-start) | Paths B and C only. |
| [devspace](https://www.devspace.sh) | Path B only, where it deploys the local NICo stack. |

Ports `10352` for the Tilt UI and `5006` for the local registry must be free.

## Path A: The Built-In Fake

The repository ships a fake NICo endpoint, so you need no hardware and no NICo
access.

In the first terminal, start the loop and leave it running. `Ctrl-C` stops the
loop, and `make tilt-down` deletes the cluster.

```bash
make tilt-up     # kind + CAPI core + kubeadm providers + CAPNICo + fake NICo
```

The Tilt UI is on `localhost:10352`, deliberately not Tilt's default 10350, so
this can run beside another Cluster API environment. `make run-fake` serves the
fake alone on `:8090`.

In the second terminal, wait for the provider to be up before you apply
anything. On a cold start, Tilt is still building images, and the CRDs and the
`capnico-system` namespace do not exist yet.

```bash
export KUBECONFIG=~/.kube/capnico.kubeconfig

kubectl wait --for=condition=Established --timeout=5m \
  crd/nicomachines.infrastructure.cluster.x-k8s.io
kubectl -n capnico-system rollout status deploy -l control-plane=controller-manager --timeout=5m

kubectl apply -f examples/cluster-fake.yaml
kubectl get nicoclusters,nicomachines -A
```

`examples/cluster-fake.yaml` creates CAPI `Machine` objects directly with
ready-made bootstrap Secrets, because CAPNICo provisions infrastructure and
nothing else.

Watch a machine converge:

```bash
kubectl describe nicomachine demo-cp-0
```

Both machines should end with a provider ID and `Provisioned`. The worker
briefly shows `ControlPlanePriorityDeferred`, and that is correct, because
workers wait behind control-plane machines competing for the same instance type.

`status.conditions` is where the answer is. Every stall has a named reason, such
as `WaitingForBootstrapData`, `InstanceTypeUnavailable`,
`WaitingForIdentitySecret`, `ControlPlanePriorityDeferred`, and
`InstanceNotReady`.

## Path B: Against a Real NICo

### What Must Already Exist

CAPNICo creates instances, and it creates nothing else. Before it can work, the
NICo side needs four things.

- A site, which goes on `NicoCluster.spec.siteID`.
- A VPC, which goes on `NicoMachine.spec.vpcID`, not on the cluster.
- A subnet, one per interface attachment. Sites with Native Networking enabled
  use `vpcPrefixID` instead of `subnetID`.
- An instance type for control-plane and worker machines.

For a local NICo, `hack/local-nico-seed.sh` creates all of these, as described
in [Seed Test Resources](#seed-test-resources) below.

### Standing Up a Local NICo

[NVIDIA/infra-controller](https://github.com/NVIDIA/infra-controller) runs
locally with mock hosts, which is enough to exercise CAPNICo against the real
REST API.

<Warning>
`bootstrap-prereqs.sh` operates on the current Kubernetes context. Set
`KUBECONFIG` to a dedicated local kind cluster before you run the script. Do not
use the Path A cluster or a shared or remote cluster.
</Warning>

From that repository, run the following two commands:

```bash
dev/deployment/devspace/bootstrap-prereqs.sh   # cert-manager, PostgreSQL, Vault, Temporal, Keycloak
devspace deploy                                # Core + REST + machine-a-tron (the mock hosts)
```

Both build a full Rust and Go workspace plus several images, so plan for two
constraints.

- Docker needs 8 CPUs, 12 GB of RAM, and 100 GB of free disk, at minimum.
- The final verification step of `devspace deploy` runs 15 to 20 minutes with no
  output. That is expected, and it is not a hang.

For a real site instead, refer to that repository's `helm-prereqs/setup.sh`.

Keep the REST API and Keycloak reachable:

```bash
kubectl -n nico-rest port-forward service/nico-rest-api 18388:8388
kubectl -n nico-rest port-forward service/keycloak     18082:8082
```

### Seed Test Resources

A fresh local site has mock hosts but no VPC, instance type, or allocation yet.
[Create a Cluster](#create-a-cluster) below needs all three, and nothing up to
this point creates them. The seed script is safe to re-run, because it reuses
what already exists rather than duplicating it. It also extends the realm's
access-token lifespan from the 5-minute default.

```bash
hack/local-nico-seed.sh > /tmp/nico-env.sh
source /tmp/nico-env.sh
```

Mint a token and confirm the API answers:

```bash
TOKEN=$(curl -fsS -X POST http://localhost:18082/realms/nico-dev/protocol/openid-connect/token \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -d 'client_id=nico-api' -d 'client_secret=nico-local-secret' \
  -d 'grant_type=password' \
  -d 'username=admin@example.com' -d 'password=adminpassword' | jq -r .access_token)

curl -fsS http://localhost:18388/v2/org/test-org/nico/tenant/current \
  -H "Authorization: Bearer ${TOKEN}" | jq
```

### The Credentials Secret

Two clusters are in play from here. NICo runs in the one `devspace deploy` just
built. CAPNICo runs in your management cluster, and the Path A kind cluster is
fine for that. The commands below target the management cluster, so point
`KUBECONFIG` at it.

The Secret requires `endpoint` and `orgID`, then exactly one authentication
mode. Never supply both, because a token and OAuth keys together are rejected.

```bash
kubectl create namespace capnico-system --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret generic nico-credentials -n capnico-system \
  --from-literal=endpoint=http://nico-rest-api.nico-rest.svc.cluster.local:8388 \
  --from-literal=orgID=test-org \
  --from-literal=apiName=nico \
  --from-literal=token="${TOKEN}"
```

`endpoint` must be reachable from a pod in the management cluster, not from your
laptop. The in-cluster DNS name above only works when CAPNICo and NICo share a
cluster. If they do not, expose the NICo REST Service to the management cluster
and use that address. You can use a NodePort on the NICo cluster, or put both
kind clusters on the same Docker network. Check it from a pod, not a shell:

```bash
kubectl -n capnico-system run netcheck --rm -it --restart=Never \
  --image=curlimages/curl -- curl -sv <endpoint>/healthz
```

Clients are cached per Secret `resourceVersion`, so an external rotation is
picked up on the next reconcile with no manager restart.

### Install the Provider

Install Cluster API and then the provider chart:

```bash
clusterctl init --bootstrap kubeadm --control-plane kubeadm

helm upgrade --install capi-provider-nico ./chart \
  --namespace capnico-system --create-namespace \
  --set manager.image.repository=ghcr.io/dsx-ai-factory/cluster-api-provider-nico/controller \
  --set manager.image.tag=v0.0.43 --wait
```

You can also install from a published release with `clusterctl` by adding the
release asset URL to `~/.config/cluster-api/clusterctl.yaml`, then running
`clusterctl init --infrastructure nico:<version>`.

### Create a Cluster

Three templates live under `examples/kubeadm/`. List a template's variables with
`clusterctl generate cluster demo --from <template> --list-variables`.

| Template | Use when |
|---|---|
| `cluster.yaml` | A stable API endpoint already exists, such as DNS, an external load balancer, or your own kube-vip. |
| `cluster-kube-vip.yaml` | You want the template to bootstrap kube-vip as a static pod. |
| `cluster-single-control-plane-static-ip.yaml` | You are doing developer bring-up only, with a single control plane, a requested IP, and no high availability. |

For a local NICo, running `source /tmp/nico-env.sh` from
[Seed Test Resources](#seed-test-resources) above sets `NICO_SITE_ID`,
`NICO_VPC_ID`, the instance type IDs, `NICO_NETWORK_METHOD`, and
`NICO_NETWORK_ID`. Only the iPXE scripts and the control-plane endpoint stay as
placeholders, since nothing local boots them.

```bash
kubectl create namespace demo

source /tmp/nico-env.sh
NICO_CONTROL_PLANE_IPXE_SCRIPT='chain https://boot.example.com/ipxe/control-plane.ipxe' \
NICO_WORKER_IPXE_SCRIPT='chain https://boot.example.com/ipxe/worker.ipxe' \
CONTROL_PLANE_ENDPOINT_HOST=10.0.0.100 \
clusterctl generate cluster demo --from examples/kubeadm/cluster.yaml \
  --target-namespace demo --kubernetes-version v1.36.0 \
  --control-plane-machine-count 1 --worker-machine-count 1 | kubectl apply -f -
```

If reconciliation reports `401 Unauthorized` or `TenantResolutionFailed` partway
through, the static token from
[The Credentials Secret](#the-credentials-secret) has expired. Re-mint it and
update the Secret, the same way as before.

Against a local NICo, this is as far as Path B goes. When
`kubectl -n demo get nicomachine` reaches `PROVISIONED=true`, the integration
between CAPNICo and NICo works end to end. It does not reach a booted, `Ready`
node, because the iPXE URLs above are placeholders and `machine-a-tron` only
mocks the hardware, so nothing ever actually boots `kubeadm`. For a real boot
chain, refer to
[What Has to Be in the OS Image](#what-has-to-be-in-the-os-image) below and to
Path C.

Nodes get their provider ID from the NICo metadata service. The templates read
`169.254.169.254:7777/latest/meta-data/instance-id` and patch kubelet with
`providerID: nico://<instance-id>`. That is how Cluster API matches a `Node`
back to its `Machine`.

## Path C: Production Against an Existing NICo Site

This path assumes the site is already installed, its machines are enrolled, and
someone can issue you a token. You are adding CAPNICo to it.

### 1. Collect Seven Values

Everything CAPNICo needs is an ID you look up. Install
[`nicocli`](https://github.com/NVIDIA/infra-controller/tree/main/rest-api/cli)
from the infra-controller repository by running `make nico-cli` and then
`nicocli init`, then read the values off the following table.

| Value | Where it goes | Find it with |
|---|---|---|
| API base URL | Secret `endpoint` | Your site operator |
| Org | Secret `orgID` | Your site operator |
| API name | Secret `apiName` | The path segment in a working URL |
| Site ID | `NicoCluster.spec.siteID` | `nicocli site list` |
| VPC ID | `NicoMachine.spec.vpcID` | `nicocli vpc list` |
| VPC prefix ID | `NICO_NETWORK_ID` | `nicocli vpc-prefix list` |
| Instance type IDs | `NICO_*_INSTANCE_TYPE_ID` | `nicocli instance-type list` |

You can optionally collect SSH key groups with `nicocli sshkeygroup list` for
Serial-over-LAN.

You also need an iPXE script URL per role that boots an OS image able to consume
kubeadm cloud-init. CAPNICo does not supply one.

### 2. Check the Instance Types Have Allocations

This is the failure that wastes the most time. CAPNICo does not create an
instance unless NICo reports free capacity for its instance type. Before every
create, it reads `allocationStats.unusedUsable` and treats zero as unavailable:

```
Instance type "…" has no unused usable allocations (total=0 used=0 unused=0 unusedUsable=0)
```

That figure comes from NICo allocations, which are a separate resource from the
instance type. An instance type with no allocation looks perfectly healthy in
`nicocli instance-type get` and provisions nothing. Machines sit in
`InstanceTypeUnavailable` and retry every 2 minutes, forever.

```bash
nicocli allocation list
nicocli instance-type get <instance-type-id>   # confirm allocationStats.unusedUsable > 0
```

If the response omits `allocationStats.unusedUsable` entirely, CAPNICo fails the
reconcile with an error naming that field rather than guessing.

The full dependency order is the site, then the site IP blocks, then the
instance types, then the allocations, then the VPCs, and finally the VPC
prefixes. NICo creates site IP blocks itself from fabric prefixes the site
reports, and the rest are yours. `nicocli site bootstrap --file <manifest>`
walks that chain in order and is the sanctioned way to create those resources,
as `rest-api/cli/examples/site-prerequisites.yaml` shows. The command never
creates the site itself.

### 3. Get a Machine-Usable Credential

CAPNICo supports a static `token` or OAuth client credentials. A token minted by
an interactive password grant works until it expires. For long-lived production
access, use OAuth client credentials.

```bash
kubectl create namespace capnico-system --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret generic nico-credentials -n capnico-system \
  --from-literal=endpoint=https://nico.example.com \
  --from-literal=orgID=your-org \
  --from-literal=apiName=nico \
  --from-literal=tokenURL=https://issuer.example.com/token \
  --from-literal=clientID=<client-id> \
  --from-literal=clientSecret=<client-secret> \
  --from-literal=scope=<scope> \
  --from-file=ca.crt=/path/to/site-ca.pem
```

Never set `token` alongside the OAuth keys, because supplying both is rejected.

If your site's API uses a private CA, pass `ca.crt`. Prefer that over
`insecureSkipTLSVerify`.

The management cluster must reach both service endpoints and pull the controller
image.

- Reach `endpoint`. Resolve and route it from a pod, not from your laptop.
- Pull the controller image from a registry it has credentials for. Build and
  push your own, as step 4 describes.
- Reach the OAuth `tokenURL`, which is often a different host from the API.

Separate provider and tenant organizations are supported, but one token must
carry the required role in both.

### 4. Build the Image, Push It, and Install

Build the controller from this checkout and push it to a registry your
management cluster can pull from. `IMG` is one variable, used by both targets:

```bash
export IMG=<your-registry>/cluster-api-provider-nico/controller:v0.0.43

make docker-build IMG="${IMG}"
make docker-push  IMG="${IMG}"
```

For a cluster whose nodes are not all the same architecture, build a multi-arch
manifest instead. This target builds and pushes in a single step:

```bash
make docker-buildx IMG="${IMG}" PLATFORMS=linux/amd64,linux/arm64
```

Then install Cluster API and the provider, pointing the chart at that image.
Split `IMG` at the colon, where the part before it is `repository` and the part
after it is `tag`.

```bash
clusterctl init --bootstrap kubeadm --control-plane kubeadm

helm upgrade --install capi-provider-nico ./chart \
  --namespace capnico-system --create-namespace \
  --set manager.image.repository="${IMG%:*}" \
  --set manager.image.tag="${IMG##*:}" \
  --wait
```

Tag with a released version rather than `latest`, so a site can say which build
it is running.

If the registry needs authentication, create the pull Secret in the same
namespace and name it on the release:

```bash
kubectl create secret docker-registry regcred -n capnico-system \
  --docker-server=<your-registry> \
  --docker-username=<user> --docker-password=<token>

helm upgrade --install capi-provider-nico ./chart \
  --namespace capnico-system \
  --set manager.image.repository="${IMG%:*}" \
  --set manager.image.tag="${IMG##*:}" \
  --set-json 'imagePullSecrets=[{"name":"regcred"}]' \
  --wait
```

Released deployments log structured JSON, and `--zap-devel` is a local-only
value.

Credentials can be provider-level, meaning one Secret in `capnico-system` used
by every cluster, or per-cluster through `NicoCluster.spec.identityRef` in the
`NicoCluster`'s namespace. Use per-cluster credentials when one management
cluster drives several sites or tenants.

### 5. Prove the Credentials Before Creating Machines

Readiness on a `NicoCluster` is a validation check that asks whether this
identity can reach NICo and resolve a tenant, and it provisions nothing. A
throwaway `NicoCluster` is therefore a free credential test that cannot cost you
hardware.

A `NicoCluster` only starts reconciling after a CAPI `Cluster` sets an owner
reference on it. A bare `NicoCluster` with no owning `Cluster` sits forever
logging `Waiting for Cluster controller to set OwnerRef on NicoCluster` and
never even attempts the credential check. Apply both objects:

```bash
kubectl create namespace demo

kubectl apply -f - <<EOF
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: NicoCluster
metadata:
  name: credcheck
  namespace: demo
spec:
  siteID: "<site-id>"
---
apiVersion: cluster.x-k8s.io/v1beta2
kind: Cluster
metadata:
  name: credcheck
  namespace: demo
spec:
  infrastructureRef:
    apiGroup: infrastructure.cluster.x-k8s.io
    kind: NicoCluster
    name: credcheck
EOF

kubectl -n demo describe nicocluster credcheck | sed -n '/Conditions/,$p'
```

Ready means the identity resolved a tenant, so auth, TLS, routing, and org are
all correct. Not ready names the failure: `WaitingForIdentitySecret` for a
missing or misnamed Secret, `IdentityConfigurationFailed` for one the provider
cannot parse, and `TenantResolutionFailed` for everything else, which step 7
covers.

Fix this before you create a single Machine, then clean up. Delete the `Cluster`
first, as described in [Common Pitfalls](#common-pitfalls) below:

```bash
kubectl -n demo delete cluster credcheck
```

### 6. First Cluster With One Control Plane and One Worker

This is the smallest thing that proves the path end to end. Use `cluster.yaml`
with an endpoint you already control, so the first run does not also debug
kube-vip.

```bash
NICO_SITE_ID=<site-id> \
NICO_VPC_ID=<vpc-id> \
NICO_CONTROL_PLANE_INSTANCE_TYPE_ID=<cp-type-id> \
NICO_WORKER_INSTANCE_TYPE_ID=<worker-type-id> \
NICO_NETWORK_METHOD=vpcPrefixID \
NICO_NETWORK_ID=<vpc-prefix-id> \
NICO_CONTROL_PLANE_IPXE_SCRIPT='chain https://boot.example.com/ipxe/control-plane.ipxe' \
NICO_WORKER_IPXE_SCRIPT='chain https://boot.example.com/ipxe/worker.ipxe' \
CONTROL_PLANE_ENDPOINT_HOST=<stable-api-endpoint> \
clusterctl generate cluster demo --from examples/kubeadm/cluster.yaml \
  --target-namespace demo --kubernetes-version v1.36.0 \
  --control-plane-machine-count 1 --worker-machine-count 1 | kubectl apply -f -
```

Watch it in the order things actually happen:

```bash
kubectl -n demo get nicocluster,nicomachine          # NicoCluster ready, then instance IDs appear
nicocli instance list --status provisioned           # the same IDs, from NICo's side
clusterctl describe cluster demo -n demo             # CAPI's view
kubectl --kubeconfig <(clusterctl get kubeconfig demo -n demo) get nodes
```

A node appearing means the whole chain worked. CAPNICo created the instance, it
booted the iPXE image, cloud-init ran kubeadm, and kubelet picked up
`providerID: nico://<instance-id>` from the metadata service.

⚠️ **The `NICO_*_IPXE_SCRIPT` values above are placeholders** —
`boot.example.com` does not exist. Replace them with your own site's real,
working iPXE chain (boot-loader URL + image URL) before expecting a node to
actually appear. CAPNICo will still create the instance and report
`Provisioned` against a placeholder script — that step only proves CAPNICo's
own job is done, not that anything booted. See "What has to be in the OS
image" below for what that chain needs to deliver.

#### Validating without the kubeadm providers

The templates above need the kubeadm bootstrap and control-plane providers
installed on the management cluster, which step 4 does with
`clusterctl init --bootstrap kubeadm --control-plane kubeadm`. Not every real
site has them, because some run a different orchestrator on top of CAPNICo
instead of vanilla kubeadm CAPI.

If yours does not, you can still validate CAPNICo on its own by creating a
`Machine` and `NicoMachine` directly instead of going through
`KubeadmControlPlane`, following the pattern in `examples/cluster-fake.yaml`.
That proves CAPNICo's own create-then-status behavior against the real REST API
and real hardware without depending on kubeadm at all.

### 7. When It Stalls

`status.conditions` on the `NicoMachine` names the reason. Read it first,
because every stall in the following table is a distinct, named reason, not a
generic timeout.

| Reason | Means |
|---|---|
| `WaitingForIdentitySecret` | The Secret is missing or misnamed. |
| `TenantResolutionFailed` | The tenant lookup failed. The cause is a wrong org or `apiName`, an expired or rejected token, an unroutable endpoint, or a CA mismatch. All four land here. |
| `InstanceTypeNotFound` | The instance type ID is wrong, or the org is wrong. |
| `InstanceTypeUnavailable` | There is no free allocation, which step 2 covers. |
| `ControlPlanePriorityDeferred` | A control-plane machine is waiting on this type, so workers wait. |
| `WaitingForBootstrapData` | The kubeadm provider has not written the Secret yet. |
| `InstanceCreateFailed` | NICo rejected the create, and the message carries its error. |
| `InstanceNotReady` | The instance exists and is booting, which is normal for several minutes. |

Nothing here polls faster than it needs to. The intervals are 15 seconds on the
fast path, 30 seconds while an instance boots or tears down, 2 minutes when
capacity is exhausted, and about 5 minutes after the object is ready, jittered
up to a minute per object so a large cluster does not requeue in lockstep.

If an instance is created but no node ever appears, CAPNICo has done its job and
the problem is below it, in the iPXE image, cloud-init, or network reachability
to the control-plane endpoint.

## What Has to Be in the OS Image

CAPNICo hands NICo an iPXE script URL and a blob of cloud-init user data, and
then stops. Everything after the machine powers on is the image's job, and
nothing in this repository builds one. A wrong image gives you a created
instance and a node that never registers, and CAPNICo reports `Ready` and is
right to do so.

### The Contract

The following table lists six requirements. All of them come from the two things
CAPNICo actually sets on the create request, `ipxeScript` and `userData`, where
`controllers/nicomachine_controller.go:902` sets the latter.

| Number | Requirement | Why |
|---|---|---|
| 1 | It boots from your iPXE script. | `NicoMachine.spec.ipxeScript` is chained verbatim, and CAPNICo does not host it. |
| 2 | It runs cloud-init, reading NICo's datasource. | The kubeadm bootstrap provider writes a `#cloud-config`, and CAPNICo passes it through untouched as instance user data. |
| 3 | It has `kubeadm`, `kubelet`, and a container runtime. | The cloud-config runs `kubeadm init` or `kubeadm join`. It does not install them. |
| 4 | It has `curl`. | `preKubeadmCommands` shells out to it before kubeadm runs. |
| 5 | It can route to `169.254.169.254:7777`. | This is the metadata service, served by the DPU agent, and the provider ID comes from here. |
| 6 | It has a CNI plan. | The templates install none, so nodes stay `NotReady` until something does. |

The `preKubeadmCommands` block at `examples/kubeadm/cluster.yaml`, lines 98 to
104, covers requirements 4 and 5:

```bash
iid=$(curl -fsS --max-time 2 169.254.169.254:7777/latest/meta-data/instance-id || true)
```

That is guarded with `|| true`, so a missing metadata service degrades quietly.
The node joins with no provider ID, and Cluster API never matches it to its
`Machine`. That is the usual cause of a cluster that looks half-built while its
nodes look healthy.

`cluster-kube-vip.yaml` is stricter. Its first `preKubeadmCommands` entry runs
`/usr/local/bin/configure-kube-vip.sh`, which sets `set -euo pipefail` and reads
the peer ASN without `|| true`, at `cluster-kube-vip.yaml:105,118`:

```bash
bgp_peer_as=$(curl -fsS --max-time 2 169.254.169.254:7777/latest/meta-data/asn)
```

A metadata error there aborts the whole step instead of degrading quietly. That
template also needs `ip` from iproute2, a writable `/etc/kubernetes/manifests`,
and the ability to pull `ghcr.io/kube-vip/kube-vip`.

### Which Datasource

NICo serves cloud-init two ways, and a production image is normally configured
for the first with the second as fallback.

- NoCloud, at `http://carbide-pxe.forge/api/v0/cloud-init/`.
- EC2, at `http://169.254.169.254:7777`.

`nico-pxe` in the infra-controller repository is the service that serves both the
iPXE script and the cloud-init data, and `pxe/ipxe/` and `pxe/templates/` are the
files it renders.

### Things You Do Not Bake

Instance identity arrives at first boot, never at build time. That includes the
hostname, join tokens, SPIRE or other trust material, and anything
site-specific. CAPNICo sets `hostname`, `preserve_hostname: false`, and
`manage_etc_hosts` into the cloud-config itself when
`spec.cloudInitInjectHostname` is true, at `internal/nico/userdata.go:13-30`, so
the image must not override them.

A build that bakes identity produces machines that are all the same machine.
Whatever recipe you use, end it with `cloud-init clean` so the next boot is a
real first boot.

### Recipes You Can Copy

Nothing in the following table is a dependency. These are worked examples of the
same contract.

| Source | What it gives you |
|---|---|
| [Image Builder](https://github.com/kubernetes-sigs/image-builder) | The upstream Cluster API way to bake kubeadm, kubelet, and a container runtime into an image. It covers requirement 3, and its output is the normal starting point. |
| [cloud-init datasource docs](https://cloudinit.readthedocs.io/en/latest/reference/datasources.html) | How to pin NoCloud and EC2 so the image reads NICo's datasource, and only NICo's. |
| `pxe/` in the infra-controller repository | `nico-pxe` itself, covering how the iPXE script and the cloud-init payload are served, and the templates it renders. |

The usual shape is Image Builder for the Kubernetes layer, then a small
provisioning step of your own for the datasource configuration and anything
site-specific.

### Installing kubeadm and a CNI at boot instead

If your image doesn't have requirement 3 baked in yet, `preKubeadmCommands`
can install it at boot instead of through Image Builder. Worked example,
validated against a real site — plain kubeadm/kubelet/containerd from
`pkgs.k8s.io`, no site-specific packaging:

```yaml
preKubeadmCommands:
  - |
    set -eux
    cat <<MOD | tee /etc/modules-load.d/k8s.conf
    overlay
    br_netfilter
    MOD
    modprobe overlay
    modprobe br_netfilter
    cat <<SYSCTL | tee /etc/sysctl.d/k8s.conf
    net.bridge.bridge-nf-call-iptables  = 1
    net.bridge.bridge-nf-call-ip6tables = 1
    net.ipv4.ip_forward                 = 1
    SYSCTL
    sysctl --system
    apt-get update
    apt-get install -y containerd
    mkdir -p /etc/containerd
    containerd config default | tee /etc/containerd/config.toml
    sed -i 's/SystemdCgroup = false/SystemdCgroup = true/' /etc/containerd/config.toml
    systemctl restart containerd
    curl -fsSL https://pkgs.k8s.io/core:/stable:/v1.36/deb/Release.key | gpg --dearmor -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg
    echo 'deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/v1.36/deb/ /' | tee /etc/apt/sources.list.d/kubernetes.list
    apt-get update
    apt-get install -y kubelet kubeadm kubectl
    apt-mark hold kubelet kubeadm
    systemctl enable --now kubelet
```

This needs real internet egress from the instance's VPC to reach
`pkgs.k8s.io` — that's a site/VPC network-policy question, not something
CAPNICo or this template controls. If `apt-get update` can't reach it, check
with whoever manages your site's networking before assuming the image itself
is broken.

Requirement 6 (a CNI) can go in the control plane's `postKubeadmCommands` the
same way, applied once `kubeadm init` has actually run:

```yaml
postKubeadmCommands:
  - |
    export KUBECONFIG=/etc/kubernetes/admin.conf
    curl -fsSL -o /tmp/kube-flannel.yml https://github.com/flannel-io/flannel/releases/latest/download/kube-flannel.yml
    pod_cidr=$(kubectl get node "$(hostname)" -o jsonpath='{.spec.podCIDR}')
    sed -i "s#10.244.0.0/16#${pod_cidr}#" /tmp/kube-flannel.yml
    kubectl apply -f /tmp/kube-flannel.yml
```

Flannel's manifest hardcodes `10.244.0.0/16` — if that doesn't match your
`Cluster.spec.clusterNetwork.pods.cidrBlocks` (it won't, for
`examples/kubeadm/cluster.yaml`'s own `192.168.0.0/16`), Flannel refuses to
start at all: `subnet "10.244.0.0/16" ... doesn't contain "<real-cidr>"
PodCIDR`. The `sed` above rewrites it to the node's real `podCIDR`, read
straight off the `Node` object rather than assumed, so it self-corrects
regardless of what CIDR your cluster actually uses.

Flannel is just an example, not a recommendation — pin a version rather than
`latest` for anything beyond a one-off test, and match whatever CNI your
production images actually use. Until something applies a CNI, nodes join
and stay `NotReady`, same as requirement 6 in the contract table above.

### Proving an image before you trust it

Boot one instance by hand, with no Cluster API, and check the four things in
order. Each failure looks like a different problem one layer up.

```bash
cloud-init status --wait                                  # 2: datasource reachable, user data applied
curl -fsS 169.254.169.254:7777/latest/meta-data/instance-id   # 5: metadata service
command -v kubeadm kubelet curl                           # 3, 4: tools present
```

Then, after it has joined, run
`kubectl get node <name> -o jsonpath='{.spec.providerID}'`. It must print
`nico://<instance-id>`. An empty value means requirement 5 failed silently.

## Common Pitfalls

### The Spec Freezes After the Provider ID Is Set

CAPNICo builds the create request from the spec and never applies later changes
to a live instance, so CEL rules reject the edit outright. To change machine
infrastructure, make a new `NicoMachineTemplate` and repoint the
`KubeadmControlPlane` or `MachineDeployment`. Cluster API then does a
replacement rollout.

### Delete the Cluster and Let It Finish Before Deleting the Namespace

Deleting the namespace first can remove a per-cluster credentials Secret before
the finalizers run, which strands machines in deletion and leaves NICo instances
to clean up by hand.

### Two Operations Are Annotations on the CAPI Machine, Not CRD Fields

Both annotations in the following table ignore an empty value.

| Annotation | Effect |
|---|---|
| `nico.nvidia.com/reboot` | One reboot per application. CAPNICo removes the annotation after NICo accepts it. Configure the key with `--reboot-annotation`. |
| `nico.nvidia.com/machine-health-issue` | Forwarded to NICo as context on the delete request. The value parses as JSON in the form `{"category","summary","details"}`, and otherwise is treated as a plain summary with category `Other`. Configure the key with `--repair-annotation`, and set it empty to disable. |

The support level for this provider is Experimental.

## Related Information

- [Architecture](architecture.md) is what to read before you change controller
  behavior.
- [Development](development.md) walks through the local kind, Tilt, Helm, and
  fake-NICo loop.
- The [README](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/README.md)
  has the install steps, the Secret layout, and the full worked examples.
- [Troubleshooting](troubleshooting.md) handles symptom lookup beyond the stall
  table above.
- The [NICo documentation](https://docs.nvidia.com/infra-controller/) describes
  the platform itself.
