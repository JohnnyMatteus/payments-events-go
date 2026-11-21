# 0009. Testes de integração com testcontainers-go sob build tag

- Status: Aceito
- Data: 2026-11-20

## Contexto

As garantias centrais (idempotência sob concorrência, `SKIP LOCKED`, confirms, delivery-limit, DLQ) só existem no PostgreSQL e no RabbitMQ reais. Mocks não provam nada sobre elas. Ao mesmo tempo, `go test ./...` precisa rodar rápido e sem Docker.

## Decisão

- Testes de integração em arquivos `*_integration_test.go` com `//go:build integration`; `go test ./...` roda só os unitários.
- `internal/testinfra` sobe PostgreSQL 17, RabbitMQ 4.1 e LocalStack 4.5 com testcontainers-go, com as mesmas imagens do `docker-compose.yml`, um contêiner por pacote (`TestMain`), e limpa dados entre testes com `TRUNCATE`.
- Sem Docker disponível, os testes de integração são pulados com mensagem explícita.
- No CI, o job `integration` roda `go test -tags integration -p 1 ./...` no runner Ubuntu, que tem Docker.
- Um teste ponta a ponta sobe `api`, `relay` e `consumer` pelo mesmo `run()` do binário.

## Consequências

- Os testes provam o comportamento que importa (20 entregas concorrentes geram um lançamento; 5 relays não compartilham mensagens; mensagem venenosa vai para a DLQ sem retry).
- Integração leva minutos, não segundos; por isso fica num job separado e paralelo.
- `-p 1` evita subir contêineres de vários pacotes ao mesmo tempo em máquinas com pouca memória.

## Alternativas

- **docker-compose dedicado a testes**: estado compartilhado entre execuções e mais scripts.
- **Bancos embutidos (SQLite, broker em memória)**: semânticas diferentes de lock e de entrega.
- **Mocks de pgx/amqp**: testam a chamada, não a garantia.
