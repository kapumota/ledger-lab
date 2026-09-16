// Comando injector. Produce violaciones deliberadas de los invariantes.
//
// Existe por una sola razon. Un verificador que nunca reporta nada no es
// evidencia de corrección, es un programa que no hace nada. Antes de aceptar
// cualquier conclusion de una campana hay que demostrar que el verificador
// detecta violaciones reales cuando las hay.
//
// El flujo de la Fase D es el siguiente.
//
//  1. base limpia, verifier debe salir con codigo 0
//  2. injector -kind=<invariante>
//  3. verifier debe salir con codigo 1 y nombrar el invariante inyectado
//  4. restaurar la base antes de cualquier medicion
//
// El inyector desactiva los triggers mediante session_replication_role. Ese
// privilegio pertenece al arnes y nunca al rol de aplicacion.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	var (
		dsn  = flag.String("dsn", os.Getenv("DATABASE_URL"), "cadena de conexion")
		kind = flag.String("kind", "", "invariante a violar: i1, i4, i5, i6")
		si   = flag.Bool("yes", false, "confirmar. El inyector corrompe datos a proposito")
	)
	flag.Parse()

	if *dsn == "" || *kind == "" {
		fmt.Fprintln(os.Stderr, "uso: injector -dsn=... -kind=i1|i4|i5|i6 -yes")
		os.Exit(2)
	}
	if !*si {
		fmt.Fprintln(os.Stderr, "este comando corrompe la base a proposito. Repetir con -yes")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "conexion: %v\n", err)
		os.Exit(2)
	}
	defer pool.Close()

	var fn func(context.Context, pgx.Tx) (string, error)
	switch *kind {
	case "i1":
		fn = inyectarI1
	case "i4":
		fn = inyectarI4
	case "i5":
		fn = inyectarI5
	case "i6":
		fn = inyectarI6
	default:
		fmt.Fprintf(os.Stderr, "invariante desconocido: %s\n", *kind)
		os.Exit(2)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "transaccion: %v\n", err)
		os.Exit(2)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Desactiva los triggers de usuario, incluido el que defiende I1 y el que
	// impide mutaciones. Sin esto la base rechazaria la inyeccion, que es
	// precisamente lo que se quiere en produccion.
	if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = 'replica'`); err != nil {
		fmt.Fprintf(os.Stderr, "no se pudieron desactivar los triggers: %v\n", err)
		os.Exit(2)
	}

	detalle, err := fn(ctx, tx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "inyeccion: %v\n", err)
		os.Exit(2)
	}
	if err := tx.Commit(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "commit: %v\n", err)
		os.Exit(2)
	}

	fmt.Printf("violacion de %s inyectada. %s\n", *kind, detalle)
	fmt.Println("ejecutar ahora el verifier. Debe salir con codigo 1 y nombrar el invariante.")
}

// inyectarI1 escribe un asiento que no suma cero.
func inyectarI1(ctx context.Context, tx pgx.Tx) (string, error) {
	var a, b string
	if err := tx.QueryRow(ctx, `
		SELECT id::text FROM accounts WHERE currency = 'PEN' ORDER BY id LIMIT 1`).Scan(&a); err != nil {
		return "", err
	}
	if err := tx.QueryRow(ctx, `
		SELECT id::text FROM accounts WHERE currency = 'PEN' AND id::text <> $1 ORDER BY id LIMIT 1`,
		a).Scan(&b); err != nil {
		return "", err
	}

	var entryID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO entries (idempotency_key, request_fingerprint, currency, metadata)
		VALUES (gen_random_uuid(), '\x00'::bytea, 'PEN', '{"injected":"i1"}'::jsonb)
		RETURNING id::text`).Scan(&entryID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO postings (entry_id, account_id, amount_minor) VALUES
			($1::uuid, $2::uuid, -10000),
			($1::uuid, $3::uuid,   9000)`, entryID, a, b); err != nil {
		return "", err
	}
	return fmt.Sprintf("asiento %s suma -1000", entryID), nil
}

// inyectarI4 muta un asiento existente, lo que el ledger prohibe.
func inyectarI4(ctx context.Context, tx pgx.Tx) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `SELECT id::text FROM entries ORDER BY created_at LIMIT 1`).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("se requiere al menos un asiento previo: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE entries SET metadata = metadata || '{"injected":"i4"}'::jsonb WHERE id = $1::uuid`,
		id); err != nil {
		return "", err
	}
	return fmt.Sprintf("asiento %s mutado, lo que I4 prohibe", id), nil
}

// inyectarI5 deja una cuenta restringida en negativo mediante un asiento
// balanceado, de modo que I1 siga cumpliendose y solo falle I5.
func inyectarI5(ctx context.Context, tx pgx.Tx) (string, error) {
	var restringida, contraparte string
	if err := tx.QueryRow(ctx, `
		SELECT id::text FROM accounts WHERE constrained AND currency = 'PEN' ORDER BY id LIMIT 1`).
		Scan(&restringida); err != nil {
		return "", err
	}
	if err := tx.QueryRow(ctx, `
		SELECT id::text FROM accounts WHERE NOT constrained AND currency = 'PEN' ORDER BY id LIMIT 1`).
		Scan(&contraparte); err != nil {
		return "", err
	}

	var entryID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO entries (idempotency_key, request_fingerprint, currency, metadata)
		VALUES (gen_random_uuid(), '\x00'::bytea, 'PEN', '{"injected":"i5"}'::jsonb)
		RETURNING id::text`).Scan(&entryID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO postings (entry_id, account_id, amount_minor) VALUES
			($1::uuid, $2::uuid, -999999999),
			($1::uuid, $3::uuid,  999999999)`, entryID, restringida, contraparte); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE balances SET amount_minor = amount_minor - 999999999 WHERE account_id = $1::uuid`,
		restringida); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE balances SET amount_minor = amount_minor + 999999999 WHERE account_id = $1::uuid`,
		contraparte); err != nil {
		return "", err
	}
	return fmt.Sprintf("cuenta restringida %s en negativo", restringida), nil
}

// inyectarI6 desincroniza la proyeccion respecto del log de postings.
func inyectarI6(ctx context.Context, tx pgx.Tx) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
		SELECT account_id::text FROM balances ORDER BY account_id LIMIT 1`).Scan(&id)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE balances SET amount_minor = amount_minor + 1, version = version + 1
		 WHERE account_id = $1::uuid`, id); err != nil {
		return "", err
	}
	return fmt.Sprintf("proyeccion de %s desviada en 1 respecto del recomputo", id), nil
}
