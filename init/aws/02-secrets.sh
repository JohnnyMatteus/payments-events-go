#!/bin/sh
# Segredos de desenvolvimento no Secrets Manager do LocalStack (dev-only).
# O webhook guarda "atual,anterior": para rotacionar, grave "novo,atual" com
# `awslocal secretsmanager put-secret-value` e, depois que o provedor migrar,
# só "novo".
set -eu

put() {
  if awslocal secretsmanager describe-secret --secret-id "$1" >/dev/null 2>&1; then
    awslocal secretsmanager put-secret-value --secret-id "$1" --secret-string "$2" >/dev/null
  else
    awslocal secretsmanager create-secret --name "$1" --secret-string "$2" >/dev/null
  fi
}

put payments/webhook "whsec_dev_only_change_me"
put payments/db '{"password":"dev-only-change-me"}'
