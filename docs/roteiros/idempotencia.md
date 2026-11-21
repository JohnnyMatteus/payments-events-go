# Roteiro: idempotência

Como o serviço garante que uma captura do PagaFácil Sandbox credita o lojista **uma única vez**, mesmo com reentregas, concorrência, quedas de processo e mensagens duplicadas no broker.

## 1. O problema em uma frase

Todo canal de integração confiável é *at-least-once*. O provedor reenvia webhooks, o relay pode publicar de novo, o broker pode reentregar. Idempotência é fazer com que "pelo menos uma vez" produza o efeito de "exatamente uma vez".

## 2. Onde a duplicação pode surgir

| Ponto | Por quê | Defesa |
| --- | --- | --- |
| Provedor → webhook | timeout de rede, 5xx, retry agressivo, entregas paralelas | `processed_events.event_id` PK + `ON CONFLICT DO NOTHING` |
| Provedor emite dois eventos para a mesma operação | bug ou reprocessamento do lado dele | `ledger_entries.transaction_id` UNIQUE → `duplicate_transaction` |
| Banco → broker | relay cai depois do confirm e antes do commit | outbox *at-least-once* + `message_id` estável (= `event_id`) |
| Broker → consumidor | ack perdido, reinício do consumidor, requeue | inbox `consumed_messages (consumer, message_id)` na mesma transação da projeção |

## 3. A solução, passo a passo

### 3.1 Webhook (`internal/adapter/postgres/recorder.go`)

```sql
BEGIN;
INSERT INTO processed_events (event_id, event_type, outcome, payload)
VALUES ($1, $2, $3, $4)
ON CONFLICT (event_id) DO NOTHING;      -- 0 linhas => duplicate, ROLLBACK
INSERT INTO ledger_entries (...) ON CONFLICT (transaction_id) DO NOTHING;
INSERT INTO outbox_messages (...);
COMMIT;
```

1. O primeiro comando da transação é a "reserva" do `event_id`.
2. Se outra transação já inseriu o mesmo id e **ainda não confirmou**, esta espera no índice único. Quando a primeira confirma, o `ON CONFLICT` não insere nada; se a primeira desfaz, esta insere e segue. Não há janela de corrida.
3. Duplicata responde `200 {"status":"duplicate"}`. Responder 200 é essencial: 4xx/5xx faria o provedor tentar de novo para sempre.
4. Lançamento e outbox estão na mesma transação: ou tudo existe, ou nada existe.

### 3.2 Publicação (`internal/adapter/postgres/outbox.go` e `internal/app/relay.go`)

O relay não é idempotente por si; ele é *at-least-once* de propósito. A mensagem leva `MessageId = event_id`, um identificador estável que o consumidor usa para deduplicar.

### 3.3 Consumidor (`internal/adapter/postgres/projection.go`)

```sql
BEGIN;
INSERT INTO consumed_messages (consumer, message_id) VALUES ($1, $2)
ON CONFLICT DO NOTHING;                -- 0 linhas => já aplicada, COMMIT sem efeito
INSERT INTO payment_balances ... ON CONFLICT (payment_id) DO UPDATE SET captured_cents = captured_cents + EXCLUDED.captured_cents;
COMMIT;
```

A chave inclui o nome do consumidor: cada consumidor tem sua própria inbox, e a mesma mensagem pode (e deve) ser processada uma vez por cada um.

## 4. Como está provado

- `TestRecordConcurrentDuplicateDeliveries`: 20 goroutines entregam o mesmo evento; exatamente um `recorded`, 19 `duplicate`, um lançamento.
- `TestRecordSecondEventForSameTransaction`: dois `event_id` para a mesma transação geram um lançamento.
- `TestProjectionAppliesEachMessageOnce`: a mesma mensagem aplicada duas vezes soma uma vez.
- `TestEndToEnd`: reentrega pelo HTTP responde `duplicate` e o saldo final é o esperado.

## 5. Alternativas e trade-offs

| Alternativa | Por que não |
| --- | --- |
| `SELECT` antes do `INSERT` | condição de corrida entre as instruções |
| Redis `SETNX` como trava | atomicidade com o ledger continua dependendo do banco; mais uma peça para operar |
| Idempotência só no consumidor | o ledger (fonte de verdade) ficaria duplicado |
| `SERIALIZABLE` | funciona, mas exige retry de `40001` e esconde a intenção |
| Chave de idempotência = hash do corpo | o provedor pode reenviar o mesmo evento com campos novos; o `id` é o contrato |

Custos aceitos: uma tabela que cresce com todos os eventos (precisa de política de retenção maior que a janela de reenvio do provedor) e uma linha a mais por mensagem no consumidor.

## 6. Perguntas que um entrevistador faria

**Por que responder 200 para uma duplicata e não 409?**
Para o provedor, qualquer não-2xx significa "não entregue, tente de novo". 409 geraria reenvios infinitos de algo que já foi processado. O corpo diz `duplicate` para quem estiver depurando.

**O que acontece se duas réplicas da API recebem o mesmo evento ao mesmo tempo?**
As duas iniciam transações; a segunda bloqueia no índice único de `event_id` até a primeira terminar. Se a primeira confirmar, a segunda recebe 0 linhas e responde `duplicate`. Se a primeira falhar, a segunda segue normalmente. Está coberto por teste.

**E se o processo cair depois do commit e antes de responder?**
O provedor não recebe 200 e reenvia. A reentrega encontra o `event_id` e responde `duplicate`. O efeito aconteceu uma vez.

**Por que não usar o `transaction_id` como chave principal?**
`event_id` identifica a entrega e é o que o provedor garante repetir. Eventos sem efeito financeiro (`payment.dispute_opened`) não têm transação, mas também precisam de idempotência. O `transaction_id` único é a segunda linha de defesa.

**Exactly-once existe?**
Não fim a fim em sistemas distribuídos. O que existe é *at-least-once* com efeito idempotente. Aqui cada fronteira tem uma chave estável e uma constraint que transforma repetição em operação sem efeito.

**Como limparia `processed_events`?**
Particionando por `received_at` (mensal) e removendo partições mais antigas que a janela máxima de reenvio do provedor mais uma margem (por exemplo, 90 dias). Antes disso, confirmaria com o provedor por quanto tempo ele reenvia.

**A projeção soma valores. E se as mensagens chegarem fora de ordem?**
Soma é comutativa, então a ordem não importa. Se a regra dependesse de ordem (por exemplo, máquina de estados do pagamento), eu guardaria uma versão por agregado e ignoraria eventos com versão menor ou igual à atual.

**Onde isso quebraria?**
Se alguém apagar linhas de `processed_events` dentro da janela de reenvio, ou se um consumidor novo usar o mesmo nome de outro (dividiriam a inbox). Ambos são tratados com processo: retenção documentada e nome do consumidor igual ao nome da fila.
