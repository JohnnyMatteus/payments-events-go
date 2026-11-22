# 0013. OIDC com PKCE para operadores e papéis checados no caso de uso

- Status: Aceito (complementa o [ADR 0011](0011-autenticacao-de-operador-e-quatro-olhos.md))
- Data: 2026-11-22

## Contexto

O ADR 0011 deixou o token estático por operador como solução de desenvolvimento e apontou o OIDC (Keycloak do
platform-edge) para produção. Além disso, a checagem de papel morava no middleware HTTP: um novo ponto de entrada
(gRPC, CLI, fila) que esquecesse de repeti-la executaria ações que mexem com dinheiro sem barreira.

## Decisão

- **Painel**: authorization code com PKCE (S256) via `oidc-client-ts`, sem segredo no navegador. O emissor e o client
  vêm de `/config.json`, montado no deploy; sem ele o painel cai no formulário de token de dev. O access token fica só
  em `sessionStorage`.
- **API**: `internal/adapter/oidcauth` verifica assinatura (JWKS por discovery), emissor, audiência (`OIDC_AUDIENCE`,
  padrão `edge-gateway`, a audiência que o realm `edge` põe nos tokens) e validade, com `coreos/go-oidc`. As roles do
  realm viram papéis: `OIDC_ROLE_OPERATOR` (padrão `payments.write`) é `operador`, `OIDC_ROLE_READER` (padrão
  `payments.read`) é `leitor`, e a mais forte vence. Token válido sem nenhuma das duas roles recebe 403.
- **Papéis no caso de uso**: o middleware só autentica e põe um `app.Actor` no contexto. Todo caso de uso que muda
  estado chama `app.Authorize(ctx, RoleOperator)` e usa o nome do ator na trilha de auditoria (não há mais parâmetro
  `operator` vindo do chamador). Falha fechado: sem ator, sem nome ou com papel desconhecido, nada acontece.
  Gatilhos de sistema (notificação do S3, cron) não passam por pessoa; só o pedido manual da conciliação exige ator.
- **Token só para dev**: `OPERATOR_TOKENS` é recusado com `APP_ENV=production`, e definir `OIDC_ISSUER_URL` junto com
  `OPERATOR_TOKENS` derruba a subida. Sem nenhum dos dois a API de operação não é registrada.

## Consequências

- Qualquer ponto de entrada novo herda a barreira, porque ela está no caso de uso.
- O emissor precisa estar de pé na subida da API (discovery); sem ele o processo não sobe, em vez de aceitar tokens
  sem verificar.
- Sem revogação instantânea: vale o `accessTokenLifespan` do realm (5 minutos no platform-edge).
- O adaptador foi exercitado contra um emissor OIDC de teste (discovery e JWKS), não contra o Keycloak real. O client
  público do painel (`payments-razao`, PKCE obrigatório, redirect `/callback`) precisa ser criado no realm do
  platform-edge, que é outro repositório.

## Alternativas

- **Gateway valida o JWT e repassa a identidade por cabeçalho**: simples, mas a API passa a confiar no cabeçalho e
  quem chegar direto à API se passa por qualquer operador.
- **Checar o papel só no middleware**: o que havia; descartado pelo motivo do contexto.
- **PKCE escrito à mão**: poucas linhas, mas é código de segurança que a biblioteca já mantém.
