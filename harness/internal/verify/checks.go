// Package verify comprueba los invariantes del contrato directamente contra la
// base de datos.
//
// Regla arquitectonica del proyecto. Este paquete no importa nada de go/ ni de
// beam/. Si el verificador compartiera codigo con una implementacion, esa
// implementacion se estaria auditando a si misma y el resultado no seria
// evidencia de nada.
//
// Todas las comprobaciones son SQL puro sobre el estado persistido. Lo que no
// se pueda comprobar leyendo la base no es un invariante del ledger, es una
// propiedad del proceso.
package verify

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Violation es un incumplimiento concreto encontrado por una comprobacion.
type Violation struct {
	Invariant string `json:"invariant"`
	Subject   string `json:"subject"`
	Detail    string `json:"detail"`
}

// Report agrupa el resultado de una pasada completa.
type Report struct {
	Checks     []CheckResult `json:"checks"`
	Violations []Violation   `json:"violations"`
}

// CheckResult resume una comprobacion individual.
type CheckResult struct {
	Invariant  string `json:"invariant"`
	Name       string `json:"name"`
	Inspected  int64  `json:"inspected"`
	Violations int    `json:"violations"`
}

// OK indica si la pasada completa no encontro violaciones.
func (r Report) OK() bool { return len(r.Violations) == 0 }

// Check es una comprobacion ejecutable.
type Check struct {
	Invariant string
	Name      string
	Run       func(ctx context.Context, pool *pgxpool.Pool) (int64, []Violation, error)
}

// All devuelve el conjunto completo de comprobaciones.
func All() []Check {
	return []Check{
		{Invariant: "I1", Name: "asientos balanceados", Run: checkI1},
		{Invariant: "I2", Name: "conservacion del sistema", Run: checkI2},
		{Invariant: "I5", Name: "no negatividad de cuentas restringidas", Run: checkI5},
		{Invariant: "I6", Name: "proyeccion igual al recomputo", Run: checkI6},
		{Invariant: "I3", Name: "unicidad de clave de idempotencia", Run: checkI3},
		{Invariant: "I7", Name: "cobertura del outbox", Run: checkI7},
	}
}

// Run ejecuta todas las comprobaciones.
func Run(ctx context.Context, pool *pgxpool.Pool) (Report, error) {
	var rep Report
	for _, c := range All() {
		n, vs, err := c.Run(ctx, pool)
		if err != nil {
			return rep, fmt.Errorf("comprobacion %s (%s): %w", c.Invariant, c.Name, err)
		}
		rep.Checks = append(rep.Checks, CheckResult{
			Invariant:  c.Invariant,
			Name:       c.Name,
			Inspected:  n,
			Violations: len(vs),
		})
		rep.Violations = append(rep.Violations, vs...)
	}
	return rep, nil
}

// I1. Todo asiento suma cero y tiene al menos dos postings.
func checkI1(ctx context.Context, pool *pgxpool.Pool) (int64, []Violation, error) {
	var total int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM entries`).Scan(&total); err != nil {
		return 0, nil, err
	}
	rows, err := pool.Query(ctx, `
		SELECT entry_id, sum_minor, posting_count
		  FROM v_entry_sums
		 WHERE sum_minor <> 0 OR posting_count < 2`)
	if err != nil {
		return total, nil, err
	}
	defer rows.Close()

	var out []Violation
	for rows.Next() {
		var id string
		var suma int64
		var n int
		if err := rows.Scan(&id, &suma, &n); err != nil {
			return total, nil, err
		}
		out = append(out, Violation{
			Invariant: "I1", Subject: id,
			Detail: fmt.Sprintf("suma %d con %d postings", suma, n),
		})
	}
	return total, out, rows.Err()
}

// I2. Conservacion. La suma de todos los postings de una moneda es cero, porque
// todo asiento suma cero. Si no lo es, hay dinero creado o destruido en algun
// punto del sistema y I1 por si sola no lo habria mostrado cuando la escritura
// evadio el trigger.
func checkI2(ctx context.Context, pool *pgxpool.Pool) (int64, []Violation, error) {
	rows, err := pool.Query(ctx, `
		SELECT e.currency, COALESCE(SUM(p.amount_minor), 0), COUNT(p.id)
		  FROM postings p JOIN entries e ON e.id = p.entry_id
		 GROUP BY e.currency`)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()

	var total int64
	var out []Violation
	for rows.Next() {
		var cur string
		var suma, n int64
		if err := rows.Scan(&cur, &suma, &n); err != nil {
			return total, nil, err
		}
		total += n
		if suma != 0 {
			out = append(out, Violation{
				Invariant: "I2", Subject: cur,
				Detail: fmt.Sprintf("la suma global de la moneda es %d en lugar de cero", suma),
			})
		}
	}
	return total, out, rows.Err()
}

// I5. Ninguna cuenta restringida presenta saldo negativo, ni en la proyeccion
// ni en el recomputo desde postings.
func checkI5(ctx context.Context, pool *pgxpool.Pool) (int64, []Violation, error) {
	var total int64
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM accounts WHERE constrained`).Scan(&total); err != nil {
		return 0, nil, err
	}
	rows, err := pool.Query(ctx, `
		SELECT r.account_id, r.amount_minor, COALESCE(b.amount_minor, 0)
		  FROM v_recomputed_balances r
		  LEFT JOIN balances b ON b.account_id = r.account_id
		 WHERE r.constrained AND (r.amount_minor < 0 OR COALESCE(b.amount_minor, 0) < 0)`)
	if err != nil {
		return total, nil, err
	}
	defer rows.Close()

	var out []Violation
	for rows.Next() {
		var id string
		var recomputo, proyeccion int64
		if err := rows.Scan(&id, &recomputo, &proyeccion); err != nil {
			return total, nil, err
		}
		out = append(out, Violation{
			Invariant: "I5", Subject: id,
			Detail: fmt.Sprintf("saldo negativo. recomputo %d, proyeccion %d", recomputo, proyeccion),
		})
	}
	return total, out, rows.Err()
}

