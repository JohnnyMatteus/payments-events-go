-- Lojista e ajustes no ledger. As colunas são aditivas: lançamentos antigos
-- ficam com merchant_id nulo e kind = 'provider'.
ALTER TABLE ledger_entries
    ADD COLUMN merchant_id            text,
    ADD COLUMN kind                   text NOT NULL DEFAULT 'provider' CHECK (kind IN ('provider', 'adjustment')),
    ADD COLUMN adjusts_transaction_id text;

CREATE INDEX ledger_entries_merchant_id_idx ON ledger_entries (merchant_id);

-- Quantas vezes o provedor entregou o evento (a primeira conta como 1).
ALTER TABLE processed_events ADD COLUMN deliveries integer NOT NULL DEFAULT 1;

-- Mensagens do outbox que esgotaram as tentativas podem ser descartadas por
-- uma pessoa, com motivo.
ALTER TABLE outbox_messages
    DROP CONSTRAINT outbox_messages_status_check,
    ADD CONSTRAINT outbox_messages_status_check CHECK (status IN ('pending', 'published', 'dead', 'discarded')),
    ADD COLUMN resolved_by     text,
    ADD COLUMN resolved_at     timestamptz,
    ADD COLUMN resolution_note text;

CREATE INDEX processed_events_received_at_idx ON processed_events (received_at DESC, event_id);

-- Mensagens que saíram da fila principal para a DLQ do broker, arquivadas
-- pelo consumidor da DLQ. O broker continua sendo onde a mensagem morre; esta
-- tabela é o registro que a operação consulta e trata.
CREATE TABLE dead_letters (
    id              bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    message_id      text        NOT NULL,
    routing_key     text        NOT NULL,
    payload         bytea       NOT NULL,
    reason          text        NOT NULL,
    error           text        NOT NULL,
    deliveries      integer     NOT NULL DEFAULT 1,
    first_failed_at timestamptz NOT NULL,
    archived_at     timestamptz NOT NULL DEFAULT now(),
    status          text        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'requeued', 'discarded')),
    resolved_by     text,
    resolved_at     timestamptz,
    resolution_note text
);

-- Uma mensagem aberta por message_id: reentradas na DLQ somam entregas.
CREATE UNIQUE INDEX dead_letters_open_message_idx ON dead_letters (message_id) WHERE status = 'open';

-- Execuções da conciliação, de qualquer origem (CLI, painel, notificação do S3).
CREATE TABLE reconciliation_runs (
    id                 bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    settlement_date    date        NOT NULL,
    statement_ref      text        NOT NULL,
    trigger            text        NOT NULL CHECK (trigger IN ('manual', 's3_notification', 'cli')),
    triggered_by       text        NOT NULL,
    status             text        NOT NULL CHECK (status IN ('succeeded', 'failed')),
    error              text,
    statement_lines    integer     NOT NULL DEFAULT 0,
    ledger_entries     integer     NOT NULL DEFAULT 0,
    matched            integer     NOT NULL DEFAULT 0,
    reversed           integer     NOT NULL DEFAULT 0,
    statement_net      bigint      NOT NULL DEFAULT 0,
    ledger_net         bigint      NOT NULL DEFAULT 0,
    started_at         timestamptz NOT NULL,
    finished_at        timestamptz NOT NULL
);

CREATE INDEX reconciliation_runs_date_idx ON reconciliation_runs (settlement_date, id DESC);

-- Linhas da partida dobrada de uma execução: pares conciliados e divergências.
CREATE TABLE reconciliation_items (
    run_id         bigint  NOT NULL REFERENCES reconciliation_runs (id) ON DELETE CASCADE,
    position       integer NOT NULL,
    kind           text    NOT NULL,
    transaction_id text    NOT NULL,
    statement      jsonb,
    ledger         jsonb,
    adjusted       boolean NOT NULL DEFAULT false,
    delta_cents    bigint  NOT NULL DEFAULT 0,
    detail         text    NOT NULL DEFAULT '',
    PRIMARY KEY (run_id, position)
);

-- Caso de divergência: sobrevive entre execuções e guarda a decisão humana.
CREATE TABLE divergence_cases (
    id              bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    settlement_date date        NOT NULL,
    transaction_id  text        NOT NULL,
    kind            text        NOT NULL,
    status          text        NOT NULL DEFAULT 'open'
                    CHECK (status IN ('open', 'adjustment_proposed', 'explained', 'resolved')),
    statement       jsonb,
    ledger          jsonb,
    delta_cents     bigint      NOT NULL DEFAULT 0,
    detail          text        NOT NULL DEFAULT '',
    first_run_id    bigint      NOT NULL REFERENCES reconciliation_runs (id),
    last_run_id     bigint      NOT NULL REFERENCES reconciliation_runs (id),
    explanation     text,
    resolved_by     text,
    resolved_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (settlement_date, transaction_id, kind)
);

-- Lançamento de ajuste proposto por uma pessoa e aprovado por outra. Só vira
-- lançamento no ledger (e mensagem no outbox) na aprovação.
CREATE TABLE ledger_adjustments (
    id                     bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    case_id                bigint      NOT NULL REFERENCES divergence_cases (id),
    transaction_id         text        NOT NULL,
    adjusts_transaction_id text,
    merchant_id            text,
    payment_id             text        NOT NULL,
    direction              text        NOT NULL CHECK (direction IN ('credit', 'debit')),
    amount_cents           bigint      NOT NULL CHECK (amount_cents > 0),
    currency               char(3)     NOT NULL,
    occurred_at            timestamptz NOT NULL,
    reason                 text        NOT NULL,
    status                 text        NOT NULL DEFAULT 'proposed' CHECK (status IN ('proposed', 'approved', 'rejected')),
    proposed_by            text        NOT NULL,
    proposed_at            timestamptz NOT NULL DEFAULT now(),
    decided_by             text,
    decided_at             timestamptz,
    decision_note          text,
    event_id               text
);

-- Um ajuste pendente por caso.
CREATE UNIQUE INDEX ledger_adjustments_pending_idx ON ledger_adjustments (case_id) WHERE status = 'proposed';
