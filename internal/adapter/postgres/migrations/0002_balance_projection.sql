-- Inbox do consumidor: garante efeito único por (consumidor, mensagem).
CREATE TABLE consumed_messages (
    consumer    text        NOT NULL,
    message_id  text        NOT NULL,
    consumed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, message_id)
);

-- Projeção de leitura mantida pelo consumidor de exemplo.
CREATE TABLE payment_balances (
    payment_id     text        PRIMARY KEY,
    currency       char(3)     NOT NULL,
    captured_cents bigint      NOT NULL DEFAULT 0 CHECK (captured_cents >= 0),
    refunded_cents bigint      NOT NULL DEFAULT 0 CHECK (refunded_cents >= 0),
    updated_at     timestamptz NOT NULL DEFAULT now()
);
