# Observabilidade

Os três sinais usam OpenTelemetry como API única (ADR [0008](adr/0008-observabilidade-opentelemetry.md)).

## Como ver localmente

```bash
make up-observability   # núcleo + Jaeger + Prometheus, com traces ligados
make seed
```

| Ferramenta | URL |
| --- | --- |
| Jaeger (traces) | http://localhost:16687 |
| Prometheus | http://localhost:9095 |
| Métricas da API | http://localhost:8085/metrics |
| Métricas do relay | http://localhost:9101/metrics |
| Métricas do consumidor | http://localhost:9102/metrics |
| RabbitMQ (UI) | http://localhost:15673 (usuário `payments`, senha de dev) |

## Logs

JSON em stdout (`slog`). Campos fixos: `time`, `level`, `msg`, `service`. Quando há span no contexto: `trace_id` e `span_id`.

```json
{"time":"2026-11-20T12:00:00Z","level":"INFO","msg":"webhook processed","service":"payments-api",
 "trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"00f067aa0ba902b7",
 "event_id":"evt_demo_001","event_type":"payment.captured","outcome":"recorded"}
```

O job `reconcile` escreve logs em stderr para deixar o relatório limpo em stdout.

## Métricas

| Métrica | Tipo | Labels | Pergunta que responde |
| --- | --- | --- | --- |
| `webhook_requests_total` | counter | `outcome` (`recorded`, `duplicate`, `ignored`, `duplicate_transaction`, `invalid_signature`, `stale_timestamp`, `invalid_payload`, `invalid_event`, `payload_too_large`, `error`) | o provedor está reenviando muito? alguém está forjando eventos? |
| `http_server_request_duration_seconds` | histogram | método, rota, status | qual o p99 do webhook? |
| `outbox_published_total` | counter | | o relay está andando? |
| `outbox_retries_total` | counter | | o broker está instável? |
| `outbox_dead_total` | counter | | há mensagens que exigem ação manual? |
| `outbox_publish_lag_seconds` | histogram | | quanto tempo entre o lançamento e o evento sair? |
| `consumer_messages_total` | counter | `outcome` (`acked`, `requeued`, `dead_lettered`) | o consumidor está falhando? |
| `go_*`, `process_*` | vários | | saúde do runtime |
| `rabbitmq_*` (porta 15692) | vários | fila | profundidade da fila e da DLQ |

Consultas úteis (PromQL):

```promql
# taxa de duplicatas recebidas
sum(rate(webhook_requests_total{outcome="duplicate"}[5m]))

# p99 do atraso de publicação do outbox
histogram_quantile(0.99, sum by (le) (rate(outbox_publish_lag_seconds_bucket[5m])))

# mensagens na DLQ
rabbitmq_queue_messages{queue="balance-projector.dlq"}
```

## Traces

O span do servidor HTTP (`POST /webhooks/pagafacil`) é pai de `EventRecorder.Record`. O `traceparent` vai para a coluna `headers` do outbox; o relay cria `outbox.deliver` como filho desse contexto e injeta o contexto atual nos headers AMQP; o consumidor cria `consume ledger.entry_recorded` (span kind consumer). Resultado: um único trace do webhook até a projeção.

Variáveis padrão do OpenTelemetry são respeitadas (`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES`, `OTEL_TRACES_SAMPLER`).

## Health checks

| Rota | Significado |
| --- | --- |
| `GET /healthz` | o processo está vivo (liveness) |
| `GET /readyz` | o banco responde (readiness) |

A imagem é distroless (sem shell nem curl); o healthcheck do contêiner usa `payments healthcheck <url>`.
