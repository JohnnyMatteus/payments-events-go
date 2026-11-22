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
| `pipeline_*`, `reconciliation_*` | gauge | | estado do pipeline lido do banco (publicado só pelo relay) |
| `consumer_messages_total` | counter | `outcome` (`acked`, `requeued`, `dead_lettered`) | o consumidor está falhando? |
| `idempotency_fastpath_total` | counter | `result` (`acquired`, `in_flight`, `done`, `error`) | o atalho do Redis está respondendo reentregas? |
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

## Alertas

`deploy/prometheus/alerts.yml` traz os sinais sugeridos no design doc: mensagem morta no outbox, p99 do atraso de
publicação acima de 30 s, assinatura inválida em excesso, webhook com erro 5xx, mensagens paradas na DLQ do broker,
dead letters abertas, divergências da conciliação sem decisão, conciliação parada há mais de 36 h e a ausência dos gauges do pipeline. O Prometheus do
compose carrega o arquivo; o CI roda `promtool check` e `promtool test rules` (`alerts_test.yml`, um caso que dispara e um
que não dispara por regra). Os limiares são pontos de partida, não medidos.

- Os contadores (`outbox_*`, `webhook_requests_total`, `consumer_messages_total`, `idempotency_fastpath_total`) nascem em
  0 no processo: sem isso o salto de "série ausente" para 1 não vira `increase() > 0` e a primeira ocorrência passaria sem alerta.
- A profundidade da DLQ vem do endpoint `/metrics/detailed?family=queue_coarse_metrics` do RabbitMQ (job `rabbitmq-queues`),
  métrica `rabbitmq_detailed_queue_messages{queue="balance-projector.dlq"}`; o `/metrics` agregado não traz métrica por fila.
  Como o consumidor da DLQ arquiva a mensagem e a retira da fila, essa fila fica vazia em operação normal: o sinal de
  "há dead letters para tratar" é `pipeline_dead_letters_open`.
- O relay publica gauges de estado lidos do banco (`pipeline_outbox_pending`, `pipeline_outbox_dead`,
  `pipeline_dead_letters_open`, `pipeline_oldest_pending_seconds`, `reconciliation_open_cases`,
  `reconciliation_last_run_timestamp_seconds`). Se a leitura falhar, a coleta não publica valor velho. Com o banco fora o gauge some e a regra `PipelineGaugesAbsent` (`absent()` por 10 min) avisa; sem ela os
  alertas de dead letters e de conciliação ficariam mudos.

## Traces

O span do servidor HTTP (`POST /webhooks/pagafacil`) é pai de `EventRecorder.Record`. O `traceparent` vai para a coluna `headers` do outbox; o relay cria `outbox.deliver` como filho desse contexto e injeta o contexto atual nos headers AMQP; o consumidor cria `consume ledger.entry_recorded` (span kind consumer). Resultado: um único trace do webhook até a projeção.

Variáveis padrão do OpenTelemetry são respeitadas (`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES`, `OTEL_TRACES_SAMPLER`).

## Health checks

| Rota | Significado |
| --- | --- |
| `GET /healthz` | o processo está vivo (liveness) |
| `GET /readyz` | o banco responde (readiness) |

A imagem é distroless (sem shell nem curl); o healthcheck do contêiner usa `payments healthcheck <url>`.
