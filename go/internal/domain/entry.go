// Package domain define el aggregate fundamental del ledger.
//
// La frontera de consistencia es el asiento (JournalEntry), no la cuenta. Si
// se eligiera la cuenta, una transferencia tocaria dos fronteras y dejaria de
// ser atomica, obligando a introducir sagas y compensaciones para resolver un
// problema que la eleccion correcta de aggregate evita por completo.
//
// Esa eleccion no resuelve todo. La restriccion de saldo no negativo (I5)
// depende del estado acumulado de una cuenta, que es una proyeccion, y por
// tanto cruza la frontera del aggregate. Ese invariante que cruza es el objeto
// central del experimento comparativo y se resuelve en la capa de persistencia,
// no aqui.
package domain

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"

	"github.com/kapumota/ledger-lab/go/internal/money"
)

var (
	ErrUnbalanced     = errors.New("domain: el asiento no suma cero")
	ErrTooFewPostings = errors.New("domain: el asiento requiere al menos dos postings")
	ErrZeroAmount     = errors.New("domain: un posting no puede tener importe cero")
	ErrNilAccount     = errors.New("domain: el posting requiere una cuenta")
	ErrNoKey          = errors.New("domain: se requiere clave de idempotencia")
)

// Posting es una linea del asiento. El signo lleva la direccion. Negativo es
// cargo contra la cuenta, positivo es abono.
type Posting struct {
	AccountID uuid.UUID   `json:"account_id"`
	Amount    money.Minor `json:"amount_minor"`
}

