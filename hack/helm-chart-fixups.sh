#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

# Apply the compatibility transformations that Kubebuilder's generic Helm
# plugin cannot infer: the legacy chart exposes imagePullSecrets at the top
# level, and the manager image honors manager.image.digest through the
# managerImage helper in _helpers.tpl. Keep the generated manager template
# otherwise identical to CAPLL.

set -euo pipefail

manager_template="${1:-chart/templates/manager/manager.yaml}"
temporary="${manager_template}.tmp"
trap 'rm -f "${temporary}"' EXIT

# replace NAME OLD NEW rewrites OLD to NEW in the manager template and fails
# when OLD is missing, so a Kubebuilder template change cannot drop a fixup.
replace() {
  local name="$1" old="$2" new="$3"
  OLD="${old}" NEW="${new}" perl -pe 's/\Q$ENV{OLD}\E/$ENV{NEW}/g' "${manager_template}" > "${temporary}"
  if cmp -s "${manager_template}" "${temporary}"; then
    echo "Helm manager ${name} hook was not found in ${manager_template}" >&2
    exit 1
  fi
  mv "${temporary}" "${manager_template}"
}

replace imagePullSecrets \
  '{{- with .Values.manager.imagePullSecrets }}' \
  '{{- with (coalesce .Values.manager.imagePullSecrets .Values.imagePullSecrets) }}'

replace image \
  'image: "{{ .Values.manager.image.repository }}{{- if not (contains "@" .Values.manager.image.repository) }}:{{ .Values.manager.image.tag | default .Chart.AppVersion }}{{- end }}"' \
  'image: {{ include "cluster-api-provider-nico.managerImage" . | quote }}'
