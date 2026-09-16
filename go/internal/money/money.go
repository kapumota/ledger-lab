// Package money representa importes como enteros con signo en unidades
// minimas de la moneda.
//
// La decision de fondo del proyecto es que el ledger nunca almacena decimales
// ni tasas. El invariante de suma cero debe ser decidible sin politica de
// redondeo implicita, y eso solo se cumple sobre enteros exactos.
//
// El calculo que si necesita precision fraccionaria, como cambio de divisa,
// intereses o comisiones proporcionales, ocurre fuera del ledger. Lo que entra
// al ledger es el resultado entero mas el residuo de redondeo materializado
// como posting explicito. Ver Allocate.
package money

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
)

// Minor es un importe en unidades minimas. 10050 en PEN son 100.50 soles.
type Minor int64

// Currency es un codigo ISO 4217 de tres letras mayusculas.
type Currency string

var (
	ErrOverflow        = errors.New("money: desbordamiento de int64")
	ErrBadCurrency     = errors.New("money: codigo de moneda invalido")
	ErrNegativeTotal   = errors.New("money: el total a repartir no puede ser negativo")
	ErrNoWeights       = errors.New("money: se requiere al menos un peso")
	ErrNegativeWeight  = errors.New("money: los pesos no pueden ser negativos")
	ErrZeroWeightTotal = errors.New("money: la suma de pesos no puede ser cero")
)

var currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)

// ParseCurrency normaliza y valida un codigo de moneda.
func ParseCurrency(s string) (Currency, error) {
	c := Currency(strings.ToUpper(strings.TrimSpace(s)))
	if !currencyRe.MatchString(string(c)) {
		return "", fmt.Errorf("%w: %q", ErrBadCurrency, s)
	}
	return c, nil
}

// scales contiene las excepciones a la escala por defecto de dos decimales.
// La lista es deliberadamente corta. Ampliarla es una decision de dominio, no
// de conveniencia.
var scales = map[Currency]int{
	"JPY": 0,
	"KRW": 0,
	"CLP": 0,
	"VND": 0,
	"ISK": 0,
	"BHD": 3,
	"JOD": 3,
	"KWD": 3,
	"OMR": 3,
	"TND": 3,
}

// Scale devuelve el numero de decimales de la moneda.
func (c Currency) Scale() int {
	if s, ok := scales[c]; ok {
		return s
	}
	return 2
}

// Add suma con deteccion de desbordamiento.
//
// Un ledger que desborda en silencio produce asientos que suman cero en
// aritmetica modular y no en aritmetica entera. El verificador externo lo
// detectaria despues, pero el fallo debe ocurrir antes de persistir.
func Add(a, b Minor) (Minor, error) {
	s := a + b
	if (b > 0 && s < a) || (b < 0 && s > a) {
		return 0, fmt.Errorf("%w: %d + %d", ErrOverflow, a, b)
	}
	return s, nil
}

// Sum suma una secuencia con deteccion de desbordamiento.
func Sum(xs []Minor) (Minor, error) {
	var total Minor
	var err error
	for _, x := range xs {
		if total, err = Add(total, x); err != nil {
			return 0, err
		}
	}
	return total, nil
}

// IsZeroSum indica si la secuencia suma exactamente cero.
// Devuelve error si la suma parcial desborda, porque en ese caso la respuesta
// booleana no tendria sentido.
func IsZeroSum(xs []Minor) (bool, error) {
	total, err := Sum(xs)
	if err != nil {
		return false, err
	}
	return total == 0, nil
}

// Neg devuelve el opuesto con deteccion del caso MinInt64.
func Neg(a Minor) (Minor, error) {
	if a == Minor(math.MinInt64) {
		return 0, fmt.Errorf("%w: -(%d)", ErrOverflow, a)
	}
	return -a, nil
}

// Format produce la representacion humana del importe segun la escala.
func Format(m Minor, c Currency) string {
	scale := c.Scale()
	neg := m < 0
	v := m
	if neg {
		// Se opera sobre uint64 para tolerar MinInt64.
		u := uint64(-(v + 1)) + 1
		return fmt.Sprintf("-%s", formatUnsigned(u, scale, c))
	}
	return formatUnsigned(uint64(v), scale, c)
}

func formatUnsigned(u uint64, scale int, c Currency) string {
	if scale == 0 {
		return fmt.Sprintf("%s %d", c, u)
	}
	div := uint64(1)
	for i := 0; i < scale; i++ {
		div *= 10
	}
	return fmt.Sprintf("%s %d.%0*d", c, u/div, scale, u%div)
}

// Allocate reparte total entre len(weights) partes proporcionalmente a los
// pesos, garantizando que la suma de las partes es exactamente total.
//
// Usa el metodo del mayor residuo. Ningun centimo se crea ni se pierde, que es
// exactamente la propiedad que el ledger necesita. Cuando el reparto proviene
// de una tasa fraccionaria, el llamador calcula la tasa en decimal o racional
// fuera del ledger, obtiene los pesos enteros y usa esta funcion. La diferencia
// que el redondeo produce no se descarta, se contabiliza.
func Allocate(total Minor, weights []int64) ([]Minor, error) {
	if len(weights) == 0 {
		return nil, ErrNoWeights
	}
	if total < 0 {
		return nil, ErrNegativeTotal
	}

	var wsum int64
	for _, w := range weights {
		if w < 0 {
			return nil, ErrNegativeWeight
		}
		prev := wsum
		wsum += w
		if wsum < prev {
			return nil, ErrOverflow
		}
	}
	if wsum == 0 {
		return nil, ErrZeroWeightTotal
	}

	type rem struct {
		idx int
		r   int64
	}

	out := make([]Minor, len(weights))
	rems := make([]rem, len(weights))
	var assigned Minor

	for i, w := range weights {
		// total y w caben en int64. El producto puede desbordar, asi que se
		// verifica antes de multiplicar.
		if w != 0 && int64(total) > math.MaxInt64/w {
			return nil, ErrOverflow
		}
		prod := int64(total) * w
		out[i] = Minor(prod / wsum)
		rems[i] = rem{idx: i, r: prod % wsum}
		assigned += out[i]
	}

	// Se reparte el resto a los mayores residuos, con desempate por indice
	// para que la funcion sea determinista y por tanto reproducible en
	// experimentos.
	left := int64(total - assigned)
	for ; left > 0; left-- {
		best := -1
		for _, rr := range rems {
			if rr.r == 0 {
				continue
			}
			if best == -1 || rr.r > rems[best].r {
				best = rr.idx
			}
		}
		if best == -1 {
			// No quedan residuos, se asigna al primer peso no nulo.
			for i, w := range weights {
				if w > 0 {
					best = i
					break
				}
			}
		}
		out[best]++
		rems[best].r = 0
	}

	return out, nil
}
