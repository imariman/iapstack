// Package main validates the evidence required before publishing a stable IAPStack release.
package main

import (
	"fmt"
	"os"

	"github.com/imariman/iapstack/internal/releasegate"
)

// main reports a concise validation result without printing evidence contents.
func main() {
	if err := run(os.Args); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "release gate failed: %v\n", err)
		os.Exit(1)
	}
}

// run validates one strict sandbox evidence file supplied by the release operator.
func run(arguments []string) error {
	if len(arguments) != 2 {
		return fmt.Errorf("usage: iapstack-release <sandbox-evidence.json>")
	}
	file, err := os.Open(arguments[1])
	if err != nil {
		return fmt.Errorf("open sandbox evidence: %w", err)
	}
	defer file.Close()
	evidence, err := releasegate.Decode(file)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(os.Stdout, "stable release evidence passed for %s at commit %s\n",
		evidence.Release, evidence.Commit)
	return nil
}
