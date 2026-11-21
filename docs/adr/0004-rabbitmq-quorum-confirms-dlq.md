# 0004. RabbitMQ com filas quorum, publisher confirms, limite de entregas e DLQ

- Status: Aceito
- Data: 2026-11-20

## Contexto

O evento de integração precisa sobreviver a reinício do broker, e o consumidor precisa distinguir falhas que se resolvem sozinhas (banco indisponível) de mensagens que nunca serão processadas (contrato violado). Mensagens venenosas não podem travar a fila nem ficar em laço infinito.

## Decisão

- Exchange `payments.events` do tipo topic; routing key `ledger.entry_recorded`; fila `balance-projector` ligada por `ledger.#`.
- Fila principal **quorum** (replicada e durável) com `x-delivery-limit` (5 por padrão) e `x-dead-letter-exchange` apontando para `payments.events.dlx`, que roteia para `balance-projector.dlq`.
- Publicação com `DeliveryMode: Persistent` e **publisher confirms**: o relay só marca a mensagem como publicada depois do ack do broker.
- Topologia declarada de forma idempotente pelo publisher e pelo consumidor, para que nenhuma mensagem seja publicada antes de existir fila ligada.
- Consumidor com ack manual e prefetch configurável:
  - sucesso: `ack`;
  - erro permanente (`app.ErrPermanent`: JSON inválido, versão de schema desconhecida, campos ausentes): `nack` sem requeue, direto para a DLQ;
  - erro transitório: espera um backoff calculado a partir de `x-delivery-count` e faz `nack` com requeue; ao passar do limite, o próprio broker manda para a DLQ.
- Consumidor idempotente: `consumed_messages (consumer, message_id)` e a atualização da projeção na mesma transação.

## Consequências

- Retry do consumidor sem filas de espera extras: o contador de entregas é do broker, não de um header que o consumidor precisaria manter.
- Durante o backoff a mensagem ocupa um slot de prefetch; aceitável para o volume do exemplo. Para atrasos longos, o próximo passo é uma fila de retry com TTL por nível.
- A DLQ precisa de alerta (profundidade > 0) e de um procedimento de reprocessamento (shovel ou ferramenta própria), registrados como próximos passos.

## Alternativas

- **Filas clássicas com `x-message-ttl` e filas de retry por nível**: dão atraso real entre tentativas, ao custo de mais filas e bindings.
- **Kafka**: log replayable e ordem por partição; mais pesado para um único consumidor de exemplo. Está no projeto de telemetria do portfólio.
- **Requeue imediato sem limite**: laço quente e mensagem venenosa consumindo CPU para sempre.
