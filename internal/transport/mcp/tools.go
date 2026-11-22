package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/imariman/iapstack/internal/controlplane"
	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/validation"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// MaxResponseBytes caps the serialized structured result of one tool call.
	MaxResponseBytes = 256 << 10
	// defaultPageSize is the page size of list tools when the caller names none.
	defaultPageSize = 50
	// maxPageSize bounds one page of a list tool.
	maxPageSize = 100
	// pageTokenVersion identifies the opaque list cursor encoding.
	pageTokenVersion = 1
	// pageTokenMaxBytes bounds an opaque list cursor before it is decoded.
	pageTokenMaxBytes = 512
	// outcomeOK is the logged outcome of a successful tool call.
	outcomeOK = "ok"
	// codeResponseTooLarge reports a result over MaxResponseBytes.
	codeResponseTooLarge = "response_too_large"
	// codeInvalidRequest reports arguments that do not match the input schema.
	codeInvalidRequest = "invalid_request"
	// codeUnauthorized reports a tool call without an authenticated actor.
	codeUnauthorized = "unauthorized"
)

// toolDefinition binds one tool's metadata to its handler.
type toolDefinition struct {
	tool    *sdk.Tool
	handler sdk.ToolHandler
}

// toolErrorOutput is the structured content of a failed tool call.
type toolErrorOutput struct {
	Error toolError `json:"error"`
}

// toolError is one stable error code and fixed message. Neither ever contains request or stored values.
type toolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// listProjectsInput pages through the projects the key may see.
type listProjectsInput struct {
	PageSize  *int   `json:"page_size,omitempty" jsonschema:"Projects per page, from 1 to 100. Defaults to 50."`
	PageToken string `json:"page_token,omitempty" jsonschema:"The next_page_token from the previous page."`
}

// projectListOutput is one page of projects.
type projectListOutput struct {
	Projects      []controlplane.Project `json:"projects"`
	NextPageToken string                 `json:"next_page_token,omitempty"`
}

// projectOverviewInput selects one project and an optional financial period.
type projectOverviewInput struct {
	ProjectID            string `json:"project_id" jsonschema:"The project id. An agent key may only name its own project."`
	FinancialPeriodStart string `json:"financial_period_start,omitempty" jsonschema:"Optional first day (YYYY-MM-DD) of the financial report period used for revenue. Requires financial_period_end."`
	FinancialPeriodEnd   string `json:"financial_period_end,omitempty" jsonschema:"Optional last day (YYYY-MM-DD) of the financial report period used for revenue. Requires financial_period_start."`
}

// subscriptionMetricsInput selects one project environment and window.
type subscriptionMetricsInput struct {
	ProjectID   string `json:"project_id" jsonschema:"The project id. An agent key may only name its own project."`
	Environment string `json:"environment,omitempty" jsonschema:"production (default), sandbox, or test."`
	WindowDays  *int   `json:"window_days,omitempty" jsonschema:"UTC day window ending today, from 1 to 90. Defaults to 30."`
}

// searchCustomersInput selects one page of matching customers.
type searchCustomersInput struct {
	ProjectID string `json:"project_id" jsonschema:"The project id. An agent key may only name its own project."`
	Query     string `json:"query,omitempty" jsonschema:"Customer id, external id, alias, or store transaction id. Exact matches rank first, then prefix matches. Empty lists customers newest first."`
	Limit     *int   `json:"limit,omitempty" jsonschema:"Customers per page, from 1 to 100. Defaults to 25."`
	PageToken string `json:"page_token,omitempty" jsonschema:"The next_page_token from the previous page of the same query."`
}

// getCustomerInput selects one customer.
type getCustomerInput struct {
	ProjectID  string `json:"project_id" jsonschema:"The project id. An agent key may only name its own project."`
	CustomerID string `json:"customer_id" jsonschema:"The internal customer id, for example from search_customers or resolve_customer."`
}

// resolveCustomerInput selects one external customer id.
type resolveCustomerInput struct {
	ProjectID  string `json:"project_id" jsonschema:"The project id. An agent key may only name its own project."`
	ExternalID string `json:"external_id" jsonschema:"The external customer id or alias the host application uses."`
}

