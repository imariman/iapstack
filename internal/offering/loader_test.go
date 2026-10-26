package offering_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/offering"
	"github.com/imariman/iapstack/internal/persistence"
)

// membershipCatalog is a read-only store-product double. It has no insert method.
type membershipCatalog struct {
	products map[core.ApplicationID]map[core.ProviderProductID]struct{}
	calls    int
}

// ConfirmStoreProducts accepts only provider products already present for the application.
func (catalog *membershipCatalog) ConfirmStoreProducts(
	_ context.Context,
	applicationID core.ApplicationID,
	ids []core.ProviderProductID,
) error {
	catalog.calls++
	known := catalog.products[applicationID]
	for _, id := range ids {
		if _, exists := known[id]; !exists {
			return persistence.ErrNotFound
		}
	}
	return nil
}

// TestLoadHappyDocument verifies a confined file whose store products already exist.
func TestLoadHappyDocument(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	writeOfferingFixture(t, directory, happyOffering("application-1"))
	catalog := &membershipCatalog{products: map[core.ApplicationID]map[core.ProviderProductID]struct{}{
		"application-1": {"premium_monthly": {}},
	}}
	before := len(catalog.products["application-1"])

	document, err := offering.Load(context.Background(), directory, "application-1", catalog)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if document.SchemaVersion != 1 || document.ApplicationID != "application-1" || document.Revision != "revision-1" {
		t.Fatalf("Load() document = %#v", document)
	}
	if len(document.Offerings) != 1 || document.Offerings[0].Packages[0].StoreProductID != "premium_monthly" {
		t.Fatalf("Load() offerings = %#v", document.Offerings)
	}
	if catalog.calls != 1 || len(catalog.products["application-1"]) != before {
		t.Fatalf("catalog calls = %d, products = %d, want one read and no new rows", catalog.calls, len(catalog.products["application-1"]))
	}
}

// TestLoadRejectsUnknownStoreProduct verifies one unknown ID fails closed for that application only.
func TestLoadRejectsUnknownStoreProduct(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	rejected := happyOffering("application-bad")
	rejected.Offerings[0].Packages = append(rejected.Offerings[0].Packages, offering.Package{
		ID: "extra", StoreProductID: "missing_product", DisplayKey: "missing_product",
	})
	writeOfferingFixture(t, directory, rejected)
	writeOfferingFixture(t, directory, happyOffering("application-ok"))
	catalog := &membershipCatalog{products: map[core.ApplicationID]map[core.ProviderProductID]struct{}{
		"application-bad": {"premium_monthly": {}},
		"application-ok":  {"premium_monthly": {}},
	}}
	before := len(catalog.products["application-bad"])

	_, err := offering.Load(context.Background(), directory, "application-bad", catalog)
	if !errors.Is(err, offering.ErrRejected) || errors.Is(err, offering.ErrNotFound) {
		t.Fatalf("Load() unknown product error = %v, want ErrRejected", err)
	}
	if len(catalog.products["application-bad"]) != before {
		t.Fatal("unknown store product created a catalog row")
	}

	document, err := offering.Load(context.Background(), directory, "application-ok", catalog)
	if err != nil || document.ApplicationID != "application-ok" {
		t.Fatalf("Load() sibling = %#v, error = %v", document, err)
	}
}

// TestLoadMissingFileNotFound verifies an absent file and an empty directory stay not-found.
func TestLoadMissingFileNotFound(t *testing.T) {
	t.Parallel()

	catalog := &membershipCatalog{products: map[core.ApplicationID]map[core.ProviderProductID]struct{}{}}
	_, err := offering.Load(context.Background(), t.TempDir(), "application-1", catalog)
	if !errors.Is(err, offering.ErrNotFound) || errors.Is(err, offering.ErrRejected) {
		t.Fatalf("Load() missing file error = %v, want ErrNotFound", err)
	}
	_, err = offering.Load(context.Background(), "  ", "application-1", nil)
	if !errors.Is(err, offering.ErrNotFound) {
		t.Fatalf("Load() empty directory error = %v, want ErrNotFound", err)
	}
	if catalog.calls != 0 {
		t.Fatalf("catalog calls = %d, want 0", catalog.calls)
	}
}

// TestLoadRejectsOversizedDocument verifies a file above 64 KiB is rejected before catalog lookup.
func TestLoadRejectsOversizedDocument(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	payload := make([]byte, offering.MaxDocumentBytes+1)
	for index := range payload {
		payload[index] = 'x'
	}
	if err := os.WriteFile(filepath.Join(directory, "application-1.json"), payload, 0o600); err != nil {
		t.Fatalf("write oversized offering: %v", err)
	}
	catalog := &membershipCatalog{}

	_, err := offering.Load(context.Background(), directory, "application-1", catalog)
	if !errors.Is(err, offering.ErrRejected) {
		t.Fatalf("Load() oversized error = %v, want ErrRejected", err)
	}
	if catalog.calls != 0 {
		t.Fatalf("catalog calls = %d, want 0", catalog.calls)
	}
}

// TestLoadRejectsEscapingApplicationID verifies an application ID cannot leave the offering directory.
func TestLoadRejectsEscapingApplicationID(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	directory := filepath.Join(root, "offerings")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatalf("mkdir offerings: %v", err)
	}
	outside := happyOffering("application-1")
	outside.Revision = "escaped-revision"
	payload, err := json.Marshal(outside)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret.json"), payload, 0o600); err != nil {
		t.Fatalf("write outside offering: %v", err)
	}
	catalog := &membershipCatalog{products: map[core.ApplicationID]map[core.ProviderProductID]struct{}{
		"../secret": {"premium_monthly": {}},
	}}

	_, err = offering.Load(context.Background(), directory, "../secret", catalog)
	if !errors.Is(err, offering.ErrNotFound) {
		t.Fatalf("Load() escaping id error = %v, want ErrNotFound", err)
	}
	if catalog.calls != 0 {
		t.Fatal("escaping application id reached the catalog")
	}
}

// happyOffering returns one valid presentation document for the named application.
func happyOffering(applicationID core.ApplicationID) offering.Document {
	return offering.Document{
		SchemaVersion: 1,
		ApplicationID: applicationID,
		Revision:      "revision-1",
		Offerings: []offering.Offering{{
			ID: "default",
			Packages: []offering.Package{{
				ID: "monthly", StoreProductID: "premium_monthly", DisplayKey: "premium_monthly",
			}},
		}},
	}
}

// writeOfferingFixture stores one document under its application file name.
func writeOfferingFixture(t *testing.T, directory string, document offering.Document) {
	t.Helper()

	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, string(document.ApplicationID)+".json"), payload, 0o600); err != nil {
		t.Fatalf("write offering: %v", err)
	}
}
