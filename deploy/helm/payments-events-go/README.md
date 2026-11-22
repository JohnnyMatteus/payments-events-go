# Chart Helm

Renderiza:

| Recurso | O que é |
| --- | --- |
| Deployments `api`, `relay`, `consumer`, `watcher` | os processos do binário; probes `startup`, `liveness` (`/healthz`) e `readiness` (`/readyz`), `securityContext` restrito, métricas em `/metrics` (anotações do Prometheus) |
| Service da API | HTTP na 80 e, com `grpc.enabled`, gRPC (ADR 0016) |
| HPA (API) | CPU, de `autoscaling.minReplicas` a `maxReplicas`; com ele ligado o Deployment não fixa `replicas` |
| PodDisruptionBudget (API) | `minAvailable` |
| Job de migração | `payments migrate`, hook `pre-install,pre-upgrade` (o ArgoCD o roda como PreSync) |
| CronJob de conciliação | `payments reconcile -daily -fail-on-divergence` (dia anterior, UTC) |
| CronJob de retenção | `payments retention` (ADR 0015) |
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

- O painel (`web/`) não está no chart: o `nginx.conf` dele encaminha `/api` para o host `api`, que no cluster é
  `<release>-api`. Ajustar o proxy (ou servir o painel atrás do mesmo Ingress) é decisão de deploy.
- Ingress, certificados e a criação do Secret TLS do gRPC ficam com a plataforma.
