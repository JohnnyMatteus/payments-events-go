#!/bin/sh
# Bootstrap do LocalStack: cria o bucket em que o PagaFácil Sandbox deposita
# os extratos de liquidação diários (settlements/AAAA-MM-DD.csv) e a fila SQS
# que recebe a notificação de cada extrato novo (dispara a conciliação).
set -eu
BUCKET=pagafacil-statements
QUEUE=pagafacil-statements-events
DLQ=pagafacil-statements-events-dlq
ACCOUNT=000000000000
REGION="${AWS_DEFAULT_REGION:-us-east-1}"

awslocal s3api create-bucket --bucket "$BUCKET" >/dev/null 2>&1 || true
awslocal s3api put-bucket-versioning --bucket "$BUCKET" --versioning-configuration Status=Enabled

# DLQ: depois de 5 entregas sem Ack a notificação sai da fila principal.
awslocal sqs create-queue --queue-name "$DLQ" >/dev/null
DLQ_ARN="arn:aws:sqs:$REGION:$ACCOUNT:$DLQ"
QUEUE_ARN="arn:aws:sqs:$REGION:$ACCOUNT:$QUEUE"
awslocal sqs create-queue --queue-name "$QUEUE" --attributes "{
  \"VisibilityTimeout\": \"60\",
  \"RedrivePolicy\": \"{\\\"deadLetterTargetArn\\\":\\\"$DLQ_ARN\\\",\\\"maxReceiveCount\\\":\\\"5\\\"}\"
}" >/dev/null

awslocal s3api put-bucket-notification-configuration --bucket "$BUCKET" --notification-configuration "{
  \"QueueConfigurations\": [{
    \"QueueArn\": \"$QUEUE_ARN\",
    \"Events\": [\"s3:ObjectCreated:*\"],
    \"Filter\": {\"Key\": {\"FilterRules\": [{\"Name\": \"prefix\", \"Value\": \"settlements/\"}]}}
  }]
}"
# Arquivo das exportações mensais do ledger (job de retenção, ADR 0015).
ARCHIVE_BUCKET=pagafacil-ledger-archive
awslocal s3api create-bucket --bucket "$ARCHIVE_BUCKET" >/dev/null 2>&1 || true
awslocal s3api put-bucket-versioning --bucket "$ARCHIVE_BUCKET" --versioning-configuration Status=Enabled

echo "bucket s3://$BUCKET, arquivo s3://$ARCHIVE_BUCKET e fila $QUEUE prontos"
