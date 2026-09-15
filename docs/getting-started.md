# Getting started

CAPNICo is a Cluster API **infrastructure provider**. It turns each Cluster API
`Machine` into one NICo instance on bare metal. It does not install Kubernetes
and does not join nodes — the kubeadm bootstrap and control-plane providers do
that, and CAPNICo carries their cloud-init to the instance as user data.

Three paths. Start with A even if you have hardware — it is the fastest way to
learn what a healthy reconcile looks like, and you will want that baseline when
C misbehaves.

| Path | Needs | Gets you |
|---|---|---|
| [**A — in-repo fake**](#path-a--the-built-in-fake) | Docker, kind | The full reconcile loop in ~5 minutes, no NICo at all |
| [**B — local NICo**](#path-b--against-a-real-nico) | Docker, kind, the infra-controller repo | The real NICo REST API, against mock hosts |
| [**C — production**](#path-c--production-against-an-existing-nico-site) | An existing NICo site with real hardware | Machines that actually boot |

## Before you start

Clone this repository — every path below runs `make` and reads files from the
checkout.

```bash
git clone https://github.com/dsx-ai-factory/cluster-api-provider-nico
cd cluster-api-provider-nico
```

Install these. `make tilt-up` calls `ctlptl`, `tilt` and `helm` directly and
does not install them for you.

| Tool | Used for |
|---|---|
| [Docker](https://docs.docker.com/engine/install/) | Builds, and the kind node |
| [ctlptl](https://github.com/tilt-dev/ctlptl) | Creates the kind cluster and its local registry |
| [tilt](https://docs.tilt.dev/install.html) | Runs the development loop |
| [helm](https://helm.sh/docs/intro/install/) | Installs the provider chart |
| `kubectl` | Everything |
| [Go](https://go.dev/dl/) | Builds the manager and the codegen tools |
| [clusterctl](https://cluster-api.sigs.k8s.io/user/quick-start) | Paths B and C only |
| [devspace](https://www.devspace.sh) | Path B only — deploys the local NICo stack |

Ports `10352` (Tilt UI) and `5006` (the local registry) must be free.

## Path A — the built-in fake

The repository ships a fake NICo endpoint, so no hardware and no NICo access is
needed.

**Terminal 1** — this stays running. `Ctrl-C` stops the loop; `make tilt-down`
deletes the cluster.

```bash
make tilt-up     # kind + CAPI core + kubeadm providers + CAPNICo + fake NICo
```

Tilt UI is on `localhost:10352`, deliberately not Tilt's default 10350, so this
can run beside another Cluster API environment. `make run-fake` serves the fake
alone on `:8090`.

**Terminal 2** — wait for the provider to be up before applying anything. On a
cold start Tilt is still building images, and the CRDs and the
`capnico-system` namespace do not exist yet.

```bash
export KUBECONFIG=~/.kube/capnico.kubeconfig

kubectl wait --for=condition=Established --timeout=5m \
  crd/nicomachines.infrastructure.cluster.x-k8s.io
kubectl -n capnico-system rollout status deploy -l control-plane=controller-manager --timeout=5m

kubectl apply -f examples/cluster-fake.yaml
kubectl get nicoclusters,nicomachines -A
```

`examples/cluster-fake.yaml` creates CAPI `Machine`s directly with ready-made
bootstrap Secrets, because CAPNICo provisions infrastructure and nothing else.

Watch a machine converge:

```bash
kubectl describe nicomachine demo-cp-0
```

Both machines should end with a provider ID and `Provisioned`. **The worker
briefly shows `ControlPlanePriorityDeferred` and that is correct** — workers
wait behind control-plane machines competing for the same instance type.

`status.conditions` is where the answer is. Every stall has a named reason —
`WaitingForBootstrapData`, `InstanceTypeUnavailable`, `WaitingForIdentitySecret`,
`ControlPlanePriorityDeferred`, `InstanceNotReady`.

## Path B — against a real NICo

### What must already exist

CAPNICo creates instances. It creates nothing else. Before it can work, the NICo
side needs:

- a **site** — goes on `NicoCluster.spec.siteID`
- a **VPC** — goes on `NicoMachine.spec.vpcID`, **not** on the cluster
- a **subnet** — one per interface attachment (`vpcPrefixID` instead of
  `subnetID` only on sites with Native Networking enabled)
- an **instance type** for control-plane and worker machines

For a local NICo, `hack/local-nico-seed.sh` creates all of these — see "Seed
test resources" below.

### Standing up a local NICo

[NVIDIA/infra-controller](https://github.com/NVIDIA/infra-controller) runs
locally with mock hosts, which is enough to exercise CAPNICo against the real
REST API. `bootstrap-prereqs.sh` operates on whatever kube context is
current, so point `KUBECONFIG` at a **dedicated local kind cluster** first —
not the Path A cluster, and not a shared or remote one. From that repository:

```bash
dev/deployment/devspace/bootstrap-prereqs.sh   # cert-manager, PostgreSQL, Vault, Temporal, Keycloak
devspace deploy                                # Core + REST + machine-a-tron (the mock hosts)
```

Both build a full Rust/Go workspace and several images.

- Docker: 8 CPUs, 12GB RAM, 100GB free disk, minimum.
- `devspace deploy`'s final verification step runs 15–20 minutes with no
  output. Expected, not a hang.

For a real site instead, see that repository's `helm-prereqs/setup.sh`.

Keep the REST API and Keycloak reachable:

```bash
kubectl -n nico-rest port-forward service/nico-rest-api 18388:8388
kubectl -n nico-rest port-forward service/keycloak     18082:8082
```

### Seed test resources

A fresh local site has mock hosts but no VPC, instance type, or allocation
yet — "Create a cluster" below needs all three, and nothing up to this point
creates them. Safe to re-run — it reuses what already exists rather than
duplicating it. It also extends the realm's access-token lifespan from the
5-minute default.

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

### The credentials Secret

**Two clusters are in play from here.** NICo runs in the one `devspace deploy`
just built. CAPNICo runs in your *management* cluster — the Path A kind cluster
is fine. The commands below target the management cluster, so point `KUBECONFIG`
at it.

Required: `endpoint`, `orgID`. Then **one** authentication mode, never both —
supplying a token and OAuth keys together is rejected.

```bash
kubectl create namespace capnico-system --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret generic nico-credentials -n capnico-system \
  --from-literal=endpoint=http://nico-rest-api.nico-rest.svc.cluster.local:8388 \
  --from-literal=orgID=test-org \
  --from-literal=apiName=nico \
  --from-literal=token="${TOKEN}"
```

`endpoint` must be reachable **from a pod in the management cluster**, not from
your laptop. The in-cluster DNS name above only works when CAPNICo and NICo share
a cluster. If they do not, expose the NICo REST Service to the management cluster
— a NodePort on the NICo cluster, or putting both kind clusters on the same
Docker network — and use that address. Check it from a pod, not a shell:

```bash
kubectl -n capnico-system run netcheck --rm -it --restart=Never \
  --image=curlimages/curl -- curl -sv <endpoint>/healthz
```

Clients are cached per Secret `resourceVersion`, so an external rotation is
picked up on the next reconcile with no manager restart.

### Install the provider

```bash
clusterctl init --bootstrap kubeadm --control-plane kubeadm

helm upgrade --install capi-provider-nico ./chart \
  --namespace capnico-system --create-namespace \
  --set manager.image.repository=ghcr.io/dsx-ai-factory/cluster-api-provider-nico/controller \
  --set manager.image.tag=v0.0.43 --wait
```

Or install from a published release with `clusterctl` by adding the release
asset URL to `~/.config/cluster-api/clusterctl.yaml`, then
`clusterctl init --infrastructure nico:<version>`.

### Create a cluster

Three templates under `examples/kubeadm/`. List a template's variables with
`clusterctl generate cluster demo --from <template> --list-variables`.

| Template | Use when |
|---|---|
| `cluster.yaml` | A stable API endpoint already exists (DNS, external LB, your own kube-vip) |
| `cluster-kube-vip.yaml` | You want the template to bootstrap kube-vip as a static pod |
| `cluster-single-control-plane-static-ip.yaml` | Developer bring-up only — single control plane, requested IP, no HA |

For a local NICo, `source /tmp/nico-env.sh` from "Seed test resources" above
sets `NICO_SITE_ID`, `NICO_VPC_ID`, the instance type IDs, and
`NICO_NETWORK_METHOD`/`NICO_NETWORK_ID` — only the iPXE scripts and the
control-plane endpoint stay as placeholders, since nothing local boots them:

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

If reconciliation reports `401 Unauthorized` or `TenantResolutionFailed`
partway through, the static token from "The credentials Secret" has expired —
re-mint it and update the Secret, same as before.

Against a local NICo, this is as far as Path B goes: `kubectl -n demo get
nicomachine` reaching `PROVISIONED=true` means the CAPNICo↔NICo integration
works end to end. It will not reach a booted, `Ready` node — the iPXE URLs
above are placeholders and `machine-a-tron` only mocks the hardware, so
nothing ever actually boots `kubeadm`. For a real boot chain, see "What has to
be in the OS image" below and Path C.

Nodes get their provider ID from the NICo metadata service: the templates read
`169.254.169.254:7777/latest/meta-data/instance-id` and patch kubelet with
`providerID: nico://<instance-id>`. That is how Cluster API matches a `Node`
back to its `Machine`.

## Path C — production, against an existing NICo site

Assumes the site is already installed, its machines are enrolled, and someone
can issue you a token. You are adding CAPNICo to it.

### 1. Collect seven values

Everything CAPNICo needs is an ID you look up. Install
[`nicocli`](https://github.com/NVIDIA/infra-controller/tree/main/rest-api/cli)
from the infra-controller repository (`make nico-cli`, then `nicocli init`) and
read them off:

| Value | Where it goes | Find it with |
|---|---|---|
| API base URL | Secret `endpoint` | your site operator |
| Org | Secret `orgID` | your site operator |
| API name | Secret `apiName` | the path segment in a working URL |
| Site ID | `NicoCluster.spec.siteID` | `nicocli site list` |
| VPC ID | `NicoMachine.spec.vpcID` | `nicocli vpc list` |
| VPC prefix ID | `NICO_NETWORK_ID` | `nicocli vpc-prefix list` |
| Instance type IDs | `NICO_*_INSTANCE_TYPE_ID` | `nicocli instance-type list` |

Optionally SSH key groups (`nicocli sshkeygroup list`) for Serial-over-LAN.

You also need an **iPXE script URL** per role that boots an OS image able to
consume kubeadm cloud-init. CAPNICo does not supply one.

### ⚠️ 2. Check the instance types have allocations

**This is the failure that wastes the most time.** CAPNICo will not create an
instance unless NICo reports free capacity for its instance type. Before every
create it reads `allocationStats.unusedUsable`, and treats zero as unavailable:

```
Instance type "…" has no unused usable allocations (total=0 used=0 unused=0 unusedUsable=0)
```

That figure comes from NICo **allocations**, which are a separate resource from
the instance type. An instance type with no allocation looks perfectly healthy
in `nicocli instance-type get` and provisions nothing. Machines sit in
`InstanceTypeUnavailable` and retry every 2 minutes, forever.

```bash
nicocli allocation list
nicocli instance-type get <instance-type-id>   # confirm allocationStats.unusedUsable > 0
```

If the response omits `allocationStats.unusedUsable` entirely, CAPNICo fails the
reconcile with an error naming that field rather than guessing.

The full dependency order is **site → site IP blocks → instance types →
allocations → VPCs → VPC prefixes**. NICo creates site IP blocks itself from
fabric prefixes the site reports; the rest are yours. `nicocli site bootstrap
--file <manifest>` walks that chain in order and is the sanctioned way to create
it — see `rest-api/cli/examples/site-prerequisites.yaml`. It never creates the
site.

### 3. Get a machine-usable credential

**CAPNICo supports the client-credentials grant only.** A token minted by an
interactive password grant works until it expires and then the provider stops
provisioning, so for anything lasting, ask for an OAuth client:

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

Never set `token` alongside the OAuth keys — supplying both is rejected.

If your site's API uses a private CA, pass `ca.crt`. Prefer that over
`insecureSkipTLSVerify`.

Three things the management cluster must be able to do, and all three fail
somewhere other than where you look:

- **Reach `endpoint`.** Resolve and route it from a pod, not from your laptop.
- **Pull the controller image** from a registry it has credentials for. Build
  and push your own — see step 4.
- **Reach the OAuth `tokenURL`**, which is often a different host from the API.

Separate provider and tenant organizations are supported, but one token must
carry the required role in both.

### 4. Build the image, push it, install

Build the controller from this checkout and push it to a registry your
management cluster can pull from. `IMG` is one variable, used by both targets:

```bash
export IMG=<your-registry>/cluster-api-provider-nico/controller:v0.0.43

make docker-build IMG="${IMG}"
make docker-push  IMG="${IMG}"
```

For a cluster whose nodes are not all the same architecture, build a multi-arch
manifest instead — this one builds and pushes in a single step:

```bash
make docker-buildx IMG="${IMG}" PLATFORMS=linux/amd64,linux/arm64
```

Then install Cluster API and the provider, pointing the chart at that image.
Split `IMG` at the colon: the part before it is `repository`, the part after is
`tag`.

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

Released deployments log structured JSON; `--zap-devel` is a local-only value.

Credentials can be **provider-level** (one Secret in `capnico-system`, used by
every cluster) or **per-cluster** (`NicoCluster.spec.identityRef`, in the
`NicoCluster`'s namespace). Use per-cluster when one management cluster drives
several sites or tenants.

### 5. Prove the credentials before creating machines

Readiness on a `NicoCluster` is a validation check — *can this identity reach
NICo and resolve a tenant* — and it provisions nothing. So a throwaway one is a
free credential test that cannot cost you hardware.

A `NicoCluster` only starts reconciling once a CAPI `Cluster` sets an owner
reference on it — a bare `NicoCluster` with no owning `Cluster` sits
forever logging `Waiting for Cluster controller to set OwnerRef on
NicoCluster` and never even attempts the credential check. Apply both:

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

Ready means the identity resolved a tenant, so auth, TLS, routing and org are
all correct. Not ready names the failure: `WaitingForIdentitySecret` for a
missing or misnamed Secret, `IdentityConfigurationFailed` for one the provider
cannot parse, and `TenantResolutionFailed` for everything else — see step 7.

**Fix this before creating a single Machine**, then clean up — delete the
`Cluster` first, same as "Things that will bite you" below:

```bash
kubectl -n demo delete cluster credcheck
```

### 6. First cluster: one control plane, one worker

Smallest thing that proves the path end to end. Use `cluster.yaml` with an
endpoint you already control, so the first run does not also debug kube-vip.

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

A node appearing means the whole chain worked: CAPNICo created the instance, it
booted the iPXE image, cloud-init ran kubeadm, and kubelet picked up
`providerID: nico://<instance-id>` from the metadata service.

#### Validating without the kubeadm providers

The templates above need the kubeadm bootstrap and control-plane providers
(`clusterctl init --bootstrap kubeadm --control-plane kubeadm`, step 4)
installed on the management cluster. Not every real site has them — some run
a different orchestrator on top of CAPNICo instead of vanilla kubeadm CAPI.

If yours doesn't, you can still validate CAPNICo on its own by creating a
`Machine` and `NicoMachine` directly instead of going through
`KubeadmControlPlane` — see `examples/cluster-fake.yaml`'s pattern. That
proves CAPNICo's own create → status behavior against the real REST API and
real hardware without depending on kubeadm at all.

### 7. When it stalls

`status.conditions` on the `NicoMachine` names the reason. Read it first — every
stall below is a distinct, named reason, not a generic timeout.

| Reason | Means |
|---|---|
| `WaitingForIdentitySecret` | Secret missing or misnamed |
| `TenantResolutionFailed` | The tenant lookup failed: wrong org or `apiName`, an expired or rejected token, an unroutable endpoint, or a CA mismatch. All four land here |
| `InstanceTypeNotFound` | Wrong instance type ID, or wrong org |
| `InstanceTypeUnavailable` | **No free allocation** — see step 2 |
| `ControlPlanePriorityDeferred` | A control-plane machine is waiting on this type; workers wait |
| `WaitingForBootstrapData` | The kubeadm provider has not written the Secret yet |
| `InstanceCreateFailed` | NICo rejected the create; message carries its error |
| `InstanceNotReady` | Instance exists and is booting — normal for several minutes |

Nothing here polls faster than it needs to: 15s on the fast path, 30s while an
instance boots or tears down, 2 minutes when capacity is exhausted, and about 5
minutes once ready — jittered up to a minute per object so a large cluster does
not requeue in lockstep.

If an instance is created but no node ever appears, CAPNICo has done its job and
the problem is below it — the iPXE image, cloud-init, or network reachability to
the control-plane endpoint.

## What has to be in the OS image

CAPNICo hands NICo an **iPXE script URL** and a blob of **cloud-init user data**,
and stops. Everything after the machine powers on is the image's job. Nothing in
this repository builds one, and if the image is wrong you get a created instance
and a node that never registers — CAPNICo will report `Ready` and be right.

### The contract

Six requirements. All of them come from the two things CAPNICo actually sets on
the create request: `ipxeScript` and `userData`
(`controllers/nicomachine_controller.go:835` sets the latter).

| # | Requirement | Why |
|---|---|---|
| 1 | **Boots from your iPXE script** | `NicoMachine.spec.ipxeScript` is chained verbatim. CAPNICo does not host it |
| 2 | **cloud-init, reading NICo's datasource** | The kubeadm bootstrap provider writes a `#cloud-config`; CAPNICo passes it through untouched as instance user data |
| 3 | **`kubeadm`, `kubelet`, a container runtime** | The cloud-config runs `kubeadm init`/`join`. It does not install them |
| 4 | **`curl`** | `preKubeadmCommands` shells out to it before kubeadm runs |
| 5 | **Route to `169.254.169.254:7777`** | The metadata service, served by the DPU agent. Provider ID comes from here |
| 6 | **A CNI plan** | The templates install none. Nodes stay `NotReady` until something does |

Two lines from `examples/kubeadm/cluster.yaml:98-104` fix requirements 4 and 5:

```bash
iid=$(curl -fsS --max-time 2 169.254.169.254:7777/latest/meta-data/instance-id || true)
```

That is guarded with `|| true`, so a missing metadata service degrades quietly —
the node joins with **no provider ID**, and Cluster API never matches it to its
`Machine`. A cluster that looks half-built with healthy-looking nodes is usually
this.

⚠️ **`cluster-kube-vip.yaml` is stricter.** Its `preKubeadmCommands` run under
`set -euo pipefail` and read the peer ASN **without** `|| true`
(`cluster-kube-vip.yaml:105,118`):

```bash
bgp_peer_as=$(curl -fsS --max-time 2 169.254.169.254:7777/latest/meta-data/asn)
```

A metadata hiccup there fails the whole step instead of degrading. That template
also needs `ip` (iproute2), a writable `/etc/kubernetes/manifests`, and the
ability to pull `ghcr.io/kube-vip/kube-vip`.

### Which datasource

NICo serves cloud-init two ways, and a production image is normally configured
for the first with the second as fallback:

- **NoCloud** — `http://carbide-pxe.forge/api/v0/cloud-init/`
- **EC2** — `http://169.254.169.254:7777`

`nico-pxe` in the infra-controller repository is the service that serves both the
iPXE script and the cloud-init data; `pxe/ipxe/` and `pxe/templates/` are the
files it renders.

### Things you do not bake

Instance identity arrives at first boot, never at build time: hostname, join
tokens, SPIRE or other trust material, anything site-specific. CAPNICo sets
`hostname`, `preserve_hostname: false` and `manage_etc_hosts` into the
cloud-config itself when `spec.cloudInitInjectHostname` is true
(`internal/nico/userdata.go:13-30`), so the image must not fight it.

A build that bakes identity produces machines that are all the same machine.
Whatever recipe you use, end it with `cloud-init clean` so the next boot is a
real first boot.

### Recipes you can copy

Nothing here is a dependency — these are worked examples of the same contract.

| Source | What it gives you |
|---|---|
| [Image Builder](https://github.com/kubernetes-sigs/image-builder) | The upstream Cluster API way to bake kubeadm, kubelet and a container runtime into an image. Covers requirement 3, and its output is the normal starting point |
| [cloud-init datasource docs](https://cloudinit.readthedocs.io/en/latest/reference/datasources.html) | How to pin NoCloud and EC2 so the image reads NICo's, and only NICo's |
| `pxe/` in the infra-controller repository | `nico-pxe` itself: how the iPXE script and the cloud-init payload are served, and the templates it renders |

The usual shape is Image Builder for the Kubernetes layer, then a small
provisioning step of your own for the datasource configuration and anything
site-specific.

### Proving an image before you trust it

Boot one instance by hand — no Cluster API — and check the four things in order.
Each failure looks like a different problem one layer up.

```bash
cloud-init status --wait                                  # 2: datasource reachable, user data applied
curl -fsS 169.254.169.254:7777/latest/meta-data/instance-id   # 5: metadata service
command -v kubeadm kubelet curl                           # 3, 4: tools present
```

Then, once it has joined: `kubectl get node <name> -o jsonpath='{.spec.providerID}'`
must print `nico://<instance-id>`. Empty means requirement 5 failed silently.

## Things that will bite you

**The spec freezes once `providerID` is set.** CAPNICo builds the create request
from the spec and never applies later changes to a live instance, so CEL rules
reject the edit outright. To change machine infrastructure, make a new
`NicoMachineTemplate` and repoint the `KubeadmControlPlane` or
`MachineDeployment` — Cluster API then does a replacement rollout.

**Delete the `Cluster` and let it finish before deleting the namespace.**
Namespace-first can remove a **per-cluster** credentials Secret before the
finalizers run,
which strands machines in deletion and leaves NICo instances to clean up by
hand.

**Two operations are annotations on the CAPI `Machine`, not CRD fields.** Both
ignore an empty value.

| Annotation | Effect |
|---|---|
| `nico.nvidia.com/reboot` | One reboot per application; CAPNICo removes the annotation once NICo accepts. `--reboot-annotation` |
| `nico.nvidia.com/machine-health-issue` | Forwarded to NICo as context on the delete request. Value parses as JSON `{"category","summary","details"}`, else is treated as a plain summary with category `Other`. `--repair-annotation`, empty to disable |

**Support level: Experimental.**

## See also

- [architecture.md](architecture) — read before changing controller behaviour
- [development.md](development) — the local Kind, Tilt, Helm and fake-NICo loop
- [README.md](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/README.md) — install, the Secret layout, the full worked examples
- [troubleshooting.md](troubleshooting): symptom lookup beyond the stall table above
- [NICo documentation](https://docs.nvidia.com/infra-controller/)
