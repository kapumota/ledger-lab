package money

import (
	"math"
	"math/rand"
	"testing"
)

func TestAddDetectaDesbordamiento(t *testing.T) {
	casos := []struct {
		a, b Minor
		ok   bool
	}{
		{math.MaxInt64, 1, false},
		{math.MinInt64, -1, false},
		{math.MaxInt64, 0, true},
		{math.MaxInt64, -1, true},
		{100, -100, true},
	}
	for _, c := range casos {
		_, err := Add(c.a, c.b)
		if (err == nil) != c.ok {
			t.Fatalf("Add(%d, %d) error=%v, se esperaba ok=%v", c.a, c.b, err, c.ok)
		}
	}
}

func TestIsZeroSumConTransferenciaSimple(t *testing.T) {
	ok, err := IsZeroSum([]Minor{-10050, 10050})
	if err != nil || !ok {
		t.Fatalf("una transferencia simple debe sumar cero, ok=%v err=%v", ok, err)
	}
}

// Propiedad. Para cualquier secuencia aleatoria de importes, anexar su opuesto
// total produce siempre una secuencia balanceada. Es la formulacion minima de
// I1 y la que usa el constructor de asientos.
func TestPropiedadBalanceoPorContrapartida(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for iter := 0; iter < 2000; iter++ {
		n := 1 + r.Intn(8)
		xs := make([]Minor, 0, n+1)
		var total Minor
		desbordo := false
		for i := 0; i < n; i++ {
			// Rango acotado para que la contrapartida nunca desborde.
			v := Minor(r.Int63n(1_000_000_000) - 500_000_000)
			if v == 0 {
				v = 1
			}
			var err error
			if total, err = Add(total, v); err != nil {
				desbordo = true
				break
			}
			xs = append(xs, v)
		}
		if desbordo {
			continue
		}
		contra, err := Neg(total)
		if err != nil {
			continue
		}
		if contra == 0 {
			continue
		}
		xs = append(xs, contra)

		ok, err := IsZeroSum(xs)
		if err != nil {
			t.Fatalf("iter %d: error inesperado %v", iter, err)
		}
		if !ok {
			t.Fatalf("iter %d: la secuencia con contrapartida no suma cero: %v", iter, xs)
		}
	}
}

// Propiedad central de Allocate. El reparto conserva el total exactamente.
// Si esta propiedad falla, el ledger crea o destruye dinero al redondear.
func TestPropiedadAllocateConservaElTotal(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for iter := 0; iter < 5000; iter++ {
		total := Minor(r.Int63n(10_000_000))
		n := 1 + r.Intn(6)
		w := make([]int64, n)
		todosCero := true
		for i := range w {
			w[i] = r.Int63n(100)
			if w[i] != 0 {
				todosCero = false
			}
		}
		if todosCero {
			w[0] = 1
		}

		partes, err := Allocate(total, w)
		if err != nil {
			t.Fatalf("iter %d: Allocate devolvio error %v", iter, err)
		}
		suma, err := Sum(partes)
		if err != nil {
			t.Fatalf("iter %d: suma con error %v", iter, err)
		}
		if suma != total {
			t.Fatalf("iter %d: Allocate(%d, %v) = %v suma %d, se esperaba %d",
				iter, total, w, partes, suma, total)
		}
		for i, p := range partes {
			if p < 0 {
				t.Fatalf("iter %d: parte negativa en indice %d", iter, i)
			}
		}
	}
}

func TestAllocateEsDeterminista(t *testing.T) {
	a, _ := Allocate(100, []int64{1, 1, 1})
	b, _ := Allocate(100, []int64{1, 1, 1})
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("Allocate no es determinista: %v vs %v", a, b)
		}
	}
	// 100 entre tres partes iguales. El residuo de uno debe asignarse, no
	// descartarse.
	if a[0]+a[1]+a[2] != 100 {
		t.Fatalf("reparto incompleto: %v", a)
	}
}

func TestFormat(t *testing.T) {
	casos := []struct {
		m    Minor
		c    Currency
		want string
	}{
		{10050, "PEN", "PEN 100.50"},
		{-10050, "PEN", "-PEN 100.50"},
		{5, "PEN", "PEN 0.05"},
		{1000, "JPY", "JPY 1000"},
		{1234, "KWD", "KWD 1.234"},
		{0, "USD", "USD 0.00"},
	}
	for _, c := range casos {
		if got := Format(c.m, c.c); got != c.want {
			t.Fatalf("Format(%d, %s) = %q, se esperaba %q", c.m, c.c, got, c.want)
		}
	}
}

func TestParseCurrency(t *testing.T) {
	if _, err := ParseCurrency("pen"); err != nil {
		t.Fatalf("pen debe normalizarse a PEN: %v", err)
	}
	if _, err := ParseCurrency("PENN"); err == nil {
		t.Fatal("PENN debe rechazarse")
	}
}
