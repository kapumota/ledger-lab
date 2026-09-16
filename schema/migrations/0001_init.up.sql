-- ledger-lab. Migracion 0001. Modelo contable de doble entrada.
--
-- Este archivo es neutral respecto del runtime. Lo aplican por igual la
-- implementacion Go y la implementacion Elixir. Las invariantes I1, I4 y I5
-- se defienden aqui, no en el codigo de aplicacion, porque una invariante
-- que solo vive en la aplicacion deja de existir cuando alguien escribe por
-- otra ruta.

BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ---------------------------------------------------------------------------
-- accounts
-- ---------------------------------------------------------------------------
CREATE TABLE accounts (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT        NOT NULL,
    currency     CHAR(3)     NOT NULL,
    kind         TEXT        NOT NULL,
    constrained  BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT accounts_kind_ck
        CHECK (kind IN ('asset', 'liability', 'equity', 'revenue', 'expense')),
    CONSTRAINT accounts_currency_ck
        CHECK (currency ~ '^[A-Z]{3}$')
);

COMMENT ON COLUMN accounts.constrained IS
    'Si es verdadero, la cuenta nunca puede quedar con saldo negativo (I5). '
    'Es el invariante que cruza la frontera del aggregate y el objeto central '
    'del experimento.';

-- ---------------------------------------------------------------------------
-- entries. Asiento contable. Append only.
-- ---------------------------------------------------------------------------
CREATE TABLE entries (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    idempotency_key      UUID        NOT NULL,
    request_fingerprint  BYTEA       NOT NULL,
    currency             CHAR(3)     NOT NULL,
    metadata             JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT entries_idempotency_key_uq UNIQUE (idempotency_key),
    CONSTRAINT entries_currency_ck CHECK (currency ~ '^[A-Z]{3}$')
);

COMMENT ON COLUMN entries.request_fingerprint IS
    'SHA-256 del cuerpo canonico de la solicitud. Permite distinguir un '
    'reintento legitimo (I3) de una reutilizacion indebida de la clave.';

-- ---------------------------------------------------------------------------
-- postings. Lineas del asiento. Append only.
-- ---------------------------------------------------------------------------
CREATE TABLE postings (
    id           BIGSERIAL PRIMARY KEY,
    entry_id     UUID   NOT NULL REFERENCES entries (id),
    account_id   UUID   NOT NULL REFERENCES accounts (id),
    amount_minor BIGINT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT postings_amount_nonzero_ck CHECK (amount_minor <> 0)
);

CREATE INDEX postings_entry_idx   ON postings (entry_id);
CREATE INDEX postings_account_idx ON postings (account_id, id);

-- ---------------------------------------------------------------------------
-- balances. Proyeccion materializada. Reconstruible desde postings (I6).
-- ---------------------------------------------------------------------------
CREATE TABLE balances (
    account_id      UUID   PRIMARY KEY REFERENCES accounts (id),
    amount_minor    BIGINT NOT NULL DEFAULT 0,
    last_posting_id BIGINT,
    version         BIGINT NOT NULL DEFAULT 0
);

-- ---------------------------------------------------------------------------
-- outbox. Publicacion fiable sin dual write (I7).
-- ---------------------------------------------------------------------------
CREATE TABLE outbox (
    id           BIGSERIAL PRIMARY KEY,
    entry_id     UUID        NOT NULL REFERENCES entries (id),
    payload      JSONB       NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    claimed_at   TIMESTAMPTZ,
    delivered_at TIMESTAMPTZ,
    attempts     INT         NOT NULL DEFAULT 0
);

CREATE INDEX outbox_pending_idx ON outbox (id) WHERE delivered_at IS NULL;

-- ---------------------------------------------------------------------------
-- I1. Todo asiento suma cero y tiene al menos dos postings.
--
-- Se implementa como CONSTRAINT TRIGGER diferido. Un CHECK de tabla no sirve
-- porque la invariante es sobre el conjunto de filas del asiento, no sobre una
-- fila, y durante la insercion el asiento esta transitoriamente desbalanceado.
-- ---------------------------------------------------------------------------
CREATE FUNCTION assert_entry_balanced() RETURNS TRIGGER AS $$
DECLARE
    v_sum   BIGINT;
    v_count INT;
