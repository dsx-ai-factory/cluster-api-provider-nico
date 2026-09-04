#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0
#
# Seed the test resources Path B's "Create a cluster" step needs against a
# local NICo (docs/getting-started.md, "Path B — against a real NICo"), and
# print them as `export` lines ready to source.
#
# bootstrap-prereqs.sh and devspace deploy stand up a site with mock hosts,
# but nothing creates the VPC, instance type, or allocations a NicoCluster /
# NicoMachineTemplate needs -- those are tenant-side resources, not part of
# the site bootstrap. This script creates them, idempotently, and exits with
# their IDs so the reader never has to hand-roll them from the API reference.
#
# Usage:
#   dev/deployment/devspace/setup-rest-integration.sh's port-forwards (or the
#   two `kubectl port-forward` commands from the guide) must already be
#   running, then:
#
#     hack/local-nico-seed.sh > /tmp/nico-env.sh
#     source /tmp/nico-env.sh
#
# Configuration is by environment variable, matching every other Keycloak/API
# constant already fixed by the guide's own examples:
#
#   API_URL        REST API base URL           (default http://localhost:18388)
#   KEYCLOAK_URL   Keycloak base URL            (default http://localhost:18082)
#   KEYCLOAK_REALM Keycloak realm               (default nico-dev)
#   CLIENT_ID      OAuth client_id              (default nico-api)
#   CLIENT_SECRET  OAuth client_secret          (default nico-local-secret)
#   USERNAME       Password-grant username      (default admin@example.com)
#   PASSWORD       Password-grant password      (default adminpassword)
#   ORG            Org path segment             (default test-org)
#   SITE_NAME      Site to seed                 (default local-dev-site)
#   ACCESS_TOKEN_LIFESPAN  Seconds to extend the realm's access tokens to, so
#                          a Secret minted from this script's TOKEN survives
#                          a normal working session instead of the realm
#                          default (300s -- five minutes)     (default 3600)
#   KEYCLOAK_ADMIN_USER / KEYCLOAK_ADMIN_PASSWORD  master-realm admin, used
#                          only for the token-lifespan bump   (default admin/admin)

set -euo pipefail

API_URL="${API_URL:-http://localhost:18388}"
KEYCLOAK_URL="${KEYCLOAK_URL:-http://localhost:18082}"
KEYCLOAK_REALM="${KEYCLOAK_REALM:-nico-dev}"
CLIENT_ID="${CLIENT_ID:-nico-api}"
CLIENT_SECRET="${CLIENT_SECRET:-nico-local-secret}"
USERNAME="${USERNAME:-admin@example.com}"
PASSWORD="${PASSWORD:-adminpassword}"
ORG="${ORG:-test-org}"
SITE_NAME="${SITE_NAME:-local-dev-site}"
ACCESS_TOKEN_LIFESPAN="${ACCESS_TOKEN_LIFESPAN:-3600}"
KEYCLOAK_ADMIN_USER="${KEYCLOAK_ADMIN_USER:-admin}"
KEYCLOAK_ADMIN_PASSWORD="${KEYCLOAK_ADMIN_PASSWORD:-admin}"

die() { echo "ERROR: $*" >&2; exit 1; }
log() { echo "  $*" >&2; }

command -v curl >/dev/null || die "curl is not on PATH."
command -v jq >/dev/null || die "jq is not on PATH."

# The realm ships with a 5-minute access-token lifespan, which routinely
# expires mid-session and then looks like a stuck reconcile or a stuck
# deletion (CAPNICo silently retries against a 401 with no visible signal
# short of reading its logs). Best-effort: a non-default local Keycloak admin
# password must not block the rest of the script.
ADMIN_TOKEN="$(curl -fsS -X POST "${KEYCLOAK_URL}/realms/master/protocol/openid-connect/token" \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    -d 'client_id=admin-cli' -d 'grant_type=password' \
    -d "username=${KEYCLOAK_ADMIN_USER}" -d "password=${KEYCLOAK_ADMIN_PASSWORD}" \
    | jq -r '.access_token' 2>/dev/null || true)"
if [[ -n "${ADMIN_TOKEN}" && "${ADMIN_TOKEN}" != "null" ]]; then
    log "extending ${KEYCLOAK_REALM} access tokens to ${ACCESS_TOKEN_LIFESPAN}s"
    curl -fsS -X PUT "${KEYCLOAK_URL}/admin/realms/${KEYCLOAK_REALM}" \
        -H "Authorization: Bearer ${ADMIN_TOKEN}" -H 'Content-Type: application/json' \
        -d "$(jq -n --argjson s "${ACCESS_TOKEN_LIFESPAN}" '{accessTokenLifespan: $s}')" >/dev/null \
        || log "could not extend the token lifespan -- continuing with the realm default"
else
    log "could not authenticate to Keycloak's master realm -- leaving the token lifespan as-is"
fi

