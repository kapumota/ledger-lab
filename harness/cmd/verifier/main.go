// Comando verifier. Comprueba los invariantes del ledger contra la base.
//
// Devuelve codigo de salida 0 si no hay violaciones y 1 si las hay, de modo que
// pueda usarse como puerta en integracion continua y como criterio de admision
// de una corrida experimental.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kapumota/ledger-lab/harness/internal/verify"
)

func main() {
	var (
		dsn      = flag.String("dsn", os.Getenv("DATABASE_URL"), "cadena de conexion")
		historia = flag.String("history", "", "ruta de history.ndjson para contraste opcional")
		salida   = flag.String("json", "", "ruta del informe JSON, vacio para no escribirlo")
		quiet    = flag.Bool("quiet", false, "solo imprimir violaciones")
	)
	flag.Parse()

	if *dsn == "" {
		fmt.Fprintln(os.Stderr, "se requiere -dsn o DATABASE_URL")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "conexion: %v\n", err)
		os.Exit(2)
	}
	defer pool.Close()

	rep, err := verify.Run(ctx, pool)
	if err != nil {
		fmt.Fprintf(os.Stderr, "verificacion: %v\n", err)
		os.Exit(2)
	}

	if *historia != "" {
		f, err := os.Open(*historia)
		if err != nil {
			fmt.Fprintf(os.Stderr, "historia: %v\n", err)
			os.Exit(2)
		}
		check, violations, err := verify.CheckHistory(ctx, pool, f)
		_ = f.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "historia: %v\n", err)
			os.Exit(2)
		}
		rep.Checks = append(rep.Checks, check)
		rep.Violations = append(rep.Violations, violations...)
	}

	if !*quiet {
		for _, c := range rep.Checks {
			estado := "ok"
			if c.Violations > 0 {
				estado = "FALLA"
			}
			fmt.Printf("%-3s %-45s inspeccionados=%-8d violaciones=%-4d %s\n",
				c.Invariant, c.Name, c.Inspected, c.Violations, estado)
		}
	}

	for _, v := range rep.Violations {
		fmt.Printf("VIOLACION %s sujeto=%s %s\n", v.Invariant, v.Subject, v.Detail)
	}

	if *salida != "" {
		b, _ := json.MarshalIndent(rep, "", "  ")
		if err := os.WriteFile(*salida, b, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "no se pudo escribir el informe: %v\n", err)
			os.Exit(2)
		}
	}

	if !rep.OK() {
		fmt.Printf("\nresultado: %d violaciones\n", len(rep.Violations))
		os.Exit(1)
	}
	if !*quiet {
		fmt.Println("\nresultado: sin violaciones")
	}
}
