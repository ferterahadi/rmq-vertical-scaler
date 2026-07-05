{{- define "rmq-vertical-scaler.fullname" -}}
{{- default .Chart.Name .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "rmq-vertical-scaler.labels" -}}
app.kubernetes.io/name: {{ include "rmq-vertical-scaler.fullname" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "rmq-vertical-scaler.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (printf "%s-sa" (include "rmq-vertical-scaler.fullname" .)) .Values.serviceAccount.name -}}
{{- else -}}
{{- required "serviceAccount.name is required when serviceAccount.create=false" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "rmq-vertical-scaler.secretName" -}}
{{- default (printf "%s-default-user" .Values.serviceName) .Values.auth.existingSecret -}}
{{- end -}}

{{- define "rmq-vertical-scaler.rmqHost" -}}
{{- default (printf "%s.%s.svc.cluster.local" .Values.serviceName .Release.Namespace) .Values.rmq.host -}}
{{- end -}}

{{- define "rmq-vertical-scaler.image" -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) -}}
{{- end -}}
