{{/*
Standard labels
*/}}
{{- define "opnsense-lb-controller.labels" -}}
app.kubernetes.io/name: {{ include "opnsense-lb-controller.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ include "opnsense-lb-controller.chart" . }}
{{- end -}}

{{- define "opnsense-lb-controller.name" -}}
{{ .Chart.Name }}
{{- end -}}

{{- define "opnsense-lb-controller.chart" -}}
{{ .Chart.Name }}-{{ .Chart.Version }}
{{- end -}}
