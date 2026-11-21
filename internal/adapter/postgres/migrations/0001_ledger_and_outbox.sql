-- Eventos recebidos do provedor. A chave primária em event_id é a garantia
-- de idempotência: uma entrega repetida não passa do INSERT ... ON CONFLICT.
CREATE TABLE processed_events (
    event_id    text        PRIMARY KEY,
    event_type  text        NOT NULL,
    outcome     text        NOT NULL CHECK (outcome IN ('recorded', 'ignored', 'duplicate_transaction')),
    payload     jsonb       NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now()
);

-- Ledger append-only. transaction_id único é a segunda linha de defesa contra
-- dois eventos distintos para a mesma operação financeira.
CREATE TABLE ledger_entries (
    id             bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_id       text        NOT NULL UNIQUE REFERENCES processed_events (event_id),
    transaction_id text        NOT NULL UNIQUE,
    payment_id     text        NOT NULL,
    direction      text        NOT NULL CHECK (direction IN ('credit', 'debit')),
    amount_cents   bigint      NOT NULL CHECK (amount_cents > 0),
    currency       char(3)     NOT NULL,
    occurred_at    timestamptz NOT NULL,
    recorded_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX ledger_entries_occurred_at_idx ON ledger_entries (occurred_at);
CREATE INDEX ledger_entries_payment_id_idx ON ledger_entries (payment_id);

-- Transactional outbox: gravado na mesma transação do ledger e drenado pelo relay.
CREATE TABLE outbox_messages (
    id              bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    message_id      text        NOT NULL UNIQUE,
    routing_key     text        NOT NULL,
    payload         jsonb       NOT NULL,
    headers         jsonb       NOT NULL DEFAULT '{}'::jsonb,
    status          text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'published', 'dead')),
    attempts        integer     NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error      text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    published_at    timestamptz
);

-- Índice parcial: o relay só olha mensagens pendentes, que são poucas.
CREATE INDEX outbox_messages_due_idx ON outbox_messages (next_attempt_at, id) WHERE status = 'pending';