BEGIN
    SELECT COALESCE(SUM(amount_minor), 0), COUNT(*)
      INTO v_sum, v_count
      FROM postings
     WHERE entry_id = NEW.entry_id;

    IF v_count < 2 THEN
        RAISE EXCEPTION 'I1 violada. El asiento % tiene % postings, se requieren al menos dos',
            NEW.entry_id, v_count
            USING ERRCODE = 'check_violation';
    END IF;

    IF v_sum <> 0 THEN
        RAISE EXCEPTION 'I1 violada. El asiento % suma % en lugar de cero',
            NEW.entry_id, v_sum
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER trg_entry_balanced
    AFTER INSERT ON postings
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION assert_entry_balanced();

-- Un trigger sobre postings no se ejecuta cuando un asiento no tiene ninguna
-- línea. Esta comprobación diferida sobre entries cierra ese hueco al commit.
CREATE FUNCTION assert_entry_has_minimum_postings() RETURNS TRIGGER AS $$
DECLARE
    v_count INT;
BEGIN
    SELECT COUNT(*)
      INTO v_count
      FROM postings
     WHERE entry_id = NEW.id;

    IF v_count < 2 THEN
        RAISE EXCEPTION 'I1 violada. El asiento % tiene % postings, se requieren al menos dos',
            NEW.id, v_count
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER trg_entry_has_minimum_postings
    AFTER INSERT ON entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION assert_entry_has_minimum_postings();

-- ---------------------------------------------------------------------------
-- Coherencia de moneda. Un posting solo puede tocar una cuenta de la misma
-- moneda que su asiento. El cambio de divisa se modela con cuatro postings y
-- cuentas puente, nunca con un asiento de moneda mixta.
-- ---------------------------------------------------------------------------
CREATE FUNCTION assert_posting_currency() RETURNS TRIGGER AS $$
DECLARE
    v_entry_currency   CHAR(3);
    v_account_currency CHAR(3);
BEGIN
    SELECT currency INTO v_entry_currency   FROM entries  WHERE id = NEW.entry_id;
    SELECT currency INTO v_account_currency FROM accounts WHERE id = NEW.account_id;

    IF v_entry_currency <> v_account_currency THEN
        RAISE EXCEPTION 'Moneda incoherente. Asiento % en % contra cuenta % en %',
            NEW.entry_id, v_entry_currency, NEW.account_id, v_account_currency
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_posting_currency
    BEFORE INSERT ON postings
    FOR EACH ROW EXECUTE FUNCTION assert_posting_currency();

-- ---------------------------------------------------------------------------
-- I4. Inmutabilidad. Ni entries ni postings admiten UPDATE o DELETE.
-- Una correccion es un asiento de reverso, nunca una edicion.
--
-- El trigger respeta session_replication_role = 'replica', lo que permite al
-- inyector de fallos del arnes producir violaciones deliberadas para demostrar
-- que el verificador las detecta. Fuera de ese uso, el rol de aplicacion no
-- debe tener permiso para cambiar session_replication_role.
-- ---------------------------------------------------------------------------
CREATE FUNCTION reject_mutation() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'I4 violada. La tabla % es append only, operacion % rechazada',
        TG_TABLE_NAME, TG_OP
        USING ERRCODE = 'check_violation';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_entries_immutable
    BEFORE UPDATE OR DELETE ON entries
    FOR EACH ROW EXECUTE FUNCTION reject_mutation();

CREATE TRIGGER trg_postings_immutable
    BEFORE UPDATE OR DELETE ON postings
    FOR EACH ROW EXECUTE FUNCTION reject_mutation();

-- ---------------------------------------------------------------------------
-- Vistas de apoyo para el verificador externo.
-- Son de solo lectura y no participan en la ruta de escritura.
-- ---------------------------------------------------------------------------
CREATE VIEW v_entry_sums AS
    SELECT e.id AS entry_id,
           e.currency,
           COALESCE(SUM(p.amount_minor), 0) AS sum_minor,
           COUNT(p.id)                      AS posting_count
      FROM entries e
      LEFT JOIN postings p ON p.entry_id = e.id
     GROUP BY e.id, e.currency;

CREATE VIEW v_recomputed_balances AS
    SELECT a.id AS account_id,
           a.currency,
           a.constrained,
           COALESCE(SUM(p.amount_minor), 0) AS amount_minor,
           MAX(p.id)                        AS last_posting_id
      FROM accounts a
      LEFT JOIN postings p ON p.account_id = a.id
     GROUP BY a.id, a.currency, a.constrained;

COMMIT;
