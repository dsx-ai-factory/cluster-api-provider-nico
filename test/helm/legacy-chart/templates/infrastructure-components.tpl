{{/*
SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
SPDX-License-Identifier: Apache-2.0
*/}}
{{- $components := .Files.Get "files/infrastructure-components.yaml" -}}
{{- if not $components -}}
{{- fail "files/infrastructure-components.yaml is required" -}}
{{- end -}}
{{- if .Values.imagePullSecrets }}
{{- $serviceAccountLine := "      serviceAccountName: controller-manager\n" -}}
{{- $imagePullSecretsBlock := printf "%s      imagePullSecrets:\n%s\n" $serviceAccountLine (toYaml .Values.imagePullSecrets | indent 8) -}}
{{- $components = replace $serviceAccountLine $imagePullSecretsBlock $components -}}
{{- end }}
{{- $components }}
