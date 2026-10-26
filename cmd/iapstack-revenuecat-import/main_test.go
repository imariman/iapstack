package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestRunDryDoesNotRequireADatabase verifies the default command writes a secret-free report.
func TestRunDryDoesNotRequireADatabase(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	mappingPath := directory + "/mapping.json"
	exportPath := directory + "/transactions.csv"
	if err := writeFile(mappingPath, `{
		"project_id": "project-1",
		"canonical_id_column": "rc_original_app_user_id",
		"entitlements": {"premium": "premium"},
		"products": [{
			"store": "app_store",
			"environment": "production",
			"provider_product_id": "com.example.annual",
			"application_id": "application-apple",
			"product_id": "product-annual"
		}]
	}`); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(exportPath, ""+
		"rc_original_app_user_id,rc_last_seen_app_user_id_alias,product_identifier,entitlement_identifiers,start_time,effective_end_time,store,is_auto_renewable,is_sandbox,updated_at\n"+
		"user-1,user-1,com.example.annual,\"[\"\"premium\"\"]\",2026-10-01 00:00:00,2026-11-01 00:00:00,app_store,true,false,2026-10-02 00:00:00\n"); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--project", "project-1",
		"--mapping", mappingPath,
		"--export", exportPath,
		"--format", "transactions-csv",
		"--now", "2026-10-26T00:00:00Z",
	}, func(string) string { return "" }, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("run() = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"dry_run": true`) || strings.Contains(stdout.String(), "REVENUECAT") {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

// TestRunApplyRequiresDatabaseURL verifies apply does not open a network or invent a database.
func TestRunApplyRequiresDatabaseURL(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	mappingPath := directory + "/mapping.json"
	exportPath := directory + "/transactions.csv"
	_ = writeFile(mappingPath, `{"project_id":"project-1","canonical_id_column":"rc_original_app_user_id","entitlements":{"premium":"premium"},"products":[{"store":"app_store","environment":"production","provider_product_id":"annual","application_id":"application-apple","product_id":"product-annual"}]}`)
	_ = writeFile(exportPath, "rc_original_app_user_id,product_identifier,entitlement_identifiers,store,is_auto_renewable,is_sandbox\nuser,annual,[],app_store,false,false\n")
	var stderr bytes.Buffer
	code := run([]string{"--project", "project-1", "--mapping", mappingPath, "--export", exportPath, "--format", "transactions-csv", "--batch-id", "batch-1", "--apply"}, func(string) string { return "" }, &bytes.Buffer{}, &stderr, "")
	if code != 2 || !strings.Contains(stderr.String(), "IAPSTACK_DATABASE_URL") {
		t.Fatalf("run() = %d stderr=%s", code, stderr.String())
	}
}

// TestRunFetchRequiresAcknowledgement verifies the secret stays out of errors and the flag is mandatory.
func TestRunFetchRequiresAcknowledgement(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	code := run([]string{"--subscriber-ids-file", "ids.txt", "--fetch-output", "out.jsonl"}, func(key string) string {
		if key == "REVENUECAT_SECRET_API_KEY" {
			return "super-secret"
		}
		return ""
	}, &bytes.Buffer{}, &stderr, "")
	if code != 2 || strings.Contains(stderr.String(), "super-secret") {
		t.Fatalf("run() = %d stderr=%s", code, stderr.String())
	}
}

// writeFile is a tiny test helper.
func writeFile(path, contents string) error {
	return os.WriteFile(path, []byte(contents), 0o600)
}
