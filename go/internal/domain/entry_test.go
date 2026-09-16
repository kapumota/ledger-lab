package domain

import (
	"errors"
	"testing"
	"testing/quick"

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

// I1 como propiedad. testing/quick genera importes y cantidades de postings
// arbitrarios. El test construye un asiento balanceado agregando la
// contrapartida exacta y exige que Validate lo acepte para todos los casos.
func TestPropertyGeneratedBalancedEntriesSatisfyI1(t *testing.T) {
	property := func(rawAmounts [7]int32, rawCount uint8) bool {
		n := 1 + int(rawCount%uint8(len(rawAmounts)))
		postings := make([]Posting, 0, n+1)
		var total money.Minor
		for i := 0; i < n; i++ {
			amount := money.Minor(rawAmounts[i])
			if amount == 0 {
				amount = 1
			}
			postings = append(postings, Posting{AccountID: testUUID(i + 2), Amount: amount})
			total += amount
		}
		if total == 0 {
			if postings[0].Amount > 0 {
				postings[0].Amount++
				total++
			} else {
				postings[0].Amount--
				total--
			}
		}
		postings = append(postings, Posting{AccountID: testUUID(n + 2), Amount: -total})

		e := Entry{
			IdempotencyKey: testUUID(1),
			Currency:       "PEN",
			Postings:       postings,
		}
		return e.Validate() == nil
	}

	if err := quick.Check(property, &quick.Config{MaxCount: 5000}); err != nil {
		t.Fatalf("la propiedad I1 falló para un asiento balanceado generado: %v", err)
	}
}

// I1 también debe sostenerse en el reparto proporcional. testing/quick varía
// el total, la cantidad de destinos y sus pesos para explorar casos de
// redondeo sin mezclar el generador con la propiedad comprobada.
func TestPropertyProportionalSplitPreservesI1(t *testing.T) {
	property := func(rawTotal uint32, rawWeights [6]uint16) bool {
		n := 1 + int(rawTotal%uint32(len(rawWeights)))
		destinations := make([]uuid.UUID, n)
		weights := make([]int64, n)
		hasPositiveWeight := false
		for i := 0; i < n; i++ {
			destinations[i] = testUUID(i + 3)
			weights[i] = int64(rawWeights[i] % 100)
			if weights[i] > 0 {
				hasPositiveWeight = true
			}
		}
		if !hasPositiveWeight {
			weights[0] = 1
		}

		total := money.Minor(int64(rawTotal) + 1)
		e, err := NewProportionalSplit(
			testUUID(1),
			"PEN",
			testUUID(2),
			total,
			destinations,
			weights,
		)
		return err == nil && e.Validate() == nil
	}

	if err := quick.Check(property, &quick.Config{MaxCount: 3000}); err != nil {
		t.Fatalf("la propiedad I1 falló para un reparto proporcional generado: %v", err)
	}
}

func testUUID(n int) uuid.UUID {
	var id uuid.UUID
	id[14] = byte(n >> 8)
	id[15] = byte(n)
	return id
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
