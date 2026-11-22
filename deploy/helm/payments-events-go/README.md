# Chart Helm

Renderiza:

| Recurso | O que é |
| --- | --- |
| Deployments `api`, `relay`, `consumer`, `watcher` | os processos do binário; probes `startup`, `liveness` (`/healthz`) e `readiness` (`/readyz`), `securityContext` restrito, métricas em `/metrics` (anotações do Prometheus) |
| Service da API | HTTP na 80 e, com `grpc.enabled`, gRPC (ADR 0016) |
| HPA (API) | CPU, de `autoscaling.minReplicas` a `maxReplicas`; com ele ligado o Deployment não fixa `replicas` |
| PodDisruptionBudget (API) | `minAvailable` |
| Job de migração | `payments migrate`, hook `pre-install,pre-upgrade` (o ArgoCD o roda como PreSync). Usa a SA `default` sem token, porque os recursos comuns do chart (ServiceAccount) ainda não existem quando o hook roda numa instalação nova; o Secret `existingSecret` também precisa existir antes |
| CronJob de conciliação | `payments reconcile -daily -fail-on-divergence` (dia anterior, UTC) |
| CronJob de retenção | `payments retention` (ADR 0015) |
| Painel Razão (opcional, `web.enabled`) | Deployment e Service do nginx do `web/`; o host da API é `web.apiUpstream` (padrão `<release>-api:80`), e o login OIDC vem de `web.oidcClientId` e do emissor |
| NetworkPolicy (opcional) | entrada na API só de `networkPolicy.ingressFrom` |

## Segredos

O chart não cria segredos. `existingSecret` deve trazer `DATABASE_URL`, `RABBITMQ_URL` e, com `secrets.provider=env`,
`WEBHOOK_SECRETS`. Com `vault` ou `awssm` o segredo do webhook (e opcionalmente a senha do banco) vem do cofre
(ADR 0014); em `APP_ENV=production` o Vault exige `secrets.vault.k8sRole` (o token estático é só para dev).

## Verificação

Sem cluster nesta máquina nada foi aplicado. O CI renderiza o chart (`helm lint` e `helm template` com
`ci/values-ci.yaml` e outras combinações) e valida o YAML gerado; renderização não prova que o cluster aceita os recursos.

```bash
docker run --rm -v "$PWD:/chart" -w /chart alpine/helm:3.17.0 template demo deploy/helm/payments-events-go -f deploy/helm/payments-events-go/ci/values-ci.yaml
```

## Fora do chart

- A imagem do painel: nenhum workflow deste repositório publica imagens, então `web.image.repository` é um espaço
  reservado e `web.enabled` fica desligado por padrão.
- Ingress, certificados e a criação do Secret TLS do gRPC ficam com a plataforma.
