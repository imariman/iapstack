package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
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

// TestRunReportsAnUnreachableDatabaseWithoutItsURL verifies the connection string stays out of output.
func TestRunReportsAnUnreachableDatabaseWithoutItsURL(t *testing.T) {
	t.Parallel()

	const databaseURL = "postgres://operator:secret-password@127.0.0.1:1/iapstack?connect_timeout=1"
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--all-projects"}, func(key string) string {
		if key == "IAPSTACK_DATABASE_URL" {
			return databaseURL
		}
		return ""
	}, &stdout, &stderr)
	if code != 1 || strings.Contains(stdout.String()+stderr.String(), "secret-password") {
		t.Fatalf("run() = %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
