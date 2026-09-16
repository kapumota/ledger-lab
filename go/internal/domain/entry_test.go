package domain

import (
	"errors"
	"math/rand"
	"testing"

	"github.com/google/uuid"

	"github.com/kapumota/ledger-lab/go/internal/money"
)

func TestTransferenciaValida(t *testing.T) {
	e, err := NewTransfer(uuid.New(), "PEN", uuid.New(), uuid.New(), 10050)
	if err != nil {
		t.Fatalf("no se esperaba error: %v", err)
	}
	if len(e.Postings) != 2 {
		t.Fatalf("se esperaban dos postings, hay %d", len(e.Postings))
	}
}

func TestAsientoDesbalanceadoSeRechaza(t *testing.T) {
	e := Entry{
		IdempotencyKey: uuid.New(),
		Currency:       "PEN",
		Postings: []Posting{
			{AccountID: uuid.New(), Amount: -100},
			{AccountID: uuid.New(), Amount: 90},
		},
	}
	if err := e.Validate(); !errors.Is(err, ErrUnbalanced) {
		t.Fatalf("se esperaba ErrUnbalanced, se obtuvo %v", err)
	}
}

func TestPostingCeroSeRechaza(t *testing.T) {
	e := Entry{
		IdempotencyKey: uuid.New(),
		Currency:       "PEN",
		Postings: []Posting{
			{AccountID: uuid.New(), Amount: 0},
			{AccountID: uuid.New(), Amount: 0},
		},
	}
	if err := e.Validate(); !errors.Is(err, ErrZeroAmount) {
		t.Fatalf("se esperaba ErrZeroAmount, se obtuvo %v", err)
	}
}

func TestEntryWithoutPostingsIsRejected(t *testing.T) {
	e := Entry{
		IdempotencyKey: uuid.New(),
		Currency:       "PEN",
	}
	if err := e.Validate(); !errors.Is(err, ErrTooFewPostings) {
		t.Fatalf("se esperaba ErrTooFewPostings, se obtuvo %v", err)
	}
}

func TestUnSoloPostingSeRechaza(t *testing.T) {
	e := Entry{
		IdempotencyKey: uuid.New(),
		Currency:       "PEN",
		Postings:       []Posting{{AccountID: uuid.New(), Amount: 100}},
	}
	if err := e.Validate(); !errors.Is(err, ErrTooFewPostings) {
		t.Fatalf("se esperaba ErrTooFewPostings, se obtuvo %v", err)
	}
}

// I1 como propiedad. Todo asiento construido por los constructores del dominio
// es valido, para cualquier entrada admisible.
func TestPropiedadI1SobreConstructores(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	for i := 0; i < 5000; i++ {
		amount := money.Minor(1 + r.Int63n(100_000_000))
		e, err := NewTransfer(uuid.New(), "PEN", uuid.New(), uuid.New(), amount)
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if err := e.Validate(); err != nil {
			t.Fatalf("iter %d: asiento construido invalido: %v", i, err)
		}
	}
}

// I1 tambien debe sostenerse en el reparto proporcional, que es el caso donde
// el redondeo podria crear o destruir centimos.
func TestPropiedadI1SobreRepartoProporcional(t *testing.T) {
	r := rand.New(rand.NewSource(13))
	for i := 0; i < 3000; i++ {
		n := 1 + r.Intn(5)
		destinos := make([]uuid.UUID, n)
		pesos := make([]int64, n)
		for j := 0; j < n; j++ {
			destinos[j] = uuid.New()
			pesos[j] = 1 + r.Int63n(50)
		}
		total := money.Minor(1 + r.Int63n(1_000_000))

		e, err := NewProportionalSplit(uuid.New(), "PEN", uuid.New(), total, destinos, pesos)
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if err := e.Validate(); err != nil {
			t.Fatalf("iter %d: reparto invalido: %v", i, err)
		}
	}
}

