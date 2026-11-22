# 0016. gRPC interno de saldo, autenticado

- Status: Aceito
- Data: 2026-11-22

## Contexto

Outros serviços precisam consultar o saldo por pagamento e por lojista. A API REST de `/api/v1` é a do painel
(operadores, com ações que mexem com dinheiro); expô-la a serviços misturaria os dois públicos. O design doc prevê uma
API gRPC interna.

## Decisão

- Contrato em `api/proto/balance/v1/balance.proto`: `GetPaymentBalance`, `ListMerchantBalances` e
  `ListPaymentBalances` (cursor opaco). Só leitura, valores em centavos (`int64`). O código gerado fica em
  `internal/adapter/grpcapi/balance/v1/` e é regenerado por `make proto` (`scripts/gen-proto.sh`: `protoc` no PATH e
  plugins com versão fixada no módulo `tools/`).
- **Autenticação**: access token OIDC no metadata `authorization: Bearer <token>`, verificado pelo mesmo adaptador do
  painel (`oidcauth`, ou o token de dev em `APP_ENV=dev`). Um serviço usa uma conta de serviço do Keycloak
  (client credentials) com a role de leitura. Todo método passa pelo interceptor; só a verificação de saúde
  (`grpc.health.v1`, usada pelo probe) dispensa o token. Falha fechado: sem token ou token inválido é
  `UNAUTHENTICATED`, identidade sem role é `PERMISSION_DENIED`.
- **Papel no caso de uso**: `app.BalanceService` chama `app.Authorize(ctx, RoleReader)`; o interceptor só autentica,
  como no HTTP.
- **Transporte**: `GRPC_ADDR` liga o servidor dentro do processo `api` (vazio, desligado). Em `APP_ENV=production`
  exige `GRPC_TLS_CERT_FILE`/`GRPC_TLS_KEY_FILE` (o token não trafega em texto claro); em dev aceita texto claro com
  aviso no log. Sem autenticação configurada, o processo recusa subir com gRPC ligado.

## Consequências

- O saldo vem da mesma leitura do painel, então herda a defasagem da projeção (`unprojected_messages` mostra quanto
  falta projetar).
- mTLS entre serviços, quotas por chamador e reflection não foram feitos. Quem exige identidade de rede em vez de
  token pode acrescentar `credentials.NewTLS` com `ClientAuth`.
- Testado com `bufconn` e o token de dev; o caminho OIDC compartilha `AuthenticateBearer` com o HTTP (testado contra um
  emissor de teste), não foi exercitado com um token de conta de serviço do Keycloak real.
- O CI não verifica se o código gerado está em dia com o `.proto` (o runner não tem `protoc`); `make proto` e
  `git diff --exit-code` fazem isso localmente.

## Alternativas

- **Reusar a API REST**: simples, mas expõe rotas de operação a serviços e depende de um papel só.
- **mTLS como única autenticação**: boa identidade de máquina, mas sem papéis e sem auditoria por conta no ledger.
- **GraphQL ou REST dedicada**: sem tipagem de contrato gerada e sem streaming, que um consumidor interno pode querer.
