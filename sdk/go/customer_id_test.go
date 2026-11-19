package iapstack

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

// externalCustomerIDVector is one case from the shared repository vector file.
type externalCustomerIDVector struct {
	Name     string `json:"name"`
	Value    []any  `json:"value"`
	Accepted bool   `json:"accepted"`
}

// TestValidateExternalCustomerIDMatchesSharedVectors keeps this copy in parity with the server rule.
func TestValidateExternalCustomerIDMatchesSharedVectors(t *testing.T) {
	t.Parallel()

	for _, vector := range readExternalCustomerIDVectors(t) {
		value := externalCustomerIDVectorValue(t, vector)
		err := validateExternalCustomerID(value)
		if vector.Accepted && err != nil {
			t.Errorf("%s: validateExternalCustomerID(%q) error = %v, want nil", vector.Name, value, err)
		}
		if !vector.Accepted && err == nil {
			t.Errorf("%s: validateExternalCustomerID(%q) error = nil, want rejection", vector.Name, value)
		}
	}
}

// TestCustomerIDsTheServerRejectsNeverReachHTTP verifies both entry points fail before sending.
func TestCustomerIDsTheServerRejectsNeverReachHTTP(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		t.Error("HTTP should not run for a customer ID the server rejects")
	}))
	for _, value := range []string{"customer\x001", "customer\u00851", "customer\x7f1", "customer\xed\xa0\x801", "customer-1　"} {
		_, err := client.CreateCustomerSession(context.Background(), value)
		if err == nil || err.Error() != "external customer ID must be non-empty valid UTF-8 without surrounding whitespace or control characters" {
			t.Errorf("CreateCustomerSession(%q) error = %v, want customer ID rejection", value, err)
		}
		_, err = client.GetEntitlements(context.Background(), CustomerSession{
			Token:              testCustomerToken,
			ExternalCustomerID: value,
		})
		if err == nil {
			t.Errorf("GetEntitlements(%q) accepted a customer ID the server rejects", value)
		}
	}
}

// readExternalCustomerIDVectors loads contracts/test-vectors/external-customer-ids.json.
func readExternalCustomerIDVectors(t *testing.T) []externalCustomerIDVector {
	t.Helper()

	var document struct {
		Cases []externalCustomerIDVector `json:"cases"`
	}
	body := readRepositoryFile(t, "contracts", "test-vectors", "external-customer-ids.json")
	if err := json.Unmarshal([]byte(body), &document); err != nil {
		t.Fatalf("decode shared customer ID vectors: %v", err)
	}
	if len(document.Cases) == 0 {
		t.Fatal("shared customer ID vectors are empty")
	}
	return document.Cases
}

// externalCustomerIDVectorValue joins the segments as UTF-16 code units and encodes them as Go bytes.
//
// A surrogate code unit that does not form a pair gets its three-byte
// generalized UTF-8 form, which is invalid UTF-8, the Go form of the
// "unpaired surrogate" cases.
func externalCustomerIDVectorValue(t *testing.T, vector externalCustomerIDVector) string {
	t.Helper()

	var units []uint16
	for _, segment := range vector.Value {
		switch value := segment.(type) {
		case string:
			for index := 0; index < len(value); index++ {
				units = append(units, uint16(value[index]))
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
