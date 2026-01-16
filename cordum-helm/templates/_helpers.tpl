{{- define "cordum.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "cordum.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "cordum.labels" -}}
app.kubernetes.io/name: {{ include "cordum.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "cordum.selectorLabels" -}}
app.kubernetes.io/name: {{ include "cordum.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "cordum.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- if .Values.serviceAccount.name -}}
{{- .Values.serviceAccount.name -}}
{{- else -}}
{{- printf "%s" (include "cordum.fullname" .) -}}
{{- end -}}
{{- else -}}
{{- .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "cordum.natsUrl" -}}
{{- if .Values.nats.enabled -}}
{{- printf "nats://%s-nats:%d" (include "cordum.fullname" .) (int .Values.nats.service.port) -}}
{{- else -}}
{{- required "external.natsUrl is required when nats.enabled=false" .Values.external.natsUrl -}}
{{- end -}}
{{- end -}}

{{- define "cordum.redisUrl" -}}
{{- if .Values.redis.enabled -}}
{{- printf "redis://%s-redis:%d" (include "cordum.fullname" .) (int .Values.redis.service.port) -}}
{{- else -}}
{{- required "external.redisUrl is required when redis.enabled=false" .Values.external.redisUrl -}}
{{- end -}}
{{- end -}}
