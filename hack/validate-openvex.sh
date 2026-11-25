#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
file="${1:-${root}/.openvex.json}"

[[ -s "${file}" ]] || { echo "OpenVEX document is missing or empty: ${file}" >&2; exit 1; }
jq --exit-status --from-file "${root}/hack/validate-openvex.jq" "${file}" >/dev/null
echo "OpenVEX validation passed: ${file}"