// listFinancialReportsInput pages through one project's financial report imports.
type listFinancialReportsInput struct {
	ProjectID string `json:"project_id" jsonschema:"The project id. An agent key may only name its own project."`
	PageSize  *int   `json:"page_size,omitempty" jsonschema:"Reports per page, from 1 to 100. Defaults to 50."`
	PageToken string `json:"page_token,omitempty" jsonschema:"The next_page_token from the previous page."`
}

// financialReportsOutput is one page of financial report imports.
type financialReportsOutput struct {
	FinancialReports []financialReportSummary `json:"financial_reports"`
	NextPageToken    string                   `json:"next_page_token,omitempty"`
}

// financialReportSummary is one stored import without report bytes, order ids, or the file digest.
type financialReportSummary struct {
	ID                    string                               `json:"id"`
	Provider              core.Provider                        `json:"provider"`
	ReportKind            string                               `json:"report_kind"`
	PeriodStart           string                               `json:"period_start"`
	PeriodEnd             string                               `json:"period_end"`
	Source                string                               `json:"source"`
	ByteSize              int64                                `json:"byte_size"`
	ContentType           string                               `json:"content_type"`
	ParserVersion         string                               `json:"parser_version"`
	Status                string                               `json:"status"`
	Reason                string                               `json:"reason"`
	SupersedesImportID    *string                              `json:"supersedes_import_id,omitempty"`
	Currencies            []string                             `json:"currencies"`
	Amounts               []controlplane.FinancialReportAmount `json:"amounts"`
	CoveredApplicationIDs []core.ApplicationID                 `json:"covered_application_ids"`
	CreatedAt             time.Time                            `json:"created_at"`
}

// setupStatusInput selects one project.
type setupStatusInput struct {
	ProjectID string `json:"project_id" jsonschema:"The project id. An agent key may only name its own project."`
}

// listPageToken is the decoded cursor of an in-memory list page, bound to its list and scope.
type listPageToken struct {
	Version int    `json:"v"`
	List    string `json:"l"`
	Scope   string `json:"s"`
	Offset  int    `json:"o"`
}

var (
	// readOnlyAnnotations marks every phase 1 tool as a closed-world, non-destructive read.
	readOnlyAnnotations = sdk.ToolAnnotations{
		ReadOnlyHint: true, DestructiveHint: boolPointer(false), IdempotentHint: true, OpenWorldHint: boolPointer(false),
	}
	// errInvalidPageToken rejects a cursor from another list, scope, or encoding.
	errInvalidPageToken = errors.New("page token is invalid")
)

// newServer builds the MCP server with the read-only tool catalogue and the embedded resources.
func newServer(service *controlplane.Service, version string, logger *slog.Logger) *sdk.Server {
	server := sdk.NewServer(&sdk.Implementation{Name: serverName, Title: "IAPStack", Version: version}, &sdk.ServerOptions{
		Instructions: serverInstructions,
		Capabilities: &sdk.ServerCapabilities{Tools: &sdk.ToolCapabilities{}, Resources: &sdk.ResourceCapabilities{}},
	})
	for _, definition := range toolDefinitions(service, logger) {
		server.AddTool(definition.tool, definition.handler)
	}
	addResources(server)
	return server
}

