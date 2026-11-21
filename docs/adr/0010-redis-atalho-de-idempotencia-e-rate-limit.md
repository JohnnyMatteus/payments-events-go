# 0010. Redis como atalho de idempotência e limite por lojista

- Status: Aceito
- Data: 2026-11-21

## Contexto

O provedor reentrega webhooks com frequência (timeout, retry agressivo). Cada reentrega abre transação no Postgres só
para descobrir, pela constraint única, que o evento já foi gravado ([ADR 0002](0002-idempotencia-por-constraint-unica.md)).
Além disso, um lojista com rajada de eventos (reprocessamento em massa do lado do provedor) pode ocupar a API e o
pool do banco de todos os outros.

## Decisão

- Redis atrás de duas portas do `app`: `IdempotencyCache` e `RateLimiter` (adaptador `internal/adapter/redisstore`).
- Idempotência: decorator `FastPathIngest` faz `SET NX GET` com TTL curto (`processing`) antes do INSERT e marca
  `done` (TTL de 24 h) depois. Só `done` responde `duplicate` sem ir ao banco; em qualquer outro caso o Postgres decide.
  Falha na gravação apaga o claim.
- Rate limit: janela fixa por `data.merchant_id` (`INCR` + `EXPIRE NX` em transação), aplicado depois da assinatura,
  resposta 429 com `Retry-After` e `RateLimit-*`.
- Fail-open nas duas funções: Redis indisponível não derruba o webhook. `REDIS_URL` vazio desliga as duas.

## Consequências

- Reentregas de eventos recentes não tocam no Postgres; a garantia continua no banco.
- Uma dependência a mais no ambiente e uma ida extra ao Redis por evento novo.
- A janela fixa permite até 2× o limite na virada da janela.

## Alternativas

- Só Postgres: correto, mas toda reentrega custa uma transação e não há proteção por lojista.
- Limite em memória por réplica: não compartilha a contagem entre réplicas.
- Sliding window / token bucket em Lua: mais preciso, mais código; desnecessário no volume atual.
