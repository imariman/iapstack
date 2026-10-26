package persistence

import (
	"errors"
	"math/big"
	"strings"
)

var (
	// ErrDecimalInvalid indicates that a money field is not an exact decimal.
	ErrDecimalInvalid = errors.New("decimal is invalid")
	// ErrDecimalScale indicates that a decimal scale is mixed or finer than the currency exponent.
	ErrDecimalScale = errors.New("decimal scale is invalid")
	// ErrDecimalOverflow indicates that a decimal coefficient does not fit in an int64 minor-unit field.
	ErrDecimalOverflow = errors.New("decimal does not fit the minor-unit integer")
)

// Decimal is an exact base-10 value. It never uses binary floating point.
type Decimal struct {
	coefficient *big.Int
	scale       int32
}

// ParseDecimal reads an exact decimal without echoing the input in errors.
func ParseDecimal(raw string) (Decimal, error) {
	if raw == "" || strings.TrimSpace(raw) != raw || len(raw) > 80 {
		return Decimal{}, ErrDecimalInvalid
	}
	sign := 1
	digits := raw
	switch raw[0] {
	case '+':
		sign = 1
		digits = raw[1:]
	case '-':
		sign = -1
		digits = raw[1:]
	}
	if digits == "" || strings.ContainsAny(digits, "eE+") {
		return Decimal{}, ErrDecimalInvalid
	}
	whole, fraction, found := strings.Cut(digits, ".")
	if strings.Contains(fraction, ".") || whole == "" || (found && fraction == "") {
		return Decimal{}, ErrDecimalInvalid
	}
	if !allDigits(whole) || (found && !allDigits(fraction)) {
		return Decimal{}, ErrDecimalInvalid
	}
	if int32(len(fraction)) > 18 {
		return Decimal{}, ErrDecimalInvalid
	}
	coefficient, ok := new(big.Int).SetString(whole+fraction, 10)
	if !ok {
		return Decimal{}, ErrDecimalInvalid
	}
	if sign < 0 && coefficient.Sign() != 0 {
		coefficient.Neg(coefficient)
	}
	return Decimal{coefficient: coefficient, scale: int32(len(fraction))}, nil
}

// String returns the exact decimal, preserving the original scale and trailing zeros.
func (decimal Decimal) String() string {
	if decimal.coefficient == nil {
		return ""
	}
	coefficient := new(big.Int).Set(decimal.coefficient)
	sign := ""
	if coefficient.Sign() < 0 {
		sign = "-"
		coefficient.Abs(coefficient)
	}
	digits := coefficient.Text(10)
	if decimal.scale == 0 {
		return sign + digits
	}
	if int32(len(digits)) <= decimal.scale {
		digits = strings.Repeat("0", int(decimal.scale)-len(digits)) + digits
		return sign + "0." + digits
	}
	split := len(digits) - int(decimal.scale)
	return sign + digits[:split] + "." + digits[split:]
}

// Scale returns the number of digits after the decimal point.
func (decimal Decimal) Scale() int32 {
	return decimal.scale
}

// Sign returns -1, 0, or 1.
func (decimal Decimal) Sign() int {
	if decimal.coefficient == nil {
		return 0
	}
	return decimal.coefficient.Sign()
}

// Add sums two decimals that already share a scale.
func (decimal Decimal) Add(other Decimal) (Decimal, error) {
	if decimal.coefficient == nil || other.coefficient == nil {
		return Decimal{}, ErrDecimalInvalid
	}
	if decimal.scale != other.scale {
		return Decimal{}, ErrDecimalScale
	}
	return Decimal{coefficient: new(big.Int).Add(decimal.coefficient, other.coefficient), scale: decimal.scale}, nil
}

// Mul multiplies two exact decimals and adds their scales.
func (decimal Decimal) Mul(other Decimal) Decimal {
	if decimal.coefficient == nil || other.coefficient == nil {
		return Decimal{}
	}
	return Decimal{
		coefficient: new(big.Int).Mul(decimal.coefficient, other.coefficient),
		scale:       decimal.scale + other.scale,
	}
}

// Cmp compares numeric value, ignoring differences in trailing zeros.
func (decimal Decimal) Cmp(other Decimal) int {
	if decimal.coefficient == nil || other.coefficient == nil {
		return 0
	}
	left := new(big.Int).Set(decimal.coefficient)
	right := new(big.Int).Set(other.coefficient)
	switch {
	case decimal.scale < other.scale:
		left.Mul(left, decimalPow10(other.scale-decimal.scale))
	case other.scale < decimal.scale:
		right.Mul(right, decimalPow10(decimal.scale-other.scale))
	}
	return left.Cmp(right)
}

// Rescale appends trailing zeros so the value matches a known stored scale.
func (decimal Decimal) Rescale(scale int32) (Decimal, error) {
	if decimal.coefficient == nil || scale < 0 {
		return Decimal{}, ErrDecimalInvalid
	}
	if scale < decimal.scale {
		return Decimal{}, ErrDecimalScale
	}
	if scale == decimal.scale {
		return decimal, nil
	}
	coefficient := new(big.Int).Mul(decimal.coefficient, decimalPow10(scale-decimal.scale))
	return Decimal{coefficient: coefficient, scale: scale}, nil
}

// ExactInt64 returns the coefficient when it fits in an int64.
func (decimal Decimal) ExactInt64() (int64, bool) {
	if decimal.coefficient == nil || !decimal.coefficient.IsInt64() {
		return 0, false
	}
	return decimal.coefficient.Int64(), true
}

// MinorUnits returns the integer value at the ISO exponent when it is exact.
// A coarser scale returns nil; trailing zeros beyond the exponent are discarded.
func (decimal Decimal) MinorUnits(exponent int32) (*int64, error) {
	if decimal.coefficient == nil || decimal.scale < 0 || exponent < 0 {
		return nil, ErrDecimalInvalid
	}
	if decimal.scale < exponent {
		return nil, nil
	}
	coefficient := new(big.Int).Set(decimal.coefficient)
	if decimal.scale > exponent {
		divisor := decimalPow10(decimal.scale - exponent)
		quotient, remainder := new(big.Int), new(big.Int)
		quotient.QuoRem(coefficient, divisor, remainder)
		if remainder.Sign() != 0 {
			return nil, ErrDecimalScale
		}
		coefficient = quotient
	}
	if !coefficient.IsInt64() {
		return nil, ErrDecimalOverflow
	}
	value := coefficient.Int64()
	return &value, nil
}

// allDigits reports whether value is one or more ASCII digits.
func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

// decimalPow10 returns 10^exponent for a non-negative exponent.
func decimalPow10(exponent int32) *big.Int {
	value := big.NewInt(1)
	if exponent <= 0 {
		return value
	}
	ten := big.NewInt(10)
	for range exponent {
		value.Mul(value, ten)
	}
	return value
}
