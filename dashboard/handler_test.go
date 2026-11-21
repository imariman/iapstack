package dashboard

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHandlerServesSecureEmbeddedAssets verifies routing, media types, and browser policy headers.
func TestHandlerServesSecureEmbeddedAssets(t *testing.T) {
	t.Parallel()

	fallback := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusTeapot)
	})
	handler, err := New(fallback)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	tests := []struct {
		path        string
		status      int
		contentType string
		body        string
	}{
		{path: "/dashboard/", status: http.StatusOK, contentType: "text/html; charset=utf-8", body: "IAPStack Operations"},
		{path: "/dashboard/app.css", status: http.StatusOK, contentType: "text/css; charset=utf-8", body: "--accent"},
		{path: "/dashboard/app.js", status: http.StatusOK, contentType: "text/javascript; charset=utf-8", body: "/v1/admin/dashboard-session"},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
		if recorder.Code != test.status {
			t.Fatalf("GET %s status = %d, want %d", test.path, recorder.Code, test.status)
		}
		if contentType := recorder.Header().Get("Content-Type"); contentType != test.contentType {
			t.Fatalf("GET %s Content-Type = %q, want %q", test.path, contentType, test.contentType)
		}
		if !strings.Contains(recorder.Body.String(), test.body) {
			t.Fatalf("GET %s body does not contain %q", test.path, test.body)
		}
		if policy := recorder.Header().Get("Content-Security-Policy"); !strings.Contains(policy, "frame-ancestors 'none'") {
			t.Fatalf("GET %s Content-Security-Policy = %q", test.path, policy)
		}
	}
}

// TestDashboardSecretLifecycleAvoidsPersistentStorage verifies browser-managed secrets remain ephemeral.
func TestDashboardSecretLifecycleAvoidsPersistentStorage(t *testing.T) {
	t.Parallel()

	script, err := fs.ReadFile(assets, "app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	document, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	for _, forbidden := range []string{"localStorage", "sessionStorage.getItem", "sessionStorage.setItem", "state.adminKey"} {
		if strings.Contains(string(script), forbidden) {
			t.Fatalf("dashboard script must not retain administrator bearer material through %q", forbidden)
		}
	}
	for _, required := range []string{
		`credentials: "same-origin"`, "/v1/admin/dashboard-session", `elements.adminKey.value = ""`,
		`sessionStorage.removeItem("iapstack.dashboard.admin")`,
		"credentialForm.reset()", "webhookForm.reset()", `revealedKey.value = ""`,
	} {
		if !strings.Contains(string(script), required) {
			t.Fatalf("dashboard script does not contain secret lifecycle control %q", required)
		}
	}
	if count := strings.Count(string(document), `autocomplete="new-password"`); count != 4 {
		t.Fatalf("dashboard secret fields with new-password autocomplete = %d, want 4", count)
	}
}

// TestDashboardSupportsGooglePlayAndLocalCredentialFiles keeps provider files bounded and ephemeral.
func TestDashboardSupportsGooglePlayAndLocalCredentialFiles(t *testing.T) {
	t.Parallel()

	document, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	script, err := fs.ReadFile(assets, "app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	for _, required := range []string{
		`value="google_play"`, `id="apple-private-key-file"`, `id="apple-root-files"`,
		`id="google-service-account-file"`, `name="google_rtdn_subscription"`,
	} {
		if !strings.Contains(string(document), required) {
			t.Fatalf("dashboard provider file form does not contain %q", required)
		}
	}
	for _, required := range []string{
		"google_play_android_publisher", "googleCredentialContentType", "saveGoogleCredential",
		"maximumCredentialFileBytes", "loadGoogleServiceAccountFile", "readAppleRootCertificate",
	} {
		if !strings.Contains(string(script), required) {
			t.Fatalf("dashboard provider file workflow does not contain %q", required)
		}
	}
}

