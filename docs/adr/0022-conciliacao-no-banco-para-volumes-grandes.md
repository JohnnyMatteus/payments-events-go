# 0022. Conciliação no banco para volumes grandes

- Status: Aceito (complementa o [ADR 0006](0006-conciliacao-por-transaction-id.md))
- Data: 2026-11-22

## Contexto

A conciliação carrega todos os lançamentos do período na memória e compara por `transaction_id` com a função pura
`domain.Reconcile` (cerca de 4 ms para 10 mil linhas). Com milhões de linhas o conjunto de lançamentos não cabe. O
design doc prevê a comparação em SQL para volumes grandes.

## Decisão

- `RECONCILE_MODE=sql` liga `postgres.SQLComparer` (porta `app.LedgerComparer`). O padrão continua `memory`.
- O banco faz o que escala: recorta o período, soma por transação o lançamento do provedor com os ajustes que o corrigem
  (`GROUP BY`), escolhe a identidade do lançamento efetivo (`DISTINCT ON`: o do provedor, ou o primeiro ajuste) e faz o
  `FULL OUTER JOIN` com as linhas do extrato, carregadas numa tabela temporária por `COPY`. Os lançamentos do período
  nunca vão para a memória; só as divergências (e, se pedido, os pares) são montadas.
- A regra de decisão não foi duplicada: a diferença entre uma linha e um lançamento vem de `domain.CompareLine`, as
  mensagens de divergência são as mesmas e a ordenação final é `Report.Finalize`, a mesma de `domain.Reconcile`.
- **Equivalência provada por teste**: o teste de integração roda as duas comparações sobre os mesmos dados (um cenário
  que passa por todos os ramos e 250 transações aleatórias de semente fixa) e exige `reflect.DeepEqual` dos relatórios.
- `RECONCILE_INCLUDE_MATCHES=false` omite o slice de pares conciliados (só a contagem `Matched` fica), o que poupa
  memória quando há milhões de pares. Sem os pares, a tela de conciliação não mostra os itens conciliados da execução.

## Consequências

- O extrato continua sendo lido inteiro para a memória pelo parser de CSV (o slice de linhas existe nos dois modos); o
  ganho é do lado do ledger. Streaming do extrato para o `COPY` é a próxima etapa e exige mudar `StatementReader`.
- A comparação no banco ocupa o banco durante a junção (uma tabela temporária por execução, removida no commit).
- Não foi medida a diferença de tempo e de memória entre os dois modos: o teste prova que o resultado é igual, não que é
  mais rápido nem em que volume compensa. O modo `memory` segue como padrão por isso.
- O teste de integração só roda no CI.

## Alternativas

- **Mover toda a regra para SQL, inclusive as mensagens**: duplicaria a regra em dois lugares e quebraria a equivalência
  com o tempo.
- **Processar o período em janelas**: não resolve uma transação cujo ajuste cai em outra janela.
