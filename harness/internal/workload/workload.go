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
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/kapumota/ledger-lab/harness/internal/history"
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
	RunID        string
	History      history.Sink
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
	historyErr                                                            error
}

// Run ejecuta la campana.
func Run(ctx context.Context, cfg Config) (Result, error) {
	if len(cfg.Accounts) < 2 {
		return Result{}, fmt.Errorf("workload: se requieren al menos dos cuentas")
	}
	if cfg.History != nil && cfg.RunID == "" {
		return Result{}, fmt.Errorf("workload: run_id es obligatorio al registrar historia")
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
			clientID := fmt.Sprintf("client-%03d", w)
			var clientSeq uint64
			for ctx.Err() == nil {
				origen, destino := elegirPar(r, cfg)
				key := nuevaClave(r)
				enviar(ctx, cliente, cfg, c, inicio, clientID, clientSeq,
					key, origen, destino, false)
				clientSeq++

				// Duplicación deliberada. El reintento con la misma clave debe
				// producir el mismo efecto una sola vez (I3). El contrato HTTP no
				// revela si la solicitud es duplicada, así que el arnés conserva
				// esa información porque él mismo generó el reintento.
				if cfg.DuplicatePct > 0 && r.Intn(100) < cfg.DuplicatePct {
					enviar(ctx, cliente, cfg, c, inicio, clientID, clientSeq,
						key, origen, destino, true)
					clientSeq++
				}
			}
		}(w)
	}
	wg.Wait()
	transcurrido := time.Since(inicio)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.historyErr != nil {
		return Result{}, c.historyErr
	}
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
	runStart time.Time, clientID string, clientSeq uint64,
	key, origen, destino string, duplicate bool) {

	solicitud := history.Request{
		IdempotencyKey: key,
		Currency:       cfg.Currency,
		Postings: []history.Posting{
			{AccountID: origen, AmountMinor: -cfg.Amount},
			{AccountID: destino, AmountMinor: cfg.Amount},
		},
	}
	fingerprint, err := history.Fingerprint(solicitud)
	if err != nil {
		registrarErrorHistoria(c, err)
		return
	}

	cuerpo, err := json.Marshal(solicitud)
	if err != nil {
		registrarErrorHistoria(c, err)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		cfg.BaseURL+"/entries", bytes.NewReader(cuerpo))
	if err != nil {
		registrarErrorHistoria(c, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	invokeNS := time.Since(runStart).Nanoseconds()
	t0 := time.Now()
	resp, requestErr := cliente.Do(req)
	elapsed := time.Since(t0)
	completeNS := time.Since(runStart).Nanoseconds()

	record := history.Record{
		SchemaVersion:      1,
		RunID:              cfg.RunID,
		OperationID:        history.OperationID(cfg.RunID, clientID, clientSeq),
		ClientID:           clientID,
		ClientSeq:          clientSeq,
		IdempotencyKey:     key,
		RequestFingerprint: fingerprint,
		InvokeNS:           invokeNS,
		CompleteNS:         completeNS,
	}

	c.mu.Lock()
	c.sent++
	c.mu.Unlock()

	if requestErr != nil {
		record.Result = history.Unknown
		record.ErrorCode = stringPtr(errorObservacional(ctx, requestErr))
		if cfg.History != nil {
			if err := cfg.History.Append(record); err != nil {
				registrarErrorHistoria(c, err)
			}
		}

		c.mu.Lock()
		defer c.mu.Unlock()
		// Toda invocación sin respuesta contractual tiene desenlace desconocido,
		// incluida la cancelación al terminar la ventana de carga. El resumen y
		// history.ndjson deben contar la misma población observada.
		c.uncertain++
		return
	}
	defer resp.Body.Close()

	var cuerpoResp struct {
		EntryID string `json:"entry_id"`
		Code    string `json:"code"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&cuerpoResp)

	record.Result, record.EntryID, record.ErrorCode = clasificarRespuesta(
		resp.StatusCode, cuerpoResp.EntryID, cuerpoResp.Code,
	)
	if cfg.History != nil {
		if err := cfg.History.Append(record); err != nil {
			registrarErrorHistoria(c, err)
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
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

func clasificarRespuesta(status int, entryID, code string) (history.Result, *string, *string) {
	switch status {
	case http.StatusAccepted:
		if entryID == "" {
			return history.Unknown, nil, stringPtr("internal")
		}
		return history.Committed, stringPtr(entryID), nil
	case http.StatusBadRequest, http.StatusConflict, http.StatusUnprocessableEntity,
		http.StatusServiceUnavailable:
		if code == "" {
			return history.Unknown, nil, stringPtr("internal")
		}
		return history.Rejected, nil, stringPtr(code)
	case http.StatusInternalServerError:
		if code == "" {
			code = "internal"
		}
		return history.Unknown, nil, stringPtr(code)
	default:
		return history.Unknown, nil, stringPtr("internal")
	}
}

func errorObservacional(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	return "transport_error"
}

func registrarErrorHistoria(c *contadores, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.historyErr == nil {
		c.historyErr = fmt.Errorf("workload: historia: %w", err)
	}
}

func stringPtr(s string) *string { return &s }

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
