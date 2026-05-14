{{- $components := .Files.Get "files/infrastructure-components.yaml" -}}
{{- if not $components -}}
{{- fail "files/infrastructure-components.yaml is required; run make helm-chart-manifests before rendering this chart" -}}
{{- end -}}
{{- if .Values.imagePullSecrets }}
{{- $serviceAccountLine := "      serviceAccountName: controller-manager\n" -}}
{{- $imagePullSecretsBlock := printf "%s      imagePullSecrets:\n%s\n" $serviceAccountLine (toYaml .Values.imagePullSecrets | indent 8) -}}
{{- $components = replace $serviceAccountLine $imagePullSecretsBlock $components -}}
{{- end }}
{{- $components }}