// toolDefinitions returns the complete phase 1 tool catalogue in its documented order.
func toolDefinitions(service *controlplane.Service, logger *slog.Logger) []toolDefinition {
	return []toolDefinition{
		defineTool(&sdk.Tool{
			Name:        "list_projects",
			Title:       "List projects",
			Description: "Lists the projects this API key can read, with application, customer, and product counts. An agent key sees only its own project.",
		}, logger, pageSizeBounds, func(ctx context.Context, actor controlplane.Actor, input listProjectsInput) (projectListOutput, error) {
			list, err := service.Projects(ctx, actor)
			if err != nil {
				return projectListOutput{}, err
			}
			page, next, err := paginate(list.Projects, "projects", "", input.PageSize, input.PageToken)
			if err != nil {
				return projectListOutput{}, err
			}
			return projectListOutput{Projects: page, NextPageToken: next}, nil
		}),
		defineTool(&sdk.Tool{
			Name:  "get_project_overview",
			Title: "Get project overview",
			Description: "Returns one project's 30-day verification activity, revenue availability from imported store reports, " +
				"applications with credential and webhook coverage, products, recent customers and transactions, queue outcomes, " +
				"and recent webhook deliveries.",
		}, logger, nil, func(ctx context.Context, actor controlplane.Actor, input projectOverviewInput) (controlplane.ProjectOverview, error) {
			return service.ProjectOverview(ctx, actor, controlplane.ProjectOverviewInput{
				ProjectID:            core.ProjectID(input.ProjectID),
				FinancialPeriodStart: input.FinancialPeriodStart,
				FinancialPeriodEnd:   input.FinancialPeriodEnd,
			})
		}),
		defineTool(&sdk.Tool{
			Name:  "get_subscription_metrics",
			Title: "Get subscription metrics",
			Description: "Returns subscription health for one project environment over a UTC day window: active, new, renewed, " +
				"and churned subscriptions, trial conversion where the store records it, a per-product breakdown, and daily active counts.",
		}, logger, subscriptionMetricsBounds, func(ctx context.Context, actor controlplane.Actor, input subscriptionMetricsInput) (controlplane.SubscriptionMetrics, error) {
			return service.SubscriptionMetrics(ctx, actor, controlplane.SubscriptionMetricsInput{
				ProjectID: core.ProjectID(input.ProjectID), Environment: core.Environment(input.Environment),
				WindowDays: input.WindowDays,
			})
		}),
		defineTool(&sdk.Tool{
			Name:  "search_customers",
			Title: "Search customers",
			Description: "Finds customers in one project by internal id, external id, alias, or store transaction id, and reports " +
				"why each matched. Purchase tokens are never searchable.",
		}, logger, searchLimitBounds, func(ctx context.Context, actor controlplane.Actor, input searchCustomersInput) (controlplane.CustomerSearchPage, error) {
			return service.SearchCustomers(ctx, actor, controlplane.CustomerSearchInput{
				ProjectID: core.ProjectID(input.ProjectID), Query: input.Query, Limit: input.Limit, PageToken: input.PageToken,
			})
		}),
		defineTool(&sdk.Tool{
			Name:  "get_customer",
			Title: "Get customer",
			Description: "Returns one customer's identity, merged members and aliases, current entitlements with the access source " +
				"each one cites, manual grants, and a bounded purchase and delivery timeline. Use it to explain why a customer " +
				"does or does not have an entitlement.",
		}, logger, nil, func(ctx context.Context, actor controlplane.Actor, input getCustomerInput) (controlplane.CustomerDetail, error) {
			return service.Customer(ctx, actor, core.ProjectID(input.ProjectID), core.CustomerID(input.CustomerID))
		}),
		defineTool(&sdk.Tool{
			Name:        "resolve_customer",
			Title:       "Resolve customer",
			Description: "Resolves an external customer id or alias to the canonical internal customer id in one project.",
		}, logger, nil, func(ctx context.Context, actor controlplane.Actor, input resolveCustomerInput) (controlplane.CustomerResolution, error) {
			return service.ResolveCustomer(ctx, actor, core.ProjectID(input.ProjectID), input.ExternalID)
		}),
		defineTool(&sdk.Tool{
			Name:  "list_financial_reports",
			Title: "List financial reports",
			Description: "Lists one project's imported store financial reports with their period, status, and accepted " +
				"currency totals per application. Report files and order ids are never returned.",
		}, logger, pageSizeBounds, func(ctx context.Context, actor controlplane.Actor, input listFinancialReportsInput) (financialReportsOutput, error) {
			list, err := service.FinancialReports(ctx, actor, core.ProjectID(input.ProjectID))
			if err != nil {
				return financialReportsOutput{}, err
			}
			page, next, err := paginate(list.FinancialReports, "financial_reports", input.ProjectID, input.PageSize, input.PageToken)
			if err != nil {
				return financialReportsOutput{}, err
			}
			output := financialReportsOutput{FinancialReports: make([]financialReportSummary, 0, len(page)), NextPageToken: next}
			for _, report := range page {
				output.FinancialReports = append(output.FinancialReports, financialReportSummaryFrom(report))
			}
			return output, nil
		}),
		defineTool(&sdk.Tool{
			Name:  "get_setup_status",
			Title: "Get setup status",
			Description: "Reports what is still missing in one project's setup: per application the stored provider credential " +
				"kinds and revisions, the webhook endpoint, and the store notification path (and the full URL when the server's " +
				"public URL is configured) to paste into App Store Connect, the Play notifications Pub/Sub push subscription, or " +
				"AppGallery Connect. It also lists queue failures and recent failed webhook deliveries. Credential payloads and " +
				"secrets are never returned.",
		}, logger, nil, func(ctx context.Context, actor controlplane.Actor, input setupStatusInput) (controlplane.SetupStatus, error) {
			return service.SetupStatus(ctx, actor, core.ProjectID(input.ProjectID))
		}),
	}
}

