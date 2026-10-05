{{- define "innkeeper.labels" -}}
app.kubernetes.io/name: innkeeper
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}