// Entry es el aggregate. Se construye, se valida y se persiste como unidad.
type Entry struct {
	ID             uuid.UUID       `json:"id"`
	IdempotencyKey uuid.UUID       `json:"idempotency_key"`
	Currency       money.Currency  `json:"currency"`
	Postings       []Posting       `json:"postings"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
}

// Validate comprueba I1 completo sobre el aggregate, antes de cualquier acceso
// a la base de datos. La base lo vuelve a comprobar con un trigger diferido.
// La duplicacion es deliberada. Una invariante que solo vive en la aplicacion
// deja de existir en cuanto alguien escribe por otra ruta.
func (e Entry) Validate() error {
	if e.IdempotencyKey == uuid.Nil {
		return ErrNoKey
	}
	if _, err := money.ParseCurrency(string(e.Currency)); err != nil {
		return err
	}
	if len(e.Postings) < 2 {
		return fmt.Errorf("%w: hay %d", ErrTooFewPostings, len(e.Postings))
	}

	amounts := make([]money.Minor, 0, len(e.Postings))
	for i, p := range e.Postings {
		if p.AccountID == uuid.Nil {
			return fmt.Errorf("%w: posting %d", ErrNilAccount, i)
		}
		if p.Amount == 0 {
			return fmt.Errorf("%w: posting %d", ErrZeroAmount, i)
		}
		amounts = append(amounts, p.Amount)
	}

	total, err := money.Sum(amounts)
	if err != nil {
		return err
	}
	if total != 0 {
		return fmt.Errorf("%w: suma %d", ErrUnbalanced, total)
	}
	return nil
}

// Fingerprint produce un resumen canonico e independiente del orden de los
// postings. Sirve para distinguir un reintento legitimo de la misma solicitud
// de una reutilizacion indebida de la clave de idempotencia (I3).
func (e Entry) Fingerprint() []byte {
	type kv struct {
		acc uuid.UUID
		amt money.Minor
	}
	items := make([]kv, 0, len(e.Postings))
	for _, p := range e.Postings {
		items = append(items, kv{p.AccountID, p.Amount})
	}
	sort.Slice(items, func(i, j int) bool {
		c := items[i].acc.String()
		d := items[j].acc.String()
		if c != d {
			return c < d
		}
		return items[i].amt < items[j].amt
	})

	h := sha256.New()
	h.Write(e.IdempotencyKey[:])
	h.Write([]byte(e.Currency))
	buf := make([]byte, 8)
	for _, it := range items {
		h.Write(it.acc[:])
		binary.BigEndian.PutUint64(buf, uint64(it.amt))
		h.Write(buf)
	}
	return h.Sum(nil)
}

// Accounts devuelve las cuentas tocadas, ordenadas de forma determinista.
// El orden importa. Bloquear siempre en el mismo orden es lo que evita el
// interbloqueo entre transferencias cruzadas A hacia B y B hacia A.
func (e Entry) Accounts() []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(e.Postings))
	out := make([]uuid.UUID, 0, len(e.Postings))
	for _, p := range e.Postings {
		if _, ok := seen[p.AccountID]; ok {
			continue
		}
		seen[p.AccountID] = struct{}{}
		out = append(out, p.AccountID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// NetByAccount agrega los importes por cuenta. Un asiento puede tocar la misma
// cuenta mas de una vez, y la proyeccion de saldo debe actualizarse una sola
// vez por cuenta con el neto.
func (e Entry) NetByAccount() (map[uuid.UUID]money.Minor, error) {
	out := make(map[uuid.UUID]money.Minor, len(e.Postings))
	for _, p := range e.Postings {
		s, err := money.Add(out[p.AccountID], p.Amount)
		if err != nil {
			return nil, err
		}
		out[p.AccountID] = s
	}
	return out, nil
}

// NewTransfer construye el asiento de dos postings de una transferencia interna
// en una sola moneda. amount debe ser positivo.
func NewTransfer(key uuid.UUID, cur money.Currency, from, to uuid.UUID, amount money.Minor) (Entry, error) {
	if amount <= 0 {
		return Entry{}, fmt.Errorf("domain: el importe de una transferencia debe ser positivo, se recibio %d", amount)
	}
	neg, err := money.Neg(amount)
	if err != nil {
		return Entry{}, err
	}
	e := Entry{
		ID:             uuid.New(),
		IdempotencyKey: key,
		Currency:       cur,
		Postings: []Posting{
			{AccountID: from, Amount: neg},
			{AccountID: to, Amount: amount},
		},
	}
	return e, e.Validate()
}

// FXLeg describe un lado de un cambio de divisa ya calculado fuera del ledger.
type FXLeg struct {
	Currency money.Currency
	From     uuid.UUID
	To       uuid.UUID
	Amount   money.Minor
}

// NewFXTransfer construye los dos asientos de un cambio de divisa.
//
// Un cambio de divisa no es un asiento de moneda mixta. Son dos asientos, uno
// por moneda, unidos por cuentas puente de FX. Cada asiento suma cero en su
// propia moneda, que es la unica forma en que I1 sigue siendo decidible.
//
// La tasa se aplica fuera de esta funcion, en decimal o racional. Lo que llega
// aqui ya es entero.
func NewFXTransfer(keyA, keyB uuid.UUID, origen, destino FXLeg, puenteOrigen, puenteDestino uuid.UUID) (Entry, Entry, error) {
	a, err := NewTransfer(keyA, origen.Currency, origen.From, puenteOrigen, origen.Amount)
	if err != nil {
		return Entry{}, Entry{}, err
	}
	b, err := NewTransfer(keyB, destino.Currency, puenteDestino, destino.To, destino.Amount)
	if err != nil {
		return Entry{}, Entry{}, err
	}
	return a, b, nil
}

// NewProportionalSplit reparte un importe entre varios destinos conservando el
// total exactamente y contabilizando el residuo de redondeo.
//
// Es la demostracion ejecutable de la tesis sobre representacion monetaria. No
// hay decimal en el ledger y aun asi el reparto fraccionario es exacto, porque
// la diferencia de redondeo no se descarta sino que se convierte en un posting.
func NewProportionalSplit(key uuid.UUID, cur money.Currency, from uuid.UUID, total money.Minor, destinos []uuid.UUID, pesos []int64) (Entry, error) {
	if len(destinos) != len(pesos) {
		return Entry{}, fmt.Errorf("domain: %d destinos contra %d pesos", len(destinos), len(pesos))
	}
	partes, err := money.Allocate(total, pesos)
	if err != nil {
		return Entry{}, err
	}
	neg, err := money.Neg(total)
	if err != nil {
		return Entry{}, err
	}

	postings := []Posting{{AccountID: from, Amount: neg}}
	for i, d := range destinos {
		if partes[i] == 0 {
			continue
		}
		postings = append(postings, Posting{AccountID: d, Amount: partes[i]})
	}

	e := Entry{
		ID:             uuid.New(),
		IdempotencyKey: key,
		Currency:       cur,
		Postings:       postings,
	}
	return e, e.Validate()
}