// defineTool derives strict schemas from the input and output types and wraps run with argument
// validation, error classification, the response size cap, and one log line per call.
func defineTool[In, Out any](
	tool *sdk.Tool,
	logger *slog.Logger,
	adjustInput func(*jsonschema.Schema),
	run func(context.Context, controlplane.Actor, In) (Out, error),
) toolDefinition {
	inputSchema := mustSchema[In]()
	if adjustInput != nil {
		adjustInput(inputSchema)
	}
	resolved, err := inputSchema.Resolve(nil)
	if err != nil {
		panic(fmt.Sprintf("resolve input schema of %s: %v", tool.Name, err))
	}
	defined := *tool
	annotations := readOnlyAnnotations
	defined.Annotations = &annotations
	defined.InputSchema = inputSchema
	defined.OutputSchema = mustSchema[Out]()
	name := defined.Name
	handler := func(ctx context.Context, request *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		startedAt := time.Now()
		actor, authenticated := actorFromContext(ctx)
		outcome := outcomeOK
		defer func() {
			logger.Info("mcp tool call",
				"tool", name,
				"key_id", actor.KeyID,
				"role", string(actor.Role),
				"outcome", outcome,
				"duration_ms", time.Since(startedAt).Milliseconds(),
			)
		}()
		if !authenticated {
			outcome = codeUnauthorized
			return toolFailure(codeUnauthorized, "a valid API key bearer is required"), nil
		}
		var arguments json.RawMessage
		if request != nil && request.Params != nil {
			arguments = request.Params.Arguments
		}
		input, err := decodeArguments[In](arguments, resolved)
		if err != nil {
			outcome = codeInvalidRequest
			return toolFailure(codeInvalidRequest, "the tool arguments do not match the input schema"), nil
		}
		output, err := run(ctx, actor, input)
		if err != nil {
			problem := controlplane.Classify(err)
			outcome = problem.Code
			return toolFailure(problem.Code, problem.Message), nil
		}
		payload, err := json.Marshal(output)
		if err != nil {
			outcome = "internal_error"
			return toolFailure("internal_error", "the request could not be completed"), nil
		}
		if len(payload) > MaxResponseBytes {
			outcome = codeResponseTooLarge
			return toolFailure(codeResponseTooLarge,
				"the result exceeds the response size limit; narrow the query or request a smaller page"), nil
		}
		return &sdk.CallToolResult{
			Content:           []sdk.Content{&sdk.TextContent{Text: string(payload)}},
			StructuredContent: json.RawMessage(payload),
		}, nil
	}
	return toolDefinition{tool: &defined, handler: handler}
}

// mustSchema infers the JSON schema of one Go type. Struct schemas forbid additional properties.
func mustSchema[T any]() *jsonschema.Schema {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		panic(fmt.Sprintf("infer JSON schema: %v", err))
	}
	return schema
}

// decodeArguments validates raw tool arguments against the input schema and decodes them strictly.
func decodeArguments[In any](arguments json.RawMessage, resolved *jsonschema.Resolved) (In, error) {
	var input In
	if len(bytes.TrimSpace(arguments)) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	var instance any
	if err := json.Unmarshal(arguments, &instance); err != nil {
		return input, err
	}
	if err := resolved.Validate(instance); err != nil {
		return input, err
	}
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return input, errors.New("tool arguments must contain one JSON object")
	}
	return input, nil
}

