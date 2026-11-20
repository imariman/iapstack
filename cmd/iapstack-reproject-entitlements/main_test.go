package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imariman/iapstack/internal/reprojection"
)

// TestRunRejectsInvalidUsageBeforeOpeningADatabase verifies every usage error exits 2 without a database.
func TestRunRejectsInvalidUsageBeforeOpeningADatabase(t *testing.T) {
	t.Parallel()

	withDatabase := func(key string) string {
		if key == "IAPSTACK_DATABASE_URL" {
			return "postgres://127.0.0.1:1/unused"
		}
		return ""
	}
	cases := []struct {
		name   string
		args   []string
		getenv func(string) string
		want   string
	}{
		{name: "no scope", args: nil, getenv: withDatabase, want: "exactly one of --project or --all-projects"},
		{name: "both scopes", args: []string{"--project", "project-1", "--all-projects"}, getenv: withDatabase, want: "exactly one of --project or --all-projects"},
		{name: "invalid project", args: []string{"--project", "bad\tproject"}, getenv: withDatabase, want: "project id is invalid"},
		{name: "apply and explicit dry run", args: []string{"--project", "project-1", "--apply", "--dry-run"}, getenv: withDatabase, want: "cannot be combined"},
		{name: "dry run disabled without apply", args: []string{"--project", "project-1", "--dry-run=false"}, getenv: withDatabase, want: "pass --apply to commit"},
		{name: "batch too small", args: []string{"--project", "project-1", "--batch-size", "0"}, getenv: withDatabase, want: "batch-size must be between 1 and 1000"},
		{name: "batch too large", args: []string{"--project", "project-1", "--batch-size", "1001"}, getenv: withDatabase, want: "batch-size must be between 1 and 1000"},
		{name: "extra argument", args: []string{"--project", "project-1", "extra"}, getenv: withDatabase, want: "unexpected arguments"},
		{name: "resume without project", args: []string{"--all-projects", "--after-customer", "customer-1"}, getenv: withDatabase, want: "--after-customer requires --project"},
		{name: "invalid resume customer", args: []string{"--project", "project-1", "--after-customer", "bad\tcustomer"}, getenv: withDatabase, want: "after-customer id is invalid"},
		{name: "missing database", args: []string{"--project", "project-1"}, getenv: func(string) string { return "" }, want: "IAPSTACK_DATABASE_URL is required"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), testCase.args, testCase.getenv, &stdout, &stderr)
			if code != 2 || !strings.Contains(stderr.String(), testCase.want) || stdout.Len() != 0 {
				t.Fatalf("run() = %d stdout=%q stderr=%q, want 2 and %q", code, stdout.String(), stderr.String(), testCase.want)
			}
		})
	}
}

// TestRunReportsAnUnreachableDatabaseWithoutItsURL verifies the connection string stays out of
// output and an earlier report file is not truncated when the database cannot be opened.
func TestRunReportsAnUnreachableDatabaseWithoutItsURL(t *testing.T) {
	t.Parallel()

	const databaseURL = "postgres://operator:secret-password@127.0.0.1:1/iapstack?connect_timeout=1"
	reportPath := filepath.Join(t.TempDir(), "dry-run.jsonl")
	if err := os.WriteFile(reportPath, []byte("{\"summary\":{}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--all-projects", "--report", reportPath}, func(key string) string {
		if key == "IAPSTACK_DATABASE_URL" {
			return databaseURL
		}
		return ""
	}, &stdout, &stderr)
	if code != 1 || strings.Contains(stdout.String()+stderr.String(), "secret-password") {
		t.Fatalf("run() = %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if payload, err := os.ReadFile(reportPath); err != nil || string(payload) != "{\"summary\":{}}\n" {
		t.Fatalf("report after a failed open = %q, %v; want it unchanged", payload, err)
	}
}

// TestReportRunErrorNamesTheCauseWithoutSecrets verifies each failure line and exit code.
func TestReportRunErrorNamesTheCauseWithoutSecrets(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		err     error
		summary reprojection.Summary
		code    int
		want    string
	}{
		{name: "success", code: 0},
		{name: "missing project", err: reprojection.ErrProjectNotFound, code: 2, want: "project does not exist"},
		{name: "failed customers", err: reprojection.ErrCustomersFailed, summary: reprojection.Summary{CustomersFailed: 2}, code: 1, want: "2 customer refreshes failed"},
		{
			name: "stopped after a customer", err: context.Canceled, code: 1,
			summary: reprojection.Summary{Stopped: &reprojection.StopPoint{ProjectID: "project-1", AfterCustomerID: "customer-9", Error: "canceled"}},
			want:    "resume with --project project-1 --after-customer customer-9",
		},
		{
			name: "stopped before any customer", err: errors.New("postgres://operator:secret@db"), code: 1,
			summary: reprojection.Summary{Stopped: &reprojection.StopPoint{ProjectID: "project-1", Error: "failed"}},
			want:    "before finishing any customer; rerun with --project project-1",
		},
		{name: "listing failed", err: errors.New("postgres://operator:secret@db"), code: 1, want: "reprojection stopped (failed); a rerun is safe"},
	}
	for _, testCase := range cases {
		var stderr bytes.Buffer
		code := reportRunError(&stderr, testCase.err, testCase.summary)
		if code != testCase.code || !strings.Contains(stderr.String(), testCase.want) || strings.Contains(stderr.String(), "secret") {
			t.Fatalf("%s: reportRunError() = %d %q, want %d and %q", testCase.name, code, stderr.String(), testCase.code, testCase.want)
		}
	}
}
