#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

# Assert the controller image reference the chart renders for each
# manager.image combination, and that the chart rejects invalid ones. The
# Test Chart workflow only installs default values, so this guards the
# digest contract that NVKR-style packaging relies on.

set -euo pipefail

chart="${1:-chart}"
helm="${HELM:-helm}"
repo="registry.example.com/capnico/controller"
digest="sha256:$(printf 'a%.0s' {1..64})"
app_version="$(sed -n 's/^appVersion: *"\{0,1\}\([^"]*\)"\{0,1\} *$/\1/p' "${chart}/Chart.yaml")"
failures=0

fail() {
  echo "FAIL $1: $2" >&2
  failures=$((failures + 1))
}

# expect_image NAME WANT ARGS... renders the chart and compares its image.
expect_image() {
  local name="$1" want="$2" out got
  shift 2
  if ! out="$("${helm}" template capnico "${chart}" "$@" 2>&1)"; then
    fail "${name}" "render failed: ${out}"
    return
  fi
  got="$(sed -n 's/^ *image: "\(.*\)"$/\1/p' <<<"${out}")"
  if [[ "${got}" != "${want}" ]]; then
    fail "${name}" "image is '${got}', want '${want}'"
    return
  fi
  echo "ok   ${name}"
}

# expect_reject NAME MESSAGE ARGS... requires helm template to fail with
# MESSAGE and, unless schema validation is skipped, helm lint to fail too.
expect_reject() {
  local name="$1" want="$2" out
  shift 2
  if out="$("${helm}" template capnico "${chart}" "$@" 2>&1)"; then
    fail "${name}" "render succeeded, want an error containing '${want}'"
    return
  fi
  if [[ "${out}" != *"${want}"* ]]; then
    fail "${name}" "error does not contain '${want}': ${out}"
    return
  fi
  if [[ " $* " != *" --skip-schema-validation "* ]] &&
    "${helm}" lint "${chart}" "$@" >/dev/null 2>&1; then
    fail "${name}" "helm lint succeeded"
    return
  fi
  echo "ok   ${name}"
}

expect_image "digest with tag" "${repo}:v1.2.3@${digest}" \
  --set "manager.image.repository=${repo}" --set manager.image.tag=v1.2.3 --set "manager.image.digest=${digest}"
expect_image "digest with appVersion" "${repo}:${app_version}@${digest}" \
  --set "manager.image.repository=${repo}" --set "manager.image.digest=${digest}"
expect_image "@ in repository" "${repo}@${digest}" \
  --set "manager.image.repository=${repo}@${digest}"
expect_image "tag" "${repo}:v1.2.3" \
  --set "manager.image.repository=${repo}" --set manager.image.tag=v1.2.3
expect_image "appVersion default" "${repo}:${app_version}" \
  --set "manager.image.repository=${repo}"
expect_image "empty digest" "${repo}:${app_version}" \
  --set "manager.image.repository=${repo}" --set-string manager.image.digest=
expect_image "null digest" "${repo}:${app_version}" \
  --set "manager.image.repository=${repo}" --set manager.image.digest=null

expect_reject "malformed digest" "does not match pattern" \
  --set "manager.image.repository=${repo}" --set manager.image.digest=sha256:abc
expect_reject "uppercase digest" "does not match pattern" \
  --set "manager.image.repository=${repo}" --set "manager.image.digest=$(tr '[:lower:]' '[:upper:]' <<<"${digest}")"
expect_reject "boolean digest" "want string" \
  --set "manager.image.repository=${repo}" --set manager.image.digest=false
expect_reject "number digest" "want string" \
  --set "manager.image.repository=${repo}" --set manager.image.digest=0
expect_reject "digest with @ repository" "/manager/image/repository" \
  --set "manager.image.repository=${repo}@${digest}" --set "manager.image.digest=${digest}"
expect_reject "digest with @ repository, schema skipped" "cannot be combined" \
  --skip-schema-validation \
  --set "manager.image.repository=${repo}@${digest}" --set "manager.image.digest=${digest}"

if ((failures > 0)); then
  echo "${failures} chart image check(s) failed" >&2
  exit 1
fi
