# Teste de carga do webhook (k6)

`webhook.js` envia eventos assinados (HMAC) a uma taxa constante e reenvia 1 em cada 10 para provar a idempotência sob
carga. Os limiares do script reprovam a execução (código de saída 99) se:

- mais de 1% das requisições falharem;
- p95 da latência passar de 300 ms ou p99 de 800 ms;
- menos de 99% dos eventos novos voltarem `recorded` ou das reentregas voltarem `duplicate`.

Os valores dos limiares são pontos de partida, não um SLO medido: ajuste depois da primeira medição no seu ambiente.

## Como rodar (contra um ambiente de teste, nunca produção)

```bash
make up                                   # a API em http://localhost:8085
# O limite por lojista do compose é baixo (20/min) para a demo; suba-o para a carga:
WEBHOOK_RATE_LIMIT=100000 make up
make loadtest                             # RATE=50 DURATION=1m por padrão
make loadtest RATE=200 DURATION=5m MERCHANTS=500
```

`make loadtest` usa a imagem `grafana/k6:1.8.3`. Variáveis: `BASE_URL`, `WEBHOOK_SECRET` (o padrão é o segredo de
dev do compose), `RATE`, `DURATION`, `MERCHANTS`, `RUN_ID`.

## O que não está feito

- O teste não roda no CI: exige o compose de pé e leva minutos. Nenhuma execução foi feita ao escrever o script, então
  não há números de referência.
- Só o webhook é medido; o gRPC e o painel não têm teste de carga.
