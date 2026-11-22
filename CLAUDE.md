# CLAUDE.md

Guia para sessões de agente neste repositório.

## Visão

Processador de webhooks do provedor fictício **PagaFácil Sandbox**: ledger idempotente, transactional outbox para RabbitMQ, consumidor idempotente e conciliação do extrato (S3) com o ledger. O núcleo do projeto é **idempotência** e **conciliação**; mudanças nessas áreas exigem teste que prove a garantia (ver `docs/roteiros/`).

## Regras

- Domínio 100% fictício. Nada de nomes reais de empresas, clientes ou pessoas; e-mails só `@example.com`.
- Nunca commitar segredos. Valores de desenvolvimento ficam no `docker-compose.yml`/`.env.example` marcados como `dev-only`.
- Docs em português do Brasil; identificadores de código em inglês; comentários em português.
- Dinheiro sempre em centavos (`int64`); nunca `float64`.
- Ledger é append-only: nada de `UPDATE`/`DELETE` em `ledger_entries`.
- Migrations são novas e aditivas (`internal/adapter/postgres/migrations/NNNN_*.sql`); nunca editar uma já aplicada.
- `internal/domain` não importa nada de infraestrutura; `internal/app` só conhece portas. Adaptadores dependem de `app`, nunca o contrário.
- Testes de integração em `*_integration_test.go` com `//go:build integration`.
- Commits em Conventional Commits, em português, com `git commit -F <arquivo>`.
- Decisão de arquitetura nova ou revista: ADR em `docs/adr/` (Contexto, Decisão, Consequências, Alternativas) e atualizar a tabela do README.

## Comandos

```bash
make test               # unitários (-race, cobertura)
make test-integration   # testcontainers: Postgres, RabbitMQ, LocalStack
make lint               # golangci-lint v2 (config em .golangci.yml)
make fmt                # gofmt + goimports
make build              # bin/payments
make up / make seed / make reconcile / make down / make clean
make up-observability   # + Jaeger e Prometheus
```

CI (`.github/workflows/ci.yml`): lint, unitários, integração, build + govulncheck + docker build. Rode os mesmos comandos localmente antes de commitar.

## Estrutura

```
cmd/payments/                 main, subcomandos, e2e (integration)
internal/domain/              Event, LedgerEntry, Reconcile, money
internal/app/                 IngestService, Relay, BalanceProjector, ReconcileService, ports.go
internal/adapter/httpapi/     webhook, assinatura HMAC, rotas
internal/adapter/postgres/    recorder, outbox, ledger reader, projeção, migrations
internal/adapter/rabbitmq/    topologia, publisher (confirms), consumer (retry/DLQ)
internal/adapter/objectstore/ abertura de s3://... e arquivos locais
internal/adapter/sqsqueue/      fila SQS com as notificações do S3 (dispara a conciliação)
internal/adapter/csvstatement/ parser do extrato
internal/adapter/reportout/   relatório texto/JSON
internal/platform/            config (env) e observabilidade
internal/testinfra/           testcontainers (integration)
init/aws/                     bootstrap do LocalStack
deploy/prometheus/            configuração de scrape
scripts/                      send-webhook.sh, seed.sh
docs/                         design-doc, adr, roteiros, observabilidade
```

## Fluxo de trabalho

1. Ler o design doc e o ADR da área afetada.
2. Escrever o teste que falha (domínio e app com fakes; adaptadores com testcontainers).
3. Implementar no menor escopo possível, respeitando a direção das dependências.
4. `make lint test test-integration`.
5. Atualizar docs (README, design doc, ADR, roteiro) se o comportamento ou a decisão mudou.
6. Commits pequenos; PR com o template de `.github/pull_request_template.md`.
