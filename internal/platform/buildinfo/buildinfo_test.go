package buildinfo

import "testing"

// TestCurrentReportsLinkedValues verifies linked values win and malformed ones are dropped.
func TestCurrentReportsLinkedValues(t *testing.T) {
	originalVersion, originalCommit := version, commit
	t.Cleanup(func() { version, commit = originalVersion, originalCommit })

	version, commit = "v0.2.0-rc.1", "4711a71d00000000000000000000000000000000"
	if got := Current(); got.Version != "v0.2.0-rc.1" || got.Commit != commit {
		t.Fatalf("Current() = %#v, want the linked version and commit", got)
	}

	version, commit = "", ""
	if got := Current(); got.Version != developmentVersion || got.Commit != clean(vcsRevision()) {
		t.Fatalf("Current() = %#v, want dev and the Go VCS stamp", got)
	}

	for _, malformed := range []string{"v1\n<script>", "a b", "\"quoted\"", string(make([]byte, maximumValueLength+1))} {
		version = malformed
		if got := Current(); got.Version != developmentVersion {
			t.Fatalf("Current() with version %q = %#v, want dev", malformed, got)
		}
	}
}
