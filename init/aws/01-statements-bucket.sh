#!/bin/sh
# Bootstrap do LocalStack: cria o bucket em que o PagaFácil Sandbox deposita
# os extratos de liquidação diários (settlements/AAAA-MM-DD.csv).
set -eu
BUCKET=pagafacil-statements
awslocal s3api create-bucket --bucket "$BUCKET" >/dev/null 2>&1 || true
awslocal s3api put-bucket-versioning --bucket "$BUCKET" --versioning-configuration Status=Enabled
echo "bucket s3://$BUCKET pronto"
