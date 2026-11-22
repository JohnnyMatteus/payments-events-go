# 0021. LISTEN/NOTIFY para acordar o relay, com polling como rede de segurança

- Status: Aceito (complementa o [ADR 0003](0003-transactional-outbox-com-polling.md))
- Data: 2026-11-22

## Contexto

O relay lê o outbox por polling (padrão de 1 s), então uma mensagem espera até um intervalo inteiro antes de sair. O
design doc prevê `LISTEN/NOTIFY` para reduzir essa latência.

## Decisão

- A gravação no outbox executa `SELECT pg_notify('payments_outbox', <message_id>)` **na mesma transação**. O PostgreSQL
  só entrega a notificação no commit, e uma transação desfeita não avisa ninguém.
- O relay mantém uma conexão dedicada em `LISTEN` (`OutboxNotifier`, tirada do pool com `Hijack` e refeita a cada queda)
  e recebe um canal de sinal em `RelayConfig.Wake`. Rajadas de notificações se juntam num sinal só.
- **O polling continua**, com o mesmo intervalo. A notificação é uma dica de latência, não uma garantia: ela se perde
  durante uma reconexão, e o outbox é a única fonte da verdade. Por isso nenhum teste ou regra depende de a
  notificação chegar.

## Consequências

- A latência típica cai de até um intervalo de polling para milissegundos; o pior caso continua sendo o intervalo.
- Uma conexão a mais fica presa por processo de relay (fora do pool). Várias réplicas do relay são todas acordadas e
  disputam o lote com `SKIP LOCKED`, como já faziam.
- Um `NOTIFY` que falha desfaz a transação (o erro não é engolido); o custo é uma instrução a mais por mensagem.
- Não foi medida a latência nem a vazão com e sem `NOTIFY` (o k6 em `loadtest/` ainda não rodou). O reprocessamento
  de dead letter (que rearma uma linha do outbox) não notifica: o polling o pega.

## Alternativas

- **CDC (Debezium)**: latência mínima e sem polling, mas outro componente para operar; fica para volume maior, como no
  ADR 0003.
- **Reduzir só o intervalo de polling**: mais consultas ociosas por segundo em todas as réplicas.