// TestDashboardSupportsAppleCatalogCommissioning keeps App Store setup available without weakening secret handling.
func TestDashboardSupportsAppleCatalogCommissioning(t *testing.T) {
	t.Parallel()

	document, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	script, err := fs.ReadFile(assets, "app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	for _, required := range []string{
		`value="apple_app_store"`, `name="private_key"`, `name="root_certificates"`,
	} {
		if !strings.Contains(string(document), required) {
			t.Fatalf("dashboard Apple form does not contain %q", required)
		}
	}
	for _, required := range []string{
		"apple_app_store_server_api", "appleCredentialContentType", "saveAppleCredential",
	} {
		if !strings.Contains(string(script), required) {
			t.Fatalf("dashboard Apple workflow does not contain %q", required)
		}
	}
}

// TestDashboardOffersAppleConsumptionAnswersOnlyForAppleApplications pins the opt-in section and its labels.
func TestDashboardOffersAppleConsumptionAnswersOnlyForAppleApplications(t *testing.T) {
	t.Parallel()

	document, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	script, err := fs.ReadFile(assets, "app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	styles, err := fs.ReadFile(assets, "app.css")
	if err != nil {
		t.Fatalf("read embedded app.css: %v", err)
	}
	for _, required := range []string{
		`id="consumption-section"`, `aria-labelledby="consumption-title" hidden`, `id="consumption-form"`,
		`<option value="false">Off</option><option value="true">On</option>`,
		`value="grant_prorated"`, "Consent is opt-in.", "No record or a recorded no skips the request.",
		`name="sample_content_provided"`, `id="consumption-rows"`, "apple-consumption-consent",
	} {
		if !strings.Contains(string(document), required) {
			t.Fatalf("dashboard consumption section does not contain %q", required)
		}
	}
	for _, required := range []string{
		`resetConsumptionSection(application.provider === "apple_app_store")`, `"/apple-consumption"`,
		"expected_revision: state.consumption?.revision || 0", "No consumption requests yet.",
		"cellBadge(request.status, request.status_reason",
	} {
		if !strings.Contains(string(script), required) {
			t.Fatalf("dashboard consumption workflow does not contain %q", required)
		}
	}
	for _, forbidden := range []string{"consent_mode", "application_attested", "customer_opt_in"} {
		if strings.Contains(string(document), forbidden) || strings.Contains(string(script), forbidden) {
			t.Fatalf("dashboard still offers the removed consent mode %q", forbidden)
		}
	}
	if !strings.Contains(string(styles), ".status-badge.sent") {
		t.Fatal("sent consumption requests have no healthy status style")
	}
}

// TestDashboardUsesEnglishInterface verifies the embedded interface stays consistently English.
func TestDashboardUsesEnglishInterface(t *testing.T) {
	t.Parallel()

	document, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	script, err := fs.ReadFile(assets, "app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	if !strings.Contains(string(document), `<html lang="en">`) {
		t.Fatal("dashboard document must declare English as its interface language")
	}
	for _, value := range []string{string(document), string(script)} {
		if strings.ContainsAny(value, "\u00e7\u011f\u0131\u00f6\u015f\u00fc\u00c7\u011e\u0130\u00d6\u015e\u00dc") {
			t.Fatal("dashboard interface contains Turkish characters")
		}
		for _, forbidden := range []string{"Yeni", "Hata", "Tamam", "Eksik", "Bekleyen", "Durum", "Katalog"} {
			if strings.Contains(value, forbidden) {
				t.Fatalf("dashboard interface contains Turkish term %q", forbidden)
			}
		}
	}
}

// TestDashboardProfessionalUIControls verifies the dashboard keeps its accessible interaction baseline.
func TestDashboardProfessionalUIControls(t *testing.T) {
	t.Parallel()

	document, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	styles, err := fs.ReadFile(assets, "app.css")
	if err != nil {
		t.Fatalf("read embedded app.css: %v", err)
	}
	script, err := fs.ReadFile(assets, "app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	for _, required := range []string{`content="light dark"`, `<svg aria-hidden="true"`, `aria-label="Close dialog"`} {
		if !strings.Contains(string(document), required) {
			t.Fatalf("dashboard document does not contain UI control %q", required)
		}
	}
	for _, required := range []string{"min-height: 44px", "prefers-reduced-motion", "outline: 3px solid var(--focus)"} {
		if !strings.Contains(string(styles), required) {
			t.Fatalf("dashboard styles do not contain accessibility rule %q", required)
		}
	}
	for _, required := range []string{`setAttribute("aria-busy", "true")`, "setFormBusy"} {
		if !strings.Contains(string(script), required) {
			t.Fatalf("dashboard script does not contain loading feedback %q", required)
		}
	}
}

// TestDashboardSupportsLightDarkAndSystemThemes keeps both color schemes, the system default, and the stored choice wired.
func TestDashboardSupportsLightDarkAndSystemThemes(t *testing.T) {
	t.Parallel()

	document, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	styles, err := fs.ReadFile(assets, "app.css")
	if err != nil {
		t.Fatalf("read embedded app.css: %v", err)
	}
	script, err := fs.ReadFile(assets, "app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	for _, required := range []string{
		`<meta name="color-scheme" content="light dark">`,
		`<meta name="theme-color" media="(prefers-color-scheme: light)" content="#f4f4f5">`,
		`<meta name="theme-color" media="(prefers-color-scheme: dark)" content="#09090b">`,
		`aria-label="Color theme: System. Switch to Light"`, `data-theme-label`,
	} {
		if !strings.Contains(string(document), required) {
			t.Fatalf("dashboard theme markup does not contain %q", required)
		}
	}
	// The toggle stays reachable before sign-in and in the signed-in top bar.
	if count := strings.Count(string(document), `class="secondary-button theme-toggle"`); count != 2 {
		t.Fatalf("dashboard theme toggles = %d, want 2", count)
	}
	for _, icon := range []string{`data-theme-icon="system"`, `data-theme-icon="light"`, `data-theme-icon="dark"`} {
		if count := strings.Count(string(document), icon); count != 2 {
			t.Fatalf("dashboard theme icon %q count = %d, want 2", icon, count)
		}
	}
	for _, required := range []string{
		"color-scheme: light dark;", `--background: light-dark(#f4f4f5, #09090b);`,
		`:root[data-theme="light"]`, "color-scheme: light;", `:root[data-theme="dark"]`, "color-scheme: dark;",
		"--chart-grid:", "--inverted-foreground:", "@media (prefers-color-scheme: light)", "%2352525b",
	} {
		if !strings.Contains(string(styles), required) {
			t.Fatalf("dashboard theme styles do not contain %q", required)
		}
	}
	for _, required := range []string{
		`themeCookieName = "iapstack_dashboard_theme"`, `themeOrder = ["system", "light", "dark"]`,
		"storedThemePreference", "resolvedTheme", "applyTheme", "persistThemePreference",
		`window.matchMedia("(prefers-color-scheme: light)")`, `root.removeAttribute("data-theme")`,
		`root.setAttribute("data-theme", preference)`, "Path=/dashboard/", "SameSite=Strict",
		`systemThemeQuery.addEventListener("change"`,
	} {
		if !strings.Contains(string(script), required) {
			t.Fatalf("dashboard theme script does not contain %q", required)
		}
	}
}

// TestDashboardSupportsSidebarToggleAndChartHover keeps the collapsible rail and chart inspection wired.
func TestDashboardSupportsSidebarToggleAndChartHover(t *testing.T) {
	t.Parallel()

	document, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	styles, err := fs.ReadFile(assets, "app.css")
	if err != nil {
		t.Fatalf("read embedded app.css: %v", err)
	}
	script, err := fs.ReadFile(assets, "app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	for _, required := range []string{
		`id="sidebar"`, `id="toggle-sidebar"`, `aria-controls="sidebar"`, `aria-label="Collapse sidebar"`,
		`aria-expanded="true" aria-controls="sidebar" title="Collapse sidebar (Ctrl+B)"`,
	} {
		if !strings.Contains(string(document), required) {
			t.Fatalf("dashboard sidebar toggle markup does not contain %q", required)
		}
	}
	// The rail collapses to an icon rail; it is never removed from the layout,
	// so the toggle that reopens it cannot disappear with it.
	if strings.Contains(string(document), `id="show-sidebar"`) || strings.Contains(string(styles), `data-sidebar="hidden"`) {
		t.Fatal("dashboard still uses the fully-hiding sidebar whose reopen control vanished with the rail")
	}
	for _, required := range []string{
		`:root[data-sidebar="collapsed"] .app-shell`, `:root[data-sidebar="collapsed"] .sidebar-brand-row`,
		".sidebar-brand-row", ".project-initials", "transition: width var(--duration-base)",
		".chart-tooltip", ".chart-hit", ".chart-guide", ".chart-bar-active",
	} {
		if !strings.Contains(string(styles), required) {
			t.Fatalf("dashboard shell styles do not contain %q", required)
		}
	}
	for _, required := range []string{
		`sidebarCookieName = "iapstack_dashboard_sidebar"`, "storedSidebarCollapsed", "applySidebar",
		"persistSidebar", "setSidebarCollapsed", `root.setAttribute("data-sidebar", "collapsed")`,
		`value === "collapsed" || value === "hidden"`, "projectInitials", `"project-initials"`,
		"addChartHover", "chart-tooltip", `"data-day": index`, `bar.setAttribute("data-day", String(index))`,
		`guide.removeAttribute("hidden")`,
	} {
		if !strings.Contains(string(script), required) {
			t.Fatalf("dashboard shell script does not contain %q", required)
		}
	}
}

// TestDashboardRendersTruthfulCommerceAnalytics keeps charts separate from untrusted client pricing.
func TestDashboardRendersTruthfulCommerceAnalytics(t *testing.T) {
	t.Parallel()

	document, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	styles, err := fs.ReadFile(assets, "app.css")
	if err != nil {
		t.Fatalf("read embedded app.css: %v", err)
	}
	script, err := fs.ReadFile(assets, "app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	for _, required := range []string{
		`class="activity-chart" id="activity-chart"`, `id="revenue-value"`, "Recognized store revenue",
		`id="sandbox-value-amounts"`, "Sandbox test value", "Daily verification outcomes",
	} {
		if !strings.Contains(string(document), required) {
			t.Fatalf("dashboard commerce panel does not contain %q", required)
		}
	}
	for _, required := range []string{
		"renderActivityChart", "renderSandboxValue", "formatMilliunitValue", "store_reports_required",
		"Test purchases never create real store revenue", "Provider-signed test value",
		`document.createElementNS("http://www.w3.org/2000/svg"`, "weeklyTick",
	} {
		if !strings.Contains(string(script), required) {
			t.Fatalf("dashboard analytics renderer does not contain %q", required)
		}
	}
	for _, forbidden := range []string{"rawPrice", "raw_price", "estimated revenue"} {
		if strings.Contains(string(script), forbidden) {
			t.Fatalf("dashboard analytics must not derive revenue from %q", forbidden)
		}
	}
	for _, required := range []string{".commerce-panel", ".activity-chart", ".revenue-badge.reports-required", ".revenue-badge.reports-imported", ".sandbox-value"} {
		if !strings.Contains(string(styles), required) {
			t.Fatalf("dashboard analytics styles do not contain %q", required)
		}
	}
}

// TestDashboardRevenueCardDistinguishesImportedReports verifies imported proceeds are not labeled sandbox-only.
func TestDashboardRevenueCardDistinguishesImportedReports(t *testing.T) {
	t.Parallel()

	script, err := fs.ReadFile(assets, "app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	document, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	source := string(script)
	revenueStart := strings.Index(source, "function renderRevenue")
	sandboxLabel := strings.Index(source, `badge.textContent = "Sandbox only"`)
	imported := strings.Index(source, `revenue.status === "reports_imported"`)
	if revenueStart < 0 || imported < revenueStart || sandboxLabel < imported {
		t.Fatal("reports_imported must be handled before the sandbox fall-through")
	}
	requiredStart := strings.Index(source, `revenue.status === "store_reports_required"`)
	requiredEnd := strings.Index(source[requiredStart:], "return;")
	requiredBranch := source[requiredStart : requiredStart+requiredEnd]
	if !strings.Contains(requiredBranch, "—") {
		t.Fatal("store_reports_required does not render an em dash")
	}
	if !strings.Contains(source, "not a complete project total") || !strings.Contains(source, "counted as zero") ||
		!strings.Contains(source, "separate account") {
		t.Fatal("recognized proceeds must describe partial coverage and name zero-filled applications")
	}
	cardStart := strings.Index(string(document), `class="revenue-card"`)
	cardEnd := strings.Index(string(document), `class="sandbox-value"`)
	card := source[revenueStart:strings.Index(source, "function renderSandboxValue")] + string(document)[cardStart:cardEnd]
	for _, forbidden := range []string{"estimated", "approximate", "projected", "forecast", "MRR", "ARR"} {
		if strings.Contains(card, forbidden) {
			t.Fatalf("revenue card contains %q", forbidden)
		}
	}
	if strings.Contains(source[revenueStart:strings.Index(source, "function renderSandboxValue")], "Sandbox only") &&
		strings.Index(source, `revenue.status === "reports_imported"`) > strings.Index(source, `badge.textContent = "Sandbox only"`) {
		t.Fatal("imported reports fall through to Sandbox only")
	}
}

// TestDashboardRendersHonestSubscriptionMetrics keeps the subscription panel to counts, definitions, and stated gaps.
func TestDashboardRendersHonestSubscriptionMetrics(t *testing.T) {
	t.Parallel()

	document, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	styles, err := fs.ReadFile(assets, "app.css")
	if err != nil {
		t.Fatalf("read embedded app.css: %v", err)
	}
	script, err := fs.ReadFile(assets, "app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	for _, required := range []string{
		`aria-labelledby="subscriptions-title"`, `id="subscription-chart"`, `id="subscription-environment"`,
		`data-window-days="30" aria-pressed="true"`, `id="subscription-rows"`, `id="subscription-trials"`,
		"Churn rate is churned subscriptions divided by subscriptions active at any time in the window",
		"Plan changes and pauses are not churn", "No money figure is shown here",
	} {
		if !strings.Contains(string(document), required) {
			t.Fatalf("dashboard subscription panel does not contain %q", required)
		}
	}
	source := string(script)
	start := strings.Index(source, "function subscriptionEnvironments")
	end := strings.Index(source, "function formatPercent")
	if start < 0 || end < start {
		t.Fatal("subscription renderers are missing")
	}
	renderers := source[start:end]
	for _, required := range []string{
		"loadSubscriptionMetrics", "renderSubscriptionChart", "renderSubscriptionTrials", "Not available for ",
		`trial.availability !== "available"`, "Reason not recorded", "Not counted as churn", "trial state not recorded",
		"ended trials converted", "carry a recorded offer phase",
	} {
		if !strings.Contains(renderers, required) {
			t.Fatalf("subscription renderers do not contain %q", required)
		}
	}
	panelStart := strings.Index(string(document), `class="panel subscription-panel"`)
	panelEnd := strings.Index(string(document), `<div class="dashboard-grid">`)
	panel := string(document)[panelStart:panelEnd] + renderers
	for _, forbidden := range []string{
		"MRR", "ARR", "estimated", "projected", "forecast", "revenue", "Revenue", "price", "currency", "formatRevenue",
		"formatMilliunitValue",
	} {
		if strings.Contains(panel, forbidden) {
			t.Fatalf("subscription panel contains %q", forbidden)
		}
	}
	for _, required := range []string{".subscription-panel", ".subscription-vitals", ".segmented-control button[aria-pressed=\"true\"]", ".subscription-side"} {
		if !strings.Contains(string(styles), required) {
			t.Fatalf("dashboard styles do not contain %q", required)
		}
	}
}

// TestHandlerRedirectsAndDelegates verifies the canonical entrypoint and API fallthrough.
func TestHandlerRedirectsAndDelegates(t *testing.T) {
	t.Parallel()

	fallback := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusTeapot)
	})
	handler, err := New(fallback)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	redirect := httptest.NewRecorder()
	handler.ServeHTTP(redirect, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if redirect.Code != http.StatusPermanentRedirect || redirect.Header().Get("Location") != "/dashboard/" {
		t.Fatalf("dashboard redirect = (%d, %q), want 308 and canonical path", redirect.Code, redirect.Header().Get("Location"))
	}

	delegated := httptest.NewRecorder()
	handler.ServeHTTP(delegated, httptest.NewRequest(http.MethodGet, "/v1/admin/projects", nil))
	if delegated.Code != http.StatusTeapot {
		t.Fatalf("delegated status = %d, want %d", delegated.Code, http.StatusTeapot)
	}
}

