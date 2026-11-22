# 0012. Disparo automático da conciliação: notificação S3 → SQS e agenda diária

- Status: Aceito
- Data: 2026-11-22

## Contexto

A conciliação rodava só sob demanda (painel ou `make reconcile`). O extrato do provedor chega ao bucket em horário
variável, e a promessa do produto é conciliar sem depender de alguém lembrar de apertar o botão. Duas garantias
importam: nenhum extrato novo fica sem conciliação, e a mesma notificação entregue duas vezes não gera duas execuções.

## Decisão

- **Gatilho por evento.** O bootstrap `init/aws/01-statements-bucket.sh` cria a fila `pagafacil-statements-events` (com
  DLQ após 5 entregas) e liga a notificação `s3:ObjectCreated:*` do prefixo `settlements/` do bucket a ela.
- **Consumidor atrás de portas.** `app.StatementWatcher` usa `ObjectNotifier` (fila), `ObjectClaims` (reserva) e
  `StatementRunner` (o mesmo `ReconciliationRunner` do painel, com origem `s3_notification`). O adaptador
  `sqsqueue` implementa a fila com long polling; o processo é `payments watch` (serviço `watcher` no compose).
- **Idempotência por objeto.** Cada versão de objeto (`bucket`, chave, `versionId`) é reservada na tabela
  `statement_object_claims` (chave primária, `INSERT ... ON CONFLICT DO NOTHING`) antes de conciliar. Reentrega da
  mesma notificação vira no-op; uma nova versão do extrato (o bucket é versionado) concilia de novo. Se a execução
  falha, a reserva é desfeita e a mensagem não é confirmada, então a fila reentrega e, esgotadas as tentativas,
  move para a DLQ.
- **Agenda diária como rede de segurança.** `payments reconcile --daily` concilia o dia anterior (UTC) com o extrato do
  bucket, grava a execução (origem `cli`) e, com `--fail-on-divergence`, sai com código 3 se houver divergência. O
  serviço `scheduler` do compose roda esse comando num crond do busybox (`RECONCILE_CRON`, padrão 03:00 UTC).

## Consequências

- Extrato novo é conciliado em segundos; se a notificação se perder, o cron do dia seguinte cobre.
- A conciliação é idempotente também no resultado: rodar de novo regrava a execução e o upsert dos casos já existe.
- Mais um processo (`watcher`) e uma imagem pequena (`scheduler`) no ambiente local.
- Em Kubernetes o cron vira um CronJob e o `watcher`, um Deployment; o código não muda.

## Alternativas

- **Polling do bucket**: simples, mas atrasa a conciliação e custa listagens.
- **Lambda no evento do S3**: fora do escopo do ambiente local e duplicaria a lógica do binário.
- **Só o cron**: não reage ao horário real do extrato.
- **Reserva no Redis**: mais rápido, mas o Redis aqui é descartável (ADR 0010) e a garantia precisa ser durável.
