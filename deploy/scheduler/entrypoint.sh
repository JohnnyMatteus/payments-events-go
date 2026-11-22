#!/bin/sh
# Agenda a conciliação do dia anterior com o crond do busybox.
# RECONCILE_CRON usa a sintaxe de cron (padrão: todo dia às 03:00 UTC).
set -eu
# O crond não repassa o ambiente aos jobs: grava as variáveis para o job carregar.
export -p > /etc/payments.env
echo "${RECONCILE_CRON:-0 3 * * *} . /etc/payments.env; /usr/local/bin/payments reconcile --daily --fail-on-divergence >/proc/1/fd/1 2>/proc/1/fd/2" > /etc/crontabs/root
echo "agenda: ${RECONCILE_CRON:-0 3 * * *} (UTC)"
exec crond -f -l 8
