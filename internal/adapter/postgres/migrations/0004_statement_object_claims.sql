-- Versões de extrato já conciliadas por notificação do S3. A chave primária
-- torna a reserva atômica: duas entregas da mesma notificação não rodam duas vezes.
CREATE TABLE statement_object_claims (
    bucket     text        NOT NULL,
    object_key text        NOT NULL,
    version_id text        NOT NULL DEFAULT '',
    claimed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (bucket, object_key, version_id)
);
