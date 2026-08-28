#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

# Apply the one compatibility transformation that Kubebuilder's generic Helm
# plugin cannot infer: the legacy chart exposes imagePullSecrets at the top
# level. Keep the generated manager template otherwise identical to CAPLL.

set -euo pipefail

manager_template="${1:-chart/templates/manager/manager.yaml}"
temporary="${manager_template}.tmp"
trap 'rm -f "${temporary}"' EXIT

old='{{- with .Values.manager.imagePullSecrets }}'
new='{{- with (coalesce .Values.manager.imagePullSecrets .Values.imagePullSecrets) }}'

sed "s|${old}|${new}|" "${manager_template}" > "${temporary}"
if cmp -s "${manager_template}" "${temporary}"; then
  echo "Helm manager imagePullSecrets hook was not found in ${manager_template}" >&2
  exit 1
fi
mv "${temporary}" "${manager_template}"
