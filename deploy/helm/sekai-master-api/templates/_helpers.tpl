{{- define "sekai-master-api.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "sekai-master-api.fullname" -}}
{{- if .Values.fullnameOverride }}{{ .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}{{ else }}{{ printf "%s-%s" .Release.Name (include "sekai-master-api.name" .) | trunc 63 | trimSuffix "-" }}{{ end }}
{{- end }}

{{- define "sekai-master-api.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | quote }}
app.kubernetes.io/name: {{ include "sekai-master-api.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "sekai-master-api.selectorLabels" -}}
app.kubernetes.io/name: {{ include "sekai-master-api.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end }}

{{- define "sekai-master-api.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}{{ default (include "sekai-master-api.fullname" .) .Values.serviceAccount.name }}{{ else }}{{ default "default" .Values.serviceAccount.name }}{{ end }}
{{- end }}

{{- define "sekai-master-api.env" -}}
- name: APP_PORT
  value: "8080"
{{- range $name, $value := .Values.common.env }}
{{- if eq $name "APP_PORT" }}{{ fail "common.env.APP_PORT is reserved; the container port is fixed at 8080" }}{{ end }}
- name: {{ $name }}
  value: {{ $value | quote }}
{{- end }}
{{- range $name, $value := .role.env }}
{{- if eq $name "APP_PORT" }}{{ fail "role env APP_PORT is reserved; the container port is fixed at 8080" }}{{ end }}
- name: {{ $name }}
  value: {{ $value | quote }}
{{- end }}
{{- range .Values.common.extraEnv }}
{{- if eq .name "APP_PORT" }}{{ fail "common.extraEnv APP_PORT is reserved; the container port is fixed at 8080" }}{{ end }}
{{ toYaml (list .) }}
{{- end }}
{{- range .role.extraEnv }}
{{- if eq .name "APP_PORT" }}{{ fail "role extraEnv APP_PORT is reserved; the container port is fixed at 8080" }}{{ end }}
{{ toYaml (list .) }}
{{- end }}
{{- end }}

{{- define "sekai-master-api.envFrom" -}}
{{- range .Values.common.envFrom.configMaps }}
- configMapRef:
    name: {{ . | quote }}
{{- end }}
{{- range .Values.common.envFrom.secrets }}
- secretRef:
    name: {{ . | quote }}
{{- end }}
{{- range .role.envFrom.configMaps }}
- configMapRef:
    name: {{ . | quote }}
{{- end }}
{{- range .role.envFrom.secrets }}
- secretRef:
    name: {{ . | quote }}
{{- end }}
{{- end }}

{{- define "sekai-master-api.probes" -}}
{{- if .probes.startup.enabled }}
startupProbe:
  httpGet: { path: /startupz, port: http }
  failureThreshold: {{ .probes.startup.failureThreshold }}
  periodSeconds: {{ .probes.startup.periodSeconds }}
{{- end }}
{{- if .probes.liveness.enabled }}
livenessProbe:
  httpGet: { path: /livez, port: http }
  initialDelaySeconds: {{ .probes.liveness.initialDelaySeconds }}
  periodSeconds: {{ .probes.liveness.periodSeconds }}
{{- end }}
{{- if .probes.readiness.enabled }}
readinessProbe:
  httpGet: { path: /readyz, port: http }
  initialDelaySeconds: {{ .probes.readiness.initialDelaySeconds }}
  periodSeconds: {{ .probes.readiness.periodSeconds }}
{{- end }}
{{- end }}
