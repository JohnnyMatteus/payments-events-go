# 0020. Testes de mutação no domínio

- Status: Aceito
- Data: 2026-11-22

## Contexto

A cobertura de linhas não diz se os testes pegam um erro de lógica: um `<` trocado por `<=` numa regra de dinheiro pode
passar com 100% de cobertura. O design doc prevê testes de mutação no domínio, onde moram a conciliação, o dinheiro e
a regra dos quatro olhos.

## Decisão

- `make mutation` roda o gremlins (`go-gremlins/gremlins` v0.6.0, fixado em `tools/go.mod`, sem instalação global) sobre
  `internal/domain` e falha se a eficácia (mortos sobre mortos mais vivos) ficar abaixo de 90%. O CI roda o mesmo alvo
  no job `mutation`.
- A primeira medição achou mutantes vivos reais em `Reconcile` (a identidade do lançamento quando o ajuste vem antes
  do lançamento do provedor, e a direção de uma transação zerada por ajuste) e em `ParseCurrency` e `ParseAmountCents`
  (limites do alfabeto e o zero). Foram escritos os testes que os matam. Eficácia medida depois: 98,7%.

## Consequências

- Resta um mutante vivo equivalente (`cases.go`, `delta < 0` contra `<= 0`: o caso `delta == 0` já foi tratado antes e a
  mutação não muda o comportamento). Seis mutantes aparecem como "não cobertos" no `Validate` do evento e em
  `Closed()` apesar de haver testes (a medição de cobertura do gremlins não os enxerga); não foram contados.
- A execução leva cerca de 1,5 min com um worker. Mutantes que estouram o tempo não contam nem como mortos nem como
  vivos: na última execução local, com a máquina carregada, 26 dos 85 estouraram (eficácia de 100% sobre os 53 mortos e
  0 vivos), enquanto outra execução teve só 4. Por isso `--workers 1` e `--timeout-coefficient 6`; se o CI mostrar muitos
  timeouts, a eficácia aparente é otimista e o coeficiente precisa subir.
- Só o domínio é mutado; os casos de uso e os adaptadores não.

## Alternativas

- **go-mutesting**: mais mutadores, projeto menos mantido; descartado.
- **Subir a cobertura de linhas**: não mede a qualidade das asserções.
