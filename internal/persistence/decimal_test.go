package persistence

import (
	"strings"
	"testing"
)

// TestParseDecimalKeepsExactScale verifies money values are never parsed as floats.
func TestParseDecimalKeepsExactScale(t *testing.T) {
	t.Parallel()

	positive, err := ParseDecimal("2.50")
	if err != nil || positive.String() != "2.50" || positive.Scale() != 2 {
		t.Fatalf("ParseDecimal(2.50) = (%s, %d, %v)", positive.String(), positive.Scale(), err)
	}
	negative, err := ParseDecimal("-1.25")
	if err != nil || negative.String() != "-1.25" || negative.Sign() != -1 {
		t.Fatalf("ParseDecimal(-1.25) = (%s, %d, %v)", negative.String(), negative.Sign(), err)
	}
	product := ParseDecimalMust(t, "1").Mul(positive)
	if product.Cmp(positive) != 0 {
		t.Fatalf("quantity times partner share = %s, want 2.50", product.String())
	}
	sum, err := positive.Add(ParseDecimalMust(t, "0.25"))
	if err != nil || sum.String() != "2.75" {
		t.Fatalf("Add() = (%s, %v), want 2.75", sum.String(), err)
	}
	if _, err := positive.Add(ParseDecimalMust(t, "0.250")); err == nil {
		t.Fatal("Add() mixed scale succeeded")
	}
	same, err := ParseDecimal("2.500")
	if err != nil || positive.Cmp(same) != 0 {
		t.Fatalf("Cmp trailing zeros = %v, %v", positive.Cmp(same), err)
	}
	minor, err := positive.MinorUnits(2)
	if err != nil || minor == nil || *minor != 250 {
		t.Fatalf("MinorUnits(2) = (%v, %v), want 250", minor, err)
	}
	wholeYen, err := ParseDecimalMust(t, "120.00").MinorUnits(0)
	if err != nil || wholeYen == nil || *wholeYen != 120 {
		t.Fatalf("MinorUnits exact trailing zeros = (%v, %v), want 120", wholeYen, err)
	}
	if _, err := ParseDecimalMust(t, "120.50").MinorUnits(0); err == nil {
		t.Fatal("MinorUnits accepted a non-zero remainder beyond the exponent")
	}
	coarser, err := positive.MinorUnits(3)
	if err != nil || coarser != nil {
		t.Fatalf("MinorUnits coarser exponent = (%v, %v), want nil", coarser, err)
	}
	sentinel := "SENTINEL-DECIMAL-9f3c2a"
	if _, err := ParseDecimal(sentinel); err == nil || strings.Contains(err.Error(), sentinel) {
		t.Fatalf("ParseDecimal error = %v, want a value-free invalid decimal", err)
	}
}

// ParseDecimalMust parses a fixture decimal or fails the test.
func ParseDecimalMust(t *testing.T, raw string) Decimal {
	t.Helper()
	decimal, err := ParseDecimal(raw)
	if err != nil {
		t.Fatalf("ParseDecimal(%q) error = %v", raw, err)
	}
	return decimal
}
