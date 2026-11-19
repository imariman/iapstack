// Package main reprojects stored customer entitlements with the current projector, so a row that
// an earlier projector ranked differently is fixed without waiting for the customer's next refresh.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/persistence/postgres"
	"github.com/imariman/iapstack/internal/reprojection"
	"github.com/imariman/iapstack/internal/verification"
)

// reportLine is one JSON Lines record: a changed customer, or the final summary.
type reportLine struct {
	Customer *reprojection.CustomerReport `json:"customer,omitempty"`
	Summary  *reprojection.Summary        `json:"summary,omitempty"`
}

// main runs the reprojection and exits 0 on success, 1 on a failed run, and 2 on invalid usage.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run parses the flags, opens the database, and writes one JSON Lines report.
func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("iapstack-reproject-entitlements", flag.ContinueOnError)
	flags.SetOutput(stderr)
	project := flags.String("project", "", "reproject one IAPStack project id")
	allProjects := flags.Bool("all-projects", false, "reproject every project instead of one")
	batchSize := flags.Int("batch-size", reprojection.DefaultBatchSize, fmt.Sprintf("canonical customers per keyset page, 1 to %d", persistence.MaxReprojectionPageSize))
	apply := flags.Bool("apply", false, "commit the refreshes and their entitlement events")
	dryRun := flags.Bool("dry-run", true, "report what would change and roll every refresh back (the default)")
	allCustomers := flags.Bool("all-customers", false, "refresh every canonical customer, not only those with an allowed import and an allowed store observation of another product for one entitlement")
	reportPath := flags.String("report", "", "write the JSON Lines report to this path instead of stdout")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected arguments")
		return 2
	}
	dryRunSet := false
	flags.Visit(func(set *flag.Flag) {
		if set.Name == "dry-run" {
			dryRunSet = true
		}
	})
	if *apply && dryRunSet && *dryRun {
		fmt.Fprintln(stderr, "--apply and --dry-run cannot be combined")
		return 2
	}
	if !*apply && !*dryRun {
		fmt.Fprintln(stderr, "pass --apply to commit; --dry-run=false alone does nothing")
		return 2
	}
	projectID := strings.TrimSpace(*project)
	if (projectID == "") == !*allProjects {
		fmt.Fprintln(stderr, "pass exactly one of --project or --all-projects")
		return 2
	}
	if projectID != "" {
		if err := core.ProjectID(projectID).Validate(); err != nil {
			fmt.Fprintln(stderr, "project id is invalid")
			return 2
		}
	}
	if *batchSize < 1 || *batchSize > persistence.MaxReprojectionPageSize {
		fmt.Fprintf(stderr, "batch-size must be between 1 and %d\n", persistence.MaxReprojectionPageSize)
		return 2
	}
	databaseURL := strings.TrimSpace(getenv("IAPSTACK_DATABASE_URL"))
	if databaseURL == "" {
		fmt.Fprintln(stderr, "IAPSTACK_DATABASE_URL is required")
		return 2
	}

	output := stdout
	if *reportPath != "" {
		file, err := os.OpenFile(*reportPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) // #nosec G304 -- the operator chooses the local report path.
		if err != nil {
			fmt.Fprintln(stderr, "report file could not be opened")
			return 1
		}
		defer file.Close()
		output = file
	}
	writer := bufio.NewWriter(output)
	encoder := json.NewEncoder(writer)

	store, err := postgres.OpenStore(ctx, databaseURL)
	if err != nil {
		fmt.Fprintln(stderr, "database could not be opened")
		return 1
	}
	defer store.Close()

	summary, runErr := reprojection.Run(ctx, store, reprojection.Options{
		ProjectID:    core.ProjectID(projectID),
		BatchSize:    *batchSize,
		Apply:        *apply,
		AllCustomers: *allCustomers,
		Projector:    verification.NewDefaultProjector(),
	}, func(customer reprojection.CustomerReport) error {
		if err := encoder.Encode(reportLine{Customer: &customer}); err != nil {
			return err
		}
		return writer.Flush()
	})
	writeErr := encoder.Encode(reportLine{Summary: &summary})
	if writeErr == nil {
		writeErr = writer.Flush()
	}
	if writeErr != nil {
		fmt.Fprintln(stderr, "report could not be written")
		return 1
	}
	if runErr != nil {
		fmt.Fprintln(stderr, "reprojection stopped; customers reported above were already processed and a rerun is safe")
		return 1
	}
	return 0
}
