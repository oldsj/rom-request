{{- define "app.fullname" -}}
{{- default .Release.Name .Values.fullnameOverride | trunc 50 | trimSuffix "-" -}}
{{- end -}}

{{- define "app.selectorLabels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name | quote }}
{{- end -}}

{{- define "app.labels" -}}
{{ include "app.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service | quote }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | quote }}
{{- end -}}

{{- define "app.dbCluster" -}}
{{- printf "%s-db" (include "app.fullname" .) -}}
{{- end -}}

{{- define "app.dbSecret" -}}
{{- if .Values.database.cnpg.enabled -}}
{{- printf "%s-app" (include "app.dbCluster" .) -}}
{{- else -}}
{{- required "database.existingSecret is required when database.cnpg.enabled is false" .Values.database.existingSecret -}}
{{- end -}}
{{- end -}}

{{- define "app.dbCASecret" -}}
{{- if .Values.database.cnpg.enabled -}}
{{- printf "%s-ca" (include "app.dbCluster" .) -}}
{{- else -}}
{{- .Values.database.caSecret -}}
{{- end -}}
{{- end -}}

{{- define "app.secretName" -}}
{{- .Values.secrets.existingSecret -}}
{{- end -}}
