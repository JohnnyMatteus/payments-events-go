# 0014. Segredos por porta de segredos e rotação sem downtime

- Status: Aceito
- Data: 2026-11-22

## Contexto

O segredo HMAC do webhook e a senha do banco vinham de variáveis de ambiente fixas na subida. Trocá-los exigia
reiniciar os processos e, no webhook, coordenar o relógio com o provedor: durante a troca, entregas legítimas
seriam recusadas com 401.

## Decisão

- Porta `app.SecretStore` (`Get(ctx, ref)`), com dois adaptadores em `internal/adapter/secretstore`: **Vault** (KV v2,
  referência `caminho#campo`, login por auth Kubernetes com renovação em 403; `VAULT_TOKEN` só em dev) e **AWS Secrets
  Manager** (referência `id` ou `id#campo`; no LocalStack por `SECRETS_ENDPOINT`). `SECRETS_PROVIDER=env` (padrão)
  mantém o comportamento anterior.
- **Webhook**: o segredo no cofre é a lista `atual,anterior`. `httpapi.Verifier.SetSecrets` troca o conjunto de forma
  atômica; durante a rotação os dois validam. O fluxo é gravar `novo,atual`, esperar o provedor migrar, gravar `novo`.
- **Banco**: `DATABASE_PASSWORD_REF` faz o pool ler a senha a cada conexão nova (`BeforeConnect`) e reciclar conexões em
  15 minutos. A rotação exige que o Postgres aceite as duas senhas nesse intervalo (estratégia de dois usuários,
  alternando entre eles); essa parte é do banco e não do serviço.
- **Rotação**: `app.SecretRotation` relê a cada `SECRETS_REFRESH_INTERVAL`. Falha fechado: a primeira leitura precisa
  dar certo (senão o processo não sobe); depois, segredo vazio ou cofre fora do ar mantêm o último valor bom e geram log
  (nunca o valor).
- Configuração ambígua derruba a subida: ref com `SECRETS_PROVIDER=env`, ref junto de `WEBHOOK_SECRETS`, provedor
  desconhecido, `VAULT_TOKEN` em `APP_ENV=production`.

## Consequências

- Rotação sem reinício e sem janela de 401, ao custo de um polling no cofre (um GET por intervalo e por segredo).
- Cofre fora do ar depois da subida não derruba o serviço, mas uma rotação durante a queda só vale quando ele volta.
- Os adaptadores foram testados contra dublês HTTP do Vault e do Secrets Manager e o override de compose
  (`make up-secrets`) só foi validado com `docker compose config`; não foram exercitados contra os serviços reais.
- O Vault e o LocalStack do platform-edge não foram ligados aqui; as referências e o papel do Vault ficam por conta do
  deploy.

## Alternativas

- **Reiniciar para rotacionar**: simples, mas abre janela de 401 e de conexões quebradas.
- **Sidecar/agent do Vault escrevendo arquivo**: funciona, mas prende a rotação a um agente e a um volume; a porta cobre
  Vault e AWS com o mesmo código.
- **Segredo versionado com identificador de chave no header**: exigiria que o provedor enviasse o id da chave; o
  provedor fictício não envia.
