// Package main imports one local RevenueCat export into an IAPStack project.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence/postgres"
	"github.com/imariman/iapstack/internal/revenuecatimport"
)

const (
	// revenueCatCreatesCustomerNotice warns that the subscriber GET creates missing RevenueCat customers.
	revenueCatCreatesCustomerNotice = "RevenueCat GET /v1/subscribers/{app_user_id} creates a customer when the id does not exist."
)

// main runs the import command and exits with the plan's status codes.
func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr, ""))
}

// run executes one import or explicit-id fetch. baseURL is empty outside tests.
func run(args []string, getenv func(string) string, stdout, stderr io.Writer, baseURL string) int {
	flags := flag.NewFlagSet("iapstack-revenuecat-import", flag.ContinueOnError)
	flags.SetOutput(stderr)
	project := flags.String("project", "", "IAPStack project id")
	mappingPath := flags.String("mapping", "", "path to the revenuecat mapping file")
	exportPath := flags.String("export", "", "path to the local RevenueCat export")
	format := flags.String("format", "", "transactions-csv or subscriber-jsonl")
	nowValue := flags.String("now", "", "RFC3339 UTC clock override")
	reportPath := flags.String("report", "", "write the secret-free JSON report to this path instead of stdout")
	batchID := flags.String("batch-id", "", "required with --apply")
	apply := flags.Bool("apply", false, "write customers, grants, and projections")
	webhooks := flags.Bool("webhooks", false, "enqueue entitlement webhooks for projections changed by this apply")
	skipAnonymous := flags.Bool("skip-anonymous", false, "drop RevenueCat anonymous app user ids")
	acknowledge := flags.Bool("acknowledge-get-creates-customers", false, "confirm that subscriber GET creates missing RevenueCat customers")
	subscriberIDs := flags.String("subscriber-ids-file", "", "file of app user ids to fetch")
	fetchOutput := flags.String("fetch-output", "", "write fetched subscriber JSONL to this path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected arguments")
		return 2
	}
	if *subscriberIDs != "" {
		return runFetch(getenv, stderr, *subscriberIDs, *fetchOutput, *acknowledge, baseURL)
	}
	if *project == "" || *mappingPath == "" || *exportPath == "" || *format == "" {
		fmt.Fprintln(stderr, "project, mapping, export, and format are required")
		return 2
	}
	if *apply && strings.TrimSpace(*batchID) == "" {
		fmt.Fprintln(stderr, "batch-id is required with --apply")
		return 2
	}
	now := time.Now().UTC()
	if *nowValue != "" {
		parsed, err := time.Parse(time.RFC3339, *nowValue)
		if err != nil {
			fmt.Fprintln(stderr, "now must be RFC3339")
			return 2
		}
		now = parsed.UTC()
	}
	mappingPayload, err := os.ReadFile(*mappingPath) // #nosec G304 -- the operator chooses the local mapping file.
	if err != nil {
		fmt.Fprintln(stderr, "mapping file could not be read")
		return 2
	}
	mapping, err := revenuecatimport.ParseMapping(mappingPayload)
	if err != nil {
		fmt.Fprintln(stderr, "mapping file is invalid")
		return 2
	}
	exportFile, err := os.Open(*exportPath) // #nosec G304 -- the operator chooses the local export file.
	if err != nil {
		fmt.Fprintln(stderr, "export file could not be read")
		return 2
	}
	defer exportFile.Close()
	exportPayload, _, err := revenuecatimport.ReadExport(exportFile)
	if err != nil {
		if errors.Is(err, revenuecatimport.ErrTruncated) {
			fmt.Fprintln(stderr, "export file is truncated")
		} else {
			fmt.Fprintln(stderr, "export file could not be read")
		}
		return 2
	}
	input := revenuecatimport.Input{
		ProjectID:     core.ProjectID(*project),
		Mapping:       mapping,
		Format:        *format,
		Export:        exportPayload,
		Now:           now,
		BatchID:       *batchID,
		SkipAnonymous: *skipAnonymous,
	}
	var report revenuecatimport.Report
	if *apply {
		databaseURL := strings.TrimSpace(getenv("IAPSTACK_DATABASE_URL"))
		if databaseURL == "" {
			fmt.Fprintln(stderr, "IAPSTACK_DATABASE_URL is required with --apply")
			return 2
		}
		store, openErr := postgres.OpenStore(context.Background(), databaseURL)
		if openErr != nil {
			fmt.Fprintln(stderr, "database could not be opened")
			return 1
		}
		defer store.Close()
		report, err = revenuecatimport.Apply(context.Background(), store, input, *webhooks)
	} else {
		if databaseURL := strings.TrimSpace(getenv("IAPSTACK_DATABASE_URL")); databaseURL != "" {
			store, openErr := postgres.OpenStore(context.Background(), databaseURL)
			if openErr != nil {
				fmt.Fprintln(stderr, "database could not be opened")
				return 1
			}
			defer store.Close()
			catalog, loadErr := revenuecatimport.LoadCatalog(context.Background(), store, input.ProjectID)
			if loadErr != nil {
				fmt.Fprintln(stderr, "catalog could not be read")
				return 1
			}
			input.Catalog = &catalog
		}
		var planned revenuecatimport.Result
		planned, err = revenuecatimport.Plan(input)
		report = planned.Report
	}
	if writeErr := writeReport(report, *reportPath, stdout); writeErr != nil {
		fmt.Fprintln(stderr, "report could not be written")
		return 1
	}
	if err != nil {
		if errors.Is(err, revenuecatimport.ErrUsage) || errors.Is(err, revenuecatimport.ErrTruncated) || errors.Is(err, revenuecatimport.ErrBatchConflict) {
			return 2
		}
		return 1
	}
	if report.Counts.Failed > 0 {
		return 1
	}
	return 0
}

