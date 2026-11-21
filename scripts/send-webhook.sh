#!/usr/bin/env bash
# Envia um webhook assinado como o PagaFácil Sandbox faria.
# uso: scripts/send-webhook.sh <event_id> <tipo> <transaction_id> <payment_id> <centavos>
set -euo pipefail

API_URL="${API_URL:-http://localhost:${API_PORT:-8085}}"
SECRET="${WEBHOOK_SECRET:-whsec_dev_only_change_me}"
CREATED_AT="${CREATED_AT:-2026-11-20T12:00:00Z}"

if [ "$#" -ne 5 ]; then
  echo "uso: $0 <event_id> <tipo> <transaction_id> <payment_id> <centavos>" >&2
  exit 2
fi

body=$(printf '{"id":"%s","type":"%s","created_at":"%s","data":{"transaction_id":"%s","payment_id":"%s","amount":%s,"currency":"BRL"}}' \
  "$1" "$2" "$CREATED_AT" "$3" "$4" "$5")
ts=$(date +%s)
sig=$(printf '%s.%s' "$ts" "$body" | openssl dgst -sha256 -hmac "$SECRET" -hex | awk '{print $NF}')

curl -sS -X POST "$API_URL/webhooks/pagafacil" \
  -H "Content-Type: application/json" \
  -H "PagaFacil-Signature: t=${ts},v1=${sig}" \
  -d "$body"
echo
