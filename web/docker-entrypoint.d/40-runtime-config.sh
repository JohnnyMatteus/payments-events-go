#!/bin/sh
# Gera o /config.json do painel a partir do ambiente, sem rebuild da imagem.
# Sem OIDC_AUTHORITY e OIDC_CLIENT_ID o painel usa o token de dev.
# API_UPSTREAM (host:porta, padrão api:8080) diz para onde o nginx encaminha /api.
set -eu
out=/tmp/config.json
upstream="${API_UPSTREAM:-api:8080}"
case "$upstream" in
  *[!A-Za-z0-9.:_-]*) echo "API_UPSTREAM deve ser host:porta" >&2; exit 1 ;;
esac
printf 'upstream payments_api {\n  server %s;\n}\n' "$upstream" > /tmp/payments-upstream.conf
authority="${OIDC_AUTHORITY:-}"
client="${OIDC_CLIENT_ID:-}"
case "$authority$client" in
  *'"'* | *'\'*) echo "OIDC_AUTHORITY/OIDC_CLIENT_ID com aspas ou barra invertida" >&2; exit 1 ;;
esac
if [ -n "$authority" ] && [ -n "$client" ]; then
  printf '{ "oidc": { "authority": "%s", "clientId": "%s" } }\n' "$authority" "$client" > "$out"
else
  printf '{ "oidc": null }\n' > "$out"
fi
