# Development

Everything here runs without hardware and without access to a real NICo
deployment. The repository ships a self-contained fake endpoint for the NICo
API surface used by CAPNICo.

## The local loop

```bash
make tilt-up
```

That creates a local kind cluster, installs Cluster API core and the kubeadm
providers, installs CAPNICo through its native Helm chart, and runs it against
the fake endpoint. The Tilt web UI is on `localhost:10352`. Tear it down with
`make tilt-down`. The local chart values enable `--zap-devel`; released
deployments omit it and use structured JSON logging by default.

With the stack up, apply the sample cluster:

```bash
kubectl apply -f examples/cluster-fake.yaml
```

That manifest is deliberately the smallest thing that provisions through
CAPNICo: one control-plane-labelled machine and one worker. It creates CAPI
Machines directly with ready-made bootstrap Secrets because CAPNICo provisions
infrastructure; it does not install Kubernetes or join nodes. Watch it
converge:

```bash
kubectl get nicoclusters,nicomachines -A
kubectl describe nicomachine demo-cp-0
```

The fake is seeded from the `fake-nico-seed` ConfigMap in
`hack/tilt/fake-nico-api.yaml`, whose machines carry `failure_domain` labels.
Those labels, plus `failureDomainLabelKey: failure_domain` on the `NicoCluster`
in `examples/cluster-fake.yaml`, are the whole failure domain path:

```bash
kubectl get nicocluster demo -o jsonpath='{.status.failureDomains[*].name}'
# rack-group-a rack-group-b

kubectl get nicomachine demo-cp-0 -o jsonpath='{.status.failureDomain}/{.status.machineID}'
# rack-group-b/machine-rack-b-1
```

`demo-cp-0` requests `rack-group-b`, so CAPNICo sends
`machineLabelSelector: {"failure_domain":"rack-group-b"}` and NICo can only
answer with the machine carrying that label. `demo-worker-0` requests no
domain and is placed unconstrained. Edit the ConfigMap to add domains or move
a machine between them.

To run the fake endpoint on its own, outside the cluster:

```bash
make run-fake          # listens on :8090
make run               # manager against the current kubecontext
```

`make run-fake` starts an empty endpoint, which publishes no failure domains.
Pass `--seed` to serve an inventory, in the format the `fake-nico-seed`
ConfigMap carries under its `seed.yaml` key:

```bash
go run ./cmd/fake --seed my-machines.yaml
```

## Tests

```bash
make test              # unit and envtest suites
make test-update       # regenerate envtest goldens, then read the diff
```

`make test-update` rewrites the fixtures under `controllers/testdata`. Always
read the resulting diff before committing it; an unexpected change there
usually means a behavior change rather than a formatting change.

## Before opening a pull request

```bash
make manifests         # regenerate CRDs, RBAC, installer bundle, and Helm chart
make lint              # golangci-lint
make vet
make test
```

`make manifests` is the step most easily forgotten. The chart and configuration
are generated from the API and controller markers, so a source change without
regeneration can leave the release artifacts stale.

## Commit requirements

Commits follow Conventional Commits and must satisfy the repository's DCO and
signature policy. See [CONTRIBUTING.md](https://github.com/dsx-ai-factory/cluster-api-provider-nico/blob/main/CONTRIBUTING.md) for the full
contribution workflow.
