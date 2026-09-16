package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kapumota/ledger-lab/go/internal/domain"
	"github.com/kapumota/ledger-lab/go/internal/money"
	"github.com/kapumota/ledger-lab/go/internal/store"
)

// Estas pruebas requieren una base real. Sin DATABASE_URL se omiten, de modo
// que go test siga siendo util sin infraestructura, pero la integracion
// continua debe definir la variable.
func dsn(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL no definida, se omite la prueba de integracion")
	}
	return v
}

func nuevoPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 32
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// crearCuenta inserta una cuenta y su fila de saldo.
func crearCuenta(t *testing.T, pool *pgxpool.Pool, nombre string, restringida bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		INSERT INTO accounts (id, name, currency, kind, constrained)
		VALUES ($1, $2, 'PEN', $3, $4)`,
		id, fmt.Sprintf("%s_%s", nombre, id.String()[:8]),
		map[bool]string{true: "liability", false: "equity"}[restringida], restringida)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO balances (account_id) VALUES ($1)`, id); err != nil {
		t.Fatal(err)
	}
	return id
}

// fondear acredita una cuenta desde una cuenta de capital sin restriccion.
func fondear(t *testing.T, st *store.Store, capital, destino uuid.UUID, monto money.Minor) {
	t.Helper()
	e, err := domain.NewTransfer(uuid.New(), "PEN", capital, destino, monto)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PostEntry(context.Background(), e); err != nil {
		t.Fatalf("no se pudo fondear: %v", err)
	}
}

func TestTransferenciaAtomica(t *testing.T) {
	pool := nuevoPool(t)
	st := store.New(pool, store.DefaultConfig())
	ctx := context.Background()

	capital := crearCuenta(t, pool, "capital", false)
	a := crearCuenta(t, pool, "a", true)
	b := crearCuenta(t, pool, "b", true)
	fondear(t, st, capital, a, 100_00)

	e, err := domain.NewTransfer(uuid.New(), "PEN", a, b, 40_00)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PostEntry(ctx, e); err != nil {
		t.Fatalf("la transferencia debio confirmarse: %v", err)
	}

	sa, _ := st.Balance(ctx, a)
	sb, _ := st.Balance(ctx, b)
	if sa != 60_00 || sb != 40_00 {
		t.Fatalf("saldos incorrectos: a=%d b=%d", sa, sb)
	}

	// I6. La proyeccion coincide con el recomputo desde postings.
	ra, _ := st.RecomputedBalance(ctx, a)
	rb, _ := st.RecomputedBalance(ctx, b)
	if ra != sa || rb != sb {
		t.Fatalf("I6 violada: proyeccion a=%d b=%d, recomputo a=%d b=%d", sa, sb, ra, rb)
	}
}

func TestSaldoInsuficienteRechazado(t *testing.T) {
	pool := nuevoPool(t)
	st := store.New(pool, store.DefaultConfig())
	ctx := context.Background()

	a := crearCuenta(t, pool, "a", true)
	b := crearCuenta(t, pool, "b", true)

	e, _ := domain.NewTransfer(uuid.New(), "PEN", a, b, 1_00)
	_, err := st.PostEntry(ctx, e)
	if !errors.Is(err, store.ErrInsufficientFunds) {
		t.Fatalf("se esperaba ErrInsufficientFunds, se obtuvo %v", err)
	}

	// El rechazo debe ser total. Ningun posting debe haber quedado escrito.
	sa, _ := st.RecomputedBalance(ctx, a)
	if sa != 0 {
		t.Fatalf("I5 fallida, quedo escritura parcial: %d", sa)
	}
}

// I3. La misma clave enviada N veces produce exactamente un asiento.
func TestIdempotenciaBajoDuplicacionConcurrente(t *testing.T) {
	pool := nuevoPool(t)
	st := store.New(pool, store.DefaultConfig())
	ctx := context.Background()

	capital := crearCuenta(t, pool, "capital", false)
	a := crearCuenta(t, pool, "a", true)
	b := crearCuenta(t, pool, "b", true)
	fondear(t, st, capital, a, 1000_00)

	key := uuid.New()
	const n = 20

	var wg sync.WaitGroup
	ids := make([]uuid.UUID, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Se reconstruye el asiento en cada intento, como haria un cliente
			// que reintenta tras perder la respuesta.
			e := domain.Entry{
				ID:             uuid.New(),
				IdempotencyKey: key,
				Currency:       "PEN",
				Postings: []domain.Posting{
					{AccountID: a, Amount: -50_00},
					{AccountID: b, Amount: 50_00},
				},
			}
			res, err := st.PostEntry(ctx, e)
			ids[i], errs[i] = res.EntryID, err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("intento %d fallo: %v", i, err)
		}
		if ids[i] != ids[0] {
			t.Fatalf("intento %d devolvio otro asiento: %s contra %s", i, ids[i], ids[0])
		}
	}

	sb, _ := st.Balance(ctx, b)
	if sb != 50_00 {
		t.Fatalf("I3 violada. El efecto se aplico %d veces", sb/50_00)
	}
}

