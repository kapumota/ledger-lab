// Comando workload. Genera carga contra el contrato y escribe resultados
// reproducibles en CSV y JSON.
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kapumota/ledger-lab/harness/internal/workload"
)

func main() {
	var (
		base         = flag.String("base-url", "http://localhost:8080", "URL base del servicio bajo prueba")
		dsn          = flag.String("dsn", os.Getenv("DATABASE_URL"), "cadena de conexion, para leer las cuentas")
		perfil       = flag.String("profile", "uniform", "uniform, hot1, hot10, fan_in, fan_out")
		concurrencia = flag.Int("concurrency", 50, "clientes simultaneos")
		duracion     = flag.Duration("duration", 30*time.Second, "duracion de la campana")
		monto        = flag.Int64("amount", 100, "importe de cada transferencia en unidades minimas")
		duplicados   = flag.Int("duplicate-pct", 0, "porcentaje de solicitudes reenviadas con la misma clave")
		semilla      = flag.Int64("seed", 1, "semilla del generador")
		moneda       = flag.String("currency", "PEN", "moneda de la campana")
		fondeo       = flag.Int64("fund", 0, "si es mayor que cero, fondea cada cuenta con este importe antes de medir")
		salidaJSON   = flag.String("out-json", "", "ruta del resumen JSON")
		salidaCSV    = flag.String("out-csv", "", "ruta del CSV de latencias crudas en microsegundos")
		etiqueta     = flag.String("label", "", "etiqueta libre de la corrida, por ejemplo go_serializable")
	)
	flag.Parse()

	if *dsn == "" {
		fmt.Fprintln(os.Stderr, "se requiere -dsn o DATABASE_URL")
		os.Exit(2)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "conexion: %v\n", err)
		os.Exit(2)
	}
	defer pool.Close()

	cuentas, fondeador, err := cargarCuentas(ctx, pool, *moneda)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cuentas: %v\n", err)
		os.Exit(2)
	}
	fmt.Printf("cuentas de cliente=%d fondeador=%s\n", len(cuentas), fondeador)

	if *fondeo > 0 {
		if err := fondearTodas(*base, *moneda, fondeador, cuentas, *fondeo); err != nil {
			fmt.Fprintf(os.Stderr, "fondeo: %v\n", err)
			os.Exit(2)
		}
		fmt.Printf("fondeadas %d cuentas con %d\n", len(cuentas), *fondeo)
	}

	cfg := workload.Config{
		BaseURL:      *base,
		Accounts:     cuentas,
		Funder:       fondeador,
		Currency:     *moneda,
		Profile:      workload.Profile(*perfil),
		Concurrency:  *concurrencia,
		Duration:     *duracion,
		Amount:       *monto,
		DuplicatePct: *duplicados,
		Seed:         *semilla,
	}

	res, err := workload.Run(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "campana: %v\n", err)
		os.Exit(2)
	}

	fmt.Printf("\netiqueta=%s perfil=%s concurrencia=%d\n", *etiqueta, res.Profile, res.Concurrency)
	fmt.Printf("enviadas=%d creadas=%d duplicadas=%d rechazadas=%d conflictos=%d inciertas=%d errores=%d\n",
		res.Sent, res.Created, res.Duplicated, res.Rejected, res.Conflicts, res.Uncertain, res.Errors)
	fmt.Printf("throughput=%.1f/s p50=%v p95=%v p99=%v max=%v\n",
		res.Throughput, res.P50, res.P95, res.P99, res.Max)

	if *salidaJSON != "" {
		b, _ := json.MarshalIndent(struct {
			Label string `json:"label"`
			workload.Result
		}{*etiqueta, res}, "", "  ")
		if err := os.WriteFile(*salidaJSON, b, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "no se pudo escribir el JSON: %v\n", err)
		}
	}

	if *salidaCSV != "" {
		f, err := os.Create(*salidaCSV)
		if err != nil {
			fmt.Fprintf(os.Stderr, "no se pudo crear el CSV: %v\n", err)
			os.Exit(2)
		}
		defer f.Close()
		w := csv.NewWriter(f)
		defer w.Flush()
		_ = w.Write([]string{"label", "profile", "concurrency", "latency_us"})
		for _, us := range res.LatenciasRaw {
			_ = w.Write([]string{*etiqueta, res.Profile,
				strconv.Itoa(res.Concurrency), strconv.FormatInt(us, 10)})
		}
	}
}

// cargarCuentas lee las cuentas de cliente y una cuenta sin restriccion que
// sirva como origen de fondeo.
func cargarCuentas(ctx context.Context, pool *pgxpool.Pool, moneda string) ([]string, string, error) {
	rows, err := pool.Query(ctx, `
		SELECT id::text FROM accounts
		 WHERE currency = $1 AND constrained
		 ORDER BY id`, moneda)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var cuentas []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, "", err
		}
		cuentas = append(cuentas, id)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var fondeador string
	err = pool.QueryRow(ctx, `
		SELECT id::text FROM accounts
		 WHERE currency = $1 AND NOT constrained AND kind = 'equity'
		 LIMIT 1`, moneda).Scan(&fondeador)
	if err != nil {
		return nil, "", fmt.Errorf("no hay cuenta de capital para %s: %w", moneda, err)
	}
	if len(cuentas) < 2 {
		return nil, "", fmt.Errorf("se requieren al menos dos cuentas de cliente, hay %d", len(cuentas))
	}
	return cuentas, fondeador, nil
}
