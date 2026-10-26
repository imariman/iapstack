package revenuecatimport

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/imariman/iapstack/internal/core"
)

// ReadExport returns the uncompressed export and its SHA-256.
// A short read or a gzip stream that does not end cleanly is truncated.
func ReadExport(reader io.Reader) ([]byte, [32]byte, error) {
	if reader == nil {
		return nil, [32]byte{}, fmt.Errorf("revenuecat export is required: %w", ErrUsage)
	}
	buffered := bufio.NewReader(reader)
	magic, err := buffered.Peek(2)
	if err != nil && err != io.EOF {
		return nil, [32]byte{}, fmt.Errorf("read revenuecat export: %w", ErrTruncated)
	}
	var payload []byte
	if len(magic) >= 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gzipReader, gzipErr := gzip.NewReader(buffered)
		if gzipErr != nil {
			return nil, [32]byte{}, fmt.Errorf("open revenuecat gzip export: %w", ErrTruncated)
		}
		payload, err = io.ReadAll(gzipReader)
		closeErr := gzipReader.Close()
		if err != nil || closeErr != nil {
			return nil, [32]byte{}, fmt.Errorf("read revenuecat gzip export: %w", ErrTruncated)
		}
	} else {
		payload, err = io.ReadAll(buffered)
		if err != nil {
			return nil, [32]byte{}, fmt.Errorf("read revenuecat export: %w", ErrTruncated)
		}
	}
	return payload, sha256.Sum256(payload), nil
}

// CustomerID derives the stable internal customer id for one canonical external id.
func CustomerID(projectID core.ProjectID, externalID string) core.CustomerID {
	sum := sha256.Sum256([]byte(string(projectID) + "\x00" + externalID))
	return core.CustomerID("cust-" + hex.EncodeToString(sum[:16]))
}

// GrantID derives the stable import grant id from the lineage and mapped catalog ids.
func GrantID(projectID, customerID, applicationID, productID, entitlementID string, lineage [32]byte) string {
	hash := sha256.New()
	for _, part := range []string{projectID, customerID, applicationID, productID, entitlementID} {
		_, _ = hash.Write([]byte(part))
		_, _ = hash.Write([]byte{0})
	}
	_, _ = hash.Write(lineage[:])
	return "imp-" + hex.EncodeToString(hash.Sum(nil)[:16])
}

// parseExportTime parses a RevenueCat UTC timestamp. An empty cell is absent.
func parseExportTime(value string) (time.Time, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false, nil
	}
	if len(value) >= 12 && value[10] == ' ' && value[12] == ':' {
		value = value[:11] + "0" + value[11:]
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
	} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed.UTC(), true, nil
		}
	}
	return time.Time{}, false, fmt.Errorf("revenuecat timestamp %q is invalid: %w", value, ErrUsage)
}

// parseBool accepts only the feed's true and false values.
func parseBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("revenuecat boolean %q is invalid: %w", value, ErrUsage)
	}
}

// exportSHA256Hex encodes a fingerprint for the secret-free report.
func exportSHA256Hex(sum [32]byte) string {
	if sum == ([32]byte{}) {
		return ""
	}
	return hex.EncodeToString(sum[:])
}

// bytesTrimBOM removes a leading UTF-8 byte order mark.
func bytesTrimBOM(payload []byte) []byte {
	return bytes.TrimPrefix(payload, []byte{0xef, 0xbb, 0xbf})
}