TOKEN="$(curl -fsS -X POST "${KEYCLOAK_URL}/realms/${KEYCLOAK_REALM}/protocol/openid-connect/token" \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    -d "client_id=${CLIENT_ID}" -d "client_secret=${CLIENT_SECRET}" \
    -d 'grant_type=password' -d "username=${USERNAME}" -d "password=${PASSWORD}" \
    | jq -r '.access_token')"
[[ -n "${TOKEN}" && "${TOKEN}" != "null" ]] || die "could not mint a token from ${KEYCLOAK_URL} -- is Keycloak port-forwarded?"

api() {
    local method="$1" path="$2" body="${3:-}"
    local args=(-fsS -X "${method}" "${API_URL}/v2/org/${ORG}/nico/${path}" \
        -H "Authorization: Bearer ${TOKEN}" -H "Accept: application/json")
    [[ -z "${body}" ]] || args+=(-H "Content-Type: application/json" -d "${body}")
    curl "${args[@]}"
}

log "resolving site '${SITE_NAME}'"
SITE_ID="$(api GET "site?query=${SITE_NAME}" | jq -r --arg n "${SITE_NAME}" '.[] | select(.name == $n) | .id' | head -1)"
[[ -n "${SITE_ID}" ]] || die "no site named '${SITE_NAME}' -- has devspace deploy finished?"

TENANT_ID="$(api GET "tenant/current" | jq -r '.id')"
[[ -n "${TENANT_ID}" && "${TENANT_ID}" != "null" ]] || die "could not resolve the current tenant."

# 1. Site IP block. A fresh site has none of its own -- these are supposed to
# come from fabric prefixes the site reports, which a mock site never does.
IPBLOCK_ID="$(api GET "ipblock?siteId=${SITE_ID}" | jq -r '.[0].id // empty')"
if [[ -z "${IPBLOCK_ID}" ]]; then
    log "creating site IP block"
    IPBLOCK_ID="$(api POST ipblock "$(jq -n --arg site "${SITE_ID}" '{
        name: "capnico-local-dev", description: "Seeded by hack/local-nico-seed.sh",
        siteId: $site, routingType: "Public", prefix: "10.99.0.0", prefixLength: 16,
        protocolVersion: "IPv4",
    }')" | jq -r '.id')"
fi

# 2. Instance type. Its machineCapabilities gate which Machines can be
# associated with it (a Machine must be a capability superset). Read the
# requirement off a real Ready Machine at the site instead of guessing --
# guessed capabilities silently associate with nothing, and every instance
# created against the type then sits forever with no Machine to run on.
INSTANCE_TYPE_ID="$(api GET "instance/type?siteId=${SITE_ID}" | jq -r '.[0].id // empty')"
if [[ -z "${INSTANCE_TYPE_ID}" ]]; then
    log "creating instance type from a Ready Machine's reported capabilities"
    REF_MACHINE_CAPS="$(api GET "machine?siteId=${SITE_ID}&pageSize=100" \
        | jq -c '[.[] | select(.status == "Ready")][0].machineCapabilities | map(select(.type == "CPU")) | .[0:1]')"
    [[ "${REF_MACHINE_CAPS}" != "null" && "${REF_MACHINE_CAPS}" != "[]" ]] \
        || die "no Ready Machine at site ${SITE_ID} to read capabilities from."
    INSTANCE_TYPE_ID="$(api POST instance/type "$(jq -n --arg site "${SITE_ID}" --argjson caps "${REF_MACHINE_CAPS}" '{
        name: "capnico-local-dev", description: "Seeded by hack/local-nico-seed.sh",
        siteId: $site, machineCapabilities: $caps,
    }')" | jq -r '.id')"
fi

# 3. Associate every unassigned Ready Machine at the site with it.
UNASSIGNED_IDS="$(api GET "machine?siteId=${SITE_ID}&pageSize=100" \
    | jq -c '[.[] | select(.status == "Ready" and .instanceTypeId == null) | .id]')"
UNASSIGNED_COUNT="$(jq 'length' <<<"${UNASSIGNED_IDS}")"
if [[ "${UNASSIGNED_COUNT}" -gt 0 ]]; then
    log "associating ${UNASSIGNED_COUNT} unassigned Machine(s) with the instance type"
    # A brand-new instance type is not always immediately queryable by ID on
    # the write path this call uses, so the very first attempt right after
    # creation can 400. Retrying is cheap and the call itself is idempotent.
    for attempt in 1 2 3; do
        api POST "instance/type/${INSTANCE_TYPE_ID}/machine" \
            "$(jq -c --argjson ids "${UNASSIGNED_IDS}" '{machineIds: $ids}')" >/dev/null && break
        [[ "${attempt}" != 3 ]] || die "could not associate Machines with instance type ${INSTANCE_TYPE_ID}."
        sleep 2
    done
fi

