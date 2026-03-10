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
{{- if .Values.global.tls.enabled -}}
{{- printf "tls://%s-nats:%d" (include "cordum.fullname" .) (int .Values.nats.service.port) -}}
{{- else -}}
{{- printf "nats://%s-nats:%d" (include "cordum.fullname" .) (int .Values.nats.service.port) -}}
{{- end -}}
{{- else -}}
{{- required "external.natsUrl is required when nats.enabled=false" .Values.external.natsUrl -}}
{{- end -}}
{{- end -}}

{{- define "cordum.redisUrl" -}}
{{- if .Values.redis.enabled -}}
{{- $scheme := ternary "rediss" "redis" .Values.global.tls.enabled -}}
{{- if .Values.redis.auth.enabled -}}
{{- printf "%s://:$(REDIS_PASSWORD)@%s-redis:%d" $scheme (include "cordum.fullname" .) (int .Values.redis.service.port) -}}
{{- else -}}
{{- printf "%s://%s-redis:%d" $scheme (include "cordum.fullname" .) (int .Values.redis.service.port) -}}
{{- end -}}
{{- else -}}
{{- required "external.redisUrl is required when redis.enabled=false" .Values.external.redisUrl -}}
{{- end -}}
{{- end -}}

{{- define "cordum.redisSecretName" -}}
{{- if .Values.redis.auth.existingSecret -}}
{{- .Values.redis.auth.existingSecret -}}
{{- else -}}
{{- printf "%s-secrets" (include "cordum.fullname" .) -}}
{{- end -}}
{{- end -}}

{{- define "cordum.redisSecretKey" -}}
{{- if .Values.redis.auth.existingSecret -}}
{{- .Values.redis.auth.existingSecretKey -}}
{{- else -}}
redisPassword
{{- end -}}
{{- end -}}

{{/*
Production safety validations — hard-fail on dangerous combinations.
TLS is mandatory in production mode; network policies and persistence
are warned about in NOTES.txt but not blocked (legitimate use cases exist).
*/}}
{{- define "cordum.validateProductionConfig" -}}
{{- if and .Values.global.production (not .Values.global.tls.enabled) -}}
{{- fail "FATAL: TLS must be enabled in production mode (global.production=true requires global.tls.enabled=true)" -}}
{{- end -}}
{{- if and .Values.global.production .Values.redis.auth.enabled (not .Values.redis.auth.password) (not .Values.redis.auth.existingSecret) -}}
{{- fail "FATAL: Redis auth is enabled in production mode but no password or existingSecret is configured" -}}
{{- end -}}
{{- end -}}

{{- define "cordum.safetyKernelAddr" -}}
{{- if .Values.safetyKernel.enabled -}}
{{- printf "%s-safety-kernel:%d" (include "cordum.fullname" .) (int .Values.safetyKernel.service.port) -}}
{{- else -}}
{{- required "external.safetyKernelAddr is required when safetyKernel.enabled=false" .Values.external.safetyKernelAddr -}}
{{- end -}}
{{- end -}}
