// Comando ledgerd. Implementacion de referencia del contrato ledger-lab.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kapumota/ledger-lab/go/internal/httpapi"
	"github.com/kapumota/ledger-lab/go/internal/store"
)

func main() {
	var (
		addr       = flag.String("addr", ":8080", "direccion de escucha")
		dsn        = flag.String("dsn", os.Getenv("DATABASE_URL"), "cadena de conexion a PostgreSQL")
		estrategia = flag.String("strategy", string(store.StrategyOrderedLocks),
			"estrategia de serializacion: ordered_locks, optimistic, weak")
		aislamiento = flag.String("isolation", "serializable",
			"nivel de aislamiento: serializable, repeatable_read, read_committed")
		reintentos = flag.Int("max-retries", 10, "reintentos ante conflicto de serializacion")
		maxConns   = flag.Int("max-conns", 32, "tamano maximo del pool")
		outbox     = flag.Bool("outbox", true, "emitir a la tabla outbox en la misma transaccion")
	)
	flag.Parse()

	if *dsn == "" {
		log.Fatal("se requiere -dsn o la variable DATABASE_URL")
	}

	cfg := store.Config{
		Strategy:   store.Strategy(*estrategia),
		Isolation:  parseAislamiento(*aislamiento),
		MaxRetries: *reintentos,
		EmitOutbox: *outbox,
	}
	if cfg.Strategy == store.StrategyWeak {
		log.Println("AVISO. Estrategia weak activa. Esta configuracion viola I5 bajo concurrencia " +
			"de forma deliberada y solo debe usarse en campanas de falsacion del verificador.")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	poolCfg, err := pgxpool.ParseConfig(*dsn)
	if err != nil {
		log.Fatalf("dsn invalido: %v", err)
	}
	poolCfg.MaxConns = int32(*maxConns)

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		log.Fatalf("no se pudo abrir el pool: %v", err)
	}
	defer pool.Close()

	if err := esperarBase(ctx, pool, 30*time.Second); err != nil {
		log.Fatalf("la base no respondio: %v", err)
	}

	st := store.New(pool, cfg)
	srv := &http.Server{
		Addr:              *addr,
		Handler:           httpapi.New(st).Routes(),
		ReadHeaderTimeout: httpapi.ReadHeaderTimeout,
	}

	go func() {
		log.Printf("ledgerd escuchando en %s. estrategia=%s aislamiento=%s",
			*addr, cfg.Strategy, cfg.Isolation)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("servidor detenido: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("apagando")
	cierre, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(cierre)
}

func parseAislamiento(s string) pgx.TxIsoLevel {
	switch s {
	case "serializable":
		return pgx.Serializable
	case "repeatable_read":
		return pgx.RepeatableRead
	case "read_committed":
		return pgx.ReadCommitted
	default:
		log.Fatalf("nivel de aislamiento desconocido: %s", s)
		return pgx.Serializable
	}
}

func esperarBase(ctx context.Context, pool *pgxpool.Pool, limite time.Duration) error {
	deadline := time.Now().Add(limite)
	for {
		if err := pool.Ping(ctx); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("tiempo de espera agotado")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
