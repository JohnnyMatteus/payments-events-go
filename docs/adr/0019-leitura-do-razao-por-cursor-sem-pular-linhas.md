# 0019. Leitura do razão por cursor sem pular linhas

- Status: Aceito
- Data: 2026-11-22

## Contexto

`GET /api/v1/ledger-entries` pagina por cursor (o id do último lançamento). O id do `ledger_entries` é uma identity,
sorteada no `INSERT`, mas a transação só confirma depois (ela ainda grava o outbox e faz o commit), e transações
concorrentes confirmam fora de ordem. Se a transação com o id 5 confirmar depois de a de id 6 e de uma página já ter
devolvido o 6, o 5 nunca aparece para quem pagina. Numa consulta financeira, uma linha que some é um defeito grave.

Ler só linhas abaixo do `xmin` do snapshot **não resolve**: uma transação A (xid 100) sorteia o id 5, depois uma
transação C (xid 99, mais antiga, mas mais lenta até o `INSERT` do ledger) sorteia o 6 e confirma. O `xmin` do snapshot
é 100, a linha 6 tem xid 99 e é devolvida; quando A confirmar, o 5 já ficou para trás do cursor.

## Decisão

Um advisory lock ordena escritores e leitores:

- Todo escritor do ledger (`record`, usado pelo webhook e pela aprovação de ajuste) pega
  `pg_advisory_xact_lock_shared(LedgerAppendLockKey)` **antes** de qualquer lock de linha e do `INSERT` (portanto antes de
  sortear o id) e o segura até o commit. É compartilhado: escritores não esperam uns aos outros.
- Cada página da leitura calcula o limite estável: numa transação curta pega o mesmo lock em modo **exclusivo**, o que
  espera os escritores em andamento, lê `MAX(id)` e solta. Sob o lock, todo id já sorteado está confirmado e qualquer
  escritor posterior sorteia um id maior. A página devolve só `id <= limite`, então nenhum id menor ou igual a um
  já devolvido pode aparecer depois.
- A espera do leitor tem `lock_timeout` de 5 s: um escritor travado derruba a consulta com erro em vez de pendurá-la.

O lock é pego antes de qualquer lock de linha para não formar ciclo com a fila do lock (o leitor espera os escritores; o
escritor que entra depois espera o leitor, e nenhum deles segura linha que o outro precise).

## Consequências

- A gravação ganha uma ida ao banco (o lock compartilhado) e, durante o instante do limite de cada página, os
  escritores novos esperam atrás do leitor; o leitor espera os em andamento (milissegundos). Uma enxurrada de
  consultas pode atrasar a gravação; as consultas exigem autenticação e têm o limite de página.
- Testado no CI (integração) com duas transações que confirmam fora de ordem e com o recorder real esperando o lock.
  Não medi o custo na carga (o k6 em `loadtest/` ainda não rodou).
- **Limite que ficou:** a lista de eventos (`GET /api/v1/events`) pagina por `(received_at, event_id)`, onde
  `received_at` é o início da transação, e tem o mesmo risco de pular um evento confirmado fora de ordem. É uma tela de
  apoio à operação (atualizável), não uma consulta de saldo, e não foi corrigida aqui.

## Alternativas

- **Serializar a gravação do ledger** (lock exclusivo do `INSERT` ao commit): resolve com um lock só, mas limita a
  vazão de gravação à de uma transação por vez.
- **Atraso fixo** (só mostrar linhas com mais de N segundos): não é prova, depende da duração máxima de uma transação.
- **Cursor por `xmin` (`pg_snapshot_xmin`)**: descartado pelo contraexemplo acima.
- **Chave de ordem atribuída no commit**: o PostgreSQL não oferece uma ordem de commit confiável para isso.