// TestDashboardCustomerSupportWorkflow keeps customer search, detail, and audited manual grants wired.
func TestDashboardCustomerSupportWorkflow(t *testing.T) {
	t.Parallel()

	document, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	script, err := fs.ReadFile(assets, "app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	styles, err := fs.ReadFile(assets, "app.css")
	if err != nil {
		t.Fatalf("read embedded app.css: %v", err)
	}
	if strings.Contains(string(document), "First 200") {
		t.Fatal("customers panel still advertises the fixed first-200 listing")
	}
	for _, required := range []string{
		`id="customer-search-form" role="search"`, `id="customer-search"`, `type="search"`, `maxlength="512"`,
		`id="customers-count" aria-live="polite"`, `id="customer-load-more"`, `id="customer-rows"`, `id="open-customer"`,
		`id="customer-detail-dialog" class="setup-dialog customer-detail-dialog"`,
		`id="grant-dialog" class="setup-dialog small-dialog"`, `id="grant-reason" required maxlength="500"`,
		`id="revoke-grant-dialog" class="setup-dialog small-dialog"`, `id="revoke-grant-reason" required maxlength="500"`,
		"never as store evidence",
	} {
		if !strings.Contains(string(document), required) {
			t.Fatalf("dashboard customer support markup does not contain %q", required)
		}
	}
	for _, required := range []string{
		"loadCustomerPage", "openCustomerDetail", "renderCustomerDetail", "timelineEntry", "submitManualGrant",
		"customerSearchMaxBytes", "state.customerSearch.sequence", "newIdempotencyKey",
		`store_observation: "Store verification"`, `manual: "Manual grant"`, `import: "Import"`,
		"No customers yet.", "No customer matches", "Imported history, not store verification.",
		"Manual grants need a stored admin key",
	} {
		if !strings.Contains(string(script), required) {
			t.Fatalf("dashboard customer support script does not contain %q", required)
		}
	}
	if strings.Contains(string(script), "innerHTML") {
		t.Fatal("dashboard must render untrusted customer data through text nodes only")
	}
	for _, required := range []string{".customer-search", ".detail-section", ".timeline-item", ".source-badge.manual", "border-style: dashed"} {
		if !strings.Contains(string(styles), required) {
			t.Fatalf("dashboard customer support styles do not contain %q", required)
		}
	}
}

// TestDashboardBootstrapHandoverAndPasswordManagerSupport verifies guided first-run upgrade and password-manager hints.
func TestDashboardBootstrapHandoverAndPasswordManagerSupport(t *testing.T) {
	t.Parallel()

	document, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	script, err := fs.ReadFile(assets, "app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	styles, err := fs.ReadFile(assets, "app.css")
	if err != nil {
		t.Fatalf("read embedded app.css: %v", err)
	}
	for _, required := range []string{
		`autocomplete="username"`, `autocomplete="current-password"`,
		`id="bootstrap-banner"`, `id="bootstrap-create-admin-key"`,
		`id="revealed-key-flag"`, `id="revealed-key-copy"`,
	} {
		if !strings.Contains(string(document), required) {
			t.Fatalf("dashboard onboarding markup does not contain %q", required)
		}
	}
	for _, required := range []string{
		"renderBootstrapBanner", "secureCookieBlockedOnHTTP",
		`apiRequest("/v1/admin/dashboard-session")`, "Bootstrap retired",
	} {
		if !strings.Contains(string(script), required) {
			t.Fatalf("dashboard onboarding script does not contain %q", required)
		}
	}
	for _, required := range []string{".sr-only", ".bootstrap-banner"} {
		if !strings.Contains(string(styles), required) {
			t.Fatalf("dashboard onboarding styles do not contain %q", required)
		}
	}
}
