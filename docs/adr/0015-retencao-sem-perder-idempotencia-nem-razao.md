# 0015. Retenção sem perder idempotência nem o razão

- Status: Aceito
- Data: 2026-11-22

## Contexto

`processed_events` (com o payload JSON completo de cada webhook), `outbox_messages` e `ledger_entries` só crescem. O
design doc prevê particionamento mensal de `processed_events` e `ledger_entries`. Duas garantias não podem se perder:
a idempotência (`event_id` único em `processed_events` e `transaction_id` único no ledger, [ADR 0002](0002-idempotencia-por-constraint-unica.md))
e a imutabilidade do razão (append-only). Apagar um `event_id` antigo reabriria a porta para um lançamento duplicado
se o provedor reenviasse o evento.

## Decisão

- **Ledger**: nunca é alterado nem apagado. Um gatilho no banco recusa `UPDATE` e `DELETE` em `ledger_entries` (a regra
  que era só convenção passa a ser garantia) e outro gatilho, por instrução, recusa `TRUNCATE` (que não dispara
  gatilho de linha e, via `TRUNCATE processed_events CASCADE`, arrastaria o ledger pela chave estrangeira). "Arquivar" é exportar: o job de retenção grava cada mês fechado (UTC, por
  `recorded_at`) em `ledger/AAAA-MM.csv` no bucket de arquivo (versionado), com sha256 e contagem registrados em
  `ledger_archives`. As linhas continuam no ledger.
- **`processed_events`**: a linha nunca sai. Depois do prazo, o payload completo vai para `processed_events_archive`
  (particionada por mês, partições criadas pelo job) e o payload da linha vira o resumo que a lista de eventos usa. A
  cópia e a redução acontecem numa transação e só se o arquivo tiver todas as cópias. A trilha do evento lê o payload do
  arquivo e, se a cópia não existir, falha em vez de devolver o resumo como se fosse o payload completo.
- **Outbox**: só mensagens `published` com mais de N dias são apagadas. `pending`, `dead` e `discarded` ficam, e uma
  mensagem com dead letter aberta também. A idempotência do consumidor está em `consumed_messages` e a do webhook em
  `processed_events`, então apagar a linha publicada não a afeta. Antes de apagar, a mesma instrução grava em
  `processed_events` o fato mínimo (`outbox_published_at`, `outbox_purged_at`): a trilha mostra "publicada, linha do
  outbox arquivada" (e não "sem mensagem"), a lista mantém o status e o redrive de uma dead letter que chegar depois
  da limpeza recria a mensagem pendente, com o mesmo `message_id`, a partir da rota e do corpo guardados na própria
  dead letter. Dead letter sem origem no outbox continua recusada. Limite: `dead_letters` não guarda os headers da
  mensagem original (o `traceparent` que ligava o reprocessamento ao trace do webhook), então a mensagem recriada
  sai sem eles e o consumidor começa um trace novo.
- **Execução**: `payments retention`, um job (CronJob), nunca no caminho do webhook. Execuções concorrentes se
  serializam por advisory lock; cada rotina anda em lotes.
- **Prazos configuráveis**: `RETENTION_OUTBOX_AFTER` (7 dias), `RETENTION_EVENTS_AFTER` (30 dias),
  `RETENTION_LEDGER_EXPORT_AFTER` (90 dias), `RETENTION_BATCH_SIZE`. Há pisos (1 h, 24 h, 24 h): um valor vazio ou em
  unidade errada derruba o job em vez de arquivar dado em uso.

## Consequências

- `processed_events` continua crescendo em linhas (poucas colunas, cerca de 100 bytes), mas deixa de crescer em
  payload. O razão não encolhe nunca; o arquivo em objeto serve para auditoria e para um futuro destino frio.
- **Não está particionado o `ledger_entries` em si.** `UNIQUE (transaction_id)` e `UNIQUE (event_id)` globais não
  sobrevivem a um particionamento por tempo sem uma tabela de chaves permanente e um `INSERT` extra no caminho do
  webhook. Para linhas estreitas e sem payload o ganho não paga o custo. Fica como decisão do autor se o volume
  mudar (ver Alternativas).
- O design doc dizia "particionamento mensal de `processed_events` e `ledger_entries`": o que existe é o arquivo
  particionado de `processed_events` e a exportação mensal do ledger.
- Nenhum prazo legal de guarda foi definido; os padrões acima são pontos de partida. Quem definir o prazo regulatório
  deve ajustá-los e decidir se o arquivo do bucket ganha bloqueio de objeto (object lock) e ciclo de vida.
- A exportação do mês carrega as linhas na memória. O repositório recebe `LIMIT` de 2 milhões mais um, então nunca
  lê mais que isso; um mês maior falha sem exportar nada. Streaming para o objeto removeria o limite e fica como
  evolução.
- **O dono da tabela ainda pode desabilitar os gatilhos** (`ALTER TABLE ... DISABLE TRIGGER`) ou apagar a tabela. Em
  produção a aplicação deve conectar com um papel que não é dono das tabelas (só `SELECT`, `INSERT`, e `UPDATE`/`DELETE`
  onde o código precisa, nunca em `ledger_entries`), e as migrações rodar com outro papel. O ambiente de teste
  desliga o gatilho de `TRUNCATE` só dentro de `testinfra.ResetDatabase`, em transação; a migração não muda.
- Testes de integração (gatilho, arquivamento, idempotência depois do arquivamento, limpeza do outbox, exportação)
  rodam só no CI, que tem Postgres.

## Alternativas

- **Particionar `ledger_entries` por mês com tabela de chaves** (`ledger_keys`: `transaction_id` e `event_id` únicos
  para sempre): mantém a idempotência global e permite destacar partições antigas, ao custo de um insert a mais por
  evento e de reescrever os leitores. Recomendada se o ledger passar de algumas centenas de milhões de linhas.
- **Apagar `processed_events` antigo**: reabre a duplicidade depois do prazo do provedor para reentregas; descartado.
- **Mover linhas antigas do ledger para uma tabela de arquivo**: é apagar do razão com outro nome, e quebra a
  conciliação de períodos antigos; descartado.
