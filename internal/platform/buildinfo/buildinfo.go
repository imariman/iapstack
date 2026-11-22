// Package buildinfo reports the release version and source commit a binary was built from.
package buildinfo

import (
	"runtime/debug"
	"strings"
	"unicode"
)

const (
	// developmentVersion is reported when the build did not supply a release version.
	developmentVersion = "dev"
	// maximumValueLength bounds a linked value so a bad build argument cannot bloat probes.
	maximumValueLength = 64
	// vcsRevisionSetting is the build setting Go records for the checked-out commit.
	vcsRevisionSetting = "vcs.revision"
	// vcsModifiedSetting is the build setting Go records when the checkout had local changes.
	vcsModifiedSetting = "vcs.modified"
	// dirtySuffix marks a commit whose checkout had uncommitted changes at build time.
	dirtySuffix = "-dirty"
)

// Info identifies one build in operator-facing probes.
type Info struct {
	// Version is the release version, such as v0.2.0, or "dev" for an unreleased build.
	Version string `json:"version"`
	// Commit is the full source commit SHA, or empty when the build did not record one.
	Commit string `json:"commit,omitempty"`
}

// version and commit are set at link time, for example:
//
//	go build -ldflags "-X github.com/imariman/iapstack/internal/platform/buildinfo.version=v0.2.0
//	  -X github.com/imariman/iapstack/internal/platform/buildinfo.commit=<sha>"
//
// The Dockerfile passes its IAPSTACK_VERSION and IAPSTACK_COMMIT build arguments here.
var (
	version string
	commit  string

	// readBuildInfo reads the binary's build metadata; tests replace it.
	readBuildInfo = debug.ReadBuildInfo
)

// Current returns the linked version and commit. Without a linked commit it falls back
// to the revision Go stamps when it builds inside a Git checkout.
func Current() Info {
	info := Info{Version: clean(version), Commit: clean(commit)}
	if info.Version == "" {
		info.Version = developmentVersion
	}
	if info.Commit == "" {
		info.Commit = clean(vcsRevision())
	}
	return info
}

// vcsRevision returns the commit Go recorded in the binary, with dirtySuffix when the
// checkout had uncommitted changes, or empty when Go recorded no commit.
func vcsRevision() string {
	build, ok := readBuildInfo()
	if !ok || build == nil {
		return ""
	}
	revision, modified := "", false
	for _, setting := range build.Settings {
		switch setting.Key {
		case vcsRevisionSetting:
			revision = setting.Value
		case vcsModifiedSetting:
			modified = setting.Value == "true"
		}
	}
	if revision != "" && modified {
		return revision + dirtySuffix
	}
	return revision
}

// clean keeps one short printable token and drops anything else, so a malformed build
// argument reports as missing instead of reaching a probe response.
func clean(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maximumValueLength {
		return ""
	}
	for _, r := range value {
		if r > unicode.MaxASCII || !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune(".-_+", r)) {
			return ""
		}
	}
	return value
}
