package offering

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
)

var (
	// ErrNotFound means this application has no offering document to serve.
	ErrNotFound = errors.New("offering document not found")
	// ErrRejected means the offering document must not be served for this application.
	ErrRejected = errors.New("offering document rejected")
)

// Load reads one application offering and confirms every store product already exists.
// An empty directory or a missing file returns ErrNotFound. An oversized, invalid,
// or unknown-product document returns ErrRejected and does not create catalog rows.
func Load(
	ctx context.Context,
	directory string,
	applicationID core.ApplicationID,
	catalog persistence.OfferingCatalog,
) (Document, error) {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return Document{}, fmt.Errorf("offering directory is not configured: %w", ErrNotFound)
	}
	name, err := documentName(applicationID)
	if err != nil {
		return Document{}, err
	}

	payload, err := readDocument(directory, name)
	if err != nil {
		return Document{}, err
	}
	document, err := parseDocument(payload, applicationID)
	if err != nil {
		return Document{}, err
	}
	if catalog == nil {
		return Document{}, fmt.Errorf("offering catalog is not configured: %w", ErrRejected)
	}
	if err := catalog.ConfirmStoreProducts(ctx, applicationID, storeProductIDs(document)); err != nil {
		if errors.Is(err, persistence.ErrUnavailable) {
			return Document{}, err
		}
		if errors.Is(err, persistence.ErrNotFound) {
			return Document{}, fmt.Errorf("store product is not on store_products: %w", ErrRejected)
		}
		return Document{}, err
	}
	return document, nil
}

// documentName maps one application ID onto a single file inside the offering directory.
func documentName(applicationID core.ApplicationID) (string, error) {
	if err := applicationID.Validate(); err != nil {
		return "", fmt.Errorf("offering application_id: %w", ErrNotFound)
	}
	name := string(applicationID) + ".json"
	if strings.ContainsAny(string(applicationID), `/\`) || !filepath.IsLocal(name) {
		return "", fmt.Errorf("offering application_id: %w", ErrNotFound)
	}
	return name, nil
}

// readDocument loads one confined offering file or rejects an oversized document.
func readDocument(directory, name string) ([]byte, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("open offering directory: %w", ErrNotFound)
		}
		return nil, fmt.Errorf("open offering directory: %w", err)
	}
	defer root.Close()

	file, err := root.Open(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("open offering document: %w", ErrNotFound)
		}
		return nil, fmt.Errorf("open offering document: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat offering document: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > MaxDocumentBytes {
		return nil, fmt.Errorf("offering document is not a bounded file: %w", ErrRejected)
	}
	payload, err := io.ReadAll(io.LimitReader(file, MaxDocumentBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read offering document: %w", err)
	}
	if len(payload) > MaxDocumentBytes {
		return nil, fmt.Errorf("offering document exceeds %d bytes: %w", MaxDocumentBytes, ErrRejected)
	}
	return payload, nil
}

// parseDocument decodes one allowlisted offering and rejects unknown or mismatched fields.
func parseDocument(payload []byte, applicationID core.ApplicationID) (Document, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var document Document
	if err := decoder.Decode(&document); err != nil {
		return Document{}, fmt.Errorf("parse offering document: %w", ErrRejected)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Document{}, fmt.Errorf("parse offering document: %w", ErrRejected)
	}
	if err := document.validate(applicationID); err != nil {
		return Document{}, err
	}
	return document, nil
}

// validate checks the public offering shape for one requested application.
func (document Document) validate(applicationID core.ApplicationID) error {
	if document.SchemaVersion != documentSchemaVersion {
		return fmt.Errorf("offering schema_version: %w", ErrRejected)
	}
	if err := document.ApplicationID.Validate(); err != nil || document.ApplicationID != applicationID {
		return fmt.Errorf("offering application_id: %w", ErrRejected)
	}
	if !validRevision(document.Revision) {
		return fmt.Errorf("offering revision: %w", ErrRejected)
	}
	if len(document.Offerings) == 0 {
		return fmt.Errorf("offering offerings: %w", ErrRejected)
	}
	offeringIDs := make(map[string]struct{}, len(document.Offerings))
	for _, group := range document.Offerings {
		if err := validIdentifier(group.ID); err != nil {
			return fmt.Errorf("offering id: %w", ErrRejected)
		}
		if _, exists := offeringIDs[group.ID]; exists {
			return fmt.Errorf("duplicate offering id: %w", ErrRejected)
		}
		offeringIDs[group.ID] = struct{}{}
		if len(group.Packages) == 0 {
			return fmt.Errorf("offering packages: %w", ErrRejected)
		}
		packageIDs := make(map[string]struct{}, len(group.Packages))
		for _, item := range group.Packages {
			if err := validIdentifier(item.ID); err != nil {
				return fmt.Errorf("offering package id: %w", ErrRejected)
			}
			if _, exists := packageIDs[item.ID]; exists {
				return fmt.Errorf("duplicate offering package id: %w", ErrRejected)
			}
			packageIDs[item.ID] = struct{}{}
			if err := item.StoreProductID.Validate(); err != nil {
				return fmt.Errorf("offering store_product_id: %w", ErrRejected)
			}
			if item.DisplayKey != "" {
				if err := validIdentifier(item.DisplayKey); err != nil {
					return fmt.Errorf("offering display_key: %w", ErrRejected)
				}
			}
		}
	}
	return nil
}

// storeProductIDs returns each distinct provider product named by the document.
func storeProductIDs(document Document) []core.ProviderProductID {
	ids := make([]core.ProviderProductID, 0)
	seen := make(map[core.ProviderProductID]struct{})
	for _, group := range document.Offerings {
		for _, item := range group.Packages {
			if _, exists := seen[item.StoreProductID]; exists {
				continue
			}
			seen[item.StoreProductID] = struct{}{}
			ids = append(ids, item.StoreProductID)
		}
	}
	return ids
}

// validRevision reports whether a revision is safe to copy into an ETag.
func validRevision(value string) bool {
	if len(value) == 0 || len(value) > maxRevisionLength {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '.' && character != '_' && character != '-' && character != '~' {
			return false
		}
	}
	return true
}

// validIdentifier applies the shared opaque identifier rules to one offering token.
func validIdentifier(value string) error {
	return core.ApplicationID(value).Validate()
}
