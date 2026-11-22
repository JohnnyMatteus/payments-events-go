-- A regra dos quatro olhos compara identidade estável (emissor + sub do OIDC),
-- não o nome de exibição, que o provedor de identidade deixa mudar.
ALTER TABLE ledger_adjustments
    ADD COLUMN proposed_by_id text,
    ADD COLUMN decided_by_id  text;

-- Ajustes anteriores foram propostos por token de dev, sem identidade estável.
-- O prefixo legacy: faz o domínio comparar também o nome, então ninguém aprova
-- o que propôs por ter sido migrado.
UPDATE ledger_adjustments SET proposed_by_id = 'legacy:' || lower(proposed_by) WHERE proposed_by_id IS NULL;
