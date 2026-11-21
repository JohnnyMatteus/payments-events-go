# 0001. Arquitetura hexagonal num binário único com subcomandos

- Status: Aceito
- Data: 2026-11-20

## Contexto

O serviço tem quatro processos com ciclos de vida diferentes: a API que recebe webhooks, o relay do outbox, o consumidor de exemplo e o job de conciliação. Todos compartilham o mesmo domínio (evento, lançamento, conciliação) e o mesmo banco. As regras de negócio precisam ser testáveis sem PostgreSQL ou RabbitMQ, e a infraestrutura precisa ser trocável (por exemplo, extrato vindo de S3 ou de disco).

## Decisão

- Arquitetura hexagonal (portas e adaptadores):
  - `internal/domain`: regras puras, sem dependências externas.
  - `internal/app`: casos de uso e portas (`EventRecorder`, `OutboxRepository`, `Publisher`, `LedgerReader`, `StatementReader`, `ProjectionRepository`).
  - `internal/adapter/*`: implementações (HTTP, PostgreSQL, RabbitMQ, S3, CSV, relatório).
  - `internal/platform/*`: configuração e observabilidade.
  - `cmd/payments`: composição (o único lugar que conhece todas as peças).
- Um binário (`payments`) com subcomandos `migrate`, `api`, `relay`, `consumer`, `reconcile` e `healthcheck`, empacotado numa única imagem. Cada processo escala de forma independente no deploy.

## Consequências

- O domínio e os casos de uso são testados com fakes, em milissegundos.
- Uma imagem só para versionar, escanear e implantar; o comando define o papel do contêiner.
- Mais interfaces e arquivos do que um serviço em camadas simples. Para um serviço deste tamanho é um custo aceitável, porque cada porta tem pelo menos um fake de teste e um adaptador real.
- O app depende só da API do OpenTelemetry nos adaptadores; o relay usa um `RelayObserver` próprio para não acoplar o caso de uso à biblioteca de métricas.

## Alternativas

- **Camadas tradicionais (handler, service, repository)**: menos arquivos, mas o service acaba conhecendo pgx e amqp, o que dificulta testes.
- **Um binário por processo (`cmd/api`, `cmd/relay`...)**: igualmente válido; foi descartado para manter uma imagem só e um ponto de configuração.
- **Microsserviços separados por repositório**: overhead de deploy e versionamento sem ganho, já que os processos compartilham o mesmo banco e contrato.