// I6. La proyeccion materializada coincide con el recomputo desde el log de
// postings. Debe evaluarse en quiescencia, porque durante la carga hay
// transacciones en vuelo.
func checkI6(ctx context.Context, pool *pgxpool.Pool) (int64, []Violation, error) {
	var total int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM balances`).Scan(&total); err != nil {
		return 0, nil, err
	}
	rows, err := pool.Query(ctx, `
		SELECT b.account_id, b.amount_minor, r.amount_minor
		  FROM balances b JOIN v_recomputed_balances r ON r.account_id = b.account_id
		 WHERE b.amount_minor <> r.amount_minor`)
	if err != nil {
		return total, nil, err
	}
	defer rows.Close()

	var out []Violation
	for rows.Next() {
		var id string
		var proyeccion, recomputo int64
		if err := rows.Scan(&id, &proyeccion, &recomputo); err != nil {
			return total, nil, err
		}
		out = append(out, Violation{
			Invariant: "I6", Subject: id,
			Detail: fmt.Sprintf("proyeccion %d contra recomputo %d, diferencia %d",
				proyeccion, recomputo, proyeccion-recomputo),
		})
	}
	return total, out, rows.Err()
}

// I3. Una clave de idempotencia identifica a lo sumo un asiento. La restriccion
// unica lo garantiza mientras exista, asi que esta comprobacion vigila que la
// restriccion siga en su sitio.
func checkI3(ctx context.Context, pool *pgxpool.Pool) (int64, []Violation, error) {
	var total int64
	if err := pool.QueryRow(ctx,
		`SELECT count(DISTINCT idempotency_key) FROM entries`).Scan(&total); err != nil {
		return 0, nil, err
	}
	rows, err := pool.Query(ctx, `
		SELECT idempotency_key::text, count(*)
		  FROM entries GROUP BY idempotency_key HAVING count(*) > 1`)
	if err != nil {
		return total, nil, err
	}
	defer rows.Close()

	var out []Violation
	for rows.Next() {
		var key string
		var n int64
		if err := rows.Scan(&key, &n); err != nil {
			return total, nil, err
		}
		out = append(out, Violation{
			Invariant: "I3", Subject: key,
			Detail: fmt.Sprintf("la clave produjo %d asientos", n),
		})
	}
	return total, out, rows.Err()
}

// I7. Todo asiento confirmado tiene fila de outbox. La entrega efectiva se
// comprueba contra el stream en la fase de reconciliacion, aqui solo se vigila
// la cobertura, que es la parte que si depende de la transaccion.
func checkI7(ctx context.Context, pool *pgxpool.Pool) (int64, []Violation, error) {
	var total int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM entries`).Scan(&total); err != nil {
		return 0, nil, err
	}
	// Si nunca se emitio outbox la comprobacion no aplica y no debe producir
	// ruido en las campanas que corren con outbox desactivado.
	var conOutbox int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox`).Scan(&conOutbox); err != nil {
		return total, nil, err
	}
	if conOutbox == 0 {
		return total, nil, nil
	}

	rows, err := pool.Query(ctx, `
		SELECT e.id::text
		  FROM entries e
		  LEFT JOIN outbox o ON o.entry_id = e.id
		 WHERE o.id IS NULL
		 LIMIT 100`)
	if err != nil {
		return total, nil, err
	}
	defer rows.Close()

	var out []Violation
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return total, nil, err
		}
		out = append(out, Violation{
			Invariant: "I7", Subject: id,
			Detail: "asiento confirmado sin fila de outbox",
		})
	}
	return total, out, rows.Err()
}
