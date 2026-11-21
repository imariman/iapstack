// Package main reprojects stored customer entitlements with the current projector, so a row that
// an earlier projector ranked differently is fixed without waiting for the customer's next refresh.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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
	afterCustomer := flags.String("after-customer", "", "with --project, resume after this canonical customer id (the summary's stopped.after_customer_id)")
	batchSize := flags.Int("batch-size", reprojection.DefaultBatchSize, fmt.Sprintf("canonical customers each keyset page examines, 1 to %d", persistence.MaxReprojectionPageSize))
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
	resumeAfter := strings.TrimSpace(*afterCustomer)
	if resumeAfter != "" {
		if projectID == "" {
			fmt.Fprintln(stderr, "--after-customer requires --project")
			return 2
		}
		if err := core.CustomerID(resumeAfter).Validate(); err != nil {
			fmt.Fprintln(stderr, "after-customer id is invalid")
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

	store, err := postgres.OpenStore(ctx, databaseURL)
	if err != nil {
		fmt.Fprintln(stderr, "database could not be opened")
		return 1
	}
	defer store.Close()

	output := stdout
	var reportFile *os.File
	if *reportPath != "" {
		reportFile, err = os.OpenFile(*reportPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) // #nosec G304 -- the operator chooses the local report path.
		if err != nil {
			fmt.Fprintln(stderr, "report file could not be opened")
			return 1
		}
		output = reportFile
	}
	writer := bufio.NewWriter(output)
	encoder := json.NewEncoder(writer)

	summary, runErr := reprojection.Run(ctx, store, reprojection.Options{
		ProjectID:       core.ProjectID(projectID),
		AfterCustomerID: core.CustomerID(resumeAfter),
		BatchSize:       *batchSize,
		Apply:           *apply,
		AllCustomers:    *allCustomers,
		Projector:       verification.NewDefaultProjector(),
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
	if reportFile != nil {
		writeErr = errors.Join(writeErr, reportFile.Close())
	}
	if writeErr != nil {
		fmt.Fprintln(stderr, "report could not be written")
		return 1
	}
	return reportRunError(stderr, runErr, summary)
}

// reportRunError writes one secret-free line about a failed run and returns the exit code.
func reportRunError(stderr io.Writer, runErr error, summary reprojection.Summary) int {
	switch {
	case runErr == nil:
		return 0
	case errors.Is(runErr, reprojection.ErrProjectNotFound):
		fmt.Fprintln(stderr, "project does not exist")
		return 2
	case errors.Is(runErr, reprojection.ErrCustomersFailed):
		fmt.Fprintf(stderr, "%d customer refreshes failed; failed_customers in the summary lists them with an error class, and a rerun is safe\n", summary.CustomersFailed)
		return 1
	case summary.Stopped != nil && summary.Stopped.AfterCustomerID == "":
		fmt.Fprintf(
			stderr, "reprojection stopped (%s) in project %q before finishing any customer; rerun with --project %s\n",
			summary.Stopped.Error, summary.Stopped.ProjectID, summary.Stopped.ProjectID,
		)
		return 1
	case summary.Stopped != nil:
		fmt.Fprintf(
			stderr,
			"reprojection stopped (%s) in project %q after customer %q; resume with --project %s --after-customer %s, or rerun from the start\n",
			summary.Stopped.Error, summary.Stopped.ProjectID, summary.Stopped.AfterCustomerID,
			summary.Stopped.ProjectID, summary.Stopped.AfterCustomerID,
		)
		return 1
	default:
		fmt.Fprintf(stderr, "reprojection stopped (%s); a rerun is safe\n", reprojection.ErrorClass(runErr))
		return 1
	}
}
