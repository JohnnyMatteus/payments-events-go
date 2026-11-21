# 0003. Transactional outbox com relay por polling e `SKIP LOCKED`

- Status: Aceito
- Data: 2026-11-20

## Contexto

Cada lançamento novo precisa virar um evento de integração (`ledger.entry_recorded`) para outros sistemas. Gravar no banco e publicar no RabbitMQ são duas operações em sistemas diferentes; sem coordenação, uma falha entre elas gera lançamento sem evento ou evento sem lançamento (*dual write*).

## Decisão

- A mensagem é gravada em `outbox_messages` na mesma transação do lançamento.
- O processo `relay` lê mensagens pendentes com `SELECT ... FOR UPDATE SKIP LOCKED` em lotes, publica cada uma com publisher confirms e grava a decisão na mesma transação do lock:
  - confirmada: `status = 'published'`;
  - falha: `attempts + 1` e `next_attempt_at = agora + backoff`, com backoff exponencial (`base * 2^(n-1)`, teto configurável) e *equal jitter*;
  - esgotou `OUTBOX_MAX_ATTEMPTS`: `status = 'dead'`, para análise e reprocessamento manual.
- O contexto de trace do webhook é gravado em `headers` e continua no relay e no consumidor.
- Índice parcial `WHERE status = 'pending'` mantém a consulta do relay barata mesmo com histórico grande.

## Consequências

- Garantia *at-least-once*: se o relay cair depois do confirm e antes do commit, a mensagem é publicada de novo. O consumidor deduplica por `message_id` (padrão inbox).
- Várias réplicas do relay podem rodar em paralelo sem entregar a mesma mensagem ao mesmo tempo (coberto por teste com 5 relays concorrentes).
- A ordem global não é garantida quando há retry. O consumidor de exemplo é comutativo (só soma), então a ordem não importa; consumidores que dependam de ordem por pagamento precisariam de sequência por agregado.
- Latência de publicação de até `OUTBOX_POLL_INTERVAL` (1 s por padrão) quando o outbox está vazio; com acúmulo, o relay processa lotes em sequência sem esperar.
- A transação fica aberta durante a publicação do lote. Lotes pequenos (100) e timeout de confirm (5 s) limitam o tempo de lock.

## Alternativas

- **CDC com Debezium lendo o WAL**: latência menor e sem polling, mas exige Kafka Connect ou servidor Debezium; é o caminho de evolução se o volume crescer.
- **`LISTEN/NOTIFY` para acordar o relay**: reduz latência; pode ser somado ao polling depois, sem mudar o modelo.
- **Publicar direto no handler após o commit**: perde mensagens se o processo cair entre o commit e a publicação.
- **Transação distribuída (XA/2PC)**: RabbitMQ não oferece, e o acoplamento seria alto.
