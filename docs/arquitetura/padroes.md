# Padrões e princípios no código

Só entra aqui o que o código realmente tem. Os links apontam para a linha da
versão atual; se o arquivo mudar, atualize o número.

## Camadas

`internal/domain` (regras puras) ← `internal/app` (casos de uso + portas) ← `internal/adapter/*` (Postgres, RabbitMQ,
Redis, S3, HTTP) ← `cmd/payments` (montagem). As portas ficam em
[`internal/app/ports.go`](../../internal/app/ports.go#L45) e a direção das dependências é a do
[ADR 0001](../adr/0001-arquitetura-hexagonal-binario-unico.md).

## Padrões

| Padrão | Onde | Problema que resolve | Trade-off |
|---|---|---|---|
| Ports and Adapters | Portas em [`app/ports.go`](../../internal/app/ports.go#L45) (`EventRecorder`, `Publisher`, `IdempotencyCache`, `RateLimiter`...); adaptadores em `internal/adapter/*` | Casos de uso testáveis com fakes, sem Docker; trocar Postgres/RabbitMQ/Redis não toca no domínio | Mais tipos e uma etapa de montagem em `cmd/` |
| Repository | [`postgres.OutboxRepository.ProcessDue`](../../internal/adapter/postgres/outbox.go#L37), [`ProjectionRepository.ApplyOnce`](../../internal/adapter/postgres/projection.go#L27) | O caso de uso pede "mensagens vencidas" ou "aplicar uma vez", não SQL; `FOR UPDATE SKIP LOCKED` fica escondido no adaptador | A interface é moldada pelo caso de uso; outra consulta pede outro método |
| Transactional Outbox | Gravação: [`insertOutbox`](../../internal/adapter/postgres/recorder.go#L120) na mesma transação do lançamento; publicação: [`Relay.RunOnce`](../../internal/app/relay.go#L73) | Lançamento e evento de integração nunca divergem (sem dual write) | Entrega pelo menos uma vez, com latência do polling; consumidor precisa ser idempotente ([ADR 0003](../adr/0003-transactional-outbox-com-polling.md)) |
| Decorator | [`app.FastPathIngest`](../../internal/app/ingest_fastpath.go#L35) embrulha o `IngestService` com o atalho de idempotência no Redis; [`otelhttp.NewHandler`](../../internal/adapter/httpapi/router.go#L25) embrulha o webhook com trace e métrica | Adiciona cache e observabilidade sem mudar o caso de uso nem o handler | A ordem de montagem importa e fica em [`cmd/payments/api.go`](../../cmd/payments/api.go#L48) |
| Adapter | [`redisstore`](../../internal/adapter/redisstore/redisstore.go#L70) (SET NX GET, INCR + EXPIRE NX), [`objectstore.Opener.Open`](../../internal/adapter/objectstore/objectstore.go#L49) (`s3://` ou arquivo local), `csvstatement` (CSV → `domain.StatementLine`) | Traduz APIs externas para as portas do `app` | Cada adaptador precisa de teste de integração próprio |
| Strategy | Jitter do backoff como função: [`Backoff.Jitter`](../../internal/app/backoff.go#L14) com [`EqualJitter`](../../internal/app/backoff.go#L37) injetado em [`cmd/payments/relay.go`](../../cmd/payments/relay.go#L51); formato do relatório escolhido em [`cmd/payments/reconcile.go`](../../cmd/payments/reconcile.go#L86) (`WriteText` ou `WriteJSON`) | Testes usam jitter determinístico; o formato do relatório muda sem `if` no caso de uso | Em Go é só uma função; não há hierarquia de classes |
| Observer | [`app.RelayObserver`](../../internal/app/relay.go#L13), implementado por [`observability.RelayMetrics`](../../internal/platform/observability/relay_metrics.go#L42) | O relay notifica publicado/retry/morto sem depender de OpenTelemetry | Mais uma interface para manter em sincronia com as métricas |
| Functional options | [`httpapi.WithRateLimiter`](../../internal/adapter/httpapi/webhook.go#L72) | O limite por lojista é opcional (sem `REDIS_URL` o serviço funciona igual) sem quebrar o construtor | Dependência opcional precisa de `nil` check no handler |
| Idempotent Consumer | [`BalanceProjector.Handle`](../../internal/app/projector.go#L28) + `ApplyOnce` | Reentregas do RabbitMQ não somam o saldo duas vezes | Tabela `consumed_messages` cresce e precisa de retenção |

Não usamos Specification nem Chain of Responsibility: as validações do evento são poucas e ficam em
`domain.Event.Validate`; quebrá-las em objetos encadeados só adicionaria indireção.

## Redis: atalho, não garantia

- **Idempotência** ([`FastPathIngest.Ingest`](../../internal/app/ingest_fastpath.go#L35),
  [`IdempotencyCache.Claim`](../../internal/adapter/redisstore/redisstore.go#L70)): `SET payments:idem:<event_id>
  processing NX GET EX 30` antes do INSERT. Se a chave já está `done`, responde `duplicate` sem ir ao banco. Em
  `processing` (outro processo gravando), erro do Redis ou chave expirada, quem decide é a constraint única do
  Postgres ([ADR 0002](../adr/0002-idempotencia-por-constraint-unica.md)). Se a gravação falha, o claim é apagado
  para que a nova tentativa do provedor não seja tomada por duplicata. Trade-off: mais uma dependência e uma ida
  extra ao Redis em todo evento novo, em troca de reentregas que não tocam no banco.
- **Rate limit por lojista** ([`WebhookHandler.allow`](../../internal/adapter/httpapi/webhook.go#L154),
  [`FixedWindowLimiter.Allow`](../../internal/adapter/redisstore/redisstore.go#L122)): janela fixa com `INCR` +
  `EXPIRE NX` numa transação, chave `payments:ratelimit:merchant:<id>:<início da janela>`, compartilhada entre
  réplicas. Roda depois da assinatura, para requisições forjadas não gastarem a cota. Se o Redis cair, deixa
  passar (fail-open): recusar pagamentos legítimos custa mais que perder o limite por alguns minutos. Trade-off da
  janela fixa: permite até 2× o limite na virada da janela; sliding window seria mais preciso e mais caro.

## Autorização no caso de uso

- **Authenticator (porta de entrada) + Authorize (caso de uso)**
  ([`httpapi.Authenticator`](../../internal/adapter/httpapi/auth.go),
  [`app.Authorize`](../../internal/app/actor.go)): token de dev e OIDC
  ([`oidcauth`](../../internal/adapter/oidcauth/oidcauth.go)) só produzem um `app.Actor`; quem decide a permissão é
  o caso de uso, falhando fechado. Trocar o mecanismo de login não muda nenhuma regra, e um ponto de entrada novo
  não consegue esquecer a checagem. Custo: o contexto carrega o ator (valor implícito), e o teste precisa montá-lo.

## Segredos por porta e rotação

- **Port/Adapter + Last-Known-Good** ([`app.SecretStore`](../../internal/app/ports.go),
  [`SecretRotation`](../../internal/app/secrets.go), [`secretstore`](../../internal/adapter/secretstore/vault.go)): o
  caso de uso não sabe se o segredo vem do Vault ou da AWS; a rotação só troca o valor quando a leitura é válida e
  guarda o último bom. O verificador aceita um conjunto de chaves trocado de forma atômica
  ([`Verifier.SetSecrets`](../../internal/adapter/httpapi/signature.go)). Custo: um polling por segredo e a regra de
  que o cofre nunca pode ser a única verdade em runtime (a queda dele não derruba, mas congela a rotação).

## Retenção

- **Tombstone por linha + arquivo particionado** ([`Retention.ArchiveEventPayloads`](../../internal/adapter/postgres/retention.go),
  [migração 0006](../../internal/adapter/postgres/migrations/0006_retention.sql)): a linha com a chave de
  idempotência fica; o peso (payload) vai para uma tabela particionada por mês. O ledger é protegido por gatilho e
  exportado, nunca movido. [`RetentionService`](../../internal/app/retention.go) só conhece portas e valida a
  política (pisos) antes de qualquer coisa. Custo: a linha antiga mostra só um resumo e a trilha precisa do arquivo.

## gRPC interno

- **Adapter de entrada fino + interceptor de autenticação** ([`grpcapi`](../../internal/adapter/grpcapi/server.go)):
  o servidor só traduz protobuf para o [`BalanceService`](../../internal/app/balance.go), que decide o papel; o
  interceptor reaproveita a porta `AuthenticateBearer` do HTTP. Custo: o código gerado entra no repositório e precisa
  de `make proto` quando o contrato muda.

## SOLID

| Princípio | Onde | O que ganha | Custo |
|---|---|---|---|
| S — responsabilidade única | [`Verifier`](../../internal/adapter/httpapi/signature.go#L30) só valida assinatura; [`WebhookHandler`](../../internal/adapter/httpapi/webhook.go#L60) só traduz HTTP; [`IngestService`](../../internal/app/ingest.go#L24) só decide o que gravar | Cada parte tem teste próprio e pequeno | Mais arquivos para seguir um request |
| O — aberto/fechado | O atalho no Redis entrou como decorator ([`ingest_fastpath.go`](../../internal/app/ingest_fastpath.go#L23)) sem editar `IngestService` | Funcionalidade nova sem risco no caminho já testado | A composição em `cmd/` precisa ser lida para saber o que está ativo |
| L — substituição | `FastPathIngest` e `IngestService` cumprem o mesmo [`app.Ingester`](../../internal/app/ingest_fastpath.go#L12) e o handler não distingue os dois; os fakes dos testes também | Handler testado uma vez para qualquer implementação | O contrato (ex.: "nunca diz duplicate para evento novo") vive na doc e nos testes, não no tipo |
| I — segregação de interfaces | Portas de um ou dois métodos em [`ports.go`](../../internal/app/ports.go#L45) (`EventRecorder.Record`, `Publisher.Publish`, `RateLimiter.Allow`) | Fakes triviais; nenhum adaptador implementa o que não usa | Várias interfaces pequenas para descobrir |
| D — inversão de dependência | `app` declara as portas e não importa adaptadores; a montagem fica em [`cmd/payments/api.go`](../../cmd/payments/api.go#L14) | Domínio e casos de uso compilam e testam sem Postgres, RabbitMQ ou Redis | Montagem manual (sem container de DI), mais verbosa |
