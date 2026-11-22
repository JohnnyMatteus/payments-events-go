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

echo "==> segundo lojista"
MERCHANT_ID=loja_demo_002 send evt_demo_005 payment.captured txn_demo_005 pay_demo_004 6000 2026-11-20T16:30:00Z

echo "==> mensagem venenosa no outbox (vai para a DLQ e aparece no painel)"
docker compose --profile core exec -T postgres psql -U payments -d payments -q -c \
  "INSERT INTO outbox_messages (message_id, routing_key, payload)
   VALUES ('evt_demo_poison', 'ledger.entry_recorded', '{\"schema_version\": 9}')
   ON CONFLICT (message_id) DO NOTHING"

echo "==> extrato do provedor no S3"
docker compose --profile core cp testdata/statements/2026-11-20.csv localstack:/tmp/2026-11-20.csv
docker compose --profile core exec -T localstack \
  awslocal s3 cp /tmp/2026-11-20.csv s3://pagafacil-statements/settlements/2026-11-20.csv

echo "==> pronto. Abra o painel (WEB_PORT) com o token dev-token-ana ou dev-token-bruno, ou rode: make reconcile"
