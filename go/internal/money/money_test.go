package money

import (
	"math"
	"testing"
	"testing/quick"
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

// Propiedad. testing/quick genera secuencias arbitrarias y, al anexar la
// contrapartida exacta, la suma debe ser siempre cero. Los int32 mantienen la
// suma intermedia lejos de los límites de int64 para aislar I1 del overflow.
func TestPropertyCounterpartBalancesSequence(t *testing.T) {
	property := func(raw [8]int32) bool {
		xs := make([]Minor, 0, len(raw)+1)
		var total Minor
		for _, value := range raw {
			v := Minor(value)
			if v == 0 {
				v = 1
			}
			xs = append(xs, v)
			total += v
		}
		if total == 0 {
			if xs[0] > 0 {
				xs[0]++
				total++
			} else {
				xs[0]--
				total--
			}
		}

		xs = append(xs, -total)
		ok, err := IsZeroSum(xs)
		return err == nil && ok
	}

	if err := quick.Check(property, &quick.Config{MaxCount: 2000}); err != nil {
		t.Fatalf("la propiedad de contrapartida falló: %v", err)
	}
}

// Propiedad central de Allocate. testing/quick varía el total y los pesos; el
// reparto debe conservar exactamente el total y nunca crear partes negativas.
func TestPropertyAllocatePreservesTotal(t *testing.T) {
	property := func(rawTotal uint32, rawWeights [6]uint16) bool {
		total := Minor(rawTotal)
		weights := make([]int64, len(rawWeights))
		hasPositiveWeight := false
		for i, value := range rawWeights {
			weights[i] = int64(value)
			if weights[i] > 0 {
				hasPositiveWeight = true
			}
		}
		if !hasPositiveWeight {
			weights[0] = 1
		}

		parts, err := Allocate(total, weights)
		if err != nil {
			return false
		}
		sum, err := Sum(parts)
		if err != nil || sum != total {
			return false
		}
		for _, part := range parts {
			if part < 0 {
				return false
			}
		}
		return true
	}

	if err := quick.Check(property, &quick.Config{MaxCount: 5000}); err != nil {
		t.Fatalf("la propiedad de conservación de Allocate falló: %v", err)
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
