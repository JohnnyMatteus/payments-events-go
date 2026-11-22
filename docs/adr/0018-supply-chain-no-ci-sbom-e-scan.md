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
- As ferramentas rodam como imagens Docker com versão fixada (`anchore/syft:v1.57.2`, `aquasec/trivy:0.76.2`), em vez
  de actions de terceiros, para não aceitar código mutável de tag num job que lê o repositório. Nenhum segredo é usado.

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
