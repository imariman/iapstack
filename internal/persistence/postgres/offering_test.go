package postgres

import (
	"strings"
	"testing"
)

// TestConfirmStoreProductsQueryDoesNotWrite verifies offering membership lookup cannot create catalog rows.
func TestConfirmStoreProductsQueryDoesNotWrite(t *testing.T) {
	t.Parallel()

	statement := strings.ToUpper(confirmStoreProductsQuery)
	if !strings.Contains(statement, "SELECT") || !strings.Contains(statement, "FROM STORE_PRODUCTS") {
		t.Fatalf("query = %q, want a store_products read", confirmStoreProductsQuery)
	}
	for _, forbidden := range []string{"INSERT", "UPDATE", "DELETE", "MERGE", "TRUNCATE"} {
		if strings.Contains(statement, forbidden) {
			t.Fatalf("query contains %s: %s", forbidden, confirmStoreProductsQuery)
		}
	}
}
