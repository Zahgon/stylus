/*!
 * Stylus - JavaScript number and string semantics
 * Copyright (c) Automattic <developer.wordpress.com>
 * MIT Licensed
 *
 * Ported from JavaScript to Go. The original computed every value with the
 * JavaScript number type and printed it with Number.prototype.toString, so the
 * port reproduces those two algorithms rather than Go's own float formatting.
 */

package stylus

import (
	"math"
	"math/big"
	"strconv"
	"strings"
)

// numToString is ECMA-262 Number::toString(x, 10): the shortest decimal that
// round-trips, written without an exponent while 1e-6 <= |x| < 1e21.
func numToString(x float64) string {
	switch {
	case math.IsNaN(x):
		return "NaN"
	case x == 0:
		return "0"
	case x < 0:
		return "-" + numToString(-x)
	case math.IsInf(x, 1):
		return "Infinity"
	}
	// strconv gives the shortest round-tripping digits as d.dddde±dd.
	e := strconv.FormatFloat(x, 'e', -1, 64)
	mant, expPart, _ := strings.Cut(e, "e")
	exp, _ := strconv.Atoi(expPart)
	digits := strings.Replace(mant, ".", "", 1)
	k := len(digits)
	n := exp + 1 // value = 0.digits * 10^n
	switch {
	case k <= n && n <= 21:
		return digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return "0." + strings.Repeat("0", -n) + digits
	}
	// Exponential form.
	var b strings.Builder
	b.WriteString(digits[:1])
	if k != 1 {
		b.WriteString("." + digits[1:])
	}
	if n-1 >= 0 {
		b.WriteString("e+")
	} else {
		b.WriteString("e-")
	}
	b.WriteString(strconv.Itoa(absInt(n - 1)))
	return b.String()
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// toFixed is Number.prototype.toFixed: the exact binary value is rounded to f
// fractional digits, ties going to the larger integer (not to even, as Go's
// own formatting does).
func toFixed(x float64, f int) string {
	if math.IsNaN(x) {
		return "NaN"
	}
	if x < 0 {
		return "-" + toFixed(-x, f)
	}
	if math.IsInf(x, 1) || x >= 1e21 {
		return numToString(x)
	}
	r := new(big.Rat).SetFloat64(x)
	if r == nil {
		return numToString(x)
	}
	scale := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(f)), nil))
	r.Mul(r, scale)
	r.Add(r, big.NewRat(1, 2))
	n := new(big.Int).Div(r.Num(), r.Denom()) // floor, both are positive here
	s := n.String()
	if f == 0 {
		return s
	}
	if len(s) <= f {
		s = strings.Repeat("0", f-len(s)+1) + s
	}
	return s[:len(s)-f] + "." + s[len(s)-f:]
}

// toFixedNum is `+n.toFixed(f)`: round to f digits, then print as a number.
func toFixedNum(x float64, f int) float64 {
	v, err := strconv.ParseFloat(toFixed(x, f), 64)
	if err != nil {
		return x
	}
	return v
}

// parseFloatJS is the global parseFloat: the longest leading decimal literal,
// NaN when there is none.
func parseFloatJS(s string) float64 {
	t := strings.TrimLeft(s, " \t\n\r\v\f\u00a0\ufeff")
	i, seenDigit, seenDot, seenExp := 0, false, false, false
	if i < len(t) && (t[i] == '+' || t[i] == '-') {
		i++
	}
	if strings.HasPrefix(t[i:], "Infinity") {
		if t[0] == '-' {
			return math.Inf(-1)
		}
		return math.Inf(1)
	}
	end := i
	for ; i < len(t); i++ {
		c := t[i]
		switch {
		case c >= '0' && c <= '9':
			seenDigit = true
			end = i + 1
		case c == '.' && !seenDot && !seenExp:
			seenDot = true
		case (c == 'e' || c == 'E') && seenDigit && !seenExp:
			j := i + 1
			if j < len(t) && (t[j] == '+' || t[j] == '-') {
				j++
			}
			if j < len(t) && t[j] >= '0' && t[j] <= '9' {
				seenExp = true
				i = j
				end = j + 1
				continue
			}
			i = len(t)
		default:
			i = len(t)
		}
	}
	if !seenDigit {
		return math.NaN()
	}
	v, err := strconv.ParseFloat(t[:end], 64)
	if err != nil {
		return math.NaN()
	}
	return v
}

// parseIntJS is the global parseInt with an explicit radix.
func parseIntJS(s string, radix int) float64 {
	t := strings.TrimLeft(s, " \t\n\r\v\f\u00a0\ufeff")
	neg := false
	if len(t) > 0 && (t[0] == '+' || t[0] == '-') {
		neg = t[0] == '-'
		t = t[1:]
	}
	if radix == 16 || radix == 0 {
		if len(t) > 1 && t[0] == '0' && (t[1] == 'x' || t[1] == 'X') {
			t = t[2:]
			radix = 16
		} else if radix == 0 {
			radix = 10
		}
	}
	digits := 0
	for digits < len(t) && digitVal(t[digits]) < radix {
		digits++
	}
	if digits == 0 {
		return math.NaN()
	}
	v := 0.0
	for i := 0; i < digits; i++ {
		v = v*float64(radix) + float64(digitVal(t[i]))
	}
	if neg {
		return -v
	}
	return v
}

func digitVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'Z':
		return int(c-'A') + 10
	}
	return 99
}

// toStringRadix is Number.prototype.toString(radix) for the integers the
// base-convert built-in passes it.
func toStringRadix(x float64, radix int) string {
	if math.IsNaN(x) {
		return "NaN"
	}
	if x < 0 {
		return "-" + toStringRadix(-x, radix)
	}
	i, frac := math.Modf(x)
	s := big.NewInt(int64(i)).Text(radix)
	if frac == 0 {
		return s
	}
	// Fractional digits, as V8 emits them: up to 20 places, trailing zeros cut.
	var b strings.Builder
	b.WriteString(s)
	b.WriteByte('.')
	for n := 0; n < 20 && frac > 0; n++ {
		frac *= float64(radix)
		d := int(frac)
		b.WriteByte("0123456789abcdefghijklmnopqrstuvwxyz"[d])
		frac -= float64(d)
	}
	return strings.TrimRight(b.String(), "0")
}
