-- Semilla de desarrollo. Capital inicial y mil cuentas de cliente.
-- La cuenta de capital no es constrained, las de cliente si lo son, de modo
-- que I5 sea una restriccion real y no decorativa.

BEGIN;

INSERT INTO accounts (id, name, currency, kind, constrained) VALUES
    ('00000000-0000-0000-0000-000000000001', 'capital',       'PEN', 'equity',    FALSE),
    ('00000000-0000-0000-0000-000000000002', 'redondeo',      'PEN', 'revenue',   FALSE),
    ('00000000-0000-0000-0000-000000000003', 'fx_puente_pen', 'PEN', 'liability', FALSE),
    ('00000000-0000-0000-0000-000000000004', 'fx_puente_usd', 'USD', 'liability', FALSE);

INSERT INTO accounts (id, name, currency, kind, constrained)
SELECT gen_random_uuid(), 'cliente_' || i, 'PEN', 'liability', TRUE
  FROM generate_series(1, 1000) AS i;

INSERT INTO balances (account_id, amount_minor)
SELECT id, 0 FROM accounts
ON CONFLICT (account_id) DO NOTHING;

COMMIT;
