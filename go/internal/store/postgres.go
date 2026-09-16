// Package store persiste asientos en PostgreSQL.
//
// Aqui vive la parte interesante del proyecto. El aggregate resuelve I1 por si
// solo, pero I5, la no negatividad de las cuentas restringidas, depende de una
// proyeccion acumulada y por tanto cruza la frontera del aggregate. La forma de
// resolver ese cruce es exactamente el factor que el experimento compara.
//
// Por eso la estrategia es configurable en lugar de estar fijada. Las
// estrategias debiles no son un descuido, son el grupo de control que demuestra
// que el verificador externo detecta violaciones reales.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kapumota/ledger-lab/go/internal/domain"
	"github.com/kapumota/ledger-lab/go/internal/money"
)

var (
	// ErrIdempotencyConflict indica reutilizacion de una clave con un cuerpo
	// distinto. No es un reintento, es un error del llamador.
	ErrIdempotencyConflict = errors.New("store: clave de idempotencia reutilizada con solicitud distinta")
	// ErrInsufficientFunds indica violacion de I5 impedida a tiempo.
	ErrInsufficientFunds = errors.New("store: saldo insuficiente en cuenta restringida")
	// ErrAccountNotFound indica una cuenta inexistente.
	ErrAccountNotFound = errors.New("store: cuenta inexistente")
	// ErrRetriesExhausted indica que se agotaron los reintentos por conflicto
	// de serializacion.
	ErrRetriesExhausted = errors.New("store: reintentos agotados por conflicto de serializacion")
)

// Strategy es la estrategia de serializacion de escrituras.
type Strategy string

const (
	// StrategyOrderedLocks toma SELECT FOR UPDATE sobre las cuentas afectadas
	// en orden determinista antes de escribir. Es pesimista y no produce
	// interbloqueos porque el orden de adquisicion es global.
	StrategyOrderedLocks Strategy = "ordered_locks"

	// StrategyOptimistic no bloquea y confia en el aislamiento serializable de
	// PostgreSQL, reintentando ante serialization_failure.
	StrategyOptimistic Strategy = "optimistic"

	// StrategyWeak comprueba el saldo antes de abrir la transaccion y no
	// bloquea. Es deliberadamente incorrecta bajo concurrencia. Existe para
	// producir violaciones de I5 reales con las que validar el verificador.
	// Nunca debe usarse fuera de una campana de falsacion.
	StrategyWeak Strategy = "weak"
)

// Config parametriza la implementacion de referencia.
type Config struct {
	Strategy   Strategy
	Isolation  pgx.TxIsoLevel // pgx.Serializable, pgx.RepeatableRead, pgx.ReadCommitted
	MaxRetries int
	EmitOutbox bool
}

// DefaultConfig es la configuracion correcta de referencia.
//
// La combinacion es bloqueo ordenado con READ COMMITTED, no con SERIALIZABLE.
// El motivo es empirico y esta documentado en docs/HALLAZGOS.md. Bajo
// SERIALIZABLE, un SELECT FOR UPDATE sobre una fila que otra transaccion
// confirmada modifico despues del snapshot no espera y reanuda, aborta con
// 40001. Sobre una cuenta caliente eso convierte el bloqueo pesimista en una
// fabrica de reintentos y agota el presupuesto de intentos sin que exista
// ningun problema de correccion.
//
// Las dos combinaciones coherentes son estas.
//
//	ordered_locks + read_committed   pesimista, el bloqueo aporta la exclusion
//	optimistic    + serializable     optimista, el motor aporta la exclusion
//
// Mezclarlas paga el costo de ambas y el beneficio de ninguna. Cualquier
// desviacion debe ser explicita y quedar registrada en la configuracion del
// experimento, porque es justamente uno de los factores medidos.
func DefaultConfig() Config {
	return Config{
		Strategy:   StrategyOrderedLocks,
		Isolation:  pgx.ReadCommitted,
		MaxRetries: 20,
		EmitOutbox: true,
	}
}

// Store es la implementacion de referencia sobre PostgreSQL.
type Store struct {
	pool *pgxpool.Pool
	cfg  Config

	// Metricas de la ruta de escritura. El arnes las lee para la comparacion.
	Metrics *Metrics
}

// Result describe el desenlace de PostEntry.
type Result struct {
	EntryID   uuid.UUID
	Duplicate bool // true si la clave de idempotencia ya existia con el mismo cuerpo
	Retries   int
	Elapsed   time.Duration
}

// New construye el Store.
func New(pool *pgxpool.Pool, cfg Config) *Store {
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 1
	}
	if cfg.Isolation == "" {
		cfg.Isolation = pgx.Serializable
	}
	if cfg.Strategy == "" {
		cfg.Strategy = StrategyOrderedLocks
	}
	return &Store{pool: pool, cfg: cfg, Metrics: NewMetrics()}
}

