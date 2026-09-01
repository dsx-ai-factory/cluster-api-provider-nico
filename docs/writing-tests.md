# Writing controller tests

Terminal means settled for one fixture input, including pending and failure
states. Envtest cases are snapshots of that state, not workflow scripts.

- **Use the existing create, update, or delete case set. Never add a case set
  for a provider-specific sub-flow.**
- Group cases only when their settled generation differs.
- A step only waits for the case-set invariant: current `observedGeneration`,
  expected generation, or deletion. Goldens assert every field and condition.
- Use E2E tests for timing, watches, and interactions across controllers. Use
  envtest cases for individual state-machine states.

## Fixtures and goldens

- Keep Kubernetes and NICo goldens together. `expected_objects.yaml` covers
  API-server state; `expected_nico.yaml` covers HTTP requests and external
  resources.
- `make test-update` generates the golden

## CAPNICo generation invariants

- `NicoCluster` create cases stay at generation 1; reconciliation only writes
  status.
- A provisioned `NicoMachine` create case reaches generation 2 when CAPNICo
  writes `spec.providerID`.
- An unprovisioned `NicoMachine` create case stays at generation 1.
- A provisioned `NicoMachine` spec update reaches generation 3.
