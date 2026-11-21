# 0007. Extrato do provedor no S3, emulado com LocalStack no ambiente local

- Status: Aceito
- Data: 2026-11-21

## Contexto

O PagaFácil Sandbox entrega o extrato de liquidação diário como CSV num bucket S3 (`pagafacil-statements/settlements/AAAA-MM-DD.csv`). O job de conciliação precisa ler esse arquivo, rodar igual em desenvolvimento, no CI e em produção, e não depender de conta AWS para testes.

## Decisão

- Porta `app.StatementReader` (`ReadStatement(ctx, ref)`), implementada por `csvstatement.Reader`, que combina um `Opener` com o parser CSV.
- `objectstore.Opener` resolve a referência: `s3://bucket/chave` vai para o S3 pelo SDK v2 da AWS; qualquer outro valor é caminho local (útil para reprocessar um arquivo recebido por outro canal).
- Endpoint configurável: `S3_ENDPOINT` vazio usa a AWS real; preenchido aponta para o emulador. `S3_USE_PATH_STYLE=true` para o LocalStack. Credenciais pela cadeia padrão do SDK (variáveis, perfil, IAM role).
- No ambiente local, **LocalStack 4.5.0** só com S3 (`SERVICES=s3`). O bootstrap `init/aws/01-statements-bucket.sh` (montado em `/etc/localstack/init/ready.d`) cria o bucket com versionamento; o healthcheck do contêiner só passa quando o bucket existe. `make seed` publica um extrato de exemplo.
- Testes de integração sobem o mesmo LocalStack via testcontainers.

## Consequências

- O mesmo binário funciona na AWS trocando variáveis; nenhum `if ambiente == local` no código.
- O versionamento do bucket preserva extratos reenviados pelo provedor, o que ajuda em auditoria.
- Mais um contêiner no ambiente local, limitado a 768 MB no compose. O LocalStack não reproduz IAM nem políticas de bucket com fidelidade; permissões reais precisam ser validadas no ambiente da AWS.
- A versão está fixada porque as imagens mais novas da LocalStack passaram a exigir token de conta.

## Alternativas

- **MinIO**: mais leve e só S3, o que bastaria hoje. Escolhemos LocalStack porque os próximos passos usam outros serviços AWS (SQS para notificar a chegada do extrato e Secrets Manager para os segredos do webhook) e porque é o emulador usado nos demais projetos do ecossistema.
- **Ler só de disco**: simples, mas esconde o fluxo real e empurra a integração com S3 para produção sem teste.
- **SFTP**: comum em adquirentes, mas não é o canal do provedor fictício.
