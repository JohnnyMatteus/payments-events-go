# Alvos padronizados do ambiente local e da qualidade de código.
# `make help` lista tudo.

SHELL := /bin/bash
COMPOSE := docker compose
CORE := $(COMPOSE) --profile core
ALL := $(COMPOSE) --profile core --profile observability --profile tools
GOLANGCI_LINT_VERSION := v2.15.0
RECONCILE_DATE ?= 2026-11-20

-include .env
APP_VERSION ?= dev
export

.DEFAULT_GOAL := help

.PHONY: help
help: ## Lista os alvos disponíveis
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

## Ambiente local

.PHONY: up
up: ## Sobe o núcleo: Postgres, RabbitMQ, LocalStack, api, relay e consumer
	$(CORE) up -d --build --wait

.PHONY: up-observability
up-observability: ## Sobe o núcleo com Jaeger e Prometheus (traces ligados)
	OTEL_EXPORTER_OTLP_ENDPOINT=http://jaeger:4318 $(COMPOSE) --profile core --profile observability up -d --build --wait

.PHONY: network
network: ## Cria a rede compartilhada portfolio-net do ecossistema
	docker network inspect portfolio-net >/dev/null 2>&1 || docker network create portfolio-net

.PHONY: up-portfolio
up-portfolio: network ## Sobe o núcleo ligado à rede portfolio-net
	$(CORE) -f docker-compose.yml -f docker-compose.portfolio.yml up -d --build --wait

.PHONY: down
down: ## Para os contêineres (mantém os volumes)
	$(ALL) down

.PHONY: logs
logs: ## Acompanha os logs dos serviços
	$(ALL) logs -f --tail=100

.PHONY: ps
ps: ## Lista os serviços e o estado dos healthchecks
	$(ALL) ps

.PHONY: seed
seed: ## Envia webhooks de exemplo e publica o extrato no S3 local
	./scripts/seed.sh

.PHONY: reconcile
reconcile: ## Concilia o extrato do S3 com o ledger (RECONCILE_DATE=AAAA-MM-DD)
	RECONCILE_DATE=$(RECONCILE_DATE) $(COMPOSE) --profile core --profile tools run --rm reconcile

.PHONY: evidencia
evidencia: ## Recria a stack (com observabilidade) e grava o vídeo de evidência com Playwright
	$(ALL) down -v --remove-orphans
	$(MAKE) up-observability
	cd e2e/evidencia && npm ci && npx playwright install chromium && npx playwright test

.PHONY: clean
clean: ## Remove contêineres, volumes e binários locais
	$(ALL) down -v --remove-orphans
	rm -rf bin coverage.out

## Qualidade

.PHONY: fmt
fmt: ## Formata o código (gofmt + goimports via golangci-lint)
	golangci-lint fmt ./...

.PHONY: lint
lint: ## Roda o golangci-lint
	golangci-lint run ./...

.PHONY: test
test: ## Testes unitários com race detector e cobertura
	go test -race -count=1 -coverprofile=coverage.out ./...

.PHONY: test-integration
test-integration: ## Testes de integração (Docker + testcontainers)
	go test -tags integration -count=1 -p 1 -timeout 15m ./...

.PHONY: fuzz
fuzz: ## Fuzzing do parser de valores por 30s
	go test -run=^$$ -fuzz=FuzzParseAmountCents -fuzztime=30s ./internal/domain/

.PHONY: bench
bench: ## Benchmarks (assinatura e conciliação)
	go test -run=^$$ -bench=. -benchmem ./internal/...

.PHONY: vuln
vuln: ## Procura vulnerabilidades conhecidas nas dependências
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

.PHONY: build
build: ## Compila o binário em bin/payments
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(APP_VERSION)" -o bin/payments ./cmd/payments

.PHONY: docker-build
docker-build: ## Constrói a imagem de produção
	docker build --build-arg VERSION=$(APP_VERSION) -t payments-events-go:$(APP_VERSION) .
