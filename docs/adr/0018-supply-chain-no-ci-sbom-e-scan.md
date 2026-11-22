# 0018. Cadeia de suprimentos no CI: SBOM e scan, sem assinatura ainda

- Status: Aceito (assinatura pendente de decisão)
- Data: 2026-11-22

## Contexto

O design doc prevê SBOM, assinatura da imagem (cosign) e scan com Trivy. O CI já roda `govulncheck`, que cobre só o
código Go. A restrição é não criar segredo novo e não alterar as permissões do workflow.

## Decisão

- No job `build`, depois de construir a imagem, o CI a exporta (`docker save`) e:
  - gera o **SBOM** com o syft (SPDX e CycloneDX), falha se o arquivo não listar `github.com/jackc/pgx` (um SBOM
    vazio passaria sem ser notado) e publica os dois como artefato do job;
  - roda o **Trivy** sobre o mesmo arquivo, reprovando o job por vulnerabilidades HIGH ou CRITICAL **com correção
    disponível** (`--ignore-unfixed`).
- As ferramentas rodam como imagens Docker fixadas por **digest do índice multi-arquitetura**
  (`anchore/syft:v1.57.2@sha256:e97e20b6...` e `aquasec/trivy:0.76.2@sha256:8f6bb947...`, completos no `ci.yml`), em
  vez de actions de terceiros: tag é mutável e imagem de scanner já foi vetor de ataque. Os digests vieram da API do
  registro (Docker Hub, `HEAD /v2/<repo>/manifests/<tag>` com `Accept` de índice OCI, cabeçalho `Docker-Content-Digest`),
  sem `docker pull`, em 2026-11-22. Atualizar é repetir essa consulta e trocar tag e digest juntos.
- **Versão do Trivy**: o aviso oficial GHSA-69fq-xp46-6x23 ("Trivy ecosystem supply chain temporarily compromised")
  lista como comprometidos o binário e a imagem v0.69.4, as imagens v0.69.5 e v0.69.6 do Docker Hub, as tags de
  `trivy-action` anteriores à 0.35.0 e `setup-trivy`. A 0.76.2 é posterior a essa janela (19 a 23 de março de 2026) e não
  consta no aviso; o digest fixado impede que a tag seja trocada depois.
- **Isolamento no runner**: a imagem exportada fica em `$RUNNER_TEMP/scan/in`, montada somente leitura nas duas
  ferramentas; o SBOM sai por um diretório separado, só com o syft. Nunca se monta o `RUNNER_TEMP` inteiro (ele guarda
  os arquivos de comando do runner, onde escrever `GITHUB_ENV` ou `GITHUB_PATH` vira execução nos passos seguintes,
  e credenciais de passos anteriores). O Trivy não escreve em nada do runner, e não há `docker.sock`. O checkout do job
  usa `persist-credentials: false`.

## Consequências

- Uma vulnerabilidade nova com correção publicada deixa o CI vermelho até a atualização da base ou da dependência. É
  intencional; um CVE sem correção não reprova.
- O Trivy baixa a base de vulnerabilidades a cada execução; uma falha de rede ou de limite de taxa derruba o passo sem
  ser achado real.
- **Assinatura não foi implementada.** Assinar sem chave guardada (keyless com cosign) exige `id-token: write` no job,
  o que muda as permissões do workflow, e assinar a imagem pede um registro: o CI só constrói a imagem localmente e quem
  publica é o `promote.yml`. Assinar com uma chave gerada na hora não prova nada. Fica como decisão do autor.

## Alternativas

- **cosign keyless num job só de `push` na `main`, com `id-token: write` limitado a ele**: recomendada; assina o digest
  da imagem publicada pelo `promote.yml` e verifica a assinatura no mesmo fluxo.
- **trivy-action e sbom-action**: mais curtas, mas dependem de tags de terceiros.
- **Trivy sem gate (só relatório)**: não reprova nada; descartado por "verde tem de ser verdadeiro".
