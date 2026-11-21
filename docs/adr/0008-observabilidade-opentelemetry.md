# 0008. Observabilidade com OpenTelemetry, Prometheus e slog

- Status: Aceito
- Data: 2026-11-20

## Contexto

Um evento atravessa quatro etapas assíncronas (webhook, transação, relay, consumidor). Para responder "o que aconteceu com o evento X?" e "o outbox está atrasando?", é preciso ligar logs, métricas e traces da mesma requisição.

## Decisão

- **Traces**: OpenTelemetry SDK com exportador OTLP/HTTP, ligado quando `OTEL_EXPORTER_OTLP_ENDPOINT` está definido (Jaeger no profile `observability`). Propagação W3C `traceparent` do webhook para o outbox (coluna `headers`), do relay para o AMQP e do AMQP para o consumidor.
- **Métricas**: instrumentos OpenTelemetry expostos em formato Prometheus em `/metrics` (porta HTTP da API; `OPS_ADDR` no relay e no consumidor), junto com métricas de runtime do Go e do processo.
- **Logs**: `log/slog` em JSON, com `trace_id` e `span_id` extraídos do contexto em cada linha.
- Métricas de negócio: `webhook_requests_total{outcome}`, `outbox_published_total`, `outbox_retries_total`, `outbox_dead_total`, `outbox_publish_lag_seconds`, `consumer_messages_total{outcome}`; `http_server_request_duration_seconds` vem do `otelhttp`.

## Consequências

- Um `trace_id` no log da API leva ao trace completo, inclusive a publicação e o consumo.
- Sem exportador configurado, spans ainda são criados (ids nos logs), com custo baixo.
- Prometheus faz pull; em ambientes que preferem push, troca-se o reader por OTLP sem mexer na instrumentação.

## Alternativas

- **client_golang direto para métricas**: mais simples, mas duas APIs de instrumentação no código.
- **zap/zerolog**: mais rápidos em microbenchmarks; `slog` é padrão da biblioteca e suficiente.
- **Agente proprietário de APM**: acoplamento a fornecedor.
