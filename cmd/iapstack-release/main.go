// Package main validates the evidence required before publishing an IAPStack release.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/imariman/iapstack/internal/releasegate"
)

// main reports a concise validation result without printing evidence contents.
func main() {
	if err := run(os.Args, os.Stdout); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "release gate failed: %v\n", err)
		os.Exit(1)
	}
}

// run validates one strict multi-provider evidence file supplied by the release operator.
func run(arguments []string, output io.Writer) error {
	if len(arguments) != 2 {
		return fmt.Errorf("usage: iapstack-release <release-evidence.json>")
	}
	if output == nil {
		return fmt.Errorf("release output is required")
	}
	file, err := os.Open(arguments[1]) // #nosec G703 -- This local operator CLI intentionally accepts an arbitrary evidence file path.
	if err != nil {
		return fmt.Errorf("open sandbox evidence: %w", err)
	}
	defer file.Close()
	evidence, err := releasegate.Decode(file)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(output, "release evidence passed for %s at commit %s\n",
		evidence.Release, evidence.Commit)
	return nil
}
