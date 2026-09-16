package store

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Metrics agrupa los contadores de la ruta de escritura y un reservorio de
// latencias. Se mantiene deliberadamente sin dependencias externas para que el
// brazo Go no arrastre un stack de observabilidad que el brazo BEAM tendria que
// replicar. La comparacion exige instrumentos equivalentes.
type Metrics struct {
	Committed         atomic.Int64
	Duplicates        atomic.Int64
	Retries           atomic.Int64
	Failed            atomic.Int64
	Invalid           atomic.Int64
	InsufficientFunds atomic.Int64

	mu        sync.Mutex
	latencias []time.Duration
}

func NewMetrics() *Metrics {
	return &Metrics{latencias: make([]time.Duration, 0, 1<<16)}
}

func (m *Metrics) observeLatency(d time.Duration) {
	m.mu.Lock()
	m.latencias = append(m.latencias, d)
	m.mu.Unlock()
}

// Observe expone la captura de latencias a los clientes externos, como el
// generador de carga.
func (m *Metrics) Observe(d time.Duration) { m.observeLatency(d) }

// Snapshot es la vista serializable de las metricas.
type Snapshot struct {
	Committed         int64         `json:"committed"`
	Duplicates        int64         `json:"duplicates"`
	Retries           int64         `json:"retries"`
	Failed            int64         `json:"failed"`
	Invalid           int64         `json:"invalid"`
	InsufficientFunds int64         `json:"insufficient_funds"`
	Samples           int           `json:"samples"`
	P50               time.Duration `json:"p50"`
	P95               time.Duration `json:"p95"`
	P99               time.Duration `json:"p99"`
	Max               time.Duration `json:"max"`
}

// Snapshot calcula los percentiles sobre la muestra completa. No es un
// estimador en linea, es exacto, porque el tamano de las campanas lo permite y
// un percentil aproximado en un experimento comparativo introduce una variable
// que nadie quiere defender despues.
func (m *Metrics) Snapshot() Snapshot {
	m.mu.Lock()
	copia := make([]time.Duration, len(m.latencias))
	copy(copia, m.latencias)
	m.mu.Unlock()

	s := Snapshot{
		Committed:         m.Committed.Load(),
		Duplicates:        m.Duplicates.Load(),
		Retries:           m.Retries.Load(),
		Failed:            m.Failed.Load(),
		Invalid:           m.Invalid.Load(),
		InsufficientFunds: m.InsufficientFunds.Load(),
		Samples:           len(copia),
	}
	if len(copia) == 0 {
		return s
	}
	sort.Slice(copia, func(i, j int) bool { return copia[i] < copia[j] })
	s.P50 = percentil(copia, 0.50)
	s.P95 = percentil(copia, 0.95)
	s.P99 = percentil(copia, 0.99)
	s.Max = copia[len(copia)-1]
	return s
}

func percentil(ordenado []time.Duration, q float64) time.Duration {
	if len(ordenado) == 0 {
		return 0
	}
	idx := int(q * float64(len(ordenado)-1))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(ordenado) {
		idx = len(ordenado) - 1
	}
	return ordenado[idx]
}
