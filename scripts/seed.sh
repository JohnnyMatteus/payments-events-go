#!/usr/bin/env bash
# Popula o ambiente local com um dia de operação fictício e o extrato do
# provedor no S3 (LocalStack), preparado para gerar uma divergência de cada tipo.
set -euo pipefail
cd "$(dirname "$0")/.."

send() { CREATED_AT="$6" scripts/send-webhook.sh "$1" "$2" "$3" "$4" "$5"; }

echo "==> webhooks do dia 2026-11-20"
send evt_demo_001 payment.captured txn_demo_001 pay_demo_001 15000 2026-11-20T12:00:00Z
send evt_demo_002 payment.refunded txn_demo_002 pay_demo_001 2500  2026-11-20T13:00:00Z  # extrato diz 30.00
send evt_demo_003 payment.captured txn_demo_003 pay_demo_002 8000  2026-11-20T14:00:00Z  # ausente no extrato
send evt_demo_001 payment.captured txn_demo_001 pay_demo_001 15000 2026-11-20T12:00:00Z  # reentrega: duplicate
send evt_demo_900 payment.dispute_opened txn_demo_900 pay_demo_001 1 2026-11-20T16:00:00Z  # sem efeito: ignored

echo "==> extrato do provedor no S3"
docker compose --profile core cp testdata/statements/2026-11-20.csv localstack:/tmp/2026-11-20.csv
docker compose --profile core exec -T localstack \
  awslocal s3 cp /tmp/2026-11-20.csv s3://pagafacil-statements/settlements/2026-11-20.csv

echo "==> pronto. Rode: make reconcile"
