# 0006. Conciliação por `transaction_id` em período semiaberto UTC

- Status: Aceito
- Data: 2026-11-20

## Contexto

Webhooks podem se perder (provedor desiste após N tentativas, bug no handler, evento descartado), e o extrato de liquidação do provedor é a fonte de verdade financeira. O lojista precisa saber, por dia, o que foi liquidado e não lançado, o que foi lançado e não liquidado, e o que diverge em valor.

## Decisão

- Chave de casamento: `transaction_id` (identifica a operação financeira, não o evento).
- Período `[from, to)` em UTC; um lançamento pertence ao período pelo `occurred_at` informado pelo provedor (a mesma fonte de tempo do extrato). Linhas do extrato fora do período são contadas como `skipped_out_of_period`.
- Tipos de divergência: `missing_in_ledger` (faltando), `missing_in_statement` (sobrando), `amount_mismatch` (valor, moeda ou direção diferentes) e `duplicate_in_statement`.
- Valores tratados em centavos (`int64`), com parser decimal estrito; nada de ponto flutuante.
- A regra é uma função pura (`domain.Reconcile`) com saída determinística (ordenada por tipo e transação).
- Extrato inválido (linha mal formada) aborta a execução com o número da linha: conciliar um arquivo parcial esconderia divergências.
- Saída em texto (operação) ou JSON (integração); `-fail-on-divergence` devolve código 3 para agendadores alertarem.

## Consequências

- Reexecutar a conciliação é seguro: ela só lê.
- Transações perto da meia-noite caem no dia do `occurred_at` do provedor nos dois lados; não há falso positivo por fuso.
- O job carrega o período inteiro em memória: 10 mil linhas levam cerca de 4 ms num notebook (`make bench`). Para milhões de linhas, o caminho é conciliar em lotes ordenados (merge join) ou no próprio banco.
- O relatório aponta, não corrige. Correções viram lançamentos de ajuste em uma próxima etapa.

## Alternativas

- **Casar por `payment_id`**: um pagamento tem várias transações (captura, estornos parciais).
- **Casar por valor e data**: ambíguo com valores repetidos.
- **Conciliar via SQL (tabela temporária e `FULL OUTER JOIN`)**: escala melhor; descartado nesta fase porque a regra em Go é mais legível e testável. Fica como evolução.
