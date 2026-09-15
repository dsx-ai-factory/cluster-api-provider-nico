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

## Extending the fake NICo backend

`internal/fake`'s `Server` is a self-contained stand-in for the NICo API.
Tests run the production client against `Server.Handler()`, so no real NICo
deployment is needed. The package rule: extend this HTTP surface instead of
bypassing serialization with an injected Go client.

**Add an endpoint.** Register the route in `Handler()`, following the
existing method-and-path-to-handler pattern (for example, `GET
/v2/org/{org}/nico/instance/{instanceID}` maps to `s.getInstance`). Write the
handler as a method on `*Server`: lock `s.mu`, read or mutate state, unlock,
then respond with `writeJSON` or `writeError`.

**Add seedable state.** Add a `Seed<Thing>(org string, thing
nicosdk.<Thing>)` method next to `SeedTenant`, `SeedInstanceType`,
`SeedInstance`, `SeedMachine`, `SeedSite`, and `SeedVPC`. Each one locks,
clones the input, and stores it in a map keyed by `resourceKey(org, id)`.
Add the matching map field to `Server`. If fixtures should seed it from
YAML, add a slice to `serverDump` and a case in `SeedFromYAML`.

**Golden state.** `Server.Dump()` marshals every resource map, plus
deduplicated write requests, to YAML, which is what `expected_nico.yaml`
compares against. Read polling is left out on purpose, so it cannot make
goldens flap. A new resource type needs a case in `Dump()` too, or it never
appears in the golden.
