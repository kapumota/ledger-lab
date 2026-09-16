BEGIN;

DROP VIEW IF EXISTS v_recomputed_balances;
DROP VIEW IF EXISTS v_entry_sums;

DROP TRIGGER IF EXISTS trg_postings_immutable ON postings;
DROP TRIGGER IF EXISTS trg_entries_immutable  ON entries;
DROP TRIGGER IF EXISTS trg_posting_currency   ON postings;
DROP TRIGGER IF EXISTS trg_entry_balanced              ON postings;
DROP TRIGGER IF EXISTS trg_entry_has_minimum_postings ON entries;

DROP FUNCTION IF EXISTS reject_mutation();
DROP FUNCTION IF EXISTS assert_posting_currency();
DROP FUNCTION IF EXISTS assert_entry_balanced();
DROP FUNCTION IF EXISTS assert_entry_has_minimum_postings();

DROP TABLE IF EXISTS outbox;
DROP TABLE IF EXISTS balances;
DROP TABLE IF EXISTS postings;
DROP TABLE IF EXISTS entries;
DROP TABLE IF EXISTS accounts;

COMMIT;
