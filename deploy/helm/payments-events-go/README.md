# Chart Helm (rascunho)

Rascunho para a próxima fase (Kubernetes e Helm estão fora do escopo da fundação). Gera Deployments para `api`, `relay` e `consumer`, um Service para a API e um CronJob de conciliação.

```bash
docker run --rm -v "$PWD:/chart" -w /chart alpine/helm:3.17.0 lint deploy/helm/payments-events-go
docker run --rm -v "$PWD:/chart" -w /chart alpine/helm:3.17.0 template demo deploy/helm/payments-events-go
```

Pendências: cálculo da data do dia anterior no CronJob, HPA, PodDisruptionBudget, NetworkPolicy, Job de migração (hook `pre-upgrade`) e integração com ArgoCD.
