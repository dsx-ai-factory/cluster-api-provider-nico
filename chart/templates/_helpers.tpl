{{/*
Expand the name of the chart.
*/}}
{{- define "cluster-api-provider-nico.name" -}}
{{- default "cluster-api-provider-nico" .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "cluster-api-provider-nico.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Namespace for generated references.
Always uses the Helm release namespace.
*/}}
{{- define "cluster-api-provider-nico.namespaceName" -}}
{{- .Release.Namespace }}
{{- end }}

{{/*
Resource name with proper truncation for Kubernetes 63-character limit.
Takes a dict with:
  - .suffix: Resource name suffix (e.g., "metrics", "webhook")
  - .context: Template context (root context with .Values, .Release, etc.)
Dynamically calculates safe truncation to ensure total name length <= 63 chars.
*/}}
{{- define "cluster-api-provider-nico.resourceName" -}}
{{- $fullname := include "cluster-api-provider-nico.fullname" .context }}
{{- $suffix := .suffix }}
{{- $legacyNames := dict
  "allow-metrics-traffic" "allow-metrics-traffic"
  "controller-manager" "controller-manager"
  "controller-manager-metrics-monitor" "controller-manager-metrics-monitor"
  "controller-manager-metrics-service" "controller-manager-metrics-service"
  "leader-election-rolebinding" "cluster-api-provider-nico-leader-election-rolebinding"
  "manager-role" "manager-role"
  "manager-rolebinding" "cluster-api-provider-nico-manager-rolebinding"
}}
{{- if hasKey $legacyNames $suffix }}
{{- get $legacyNames $suffix }}
{{- else }}
{{- $maxLen := sub 62 (len $suffix) | int }}
{{- if gt (len $fullname) $maxLen }}
{{- printf "%s-%s" (trunc $maxLen $fullname | trimSuffix "-") $suffix | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" $fullname $suffix | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
ServiceAccount name to use.
If serviceAccount.enabled is false and serviceAccount.name is set, use that name.
Otherwise, use the standard resourceName helper with "controller-manager" suffix.
*/}}
{{- define "cluster-api-provider-nico.serviceAccountName" -}}
{{- if and (hasKey .Values.serviceAccount "enabled") (eq .Values.serviceAccount.enabled false) .Values.serviceAccount.name }}
{{- .Values.serviceAccount.name }}
{{- else }}
{{- print "controller-manager" }}
{{- end }}
{{- end }}
