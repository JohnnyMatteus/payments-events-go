-- Retenção (ADR 0015). O ledger é imutável: o banco passa a recusar UPDATE e
-- DELETE em ledger_entries (o TRUNCATE dos testes não dispara gatilho de linha).
CREATE FUNCTION ledger_entries_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'ledger_entries é append-only: % não é permitido', TG_OP
        USING ERRCODE = 'restrict_violation';
END;
$$;

CREATE TRIGGER ledger_entries_append_only
    BEFORE UPDATE OR DELETE ON ledger_entries
    FOR EACH ROW EXECUTE FUNCTION ledger_entries_append_only();

-- TRUNCATE não dispara gatilho de linha: sem este, TRUNCATE ledger_entries ou
-- TRUNCATE processed_events CASCADE (a FK arrasta o ledger) zeraria o razão.
CREATE FUNCTION ledger_entries_no_truncate() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'ledger_entries é append-only: TRUNCATE não é permitido'
        USING ERRCODE = 'restrict_violation';
END;
$$;

CREATE TRIGGER ledger_entries_no_truncate
    BEFORE TRUNCATE ON ledger_entries
    FOR EACH STATEMENT EXECUTE FUNCTION ledger_entries_no_truncate();

-- processed_events nunca perde a linha: o event_id continua sendo a garantia
-- de idempotência. Só o payload completo sai para o arquivo particionado e a
-- linha guarda o resumo que as telas usam.
ALTER TABLE processed_events
    ADD COLUMN archived_at          timestamptz,
    -- Fato mínimo de publicação: quando a limpeza do outbox apaga a linha
    -- publicada, a trilha continua sabendo que a mensagem foi publicada e o
    -- redrive de uma dead letter sabe que ela teve origem no outbox.
    ADD COLUMN outbox_published_at  timestamptz,
    ADD COLUMN outbox_purged_at     timestamptz;

CREATE INDEX processed_events_unarchived_idx ON processed_events (received_at, event_id)
    WHERE archived_at IS NULL;

-- Partições mensais (UTC) são criadas pelo job de retenção antes de gravar.
-- Sem partição padrão: um mês sem partição falha em vez de cair num balde sem prazo.
CREATE TABLE processed_events_archive (
    event_id    text        NOT NULL,
    event_type  text        NOT NULL,
    outcome     text        NOT NULL,
    payload     jsonb       NOT NULL,
    received_at timestamptz NOT NULL,
    deliveries  integer     NOT NULL,
    archived_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, received_at)
) PARTITION BY RANGE (received_at);

-- Exportações do ledger para o armazenamento de objetos: o mês fica no ledger
-- e uma cópia verificável (hash e contagem) vai para o bucket de arquivo.
CREATE TABLE ledger_archives (
    month       date        PRIMARY KEY,
    object_ref  text        NOT NULL,
    row_count   bigint      NOT NULL CHECK (row_count > 0),
    sha256      text        NOT NULL,
    archived_at timestamptz NOT NULL DEFAULT now()
);
