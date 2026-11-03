// Package amount handles CS amounts exactly: an amount is an integer number of units of
// 10^-18 CS (the node's general.Amount is integral + fraction/10^18). No floating point.
package amount

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"

	"github.com/CreditsCommunity/wallet/internal/echo"
	"github.com/CreditsCommunity/wallet/thrift/general"
)

// Decimals is the number of fractional digits of CS.
const Decimals = 18

var unit = new(big.Int).Exp(big.NewInt(10), big.NewInt(Decimals), nil) // 10^18

// Amount is a number of 10^-18 CS units. The zero value is 0 CS.
type Amount struct{ units big.Int }

// FromUnits returns the amount of the given number of 10^-18 units.
func FromUnits(u *big.Int) Amount {
	var a Amount
	a.units.Set(u)
	return a
}

// Units returns the amount in 10^-18 units.
func (a Amount) Units() *big.Int { return new(big.Int).Set(&a.units) }

// Sign returns -1, 0 or +1.
func (a Amount) Sign() int { return a.units.Sign() }

// Cmp compares two amounts.
func (a Amount) Cmp(b Amount) int { return a.units.Cmp(&b.units) }

// Add returns a + b.
func (a Amount) Add(b Amount) Amount {
	var r Amount
	r.units.Add(&a.units, &b.units)
	return r
}

// Neg returns -a.
func (a Amount) Neg() Amount {
	var r Amount
	r.units.Neg(&a.units)
	return r
}

// Parse reads a decimal amount such as "12.5", "1000" or "0.000000000000000001". It accepts at
// most 18 fractional digits (more would be lost, so it is an error); no sign, no exponent and no
// thousands separators (',' is a decimal mark in some locales: refusing it avoids a 1000x mistake).
func Parse(text string) (Amount, error) {
	var a Amount
	s := strings.TrimSpace(text)
	whole, frac, dot := strings.Cut(s, ".")
	if s == "" || (whole == "" && frac == "") || (dot && frac == "" && whole == "") {
		return a, fmt.Errorf("%s: not an amount", echo.Quote(text))
	}
	for _, part := range []string{whole, frac} {
		for _, c := range part {
			if c < '0' || c > '9' {
				return a, fmt.Errorf("%s: not an amount (digits and one '.' only)", echo.Quote(text))
			}
		}
	}
	if len(frac) > Decimals {
		return a, fmt.Errorf("%s: CS has at most %d decimals", echo.Quote(text), Decimals)
	}
	digits := whole + frac + strings.Repeat("0", Decimals-len(frac))
	if _, ok := a.units.SetString(digits, 10); !ok {
		return a, fmt.Errorf("%s: not an amount", echo.Quote(text))
	}
	return a, nil
}

// ParsePositive is Parse for amounts to send: more than 0 and representable by the node.
func ParsePositive(text string) (Amount, error) {
	a, err := Parse(text)
	if err != nil {
		return a, err
	}
	if a.Sign() <= 0 {
		return a, fmt.Errorf("%s: the amount must be more than 0", echo.Quote(text))
	}
	if _, err := a.Thrift(); err != nil {
		return a, fmt.Errorf("%s: %w", echo.Quote(text), err)
	}
	return a, nil
}

// String prints the exact amount without trailing zeros: "12.5", "0", "-3.000001".
func (a Amount) String() string {
	return a.format(false, -1)
}

// Grouped prints the amount with thousands separators: "12,345.678".
func (a Amount) Grouped() string {
	return a.format(true, -1)
}

// Fixed prints the amount with exactly n decimals (rounded half up, away from zero), grouped.
func (a Amount) Fixed(n int) string {
	return a.format(true, n)
}

func (a Amount) format(group bool, decimals int) string {
	u := new(big.Int).Abs(&a.units)
	if decimals >= 0 && decimals < Decimals {
		// round half up on the dropped digits
		step := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(Decimals-decimals)), nil)
		half := new(big.Int).Rsh(step, 1)
		u.Add(u, half)
		u.Sub(u, new(big.Int).Mod(u, step))
	}
	whole, frac := new(big.Int).QuoRem(u, unit, new(big.Int))
	fracText := fmt.Sprintf("%018s", frac.String())
	if decimals >= 0 {
		fracText = fracText[:min(decimals, Decimals)]
	} else {
		fracText = strings.TrimRight(fracText, "0")
	}
	wholeText := whole.String()
	if group {
		wholeText = groupThousands(wholeText)
	}
	s := wholeText
	if fracText != "" {
		s += "." + fracText
	}
	if a.units.Sign() < 0 && strings.Trim(s, "0.,") != "" {
		s = "-" + s
	}
	return s
}