// toolFailure builds an isError result whose structured content carries only a stable code and fixed message.
func toolFailure(code, message string) *sdk.CallToolResult {
	payload, _ := json.Marshal(toolErrorOutput{Error: toolError{Code: code, Message: message}})
	return &sdk.CallToolResult{
		IsError:           true,
		Content:           []sdk.Content{&sdk.TextContent{Text: string(payload)}},
		StructuredContent: json.RawMessage(payload),
	}
}

// paginate returns one page of an already bounded list and the cursor for the next page.
func paginate[T any](items []T, list, scope string, pageSize *int, token string) ([]T, string, error) {
	size := defaultPageSize
	if pageSize != nil {
		if *pageSize < 1 || *pageSize > maxPageSize {
			return nil, "", validation.Wrap(errors.New("page size is out of range"))
		}
		size = *pageSize
	}
	offset := 0
	if token != "" {
		decoded, err := decodeListPageToken(token, list, scope)
		if err != nil {
			return nil, "", validation.Wrap(err)
		}
		offset = decoded
	}
	if offset > len(items) {
		offset = len(items)
	}
	end := min(offset+size, len(items))
	page := items[offset:end]
	if page == nil {
		page = []T{}
	}
	next := ""
	if end < len(items) {
		next = encodeListPageToken(list, scope, end)
	}
	return page, next, nil
}

// encodeListPageToken returns an opaque cursor for one list and scope.
func encodeListPageToken(list, scope string, offset int) string {
	payload, _ := json.Marshal(listPageToken{Version: pageTokenVersion, List: list, Scope: scope, Offset: offset})
	return base64.RawURLEncoding.EncodeToString(payload)
}

// decodeListPageToken parses an opaque cursor and rejects one issued for another list or scope.
func decodeListPageToken(token, list, scope string) (int, error) {
	if len(token) > pageTokenMaxBytes {
		return 0, errInvalidPageToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, errInvalidPageToken
	}
	var decoded listPageToken
	if err := json.Unmarshal(payload, &decoded); err != nil || decoded.Version != pageTokenVersion ||
		decoded.List != list || decoded.Scope != scope || decoded.Offset < 0 {
		return 0, errInvalidPageToken
	}
	return decoded.Offset, nil
}

// financialReportSummaryFrom drops the report file digest from the shared financial report view.
func financialReportSummaryFrom(report controlplane.FinancialReport) financialReportSummary {
	return financialReportSummary{
		ID: report.ID, Provider: report.Provider, ReportKind: report.ReportKind,
		PeriodStart: report.PeriodStart, PeriodEnd: report.PeriodEnd, Source: report.Source,
		ByteSize: report.ByteSize, ContentType: report.ContentType, ParserVersion: report.ParserVersion,
		Status: report.Status, Reason: report.Reason, SupersedesImportID: report.SupersedesImportID,
		Currencies: report.Currencies, Amounts: report.Amounts,
		CoveredApplicationIDs: report.CoveredApplicationIDs, CreatedAt: report.CreatedAt,
	}
}

// pageSizeBounds documents the page size range in the input schema.
func pageSizeBounds(schema *jsonschema.Schema) {
	setIntegerBounds(schema, "page_size", 1, maxPageSize)
}

// searchLimitBounds documents the customer search page size range in the input schema.
func searchLimitBounds(schema *jsonschema.Schema) {
	setIntegerBounds(schema, "limit", 1, persistence.AdminCustomerSearchMaxLimit)
}

// subscriptionMetricsBounds documents the environment values and window range in the input schema.
func subscriptionMetricsBounds(schema *jsonschema.Schema) {
	setIntegerBounds(schema, "window_days", 1, persistence.SubscriptionMetricsMaximumWindowDays)
	if property := schema.Properties["environment"]; property != nil {
		property.Enum = []any{
			string(core.EnvironmentProduction), string(core.EnvironmentSandbox), string(core.EnvironmentTest),
		}
	}
}

// setIntegerBounds sets the inclusive minimum and maximum of one integer property.
func setIntegerBounds(schema *jsonschema.Schema, name string, minimum, maximum int) {
	property := schema.Properties[name]
	if property == nil {
		panic(fmt.Sprintf("input schema has no %s property", name))
	}
	low, high := float64(minimum), float64(maximum)
	property.Minimum = &low
	property.Maximum = &high
}

// boolPointer returns a pointer to a copy of value.
func boolPointer(value bool) *bool {
	return &value
}
