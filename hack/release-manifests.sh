#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0
#
# Generate the clusterctl provider release artifacts from the Kustomize source
# of truth. The script updates the manager image in a temporary copy of
# config/manager, renders config/default, and writes the files expected by
# clusterctl provider repositories into RELEASE_DIR.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"

RELEASE_DIR="${RELEASE_DIR:-${REPO_ROOT}/out}"
KUSTOMIZE="${KUSTOMIZE:-go run sigs.k8s.io/kustomize/kustomize/v5@latest}"
CONTROLLER_IMG="${CONTROLLER_IMG:-controller:latest}"

WORK_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "${WORK_DIR}"
}
trap cleanup EXIT

mkdir -p "${RELEASE_DIR}"
cp -R "${REPO_ROOT}/config" "${WORK_DIR}/config"

(cd "${WORK_DIR}/config/manager" && ${KUSTOMIZE} edit set image "controller=${CONTROLLER_IMG}")

(cd "${WORK_DIR}" && ${KUSTOMIZE} build config/default) > "${RELEASE_DIR}/infrastructure-components.yaml"
grep -v '^# yaml-language-server:' "${REPO_ROOT}/metadata.yaml" > "${RELEASE_DIR}/metadata.yaml"

if [ ! -s "${RELEASE_DIR}/infrastructure-components.yaml" ]; then
  echo "Generated infrastructure-components.yaml is empty" >&2
  exit 1
fi

echo "Generated release manifests in ${RELEASE_DIR}"