func groupThousands(digits string) string {
	var b strings.Builder
	for i, c := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// FromThrift converts the node's general.Amount (integral + fraction/10^18).
func FromThrift(t *general.Amount) Amount {
	var a Amount
	if t == nil {
		return a
	}
	a.units.Mul(big.NewInt(int64(t.Integral)), unit)
	a.units.Add(&a.units, big.NewInt(t.Fraction))
	return a
}

// Thrift converts to the node's general.Amount: integral (i32) and fraction in [0, 10^18).
func (a Amount) Thrift() (*general.Amount, error) {
	integral, frac := new(big.Int).DivMod(&a.units, unit, new(big.Int)) // Euclidean: frac >= 0
	if !integral.IsInt64() || integral.Int64() > math.MaxInt32 || integral.Int64() < math.MinInt32 {
		return nil, errors.New("too large for the node (more than 2^31 CS)")
	}
	return &general.Amount{Integral: int32(integral.Int64()), Fraction: frac.Int64()}, nil
}

// Commission is the node's 16-bit fee encoding (csdb::AmountCommission): bit 15 sign, bits
// 10-14 the exponent e (biased by 18), bits 0-9 the mantissa m; the value is m/1024 * 10^(e-18).
type Commission uint16

// Amount returns the exact value of the encoded fee, truncated to 10^-18 CS.
func (c Commission) Amount() Amount {
	var a Amount
	e := int64((c >> 10) & 0x1F)
	m := int64(c & 0x3FF)
	// m/1024 * 10^(e-18) CS = m * 10^e / 1024 units
	a.units.Exp(big.NewInt(10), big.NewInt(e), nil)
	a.units.Mul(&a.units, big.NewInt(m))
	a.units.Quo(&a.units, big.NewInt(1024))
	if c&0x8000 != 0 {
		a.units.Neg(&a.units)
	}
	return a
}

// EncodeCommission mirrors csdb::AmountCommission(double) for a positive fee given as a float.
// It is used only with constants (the max fee a sender accepts), as the node's sample clients do.
func EncodeCommission(value float64) Commission {
	v := math.Abs(value)
	expf := math.Log10(v)
	var expi int
	if expf >= 0 {
		expi = int(expf + 0.5)
	} else {
		expi = int(expf - 0.5)
	}
	v /= math.Pow(10, float64(expi))
	if v >= 1.0 {
		v *= 0.1
		expi++
	}
	return Commission((((expi + 18) & 0x1F) << 10) | (int(math.Round(v*1024)) & 0x3FF))
}

// ---- token amounts -------------------------------------------------------------------------------

// MaxTokenDecimals bounds the decimals a token may declare (getDecimal); more is not a token the
// wallet handles.
const MaxTokenDecimals = 36

// ParseToken reads a positive token amount with at most decimals fractional digits, with the same
// rules as Parse (digits and one '.', no sign, no exponent, no separators). It returns the amount
// as the plain decimal text the contract receives (no leading zeros, trailing fractional zeros
// dropped: "0012.50" -> "12.5").
func ParseToken(text string, decimals int) (string, error) {
	if decimals < 0 || decimals > MaxTokenDecimals {
		return "", fmt.Errorf("the token declares %d decimals: not supported", decimals)
	}
	s := strings.TrimSpace(text)
	whole, frac, _ := strings.Cut(s, ".")
	if s == "" || (whole == "" && frac == "") {
		return "", fmt.Errorf("%s: not an amount", echo.Quote(text))
	}
	for _, part := range []string{whole, frac} {
		for _, c := range part {
			if c < '0' || c > '9' {
				return "", fmt.Errorf("%s: not an amount (digits and one '.' only)", echo.Quote(text))
			}
		}
	}
	frac = strings.TrimRight(frac, "0")
	if len(frac) > decimals {
		return "", fmt.Errorf("%s: this token has %d decimals", echo.Quote(text), decimals)
	}
	whole = strings.TrimLeft(whole, "0")
	if whole == "" {
		whole = "0"
	}
	if whole == "0" && frac == "" {
		return "", fmt.Errorf("%s: the amount must be more than 0", echo.Quote(text))
	}
	if len(whole) > 40 {
		return "", fmt.Errorf("%s: too large", echo.Quote(text))
	}
	if frac == "" {
		return whole, nil
	}
	return whole + "." + frac, nil
}

// CompareDecimal compares two decimal texts as the node and contracts write them (a BigDecimal's
// toString, possibly with an exponent): -1, 0 or +1, and false when one is not a number.
func CompareDecimal(a, b string) (int, bool) {
	x, ok1 := new(big.Rat).SetString(strings.TrimSpace(a))
	y, ok2 := new(big.Rat).SetString(strings.TrimSpace(b))
	if !ok1 || !ok2 {
		return 0, false
	}
	return x.Cmp(y), true
}

// IsZero tells whether the amount is 0.
func (a Amount) IsZero() bool { return a.units.Sign() == 0 }