# 4. Compute allocation, sized to every Machine now associated with the type
# -- creating one exceeding that count is rejected outright.
MACHINE_COUNT="$(api GET "instance/type/${INSTANCE_TYPE_ID}/machine" | jq 'length')"
[[ "${MACHINE_COUNT}" -gt 0 ]] || die "instance type ${INSTANCE_TYPE_ID} has no associated Machines to allocate."
HAS_COMPUTE_ALLOC="$(api GET "allocation?siteId=${SITE_ID}" \
    | jq --arg it "${INSTANCE_TYPE_ID}" '[.[].allocationConstraints[]? | select(.resourceType == "InstanceType" and .resourceTypeId == $it)] | length > 0')"
if [[ "${HAS_COMPUTE_ALLOC}" != "true" ]]; then
    log "creating compute allocation for ${MACHINE_COUNT} Machine(s)"
    api POST allocation "$(jq -n --arg tenant "${TENANT_ID}" --arg site "${SITE_ID}" \
        --arg it "${INSTANCE_TYPE_ID}" --argjson count "${MACHINE_COUNT}" '{
        name: "capnico-local-dev-compute", description: "Seeded by hack/local-nico-seed.sh",
        tenantId: $tenant, siteId: $site,
        allocationConstraints: [{resourceType: "InstanceType", resourceTypeId: $it, constraintType: "Reserved", constraintValue: $count}],
    }')" >/dev/null
fi

# 5. Network allocation, which is what actually derives a tenant-scoped IP
# block from the site IP block -- a VPC/Subnet cannot be created without one,
# no matter how healthy the site IP block itself looks.
TENANT_IPBLOCK_ID="$(api GET "allocation?siteId=${SITE_ID}" \
    | jq -r --arg ib "${IPBLOCK_ID}" '[.[].allocationConstraints[]? | select(.resourceType == "IPBlock" and .resourceTypeId == $ib)][0].derivedResourceId // empty')"
if [[ -z "${TENANT_IPBLOCK_ID}" ]]; then
    log "creating network allocation to derive a tenant IP block"
    TENANT_IPBLOCK_ID="$(api POST allocation "$(jq -n --arg tenant "${TENANT_ID}" --arg site "${SITE_ID}" --arg ib "${IPBLOCK_ID}" '{
        name: "capnico-local-dev-network", description: "Seeded by hack/local-nico-seed.sh",
        tenantId: $tenant, siteId: $site,
        allocationConstraints: [{resourceType: "IPBlock", resourceTypeId: $ib, constraintType: "Reserved", constraintValue: 24}],
    }')" | jq -r '.allocationConstraints[0].derivedResourceId')"
fi

# 6. VPC + Subnet. The local devspace site never has Native Networking (FNN)
# enabled -- so this always uses ETHERNET_VIRTUALIZER + Subnet, never FNN +
# VpcPrefix. Getting that wrong isn't a style choice: an FNN VPC on a site
# that doesn't support it has no backing FNN config, and a Machine assigned to
# it gets stuck in WaitingForNetworkConfig forever with no API-level way to
# recover the Machine or the Instance. Reuse only a VPC of this type -- a
# leftover VPC of the wrong type must not be picked up by mere presence.
VPC_ID="$(api GET "vpc?siteId=${SITE_ID}" | jq -r '[.[] | select(.networkVirtualizationType == "ETHERNET_VIRTUALIZER")][0].id // empty')"
if [[ -z "${VPC_ID}" ]]; then
    log "creating VPC"
    VPC_ID="$(api POST vpc "$(jq -n --arg site "${SITE_ID}" '{
        name: "capnico-local-dev", description: "Seeded by hack/local-nico-seed.sh",
        siteId: $site, networkVirtualizationType: "ETHERNET_VIRTUALIZER",
    }')" | jq -r '.id')"
fi

NETWORK_METHOD="subnetID"
NETWORK_ID="$(api GET "subnet?vpcId=${VPC_ID}" | jq -r '.[0].id // empty')"
if [[ -z "${NETWORK_ID}" ]]; then
    log "creating subnet"
    NETWORK_ID="$(api POST subnet "$(jq -n --arg vpc "${VPC_ID}" --arg ib "${TENANT_IPBLOCK_ID}" '{
        name: "capnico-local-dev", vpcId: $vpc, ipv4BlockId: $ib, prefixLength: 28,
    }')" | jq -r '.id')"
fi

cat <<EOF
export NICO_SITE_ID=${SITE_ID}
export NICO_VPC_ID=${VPC_ID}
export NICO_CONTROL_PLANE_INSTANCE_TYPE_ID=${INSTANCE_TYPE_ID}
export NICO_WORKER_INSTANCE_TYPE_ID=${INSTANCE_TYPE_ID}
export NICO_NETWORK_METHOD=${NETWORK_METHOD}
export NICO_NETWORK_ID=${NETWORK_ID}
EOF
