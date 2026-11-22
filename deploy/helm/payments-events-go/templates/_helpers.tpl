{{- define "payments.fullname" -}}
{{- .Release.Name | trunc 50 | trimSuffix "-" -}}
{{- end -}}

{{- define "payments.image" -}}
{{- if .Values.image.digest -}}
{{ .Values.image.repository }}@{{ .Values.image.digest }}
{{- else -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}
{{- end -}}
{{- end -}}

{{- define "payments.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{ .Values.serviceAccount.name | default (include "payments.fullname" .) }}
{{- else -}}
{{ .Values.serviceAccount.name | default "default" }}
{{- end -}}
{{- end -}}

{{- define "payments.labels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Values.image.tag | default .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/* Ambiente comum a todos os processos e jobs. Segredos vêm só do Secret ou do cofre. */}}
{{- define "payments.env" -}}
envFrom:
  - secretRef:
      name: {{ .Values.existingSecret }}
env:
  - name: APP_ENV
    value: {{ .Values.config.appEnv | quote }}
  - name: LOG_LEVEL
    value: {{ .Values.config.logLevel | quote }}
  - name: AWS_REGION
    value: {{ .Values.config.awsRegion | quote }}
  - name: STATEMENTS_BUCKET
    value: {{ .Values.config.statementsBucket | quote }}
{{- if .Values.config.s3Endpoint }}
  - name: S3_ENDPOINT
    value: {{ .Values.config.s3Endpoint | quote }}
  - name: S3_USE_PATH_STYLE
    value: "true"
{{- end }}
{{- if .Values.config.statementsQueueUrl }}
  - name: STATEMENTS_QUEUE_URL
    value: {{ .Values.config.statementsQueueUrl | quote }}
{{- end }}
{{- if .Values.config.otlpEndpoint }}
  - name: OTEL_EXPORTER_OTLP_ENDPOINT
    value: {{ .Values.config.otlpEndpoint | quote }}
{{- end }}
{{- if .Values.oidc.issuerUrl }}
  - name: OIDC_ISSUER_URL
    value: {{ .Values.oidc.issuerUrl | quote }}
  - name: OIDC_AUDIENCE
    value: {{ .Values.oidc.audience | quote }}
  - name: OIDC_ROLE_READER
    value: {{ .Values.oidc.roleReader | quote }}
  - name: OIDC_ROLE_OPERATOR
    value: {{ .Values.oidc.roleOperator | quote }}
{{- end }}
  - name: SECRETS_PROVIDER
    value: {{ .Values.secrets.provider | quote }}
{{- if ne .Values.secrets.provider "env" }}
  - name: SECRETS_REFRESH_INTERVAL
    value: {{ .Values.secrets.refreshInterval | quote }}
  - name: WEBHOOK_SECRETS_REF
    value: {{ required "secrets.webhookRef é obrigatório com secrets.provider vault ou awssm" .Values.secrets.webhookRef | quote }}
{{- if .Values.secrets.databasePasswordRef }}
  - name: DATABASE_PASSWORD_REF
    value: {{ .Values.secrets.databasePasswordRef | quote }}
{{- end }}
{{- if .Values.secrets.endpoint }}
  - name: SECRETS_ENDPOINT
    value: {{ .Values.secrets.endpoint | quote }}
{{- end }}
{{- if eq .Values.secrets.provider "vault" }}
  - name: VAULT_ADDR
    value: {{ required "secrets.vault.addr é obrigatório com secrets.provider vault" .Values.secrets.vault.addr | quote }}
  - name: VAULT_KV_MOUNT
    value: {{ .Values.secrets.vault.kvMount | quote }}
  - name: VAULT_K8S_ROLE
    value: {{ required "secrets.vault.k8sRole é obrigatório em produção (o token estático é só para dev)" .Values.secrets.vault.k8sRole | quote }}
{{- end }}
{{- end }}
{{- range $k, $v := .Values.config.extraEnv }}
  - name: {{ $k }}
    value: {{ $v | quote }}
{{- end }}
{{- end -}}

{{- define "payments.podSecurityContext" -}}
securityContext:
  runAsNonRoot: true
  runAsUser: 65532
  runAsGroup: 65532
  seccompProfile:
    type: RuntimeDefault
{{- end -}}

{{- define "payments.securityContext" -}}
securityContext:
  allowPrivilegeEscalation: false
  readOnlyRootFilesystem: true
  capabilities:
    drop: ["ALL"]
{{- end -}}

{{/* Só o login do Vault por Kubernetes precisa do token da service account. */}}
{{- define "payments.automount" -}}
{{- if and (eq .Values.secrets.provider "vault") .Values.secrets.vault.k8sRole -}}true{{- else -}}{{ .Values.serviceAccount.automount }}{{- end -}}
{{- end -}}

{{/* Pod comum dos Deployments e dos jobs. Recebe o contexto raiz. */}}
{{- define "payments.podCommon" -}}
serviceAccountName: {{ include "payments.serviceAccountName" . }}
automountServiceAccountToken: {{ include "payments.automount" . }}
{{- with .Values.imagePullSecrets }}
imagePullSecrets:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{ include "payments.podSecurityContext" . }}
{{- end -}}

{{/*
Deployment de um processo. Recebe dict: root, name, cfg, port, hpa, grpc.
O comando do binário é o próprio nome do processo (api, relay, consumer, watch).
*/}}
{{- define "payments.workload" -}}
{{- $root := .root -}}
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ include "payments.fullname" $root }}-{{ .name }}
  labels:
    {{- include "payments.labels" $root | nindent 4 }}
    app.kubernetes.io/component: {{ .name }}
spec:
  {{- if not .hpa }}
  replicas: {{ .cfg.replicas }}
  {{- end }}
  selector:
    matchLabels:
      app.kubernetes.io/instance: {{ $root.Release.Name }}
      app.kubernetes.io/component: {{ .name }}
  template:
    metadata:
      labels:
        {{- include "payments.labels" $root | nindent 8 }}
        app.kubernetes.io/component: {{ .name }}
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: {{ .port | quote }}
    spec:
      {{- include "payments.podCommon" $root | nindent 6 }}
      containers:
        - name: {{ .name }}
          image: {{ include "payments.image" $root }}
          imagePullPolicy: {{ $root.Values.image.pullPolicy }}
          args: [{{ .command | quote }}]
          ports:
            - name: http
              containerPort: {{ .port }}
            {{- if .grpc }}
            - name: grpc
              containerPort: {{ $root.Values.grpc.port }}
            {{- end }}
          {{- include "payments.env" $root | nindent 10 }}
          {{- if .grpc }}
            - name: GRPC_ADDR
              value: {{ printf ":%d" (int $root.Values.grpc.port) | quote }}
            {{- if $root.Values.grpc.tlsSecret }}
            - name: GRPC_TLS_CERT_FILE
              value: /etc/payments/grpc-tls/tls.crt
            - name: GRPC_TLS_KEY_FILE
              value: /etc/payments/grpc-tls/tls.key
            {{- end }}
          {{- end }}
          startupProbe:
            httpGet: {path: /healthz, port: http}
            periodSeconds: 2
            failureThreshold: 30
          livenessProbe:
            httpGet: {path: /healthz, port: http}
            periodSeconds: 10
            failureThreshold: 3
          readinessProbe:
            httpGet: {path: /readyz, port: http}
            periodSeconds: 5
            failureThreshold: 3
          resources:
            {{- toYaml .cfg.resources | nindent 12 }}
          {{- include "payments.securityContext" $root | nindent 10 }}
          {{- if and .grpc $root.Values.grpc.tlsSecret }}
          volumeMounts:
            - name: grpc-tls
              mountPath: /etc/payments/grpc-tls
              readOnly: true
          {{- end }}
      {{- if and .grpc $root.Values.grpc.tlsSecret }}
      volumes:
        - name: grpc-tls
          secret:
            secretName: {{ $root.Values.grpc.tlsSecret }}
      {{- end }}
{{- end -}}
