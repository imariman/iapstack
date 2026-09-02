package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	// releaseEvidenceExamplePath locates the operator-facing evidence template from this package.
	releaseEvidenceExamplePath = "../../docs/releases/v0.1.0-rc.1-release-evidence.example.json"
	// releaseIntegrationCommit identifies the complete evidence file created by the CLI test.
	releaseIntegrationCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

var (
	// releaseEvidencePlaceholder matches non-secret operator placeholders in the example document.
	releaseEvidencePlaceholder = regexp.MustCompile(`REPLACE_WITH_[A-Z0-9_]+`)
)

// TestRunAcceptsCompleteReleaseEvidence verifies the CLI reads, validates, and reports one release record.
func TestRunAcceptsCompleteReleaseEvidence(t *testing.T) {
	evidencePath := writeCompleteReleaseEvidence(t)
	var output bytes.Buffer
	if err := run([]string{"iapstack-release", evidencePath}, &output); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	want := "release evidence passed for v0.1.0-rc.1 at commit " + releaseIntegrationCommit + "\n"
	if output.String() != want {
		t.Fatalf("run() output = %q, want %q", output.String(), want)
	}
}

// TestRunRejectsInvalidInvocation verifies CLI failures remain explicit and output-free.
func TestRunRejectsInvalidInvocation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		arguments []string
		output    io.Writer
		want      string
	}{
		{name: "missing argument", arguments: []string{"iapstack-release"}, output: io.Discard, want: "usage"},
		{name: "missing output", arguments: []string{"iapstack-release", "evidence.json"}, want: "output is required"},
		{name: "missing file", arguments: []string{"iapstack-release", "missing-evidence.json"}, output: io.Discard, want: "open sandbox evidence"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := run(test.arguments, test.output)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("run() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

// writeCompleteReleaseEvidence converts the documented template into a deterministic passing record.
func writeCompleteReleaseEvidence(t *testing.T) string {
	t.Helper()
	payload, err := os.ReadFile(releaseEvidenceExamplePath)
	if err != nil {
		t.Fatalf("read release evidence example: %v", err)
	}
	completed := strings.ReplaceAll(string(payload), "false", "true")
	completed = strings.ReplaceAll(completed, strings.Repeat("0", 40), releaseIntegrationCommit)
	completed = releaseEvidencePlaceholder.ReplaceAllString(completed, "completed")
	completed = strings.ReplaceAll(completed, "REPLACE-", "request-")
	path := filepath.Join(t.TempDir(), "release-evidence.json")
	if err := os.WriteFile(path, []byte(completed), 0o600); err != nil {
		t.Fatalf("write release evidence: %v", err)
	}
	return path
}
