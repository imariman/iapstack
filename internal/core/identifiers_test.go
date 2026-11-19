package core_test

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/imariman/iapstack/internal/core"
)

// externalCustomerIDVectorsPath is the shared case file every client SDK reads too.
var externalCustomerIDVectorsPath = filepath.Join("..", "..", "contracts", "test-vectors", "external-customer-ids.json")

// externalCustomerIDVector is one shared accept or reject case.
type externalCustomerIDVector struct {
	Name     string `json:"name"`
	Value    []any  `json:"value"`
	Accepted bool   `json:"accepted"`
}

// TestExternalCustomerIDMatchesSharedVectors keeps the server rule and the SDK copies in parity.
func TestExternalCustomerIDMatchesSharedVectors(t *testing.T) {
	t.Parallel()

	vectors := readExternalCustomerIDVectors(t)
	accepted, rejected := 0, 0
	for _, vector := range vectors {
		value := externalCustomerIDVectorValue(t, vector)
		err := core.ValidateExternalCustomerID(value)
		if vector.Accepted {
			accepted++
			if err != nil {
				t.Errorf("%s: ValidateExternalCustomerID(%q) error = %v, want nil", vector.Name, value, err)
			}
			continue
		}
		rejected++
		if err == nil {
			t.Errorf("%s: ValidateExternalCustomerID(%q) error = nil, want rejection", vector.Name, value)
		}
	}
	if accepted == 0 || rejected == 0 {
		t.Fatalf("shared vectors need accepted and rejected cases, got %d and %d", accepted, rejected)
	}
}

// readExternalCustomerIDVectors loads the shared file and checks its parser-proof format.
func readExternalCustomerIDVectors(t *testing.T) []externalCustomerIDVector {
	t.Helper()

	body, err := os.ReadFile(externalCustomerIDVectorsPath)
	if err != nil {
		t.Fatalf("read %s: %v", externalCustomerIDVectorsPath, err)
	}
	var document struct {
		Cases []externalCustomerIDVector `json:"cases"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatalf("decode %s: %v", externalCustomerIDVectorsPath, err)
	}
	if len(document.Cases) == 0 {
		t.Fatalf("%s has no cases", externalCustomerIDVectorsPath)
	}
	names := make(map[string]bool, len(document.Cases))
	for _, vector := range document.Cases {
		if vector.Name == "" || names[vector.Name] {
			t.Fatalf("vector name %q is empty or repeated", vector.Name)
		}
		names[vector.Name] = true
	}
	return document.Cases
}

// externalCustomerIDVectorValue joins the segments as UTF-16 code units and encodes them as Go bytes.
//
// A surrogate code unit that does not form a pair gets its three-byte
// generalized UTF-8 form, which utf8.ValidString rejects, so the UTF-16
// "unpaired surrogate" cases map to the server's "invalid UTF-8" rule.
func externalCustomerIDVectorValue(t *testing.T, vector externalCustomerIDVector) string {
	t.Helper()

	var units []uint16
	for _, segment := range vector.Value {
		switch value := segment.(type) {
		case string:
			for index := 0; index < len(value); index++ {
				char := value[index]
				if char < 0x20 || char > 0x7e || char == '"' || char == '\\' {
					t.Fatalf("%s: string segment %q must hold printable ASCII without quotes or backslashes", vector.Name, value)
				}
				units = append(units, uint16(char))
			}
		case float64:
			if value != math.Trunc(value) || value < 0 || value > 0xffff {
				t.Fatalf("%s: number segment %v is not a UTF-16 code unit", vector.Name, value)
			}
			units = append(units, uint16(value))
		default:
			t.Fatalf("%s: segment %v must be a string or a number", vector.Name, segment)
		}
	}

	var encoded []byte
	for index := 0; index < len(units); index++ {
		unit := rune(units[index])
		if utf16.IsSurrogate(unit) && index+1 < len(units) {
			if pair := utf16.DecodeRune(unit, rune(units[index+1])); pair != utf8.RuneError {
				encoded = utf8.AppendRune(encoded, pair)
				index++
				continue
			}
		}
		if utf16.IsSurrogate(unit) {
			encoded = append(encoded, 0xe0|byte(unit>>12), 0x80|byte(unit>>6&0x3f), 0x80|byte(unit&0x3f))
			continue
		}
		encoded = utf8.AppendRune(encoded, unit)
	}
	return string(encoded)
}