// PostEntry persiste un asiento de forma atomica e idempotente.
//
// Secuencia dentro de una unica transaccion.
//
//  1. insercion del asiento con ON CONFLICT sobre la clave de idempotencia
//  2. bloqueo ordenado de cuentas, si la estrategia lo requiere
//  3. insercion de los postings
//  4. actualizacion de la proyeccion de saldos
//  5. verificacion de I5 sobre el saldo resultante
//  6. insercion en outbox
//  7. commit, momento en el que dispara el trigger diferido que verifica I1
func (s *Store) PostEntry(ctx context.Context, e domain.Entry) (Result, error) {
	inicio := time.Now()

	if err := e.Validate(); err != nil {
		s.Metrics.Invalid.Add(1)
		return Result{}, err
	}
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}

	var res Result
	var err error

	for intento := 0; intento < s.cfg.MaxRetries; intento++ {
		res, err = s.intentar(ctx, e)
		res.Retries = intento
		res.Elapsed = time.Since(inicio)

		if err == nil {
			s.Metrics.Committed.Add(1)
			if res.Duplicate {
				s.Metrics.Duplicates.Add(1)
			}
			s.Metrics.observeLatency(res.Elapsed)
			return res, nil
		}
		if !esConflictoDeSerializacion(err) {
			s.Metrics.Failed.Add(1)
			return res, err
		}

		s.Metrics.Retries.Add(1)
		// Retroceso exponencial acotado con tope, suficiente para que el
		// experimento no degenere en sincronizacion de reintentos.
		espera := time.Duration(1<<uint(min(intento, 6))) * time.Millisecond
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-time.After(espera):
		}
	}

	s.Metrics.Failed.Add(1)
	return res, fmt.Errorf("%w tras %d intentos: %v", ErrRetriesExhausted, s.cfg.MaxRetries, err)
}

