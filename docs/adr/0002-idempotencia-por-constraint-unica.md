# 0002. Idempotência por `event_id` com constraint única no PostgreSQL

- Status: Aceito
- Data: 2026-11-20

## Contexto

O PagaFácil Sandbox entrega webhooks *at-least-once*: reenvia o mesmo evento (mesmo `id`) quando não recebe 2xx a tempo, inclusive em paralelo. Processar duas vezes uma captura credita o lojista em dobro. Também já ocorreu, em provedores reais, emitir dois eventos distintos para a mesma operação financeira.

## Decisão

- `processed_events.event_id` é chave primária. A ingestão faz `INSERT ... ON CONFLICT (event_id) DO NOTHING` como **primeiro** comando da transação; se nenhuma linha foi inserida, a transação é desfeita e a resposta é `200 {"status":"duplicate"}`, sem tocar ledger nem outbox.
- `ledger_entries.transaction_id` é único (segunda linha de defesa). Um evento novo para uma transação já lançada é auditado em `processed_events` com resultado `duplicate_transaction` e não gera lançamento nem mensagem.
- Evento, lançamento e mensagem de outbox são gravados na mesma transação (ver ADR 0003).
- Tipos de evento sem efeito financeiro são registrados como `ignored` e também respondem 200, para o provedor não reenviar para sempre.

## Consequências

- Entregas concorrentes do mesmo evento são serializadas pelo índice único: a segunda transação espera a primeira e, quando ela confirma, não insere nada. Coberto por teste de integração com 20 entregas simultâneas.
- A garantia vale enquanto a linha existir. Uma política de retenção de `processed_events` precisa ser maior que a janela de reenvio do provedor (próximo passo).
- Uma consulta e um índice a mais por evento; desprezível frente ao custo de um crédito duplicado.

## Alternativas

- **Checar antes de inserir (`SELECT` e depois `INSERT`)**: condição de corrida entre as duas instruções.
- **Lock distribuído (Redis)**: mais uma peça de infraestrutura e a garantia continua dependendo do banco para atomicidade.
- **Cache de idempotência em memória**: perde o estado em restart e não funciona com várias réplicas.
- **`SERIALIZABLE` em toda a transação**: funciona, mas troca a resposta determinística do `ON CONFLICT` por retries de serialização.
