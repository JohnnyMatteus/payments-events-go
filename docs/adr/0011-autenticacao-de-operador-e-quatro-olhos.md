# 0011. Autenticação de operador e aprovação com quatro olhos

- Status: Aceito
- Data: 2026-11-21

## Contexto

O painel Razão e a API `/api/v1` permitem ações que mexem com dinheiro: reprocessar mensagens da DLQ, disparar a
conciliação, justificar divergências e propor e aprovar lançamentos de ajuste no ledger. Cada ação precisa de uma pessoa
identificada na trilha de auditoria, e a aprovação de um ajuste não pode ser feita por quem o propôs.

## Decisão

- Toda rota `/api/v1` exige `Authorization: Bearer <token>`. O token identifica um operador; o nome do operador vem da
  configuração `OPERATOR_TOKENS` (`operador:papel:token,...`), nunca do corpo da requisição.
- Os tokens são guardados na memória como SHA-256 e comparados em tempo constante; token com menos de 8 caracteres ou
  operador repetido derruba a inicialização. Sem `OPERATOR_TOKENS`, as rotas não são registradas (a API de operação
  fica desligada em vez de aberta).
- Papéis: `leitor` só consulta (rotas `GET`); `operador` também reprocessa, descarta, executa a conciliação, envia
  extrato, justifica, propõe, aprova e recusa. Cada token tem um papel, declarado em `OPERATOR_TOKENS`; papel ausente ou
  desconhecido derruba a inicialização. A API responde 403 (`application/problem+json`) quando o `leitor` tenta uma
  ação, antes de qualquer caso de uso, e `GET /api/v1/me` devolve o papel para o painel esconder as ações do leitor
  (a barreira real é a API, o painel só evita botões que dariam erro).
- Quatro olhos: o ajuste proposto por um operador só é aprovado por outro (comparação sem diferenciar maiúsculas). A
  regra mora no domínio (`CanApprove`) e é executada dentro da transação que trava o ajuste, então duas aprovações
  concorrentes não passam. O ajuste aprovado vira um lançamento novo (`kind = adjustment`) com mensagem no outbox; o
  ledger continua append-only.
- Ações que mexem com dinheiro (reprocessar, descartar, justificar, propor, recusar) exigem motivo, gravado com o nome
  do operador e a hora.
- O painel guarda o token só em `sessionStorage` e o envia pelo cliente HTTP; um 401 encerra a sessão.

## Consequências

- Simples de rodar localmente e de testar; os tokens de exemplo são `dev-only`.
- Dois papéis fixos, sem expiração nem revogação de token; aprovar exige o papel `operador` além de ser outra pessoa.
- Em produção, a autenticação deve ficar num gateway OIDC (por exemplo o Keycloak do restante do portfólio) que valida
  o JWT e repassa a identidade e o papel (claim); o adaptador `OperatorAuth` é a única peça a trocar, porque o `app` só recebe o nome do
  operador.

## Alternativas

- **OIDC com PKCE direto no painel**: melhor para produção, mas exige um provedor de identidade só para este projeto.
- **Cabeçalho `X-Operator` confiado**: qualquer cliente se passaria por outro operador e anularia os quatro olhos.
- **Basic auth**: sem vantagem sobre o token e pior para rotação.
