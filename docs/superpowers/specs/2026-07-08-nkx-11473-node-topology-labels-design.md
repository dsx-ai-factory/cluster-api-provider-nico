# NKX-11473: CAPI node topology labels

## Goal

Give CAPI-NICo tenant nodes the same Forge/Carbide topology labels as nodes
provisioned through the direct-to-Forge path, without changing the existing
`node.kubernetes.io/instance-type` label or adding `sku-id`.

## Status contract

`NicoMachine.status` is the provider's raw observed topology snapshot. It
contains the existing `machineID` plus the following optional fields:

- `siteID`
- `siteName`
- `vpcID`
- `vpcName`

The values are raw Forge/Carbide API values. In particular, the two names are
not normalized in CAPNICo. CAPNICo resolves the site and VPC names with its
tenant-visible NICo client while reconciling the backing instance. Failure to
resolve either name is non-fatal: the corresponding status field remains
absent and a later reconcile retries it. IDs and machine identity continue to
be recorded independently.

## Tenant-node reconciliation

CWE's existing controller-runtime reconciler that watches `NicoMachine` will
continue to set `Node.spec.providerID` and will additionally own only these
node label keys:

- `nke.nvidia.com/machine-id`
- `nke.nvidia.com/site-id`
- `nke.nvidia.com/site-name`
- `nke.nvidia.com/vpc-id`
- `nke.nvidia.com/vpc-name`

It derives values only from `NicoMachine.status`. Before writing `site-name`
or `vpc-name`, CWE applies the same direct-to-Forge normalization: lowercase
ASCII; replace invalid characters, including spaces and slashes, with `-`;
collapse repeated dashes; trim invalid edge characters; and limit the result
to 63 characters. A missing or empty normalized value is absent from the
tenant node, not written as an empty-string label. The controller also removes
one of its managed labels when the matching status field is absent.

The controller must not alter any other Node labels, including
`node.kubernetes.io/instance-type`; it must not add `nke.nvidia.com/sku-id`.

## Error handling and convergence

The controller retains the current waits for an owning CAPI `Machine`, CAPI
`Cluster`, tenant kubeconfig, and tenant Node. It skips deleting objects. A
normal reconciliation is a no-op when both `providerID` and all managed labels
already match. Status updates retrigger the controller, allowing late topology
name resolution to converge without a separate sync operator.

## Tests

CAPNICo tests will cover raw status population from the NICo API, including a
missing optional name. CWE controller tests will cover normalized name labels,
IDs and machine ID, missing-value omission and stale-label removal, preservation
of unrelated labels, and the existing provider-ID behavior.
