// Package workload genera carga contra el contrato HTTP.
//
// Es neutral respecto del runtime. Apunta a una URL base y no sabe si detras
// hay Go o Elixir. Esa ignorancia es el requisito que hace comparables las dos
// mediciones.
package workload

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Profile es el perfil de contencion.
type Profile string

const (
	// Uniform elige origen y destino al azar entre todas las cuentas.
	Uniform Profile = "uniform"
	// Hot1 concentra el uno por ciento de las cuentas como origen.
	Hot1 Profile = "hot1"
	// Hot10 concentra el diez por ciento.
	Hot10 Profile = "hot10"
	// FanIn concentra un unico destino.
	FanIn Profile = "fan_in"
	// FanOut concentra un unico origen.
	FanOut Profile = "fan_out"
)

// Config parametriza una campana.
type Config struct {
	BaseURL      string
	Accounts     []string // identificadores de cuentas de cliente
	Funder       string   // cuenta sin restriccion, usada como origen de fondeo
	Currency     string
	Profile      Profile
	Concurrency  int
	Duration     time.Duration
	Amount       int64
	DuplicatePct int   // porcentaje de solicitudes reenviadas con la misma clave
	Seed         int64 // semilla, para que la campana sea reproducible
}

// Result resume la campana.
type Result struct {
	Profile      string        `json:"profile"`
	Concurrency  int           `json:"concurrency"`
	DurationMs   int64         `json:"duration_ms"`
	Sent         int64         `json:"sent"`
	Created      int64         `json:"created"`
	Duplicated   int64         `json:"duplicated"`
	Rejected     int64         `json:"rejected_insufficient_funds"`
	Conflicts    int64         `json:"idempotency_conflicts"`
	Unavailable  int64         `json:"unavailable"`
	Errors       int64         `json:"errors"`
	Throughput   float64       `json:"throughput_per_sec"`
	P50          time.Duration `json:"p50"`
	P95          time.Duration `json:"p95"`
	P99          time.Duration `json:"p99"`
	Max          time.Duration `json:"max"`
	Uncertain    int64         `json:"uncertain"` // sin respuesta, desenlace desconocido
	LatenciasRaw []int64       `json:"-"`
}

type contadores struct {
	mu                                                                    sync.Mutex
	sent, created, dup, rejected, conflicts, unavailable, errs, uncertain int64
	latencias                                                             []time.Duration
}

// Run ejecuta la campana.
func Run(ctx context.Context, cfg Config) (Result, error) {
	if len(cfg.Accounts) < 2 {
		return Result{}, fmt.Errorf("workload: se requieren al menos dos cuentas")
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 1
	}
	if cfg.Amount <= 0 {
		cfg.Amount = 100
	}

	ctx, cancel := context.WithTimeout(ctx, cfg.Duration)
	defer cancel()

	c := &contadores{latencias: make([]time.Duration, 0, 1<<16)}
	cliente := &http.Client{Timeout: 30 * time.Second}

	inicio := time.Now()
	var wg sync.WaitGroup
	for w := 0; w < cfg.Concurrency; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(cfg.Seed + int64(w)))
			for ctx.Err() == nil {
				origen, destino := elegirPar(r, cfg)
				key := nuevaClave(r)
				enviar(ctx, cliente, cfg, c, key, origen, destino, false)

				// Duplicación deliberada. El reintento con la misma clave debe
				// producir el mismo efecto una sola vez (I3). El contrato HTTP no
				// revela si la solicitud es duplicada, así que el arnés conserva
				// esa información porque él mismo generó el reintento.
				if cfg.DuplicatePct > 0 && r.Intn(100) < cfg.DuplicatePct {
					enviar(ctx, cliente, cfg, c, key, origen, destino, true)
				}
			}
		}(w)
	}
	wg.Wait()
	transcurrido := time.Since(inicio)

	c.mu.Lock()
	defer c.mu.Unlock()
	sort.Slice(c.latencias, func(i, j int) bool { return c.latencias[i] < c.latencias[j] })

	res := Result{
		Profile:     string(cfg.Profile),
		Concurrency: cfg.Concurrency,
		DurationMs:  transcurrido.Milliseconds(),
		Sent:        c.sent,
		Created:     c.created,
		Duplicated:  c.dup,
		Rejected:    c.rejected,
		Conflicts:   c.conflicts,
		Unavailable: c.unavailable,
		Errors:      c.errs,
		Uncertain:   c.uncertain,
	}
	if transcurrido > 0 {
		res.Throughput = float64(c.created) / transcurrido.Seconds()
	}
	res.P50 = pct(c.latencias, 0.50)
	res.P95 = pct(c.latencias, 0.95)
	res.P99 = pct(c.latencias, 0.99)
	if n := len(c.latencias); n > 0 {
		res.Max = c.latencias[n-1]
	}
	for _, d := range c.latencias {
		res.LatenciasRaw = append(res.LatenciasRaw, d.Microseconds())
	}
	return res, nil
}

func enviar(ctx context.Context, cliente *http.Client, cfg Config, c *contadores,
	key, origen, destino string, duplicate bool) {

	cuerpo, _ := json.Marshal(map[string]any{
		"idempotency_key": key,
		"currency":        cfg.Currency,
		"postings": []map[string]any{
			{"account_id": origen, "amount_minor": -cfg.Amount},
			{"account_id": destino, "amount_minor": cfg.Amount},
		},
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		cfg.BaseURL+"/entries", bytes.NewReader(cuerpo))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	t0 := time.Now()
	resp, err := cliente.Do(req)
	elapsed := time.Since(t0)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent++

	if err != nil {
		if ctx.Err() != nil {
			return
		}
		// Sin respuesta. El desenlace es desconocido y solo la reconciliacion
		// puede resolverlo. Se contabiliza aparte porque es una metrica del
		// experimento, no un error del arnes.
		c.uncertain++
		return
	}
	defer resp.Body.Close()

	var cuerpoResp struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&cuerpoResp)

	c.latencias = append(c.latencias, elapsed)

	switch resp.StatusCode {
	case http.StatusAccepted:
		if duplicate {
			c.dup++
		} else {
			c.created++
		}
	case http.StatusConflict:
		if cuerpoResp.Code == "idempotency_conflict" {
			c.conflicts++
		} else {
			c.rejected++
		}
	case http.StatusServiceUnavailable:
		c.unavailable++
	default:
		c.errs++
	}
}

func elegirPar(r *rand.Rand, cfg Config) (string, string) {
	n := len(cfg.Accounts)
	calientes := func(pct int) int {
		k := n * pct / 100
		if k < 1 {
			k = 1
		}
		return k
	}

	var i, j int
	switch cfg.Profile {
	case Hot1:
		i = r.Intn(calientes(1))
		j = r.Intn(n)
	case Hot10:
		i = r.Intn(calientes(10))
		j = r.Intn(n)
	case FanIn:
		i = r.Intn(n)
		j = 0
	case FanOut:
		i = 0
		j = r.Intn(n)
	default:
		i = r.Intn(n)
		j = r.Intn(n)
	}
	if i == j {
		j = (j + 1) % n
	}
	return cfg.Accounts[i], cfg.Accounts[j]
}

func nuevaClave(r *rand.Rand) string {
	var b [16]byte
	for i := range b {
		b[i] = byte(r.Intn(256))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variante
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func pct(ordenado []time.Duration, q float64) time.Duration {
	if len(ordenado) == 0 {
		return 0
	}
	i := int(q * float64(len(ordenado)-1))
	if i < 0 {
		i = 0
	}
	if i >= len(ordenado) {
		i = len(ordenado) - 1
	}
	return ordenado[i]
}