func TestConflictoDeIdempotencia(t *testing.T) {
	pool := nuevoPool(t)
	st := store.New(pool, store.DefaultConfig())
	ctx := context.Background()

	capital := crearCuenta(t, pool, "capital", false)
	a := crearCuenta(t, pool, "a", true)
	b := crearCuenta(t, pool, "b", true)
	fondear(t, st, capital, a, 1000_00)

	key := uuid.New()
	e1, _ := domain.NewTransfer(key, "PEN", a, b, 10_00)
	if _, err := st.PostEntry(ctx, e1); err != nil {
		t.Fatal(err)
	}

	// Misma clave, importe distinto. Es reutilizacion indebida, no reintento.
	e2, _ := domain.NewTransfer(key, "PEN", a, b, 20_00)
	if _, err := st.PostEntry(ctx, e2); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("se esperaba ErrIdempotencyConflict, se obtuvo %v", err)
	}
}

// Criterio de aceptacion de la Fase B. Doscientas transferencias concurrentes
// sobre una cuenta caliente preservan I2 e I5 bajo la configuracion correcta.
func TestConcurrenciaSobreCuentaCaliente(t *testing.T) {
	pool := nuevoPool(t)
	st := store.New(pool, store.DefaultConfig())
	ctx := context.Background()

	capital := crearCuenta(t, pool, "capital", false)
	caliente := crearCuenta(t, pool, "caliente", true)
	fondear(t, st, capital, caliente, 200_00)

	const n = 200
	destinos := make([]uuid.UUID, n)
	for i := range destinos {
		destinos[i] = crearCuenta(t, pool, "dst", true)
	}

	var wg sync.WaitGroup
	var confirmadas, rechazadas int64
	var mu sync.Mutex

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e, err := domain.NewTransfer(uuid.New(), "PEN", caliente, destinos[i], 1_00)
			if err != nil {
				t.Error(err)
				return
			}
			_, err = st.PostEntry(ctx, e)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				confirmadas++
			case errors.Is(err, store.ErrInsufficientFunds):
				rechazadas++
			default:
				t.Errorf("error inesperado: %v", err)
			}
		}(i)
	}
	wg.Wait()

	// I5. La cuenta caliente nunca queda negativa.
	saldo, _ := st.Balance(ctx, caliente)
	if saldo < 0 {
		t.Fatalf("I5 violada. La cuenta caliente quedo en %d", saldo)
	}

	// I6. La proyeccion coincide con el recomputo.
	recomputo, _ := st.RecomputedBalance(ctx, caliente)
	if saldo != recomputo {
		t.Fatalf("I6 violada. Proyeccion %d contra recomputo %d", saldo, recomputo)
	}

	// I2. Conservacion. El total movido desde la cuenta caliente coincide con
	// lo recibido por los destinos.
	var totalDestinos money.Minor
	for _, d := range destinos {
		v, _ := st.RecomputedBalance(ctx, d)
		totalDestinos += v
	}
	if totalDestinos != money.Minor(confirmadas*100) {
		t.Fatalf("I2 violada. Confirmadas %d, recibido %d", confirmadas, totalDestinos)
	}

	t.Logf("confirmadas=%d rechazadas=%d saldo_final=%d", confirmadas, rechazadas, saldo)
}

// Falsacion. La estrategia weak debe producir violaciones de I5 bajo
// concurrencia. Si no las produce, el arnes no esta ejerciendo presion
// suficiente y las conclusiones de la campana no valen nada.
//
// La prueba no falla cuando detecta la violacion, la registra. Es informativa
// por naturaleza, porque la ventana de carrera depende de la maquina.
func TestFalsacionEstrategiaDebil(t *testing.T) {
	if os.Getenv("LEDGER_LAB_FALSACION") == "" {
		t.Skip("definir LEDGER_LAB_FALSACION para ejecutar la campana de falsacion")
	}
	pool := nuevoPool(t)
	cfg := store.DefaultConfig()
	cfg.Strategy = store.StrategyWeak
	cfg.Isolation = pgx.ReadCommitted
	st := store.New(pool, cfg)
	ctx := context.Background()

	capital := crearCuenta(t, pool, "capital", false)
	caliente := crearCuenta(t, pool, "caliente", true)
	fondear(t, st, capital, caliente, 10_00)

	const n = 100
	var wg sync.WaitGroup
	inicio := make(chan struct{})
	for i := 0; i < n; i++ {
		destino := crearCuenta(t, pool, "dst", true)
		wg.Add(1)
		go func(d uuid.UUID) {
			defer wg.Done()
			<-inicio // arranque simultaneo, para maximizar la ventana de carrera
			e, _ := domain.NewTransfer(uuid.New(), "PEN", caliente, d, 1_00)
			_, _ = st.PostEntry(ctx, e)
		}(destino)
	}
	close(inicio)
	wg.Wait()

	saldo, _ := st.Balance(ctx, caliente)
	if saldo < 0 {
		t.Logf("falsacion exitosa. La estrategia weak dejo la cuenta en %d, "+
			"el verificador externo debe detectarlo", saldo)
	} else {
		t.Logf("la estrategia weak no produjo violacion en esta ejecucion, saldo %d. "+
			"Aumentar concurrencia o reducir el fondeo inicial", saldo)
	}
	_ = time.Now
}
