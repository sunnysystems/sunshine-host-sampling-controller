{{- define "hsc.name" -}}
sunshine-host-sampling-controller
{{- end -}}

{{- define "hsc.fullname" -}}
{{- printf "%s-%s" .Release.Name (include "hsc.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "hsc.labels" -}}
app.kubernetes.io/name: {{ include "hsc.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
{{- end -}}

{{- define "hsc.selectorLabels" -}}
app.kubernetes.io/name: {{ include "hsc.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}
