{{/*
Common helpers for rimsky chart.
*/}}

{{- define "rimsky.name" -}}
rimsky
{{- end -}}

{{- define "rimsky.fullname" -}}
{{ .Release.Name }}-rimsky
{{- end -}}

{{- define "rimsky.labels" -}}
app.kubernetes.io/name: {{ include "rimsky.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end -}}

{{- define "rimsky.selectorLabels" -}}
app.kubernetes.io/name: {{ include "rimsky.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "rimsky.serviceAccountName" -}}
{{ include "rimsky.fullname" . }}-sa
{{- end -}}