func (s *Store) intentar(ctx context.Context, e domain.Entry) (Result, error) {
	// La estrategia debil lee el saldo fuera de la transaccion. Es el grupo de
	// control. Bajo concurrencia produce violaciones de I5 y debe producirlas.
	if s.cfg.Strategy == StrategyWeak {
		if err := s.verificarSaldoFueraDeTransaccion(ctx, e); err != nil {
			return Result{}, err
		}
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: s.cfg.Isolation})
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	fp := e.Fingerprint()

	// 1. Asiento con idempotencia. ON CONFLICT DO NOTHING deja detectar el
	// reintento sin carrera adicional.
	var insertadoID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO entries (id, idempotency_key, request_fingerprint, currency, metadata)
		VALUES ($1, $2, $3, $4, COALESCE($5::jsonb, '{}'::jsonb))
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING id`,
		e.ID, e.IdempotencyKey, fp, string(e.Currency), metadataOrNil(e),
	).Scan(&insertadoID)

	if errors.Is(err, pgx.ErrNoRows) {
		// La clave ya existia. Se compara el fingerprint para distinguir un
		// reintento legitimo (I3) de una reutilizacion indebida.
		var existenteID uuid.UUID
		var existenteFP []byte
		if err := tx.QueryRow(ctx, `
			SELECT id, request_fingerprint FROM entries WHERE idempotency_key = $1`,
			e.IdempotencyKey,
		).Scan(&existenteID, &existenteFP); err != nil {
			return Result{}, err
		}
		if string(existenteFP) != string(fp) {
			return Result{}, ErrIdempotencyConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return Result{}, err
		}
		return Result{EntryID: existenteID, Duplicate: true}, nil
	}
	if err != nil {
		return Result{}, err
	}

	cuentas := e.Accounts()

	// 2. Bloqueo pesimista en orden determinista. El orden global evita el
	// interbloqueo entre transferencias cruzadas.
	if s.cfg.Strategy == StrategyOrderedLocks {
		rows, err := tx.Query(ctx, `
			SELECT account_id FROM balances
			WHERE account_id = ANY($1::uuid[])
			ORDER BY account_id
			FOR UPDATE`, cuentas)
		if err != nil {
			return Result{}, err
		}
		n := 0
		for rows.Next() {
			n++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return Result{}, err
		}
		if n != len(cuentas) {
			return Result{}, fmt.Errorf("%w: se bloquearon %d de %d cuentas", ErrAccountNotFound, n, len(cuentas))
		}
	}

	// 3. Postings.
	neto, err := e.NetByAccount()
	if err != nil {
		return Result{}, err
	}
	batch := &pgx.Batch{}
	for _, p := range e.Postings {
		batch.Queue(`
			INSERT INTO postings (entry_id, account_id, amount_minor)
			VALUES ($1, $2, $3)`, e.ID, p.AccountID, int64(p.Amount))
	}
	br := tx.SendBatch(ctx, batch)
	for _, posting := range e.Postings {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			if isMissingPostingAccount(err) {
				return Result{}, fmt.Errorf("%w: %s", ErrAccountNotFound, posting.AccountID)
			}
			return Result{}, err
		}
	}
	if err := br.Close(); err != nil {
		return Result{}, err
	}

	// 4 y 5. Proyeccion de saldos y verificacion de I5 sobre el resultado.
	// La comprobacion se hace sobre el saldo ya actualizado dentro de la misma
	// transaccion. Comprobar antes de escribir seria una carrera.
	for _, acc := range cuentas {
		delta := neto[acc]
		if delta == 0 {
			continue
		}
		var nuevoSaldo int64
		var restringida bool
		err := tx.QueryRow(ctx, `
			WITH upd AS (
				UPDATE balances
				   SET amount_minor = amount_minor + $2,
				       version      = version + 1
				 WHERE account_id = $1
				RETURNING account_id, amount_minor
			)
			SELECT upd.amount_minor, a.constrained
			  FROM upd JOIN accounts a ON a.id = upd.account_id`,
			acc, int64(delta),
		).Scan(&nuevoSaldo, &restringida)

		if errors.Is(err, pgx.ErrNoRows) {
			return Result{}, fmt.Errorf("%w: %s", ErrAccountNotFound, acc)
		}
		if err != nil {
			return Result{}, err
		}
		// La estrategia debil ya comprobo el saldo fuera de la transaccion y
		// deliberadamente no vuelve a comprobarlo aqui. Esa omision es la que
		// produce la violacion de I5 que el verificador debe encontrar.
		if s.cfg.Strategy != StrategyWeak && restringida && nuevoSaldo < 0 {
			s.Metrics.InsufficientFunds.Add(1)
			return Result{}, fmt.Errorf("%w: cuenta %s quedaria en %d", ErrInsufficientFunds, acc, nuevoSaldo)
		}
	}

	// 6. Outbox en la misma transaccion. Esta es la unica razon por la que el
	// outbox es fiable. Ni Broadway ni GenStage ni ningun worker cambian este
	// hecho, solo gestionan el relay posterior.
	if s.cfg.EmitOutbox {
		if _, err := tx.Exec(ctx, `
			INSERT INTO outbox (entry_id, payload)
			VALUES ($1, jsonb_build_object(
				'entry_id', $2::text,
				'currency', $3::text,
				'posting_count', $4::int))`,
			e.ID, e.ID.String(), string(e.Currency), len(e.Postings)); err != nil {
			return Result{}, err
		}
	}

	// 7. Commit. Aqui dispara el trigger diferido que vuelve a verificar I1.
	if err := tx.Commit(ctx); err != nil {
		return Result{}, err
	}
	return Result{EntryID: e.ID}, nil
}

// verificarSaldoFueraDeTransaccion implementa la comprobacion incorrecta a
// proposito. Entre la lectura y la escritura hay una ventana en la que otra
// transferencia puede consumir el saldo.
func (s *Store) verificarSaldoFueraDeTransaccion(ctx context.Context, e domain.Entry) error {
	neto, err := e.NetByAccount()
	if err != nil {
		return err
	}
	for acc, delta := range neto {
		if delta >= 0 {
			continue
		}
		var saldo int64
		var restringida bool
		err := s.pool.QueryRow(ctx, `
			SELECT b.amount_minor, a.constrained
			  FROM balances b JOIN accounts a ON a.id = b.account_id
			 WHERE b.account_id = $1`, acc).Scan(&saldo, &restringida)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrAccountNotFound, acc)
		}
		if err != nil {
			return err
		}
		if restringida && saldo+int64(delta) < 0 {
			return fmt.Errorf("%w: cuenta %s", ErrInsufficientFunds, acc)
		}
	}
	return nil
}

// Balance devuelve el saldo materializado de una cuenta.
func (s *Store) Balance(ctx context.Context, acc uuid.UUID) (money.Minor, error) {
	var v int64
	err := s.pool.QueryRow(ctx,
		`SELECT amount_minor FROM balances WHERE account_id = $1`, acc).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("%w: %s", ErrAccountNotFound, acc)
	}
	return money.Minor(v), err
}

// RecomputedBalance recalcula el saldo desde los postings. Sirve para I6 y no
// debe usarse en la ruta de escritura.
func (s *Store) RecomputedBalance(ctx context.Context, acc uuid.UUID) (money.Minor, error) {
	var v int64
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(p.amount_minor), 0)
		  FROM accounts a
		  LEFT JOIN postings p ON p.account_id = a.id
		 WHERE a.id = $1
		 GROUP BY a.id`, acc).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("%w: %s", ErrAccountNotFound, acc)
	}
	return money.Minor(v), err
}

func metadataOrNil(e domain.Entry) any {
	if len(e.Metadata) == 0 {
		return nil
	}
	return string(e.Metadata)
}

// isMissingPostingAccount reconoce exclusivamente la violación de clave
// foránea que indica que el account_id del posting no existe. No traduce
// otras violaciones de integridad para evitar ocultar errores distintos.
func isMissingPostingAccount(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == "23503" &&
		pgErr.ConstraintName == "postings_account_id_fkey"
}

// esConflictoDeSerializacion reconoce los codigos que PostgreSQL usa para
// indicar que la transaccion puede reintentarse tal cual.
func esConflictoDeSerializacion(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	switch pgErr.Code {
	case "40001": // serialization_failure
		return true
	case "40P01": // deadlock_detected
		return true
	default:
		return false
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