// runFetch writes subscriber JSONL for explicit ids. It never stores the API key.
func runFetch(getenv func(string) string, stderr io.Writer, idsPath, outputPath string, acknowledge bool, baseURL string) int {
	if !acknowledge {
		fmt.Fprintln(stderr, "pass --acknowledge-get-creates-customers before fetching subscribers")
		return 2
	}
	if outputPath == "" {
		fmt.Fprintln(stderr, "fetch-output is required")
		return 2
	}
	apiKey := getenv("REVENUECAT_SECRET_API_KEY")
	if strings.TrimSpace(apiKey) == "" {
		fmt.Fprintln(stderr, "REVENUECAT_SECRET_API_KEY is required")
		return 2
	}
	payload, err := os.ReadFile(idsPath) // #nosec G304 -- the operator chooses the local id file.
	if err != nil {
		fmt.Fprintln(stderr, "subscriber id file could not be read")
		return 2
	}
	fmt.Fprintln(stderr, revenueCatCreatesCustomerNotice)
	var buffer strings.Builder
	err = revenuecatimport.FetchSubscribers(context.Background(), revenuecatimport.FetchConfig{
		APIKey:  apiKey,
		IDs:     revenuecatimport.ReadSubscriberIDs(payload),
		BaseURL: baseURL,
		Output:  &buffer,
	})
	if err != nil {
		fmt.Fprintln(stderr, "subscriber fetch failed")
		return 1
	}
	if strings.Contains(buffer.String(), apiKey) {
		fmt.Fprintln(stderr, "subscriber fetch failed")
		return 1
	}
	if err := os.WriteFile(outputPath, []byte(buffer.String()), 0o600); err != nil { // #nosec G703 -- the operator chooses the local output path.
		fmt.Fprintln(stderr, "fetch output could not be written")
		return 1
	}
	return 0
}

// writeReport writes one JSON report to a file or stdout.
func writeReport(report revenuecatimport.Report, path string, stdout io.Writer) error {
	if report.ProjectID == "" && report.Format == "" {
		return nil
	}
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if path == "" {
		_, err = stdout.Write(payload)
		return err
	}
	return os.WriteFile(path, payload, 0o600) // #nosec G703 -- the operator chooses the local report path.
}
