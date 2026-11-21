## O que muda

<!-- Resumo em uma ou duas frases e o motivo da mudança. -->

## Como testar

<!-- Comandos ou passos para reproduzir. Ex.: make test, make test-integration, make up && make seed. -->

## Checklist

- [ ] Testes unitários e, se tocar em adaptadores, de integração
- [ ] `make lint` sem apontamentos
- [ ] Idempotência preservada (reentrega do mesmo evento não muda o ledger)
- [ ] Migrations novas são aditivas e compatíveis com a versão anterior
- [ ] Docs atualizadas (README, design doc, ADR ou roteiro), se a decisão mudou
- [ ] Nenhum segredo, dado real ou nome de empresa real no diff
