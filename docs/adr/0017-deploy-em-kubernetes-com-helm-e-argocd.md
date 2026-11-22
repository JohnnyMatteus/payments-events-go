# 0017. Deploy em Kubernetes com Helm e ArgoCD, com sincronização manual

- Status: Aceito
- Data: 2026-11-22

## Contexto

O serviço roda hoje só em Docker Compose. O design doc prevê Helm (Deployments, CronJob de conciliação, HPA, probes) e
GitOps com ArgoCD. Neste repositório todo `feat/*` com CI verde é promovido sozinho para a `main`.

## Decisão

- **Chart** em `deploy/helm/payments-events-go`: Deployments `api`, `relay`, `consumer` e `watcher` com probes de
  startup, liveness (`/healthz`) e readiness (`/readyz`); HPA e PodDisruptionBudget na API (o `relay` e o `watcher`
  ficam em uma réplica: são consumidores do outbox e da fila, e o outbox já tolera várias réplicas, mas o ganho não
  paga a complexidade agora); CronJobs de conciliação (`reconcile -daily`) e de retenção (ADR 0015); Service com gRPC
  opcional e TLS por Secret (ADR 0016); NetworkPolicy opcional.
- **Migração como hook** `pre-install,pre-upgrade` (PreSync no ArgoCD). Como o hook roda antes dos recursos comuns do
  chart, o Job não pode depender deles: usa a ServiceAccount `default` sem token e um Secret que já exista. O CI
  confere no render que o hook não referencia a ServiceAccount do chart.
- **O chart não cria segredos.** O Secret `existingSecret` (ou o cofre, ADR 0014) é responsabilidade da plataforma.
- **ArgoCD com sincronização manual e revisão fixada por tag.** `deploy/argocd/application.yaml` não tem `automated`
  nem aponta para a `main`: como todo commit verde entra na `main` sem aprovação humana, sincronização automática com
  `prune` seria deploy em produção, e remoção de recursos, de um commit que ninguém aprovou. Promover é trocar a tag e
  sincronizar. O `ignoreDifferences` de `replicas` evita brigar com o HPA.

## Consequências

- Nada foi aplicado em cluster: o CI só renderiza o chart (lint, `template` em combinações de valores, YAML válido,
  presença de HPA, PDB, probes, CronJobs e a regra do hook). Renderizar não prova que o cluster aceita os recursos nem
  que os probes passam com a configuração real.
- O painel (`web/`) está no chart como opcional (`web.enabled`, desligado por padrão): o `nginx` do painel lê o host da
  API de `API_UPSTREAM`, o Service do chart é o padrão. Nenhum workflow publica a imagem do painel.
- Com `secrets.provider=vault` e `databasePasswordRef`, o hook de migração precisa de uma ServiceAccount pré-existente
  com papel no Vault (`migrate.serviceAccountName`); o chart falha a renderização se faltar.
- Ingress, certificados, o Secret TLS do gRPC e a política de promoção (quem cria a tag) ficam com o autor.

## Alternativas

- **`automated` com `selfHeal` na `main`**: GitOps "puro", incompatível com a promoção automática de `feat/*` daqui.
- **ServiceAccount do chart como hook de peso menor**: funciona, mas o recurso deixa de pertencer ao release e sobra
  depois do `helm uninstall`.
- **Migração no `initContainer` da API**: várias réplicas disputariam a migração (há advisory lock, mas o hook deixa a
  falha visível antes de trocar a versão).
