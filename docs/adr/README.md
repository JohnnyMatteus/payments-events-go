# Architecture Decision Records

Formato: Contexto, Decisão, Consequências e Alternativas. Um ADR por decisão; uma decisão revista ganha um ADR novo que substitui o antigo.

| ADR | Decisão | Status |
| --- | --- | --- |
| [0001](0001-arquitetura-hexagonal-binario-unico.md) | Arquitetura hexagonal num binário único com subcomandos | Aceito |
| [0002](0002-idempotencia-por-constraint-unica.md) | Idempotência por `event_id` com constraint única no PostgreSQL | Aceito |
| [0003](0003-transactional-outbox-com-polling.md) | Transactional outbox com relay por polling e `SKIP LOCKED` | Aceito |
| [0004](0004-rabbitmq-quorum-confirms-dlq.md) | RabbitMQ com filas quorum, publisher confirms, limite de entregas e DLQ | Aceito |
| [0005](0005-assinatura-hmac-e-janela-anti-replay.md) | Webhook autenticado por HMAC-SHA256 com timestamp e janela anti-replay | Aceito |
| [0006](0006-conciliacao-por-transaction-id.md) | Conciliação por `transaction_id` em período semiaberto UTC | Aceito |
| [0007](0007-extrato-no-s3-com-localstack.md) | Extrato do provedor no S3, emulado com LocalStack no ambiente local | Aceito |
| [0008](0008-observabilidade-opentelemetry.md) | Observabilidade com OpenTelemetry, Prometheus e slog | Aceito |
| [0009](0009-testes-de-integracao-com-testcontainers.md) | Testes de integração com testcontainers-go sob build tag | Aceito |
| [0010](0010-redis-atalho-de-idempotencia-e-rate-limit.md) | Redis como atalho de idempotência e limite por lojista | Aceito |
| [0011](0011-autenticacao-de-operador-e-quatro-olhos.md) | Autenticação de operador e aprovação com quatro olhos | Aceito |
| [0012](0012-disparo-automatico-da-conciliacao.md) | Disparo automático da conciliação: S3 → SQS e agenda diária | Aceito |
| [0013](0013-oidc-pkce-e-papeis-no-caso-de-uso.md) | OIDC com PKCE para operadores e papéis checados no caso de uso | Aceito |
| [0014](0014-segredos-por-porta-e-rotacao-sem-downtime.md) | Segredos por porta (Vault e Secrets Manager) e rotação sem downtime | Aceito |
| [0015](0015-retencao-sem-perder-idempotencia-nem-razao.md) | Retenção sem perder idempotência nem o razão (arquivar não é apagar) | Aceito |
| [0016](0016-grpc-interno-de-saldo-autenticado.md) | gRPC interno de saldo, autenticado por token OIDC | Aceito |
