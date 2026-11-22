package buildinfo

import (
	"runtime/debug"
	"strings"
	"testing"
)

// TestCurrentReportsBuild verifies linked values, the Go VCS fallback, and rejection of malformed values.
func TestCurrentReportsBuild(t *testing.T) {
	originalVersion, originalCommit, originalRead := version, commit, readBuildInfo
	t.Cleanup(func() { version, commit, readBuildInfo = originalVersion, originalCommit, originalRead })
	stamp := func(settings ...debug.BuildSetting) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) { return &debug.BuildInfo{Settings: settings}, true }
	}
	revision := debug.BuildSetting{Key: vcsRevisionSetting, Value: "4711a71dfeedface"}
	modified := debug.BuildSetting{Key: vcsModifiedSetting, Value: "true"}
	tooLong := strings.Repeat("a", maximumValueLength+1)

	tests := []struct {
		name    string
		version string
		commit  string
		read    func() (*debug.BuildInfo, bool)
		want    Info
	}{
		{
			name: "linked values win", version: "v0.2.0-rc.1+meta", commit: "13e83da0", read: stamp(revision),
			want: Info{Version: "v0.2.0-rc.1+meta", Commit: "13e83da0"},
		},
		{name: "clean checkout", read: stamp(revision), want: Info{Version: "dev", Commit: "4711a71dfeedface"}},
		{name: "dirty checkout", read: stamp(revision, modified), want: Info{Version: "dev", Commit: "4711a71dfeedface-dirty"}},
		{name: "no stamp", read: stamp(), want: Info{Version: "dev"}},
		{name: "no build info", read: func() (*debug.BuildInfo, bool) { return nil, false }, want: Info{Version: "dev"}},
		{
			name: "malformed commit falls back", version: "v1\n<script>", commit: "a b", read: stamp(revision),
			want: Info{Version: "dev", Commit: "4711a71dfeedface"},
		},
		{name: "quoted and too long", version: "\"v1\"", commit: tooLong, read: stamp(), want: Info{Version: "dev"}},
		{name: "non-ASCII", version: "v1.0.0-ş", commit: "commitş", read: stamp(), want: Info{Version: "dev"}},
	}
	for _, test := range tests {
		version, commit, readBuildInfo = test.version, test.commit, test.read
		if got := Current(); got != test.want {
			t.Errorf("%s: Current() = %#v, want %#v", test.name, got, test.want)
		}
	}
}