func TestFingerprintIndependienteDelOrden(t *testing.T) {
	key := uuid.New()
	a, b := uuid.New(), uuid.New()

	e1 := Entry{IdempotencyKey: key, Currency: "PEN", Postings: []Posting{
		{AccountID: a, Amount: -100}, {AccountID: b, Amount: 100},
	}}
	e2 := Entry{IdempotencyKey: key, Currency: "PEN", Postings: []Posting{
		{AccountID: b, Amount: 100}, {AccountID: a, Amount: -100},
	}}

	if string(e1.Fingerprint()) != string(e2.Fingerprint()) {
		t.Fatal("el fingerprint debe ser independiente del orden de los postings")
	}

	e3 := Entry{IdempotencyKey: key, Currency: "PEN", Postings: []Posting{
		{AccountID: a, Amount: -200}, {AccountID: b, Amount: 200},
	}}
	if string(e1.Fingerprint()) == string(e3.Fingerprint()) {
		t.Fatal("importes distintos deben producir fingerprints distintos")
	}
}

func TestFingerprintIncludesMetadata(t *testing.T) {
	key := uuid.New()
	a, b := uuid.New(), uuid.New()
	base := Entry{
		IdempotencyKey: key,
		Currency:       "PEN",
		Postings: []Posting{
			{AccountID: a, Amount: -100},
			{AccountID: b, Amount: 100},
		},
		Metadata: []byte(`{"reference":"A-001"}`),
	}
	changed := base
	changed.Metadata = []byte(`{"reference":"A-002"}`)

	if string(base.Fingerprint()) == string(changed.Fingerprint()) {
		t.Fatal("metadata distinta debe producir fingerprints distintos")
	}
}

func TestFingerprintCanonicalizesMetadata(t *testing.T) {
	key := uuid.New()
	a, b := uuid.New(), uuid.New()
	base := Entry{
		IdempotencyKey: key,
		Currency:       "PEN",
		Postings: []Posting{
			{AccountID: a, Amount: -100},
			{AccountID: b, Amount: 100},
		},
		Metadata: []byte(`{"reference":"A-001","details":{"channel":"web","attempt":1}}`),
	}
	equivalent := base
	equivalent.Metadata = []byte(` { "details" : { "attempt" : 1, "channel" : "web" }, "reference" : "A-001" } `)

	if string(base.Fingerprint()) != string(equivalent.Fingerprint()) {
		t.Fatal("metadata JSON equivalente debe producir el mismo fingerprint")
	}

	withoutMetadata := base
	withoutMetadata.Metadata = nil
	emptyMetadata := base
	emptyMetadata.Metadata = []byte(`{}`)
	if string(withoutMetadata.Fingerprint()) != string(emptyMetadata.Fingerprint()) {
		t.Fatal("metadata omitida y objeto vacio deben producir el mismo fingerprint")
	}
}

func TestNetByAccountAgregaCuentaRepetida(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	e := Entry{IdempotencyKey: uuid.New(), Currency: "PEN", Postings: []Posting{
		{AccountID: a, Amount: -100},
		{AccountID: a, Amount: -50},
		{AccountID: b, Amount: 150},
	}}
	if err := e.Validate(); err != nil {
		t.Fatalf("el asiento debe ser valido: %v", err)
	}
	net, err := e.NetByAccount()
	if err != nil {
		t.Fatal(err)
	}
	if net[a] != -150 || net[b] != 150 {
		t.Fatalf("neto incorrecto: %v", net)
	}
}

func TestAccountsOrdenDeterminista(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	e1 := Entry{Postings: []Posting{{AccountID: a, Amount: -1}, {AccountID: b, Amount: 1}}}
	e2 := Entry{Postings: []Posting{{AccountID: b, Amount: 1}, {AccountID: a, Amount: -1}}}
	x, y := e1.Accounts(), e2.Accounts()
	for i := range x {
		if x[i] != y[i] {
			t.Fatal("el orden de bloqueo debe ser independiente del orden de los postings")
		}
	}
}
