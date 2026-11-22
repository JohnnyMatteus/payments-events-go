#!/bin/sh
# Gera o /config.json do painel a partir do ambiente, sem rebuild da imagem.
# Sem OIDC_AUTHORITY e OIDC_CLIENT_ID o painel usa o token de dev.
set -eu
out=/tmp/config.json
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
