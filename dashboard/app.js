"use strict";

const huaweiCredentialContentType = "application/vnd.iapstack.huawei-credentials+json";
const appleCredentialContentType = "application/vnd.iapstack.apple-credentials+json";
const googleCredentialContentType = "application/vnd.iapstack.google-play-credentials+json";
const maximumCredentialFileBytes = 1024 * 1024;
const state = {
  projects: [],
  selectedProject: "",
  managedApplication: null,
  overview: null,
  apiKeys: [],
  pendingRevokeKey: null,
};

const elements = {
  authStage: document.querySelector("#auth-stage"),
  authForm: document.querySelector("#auth-form"),
  adminKey: document.querySelector("#admin-key"),
  authMessage: document.querySelector("#auth-message"),
  appShell: document.querySelector("#app-shell"),
  projectNav: document.querySelector("#project-nav"),
  projectTitle: document.querySelector("#project-title"),
  updatedAt: document.querySelector("#updated-at"),
  globalMessage: document.querySelector("#global-message"),
  refresh: document.querySelector("#refresh"),
  openAPIKeys: document.querySelector("#open-api-keys"),
  logout: document.querySelector("#logout"),
  setupDialog: document.querySelector("#setup-dialog"),
  setupForm: document.querySelector("#setup-form"),
  setupMessage: document.querySelector("#setup-message"),
  setupProvider: document.querySelector("#setup-provider"),
  providerApplicationLabel: document.querySelector("#provider-application-label"),
  providerApplicationID: document.querySelector("#provider-application-id"),
  providerProductLabel: document.querySelector("#provider-product-label"),
  providerProductID: document.querySelector("#provider-product-id"),
  applicationDialog: document.querySelector("#application-dialog"),
  credentialForm: document.querySelector("#credential-form"),
  credentialMessage: document.querySelector("#credential-message"),
  huaweiCredentialFields: document.querySelector("#huawei-credential-fields"),
  appleCredentialFields: document.querySelector("#apple-credential-fields"),
  googleCredentialFields: document.querySelector("#google-credential-fields"),
  credentialTitle: document.querySelector("#credential-title"),
  credentialDescription: document.querySelector("#credential-description"),
  credentialStepName: document.querySelector("#credential-step-name"),
  credentialSubmit: document.querySelector("#credential-submit"),
  webhookForm: document.querySelector("#webhook-form"),
  webhookMessage: document.querySelector("#webhook-message"),
  applicationKeyForm: document.querySelector("#application-key-form"),
  applicationKeyMessage: document.querySelector("#application-key-message"),
  apiKeyDialog: document.querySelector("#api-key-dialog"),
  apiKeyList: document.querySelector("#api-key-list"),
  apiKeyMessage: document.querySelector("#api-key-message"),
  createAdminKey: document.querySelector("#create-admin-key"),
  revokeKeyDialog: document.querySelector("#revoke-key-dialog"),
  revokeKeyIdentity: document.querySelector("#revoke-key-identity"),
  confirmRevokeKey: document.querySelector("#confirm-revoke-key"),
  customerDialog: document.querySelector("#customer-dialog"),
  customerForm: document.querySelector("#customer-form"),
  customerMessage: document.querySelector("#customer-message"),
  keyDialog: document.querySelector("#key-dialog"),
  revealedKeyTitle: document.querySelector("#revealed-key-title"),
  revealedKeyLabel: document.querySelector("#revealed-key-label"),
  revealedKey: document.querySelector("#revealed-key"),
  copyMessage: document.querySelector("#copy-message"),
};

async function apiRequest(path, options = {}) {
  const headers = new Headers(options.headers || {});
  headers.set("X-IAPStack-Dashboard", "1");
  if (options.body) {
    headers.set("Content-Type", "application/json");
  }
  const response = await fetch(path, {
    ...options,
    headers,
    cache: "no-store",
    credentials: "same-origin",
  });
  const requestID = response.headers.get("X-Request-ID") || "";
  let body = null;
  try {
    body = await response.json();
  } catch (_) {
    body = null;
  }
  if (!response.ok) {
    const error = new Error(body?.error?.message || `HTTP ${response.status}`);
    error.status = response.status;
    error.code = body?.error?.code || "request_failed";
    error.requestID = body?.error?.request_id || requestID;
    throw error;
  }
  return body;
}

function showAuthenticated(authenticated) {
  elements.authStage.hidden = authenticated;
  elements.appShell.hidden = !authenticated;
}

function clearLegacyBearerStorage() {
  try {
    window.sessionStorage.removeItem("iapstack.dashboard.admin");
  } catch (_) {
    // Storage access may be disabled; no new bearer material is written there.
  }
}

function errorMessage(error) {
  const suffix = error.requestID ? ` · Request ${error.requestID}` : "";
  if (error.status === 401) {
    return `Administrator access is invalid or expired.${suffix}`;
  }
  if (error.status === 503) {
    return `The database is currently unavailable.${suffix}`;
  }
  return `${error.message || "The request could not be completed."}${suffix}`;
}

function setFormMessage(element, message, tone = "error") {
  element.textContent = message;
  element.classList.toggle("success", tone === "success");
  element.classList.toggle("working", tone === "working");
}

function setGlobalMessage(message, tone = "error") {
  elements.globalMessage.textContent = message;
  elements.globalMessage.classList.toggle("success", tone === "success");
  elements.globalMessage.classList.toggle("info", tone === "info");
}

function setFormBusy(form, busy) {
  const submit = form.querySelector("button[type='submit']");
  if (busy) {
    form.setAttribute("aria-busy", "true");
  } else {
    form.removeAttribute("aria-busy");
  }
  if (submit) submit.disabled = busy;
}

async function connect(adminKey) {
  elements.authMessage.textContent = "";
  setFormBusy(elements.authForm, true);
  try {
    await createDashboardSession(adminKey.trim());
    elements.adminKey.value = "";
    await loadProjects();
    showAuthenticated(true);
  } catch (error) {
    elements.adminKey.value = "";
    elements.authMessage.textContent = errorMessage(error);
    showAuthenticated(false);
  } finally {
    setFormBusy(elements.authForm, false);
  }
}

async function createDashboardSession(adminKey) {
  const response = await fetch("/v1/admin/dashboard-session", {
    method: "POST",
    headers: {
      Authorization: `Bearer ${adminKey}`,
      "X-IAPStack-Dashboard": "1",
    },
    cache: "no-store",
    credentials: "same-origin",
  });
  const requestID = response.headers.get("X-Request-ID") || "";
  let body = null;
  try {
    body = await response.json();
  } catch (_) {
    body = null;
  }
  if (!response.ok) {
    const error = new Error(body?.error?.message || `HTTP ${response.status}`);
    error.status = response.status;
    error.code = body?.error?.code || "request_failed";
    error.requestID = body?.error?.request_id || requestID;
    throw error;
  }
  return body;
}

async function loadProjects(preferredProject = state.selectedProject) {
  const response = await apiRequest("/v1/admin/projects");
  state.projects = response.projects || [];
  if (preferredProject && state.projects.some((project) => project.id === preferredProject)) {
    state.selectedProject = preferredProject;
  } else {
    state.selectedProject = state.projects[0]?.id || "";
  }
  renderProjectNavigation();
  if (state.selectedProject) {
    await loadOverview(state.selectedProject);
  } else {
    renderEmptyWorkspace();
  }
}

async function loadOverview(projectID, focusWorkspace = false) {
  setGlobalMessage("");
  elements.refresh.disabled = true;
  elements.refresh.setAttribute("aria-busy", "true");
  try {
    state.overview = await apiRequest(`/v1/admin/projects/${encodeURIComponent(projectID)}/overview`);
    state.selectedProject = projectID;
    renderProjectNavigation();
    renderOverview();
    elements.updatedAt.textContent = `Updated ${formatTime(new Date().toISOString())}`;
    if (focusWorkspace) elements.appShell.querySelector("#workspace").focus({ preventScroll: true });
  } catch (error) {
    if (error.status === 401) {
      logout();
      elements.authMessage.textContent = errorMessage(error);
      return;
    }
    setGlobalMessage(errorMessage(error));
  } finally {
    elements.refresh.disabled = false;
    elements.refresh.removeAttribute("aria-busy");
  }
}

function renderProjectNavigation() {
  elements.projectNav.replaceChildren();
  for (const project of state.projects) {
    const button = node("button", "project-button");
    button.type = "button";
    button.classList.toggle("active", project.id === state.selectedProject);
    button.setAttribute("aria-current", project.id === state.selectedProject ? "page" : "false");
    button.append(
      node("strong", "", project.id),
      node("small", "", `${countLabel(project.application_count, "app")} · ${countLabel(project.customer_count, "customer")}`),
    );
    button.addEventListener("click", () => loadOverview(project.id, true));
    elements.projectNav.append(button);
  }
  if (!state.projects.length) {
    elements.projectNav.append(node("p", "empty-state", "No projects yet. Use + to create your first catalog."));
  }
}

function renderEmptyWorkspace() {
  state.overview = null;
  elements.projectTitle.textContent = "Create your first project";
  setGlobalMessage("Use the + button in the sidebar to create your first store catalog.", "info");
  setText("metric-apps", "0");
  setText("metric-customers", "0");
  setText("metric-products", "0");
  setText("metric-pending", "0");
  renderAnalytics(emptyAnalytics());
  for (const id of ["application-grid", "product-list", "queue-list"]) {
    const container = document.querySelector(`#${id}`);
    container.replaceChildren(node("p", "empty-state", "This section will populate after you create a project."));
  }
  renderEmptyTable("transaction-rows", 5, "No verifications yet.");
  renderEmptyTable("customer-rows", 3, "No customers yet.");
  renderEmptyTable("webhook-rows", 3, "No webhook events yet.");
}

function renderOverview() {
  const overview = state.overview;
  const pending = sum(overview.queues, "pending");
  const failed = sum(overview.queues, "failed");
  const configured = overview.applications.filter(
    (application) => application.credential_configured && application.webhook_configured,
  ).length;
  const allowed = sum(overview.customers, "allowed_count");
  const delivered = overview.recent_webhook_events.filter((event) => event.status === "delivered").length;

  elements.projectTitle.textContent = overview.project.id;
  setText("metric-apps", overview.project.application_count);
  setText("metric-apps-note", `${configured} fully configured`);
  setText("metric-customers", overview.project.customer_count);
  setText("metric-customers-note", countLabel(allowed, "active entitlement"));
  setText("metric-products", overview.project.product_count);
  setText("metric-products-note", countLabel(sum(overview.products, "store_mapping_count"), "store mapping"));
  setText("metric-pending", pending);
  setText("metric-pending-note", failed ? countLabel(failed, "terminal failure") : "No terminal failures");

  renderAnalytics(overview.analytics);

  setText("signal-store", `${configured}/${overview.applications.length} ready`);
  setText("signal-verify", countLabel(overview.analytics.verified_count, "30-day event"));
  setText("signal-access", `${overview.analytics.active_entitlement_count} active`);
  setText("signal-deliver", `${delivered} delivered`);
  setSignalWarning("store", configured !== overview.applications.length);
  setSignalWarning("verify", failed > 0);
  setSignalWarning("access", overview.analytics.unresolved_count > 0);
  setSignalWarning("deliver", overview.recent_webhook_events.some((event) => event.status === "failed"));

  renderApplications(overview.applications);
  renderProducts(overview.products);
  renderQueues(overview.queues);
  renderTransactions(overview.recent_transactions);
  renderCustomers(overview.customers);
  renderWebhooks(overview.recent_webhook_events);
}

function emptyAnalytics() {
  const today = new Date();
  const dailyActivity = [];
  for (let offset = 29; offset >= 0; offset -= 1) {
    const day = new Date(Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), today.getUTCDate() - offset));
    dailyActivity.push({
      date: day.toISOString().slice(0, 10),
      verified: 0,
      allowed: 0,
      denied: 0,
      unresolved: 0,
      reversed: 0,
    });
  }
  return {
    window_days: 30,
    verified_count: 0,
    active_entitlement_count: 0,
    reversed_count: 0,
    daily_activity: dailyActivity,
    revenue: {
      status: "no_applications",
      recognized_minor_units: 0,
      currency: null,
      production_application_count: 0,
      test_application_count: 0,
    },
    sandbox_value: {
      amounts: [],
      transaction_count: 0,
      missing_price_count: 0,
    },
  };
}

function renderAnalytics(analytics = emptyAnalytics()) {
  setText("analytics-window", `UTC · ${analytics.window_days || 30} days`);
  setText("analytics-verified", formatNumber(analytics.verified_count));
  setText("analytics-active", formatNumber(analytics.active_entitlement_count));
  setText("analytics-reversed", formatNumber(analytics.reversed_count));
  renderRevenue(analytics.revenue || emptyAnalytics().revenue);
  renderSandboxValue(analytics.sandbox_value || emptyAnalytics().sandbox_value);
  renderActivityChart(analytics.daily_activity || []);
}

function renderRevenue(revenue) {
  const badge = document.querySelector("#revenue-status");
  badge.className = "revenue-badge";
  setText("revenue-production-apps", formatNumber(revenue.production_application_count));
  setText("revenue-test-apps", formatNumber(revenue.test_application_count));

  if (revenue.status === "store_reports_required") {
    badge.textContent = "Reports required";
    badge.classList.add("reports-required");
    setText("revenue-value", "—");
    setText("revenue-currency", "not calculated");
    setText(
      "revenue-note",
      "Official store financial reports are required to calculate proceeds, refunds, tax, and commission.",
    );
    return;
  }

  if (revenue.status === "no_applications") {
    badge.textContent = "No applications";
    badge.classList.add("no-applications");
    setText("revenue-value", "0");
    setText("revenue-currency", "real revenue");
    setText("revenue-note", "Add an application to start verification activity.");
    return;
  }

  badge.textContent = "Sandbox only";
  setText("revenue-value", formatRevenue(revenue.recognized_minor_units, revenue.currency));
  setText("revenue-currency", revenue.currency || "real revenue");
  setText("revenue-note", "Test purchases never create real store revenue.");
}

function renderSandboxValue(sandboxValue) {
  const amounts = Array.isArray(sandboxValue.amounts) ? sandboxValue.amounts : [];
  const transactionCount = Number(sandboxValue.transaction_count || 0);
  const missingPriceCount = Number(sandboxValue.missing_price_count || 0);
  setText("sandbox-value-count", countLabel(transactionCount, "priced transaction"));

  if (!amounts.length) {
    setText("sandbox-value-amounts", "0");
    if (missingPriceCount > 0) {
      setText(
        "sandbox-value-note",
        `${countLabel(missingPriceCount, "transaction")} need a fresh verification to capture the signed store price.`,
      );
      return;
    }
    setText("sandbox-value-note", "No priced sandbox transactions in this window.");
    return;
  }

  setText(
    "sandbox-value-amounts",
    amounts.map((amount) => formatMilliunitValue(amount.milliunits, amount.currency)).join(" + "),
  );
  if (missingPriceCount > 0) {
    setText(
      "sandbox-value-note",
      `Provider-signed test value; ${countLabel(missingPriceCount, "older transaction")} still have no captured price.`,
    );
    return;
  }
  setText("sandbox-value-note", "Provider-signed test value, not real proceeds or accounting revenue.");
}

function renderActivityChart(activity) {
  const chart = document.querySelector("#activity-chart");
  chart.replaceChildren();
  const width = 760;
  const top = 10;
  const right = 8;
  const bottom = 30;
  const left = 34;
  const plotWidth = width - left - right;
  const plotHeight = 220 - top - bottom;
  const baseline = top + plotHeight;
  const maximum = Math.max(1, ...activity.map((day) => Number(day.verified || 0)));
  const verified = activity.reduce((total, day) => total + Number(day.verified || 0), 0);
  const denied = activity.reduce((total, day) => total + Number(day.denied || 0), 0);
  const unresolved = activity.reduce((total, day) => total + Number(day.unresolved || 0), 0);
  chart.setAttribute(
    "aria-label",
    `${verified} verified events in ${activity.length || 30} days: ${denied} denied and ${unresolved} unresolved.`,
  );

  const ticks = [...new Set([maximum, Math.ceil(maximum / 2), 0])];
  for (const tick of ticks) {
    const y = baseline - (tick / maximum) * plotHeight;
    chart.append(
      svgNode("line", { class: "chart-grid-line", x1: left, x2: width - right, y1: y, y2: y }),
      svgNode("text", { class: "chart-axis-label", x: left - 7, y: y + 3, "text-anchor": "end" }, tick),
    );
  }

  if (!verified) {
    chart.append(svgNode(
      "text",
      { class: "chart-empty-label", x: left + plotWidth / 2, y: top + plotHeight / 2, "text-anchor": "middle" },
      "No verification activity in this window",
    ));
  }

  const slotWidth = activity.length ? plotWidth / activity.length : plotWidth;
  const barWidth = Math.max(3, Math.min(14, slotWidth * 0.62));
  activity.forEach((day, index) => {
    const x = left + index * slotWidth + (slotWidth - barWidth) / 2;
    let stackedHeight = 0;
    for (const [key, className] of [
      ["allowed", "chart-bar-allowed"],
      ["denied", "chart-bar-denied"],
      ["unresolved", "chart-bar-unresolved"],
    ]) {
      const value = Number(day[key] || 0);
      if (!value) continue;
      const height = (value / maximum) * plotHeight;
      const bar = svgNode("rect", {
        class: `chart-bar ${className}`,
        x,
        y: baseline - stackedHeight - height,
        width: barWidth,
        height,
        rx: 2,
      });
      bar.append(svgNode(
        "title",
        {},
        `${formatChartDate(day.date)}: ${day.verified} verified, ${day.allowed} allowed, ${day.denied} denied, ${day.unresolved} unresolved`,
      ));
      chart.append(bar);
      stackedHeight += height;
    }

    const finalDay = index === activity.length - 1;
    const weeklyTick = index % 7 === 0 && index < activity.length - 2;
    if (index === 0 || finalDay || weeklyTick) {
      chart.append(svgNode(
        "text",
        { class: "chart-axis-label", x: x + barWidth / 2, y: baseline + 19, "text-anchor": "middle" },
        formatChartDate(day.date),
      ));
    }
  });
}

function svgNode(tag, attributes = {}, text = "") {
  const element = document.createElementNS("http://www.w3.org/2000/svg", tag);
  for (const [name, value] of Object.entries(attributes)) {
    element.setAttribute(name, String(value));
  }
  if (text !== "") element.textContent = String(text);
  return element;
}

function formatChartDate(value) {
  const date = new Date(`${value}T00:00:00Z`);
  if (Number.isNaN(date.valueOf())) return "—";
  return new Intl.DateTimeFormat("en-GB", {
    day: "numeric",
    month: "short",
    timeZone: "UTC",
  }).format(date);
}

function formatRevenue(minorUnits, currency) {
  if (minorUnits === null || minorUnits === undefined) return "—";
  if (!currency) return formatNumber(minorUnits);
  try {
    const formatter = new Intl.NumberFormat("en", { style: "currency", currency });
    const digits = formatter.resolvedOptions().maximumFractionDigits;
    return formatter.format(Number(minorUnits) / (10 ** digits));
  } catch (_) {
    return `${formatNumber(minorUnits)} ${currency}`;
  }
}

function formatMilliunitValue(milliunits, currency) {
  const value = Number(milliunits || 0) / 1000;
  if (!currency) return formatNumber(value);
  try {
    const formatter = new Intl.NumberFormat("en", { style: "currency", currency });
    return formatter.format(value);
  } catch (_error) {
    return `${value.toFixed(3)} ${currency}`;
  }
}

function formatNumber(value) {
  return new Intl.NumberFormat("en").format(Number(value || 0));
}

function renderApplications(applications) {
  const container = document.querySelector("#application-grid");
  container.replaceChildren();
  setText("apps-count", countLabel(applications.length, "record"));
  if (!applications.length) {
    container.append(node("p", "empty-state", "This project has no applications."));
    return;
  }
  for (const application of applications) {
    const card = node("article", "application-card");
    const heading = node("div", "application-card-head");
    const identity = node("div");
    identity.append(node("h3", "", application.id), node("code", "", application.provider_application_id));
    heading.append(identity, node("span", "provider-badge", providerLabel(application.provider)));
    const config = node("div", "config-list");
    config.append(
      configRow("Environment", application.environment, true),
      configRow("Provider credential", application.credential_configured ? "Ready" : "Missing", application.credential_configured),
      configRow("Webhook", application.webhook_configured ? "Ready" : "Missing", application.webhook_configured),
    );
    const actions = node("div", "application-actions");
    const manage = node("button", "secondary-button", "Manage connections");
    manage.type = "button";
    manage.setAttribute("aria-label", `Manage connections for ${application.id}`);
    manage.addEventListener("click", () => openApplicationManager(application));
    actions.append(manage);
    card.append(heading, config, actions);
    container.append(card);
  }
}

function configRow(label, value, ready) {
  const row = node("div", "config-row");
  const status = node("span", `config-state${ready ? " ready" : ""}`, value);
  row.append(node("span", "", label), status);
  return row;
}

function openApplicationManager(application) {
  state.managedApplication = application;
  elements.credentialForm.reset();
  resetCredentialFileMessages();
  elements.webhookForm.reset();
  elements.credentialMessage.textContent = "";
  elements.webhookMessage.textContent = "";
  elements.applicationKeyMessage.textContent = "";
  configureCredentialForm(application);
  renderCommissioningStatus();
  elements.applicationDialog.showModal();
  const activeFields = {
    apple_app_store: elements.appleCredentialFields,
    google_play: elements.googleCredentialFields,
    huawei_appgallery: elements.huaweiCredentialFields,
  }[application.provider];
  activeFields.querySelector("input, textarea").focus();
}

function configureCredentialForm(application) {
  const apple = application.provider === "apple_app_store";
  const google = application.provider === "google_play";
  const huawei = application.provider === "huawei_appgallery";
  setCredentialFieldsEnabled(elements.appleCredentialFields, apple);
  setCredentialFieldsEnabled(elements.googleCredentialFields, google);
  setCredentialFieldsEnabled(elements.huaweiCredentialFields, huawei);
  elements.appleCredentialFields.hidden = !apple;
  elements.googleCredentialFields.hidden = !google;
  elements.huaweiCredentialFields.hidden = !huawei;
  const provider = providerLabel(application.provider);
  elements.credentialStepName.textContent = apple ? "Apple" : google ? "Google" : "Huawei";
  elements.credentialTitle.textContent = `${provider} server connection`;
  elements.credentialDescription.textContent = apple
    ? "Used to verify StoreKit transactions and query App Store Server API state. Saved keys and certificates are never shown again."
    : google
      ? "Used to verify purchases with Android Publisher and optionally authenticate RTDN pushes. Saved service-account values are never shown again."
      : "Used for server-side calls to Huawei token, order, and subscription services. Saved values are never shown again.";
  elements.credentialSubmit.textContent = `Save ${apple ? "Apple" : google ? "Google" : "Huawei"} connection`;
  if (apple) {
    const bundleID = elements.appleCredentialFields.querySelector("input[name='bundle_id']");
    const appAppleID = elements.appleCredentialFields.querySelector("input[name='app_apple_id']");
    bundleID.value = application.provider_application_id;
    appAppleID.required = application.environment === "production";
  }
	if (google) {
		const credentialRequired = !application.credential_configured;
		for (const name of ["google_client_email", "google_private_key_id", "google_private_key"]) {
			elements.googleCredentialFields.querySelector(`[name='${name}']`).required = credentialRequired;
		}
	}
}

function setCredentialFieldsEnabled(container, enabled) {
  for (const field of container.querySelectorAll("input, textarea, select")) {
    field.disabled = !enabled;
  }
}

function renderCommissioningStatus() {
  const application = state.managedApplication;
  if (!application) return;
  setText("application-dialog-title", application.id);
  setText("application-dialog-meta", `${providerLabel(application.provider)} · ${application.environment} · ${application.provider_application_id}`);
  setText("credential-revision", application.credential_revision ? `Revision ${application.credential_revision}` : "New");
  setText("webhook-revision", application.webhook_revision ? `Revision ${application.webhook_revision}` : "New");
  setText("credential-step-status", application.credential_configured ? `Revision ${application.credential_revision}` : "Missing");
  setText("webhook-step-status", application.webhook_configured ? `Revision ${application.webhook_revision}` : "Missing");
  document.querySelector("#credential-step").classList.toggle("complete", application.credential_configured);
  document.querySelector("#webhook-step").classList.toggle("complete", application.webhook_configured);
}

function closeApplicationManager() {
  elements.credentialForm.reset();
  resetCredentialFileMessages();
  elements.webhookForm.reset();
  elements.credentialMessage.textContent = "";
  elements.webhookMessage.textContent = "";
  elements.applicationKeyMessage.textContent = "";
  state.managedApplication = null;
  elements.applicationDialog.close();
}

async function refreshManagedApplication() {
  const applicationID = state.managedApplication?.id;
  await loadOverview(state.selectedProject);
  state.managedApplication = state.overview?.applications.find((application) => application.id === applicationID) || null;
  renderCommissioningStatus();
}

function applicationAdminPath(suffix) {
  const project = encodeURIComponent(state.selectedProject);
  const application = encodeURIComponent(state.managedApplication.id);
  return `/v1/admin/projects/${project}/applications/${application}${suffix}`;
}

async function saveHuaweiCredential(form) {
  const data = Object.fromEntries(new FormData(form));
  await apiRequest(applicationAdminPath("/credentials/huawei_server_api"), {
    method: "PUT",
    body: JSON.stringify({
      content_type: huaweiCredentialContentType,
      schema_version: 1,
      expected_revision: state.managedApplication.credential_revision,
      payload: {
        client_id: data.client_id,
        client_secret: data.client_secret,
        public_key: data.public_key,
        token_url: data.token_url,
        order_url: data.order_url,
        subscription_url: data.subscription_url,
      },
    }),
  });
}

async function saveAppleCredential(form) {
  const data = Object.fromEntries(new FormData(form));
  const roots = data.root_certificates.match(
    /-----BEGIN CERTIFICATE-----[\s\S]*?-----END CERTIFICATE-----/g,
  )?.map((certificate) => certificate.trim()) || [];
  if (!roots.length) {
    throw new Error("Add at least one PEM-encoded trusted Apple root certificate.");
  }
  const payload = {
    issuer_id: data.issuer_id,
    key_id: data.key_id,
    bundle_id: data.bundle_id,
    private_key: data.private_key,
    root_certificates: roots,
  };
  if (data.app_apple_id) {
    const appAppleID = Number(data.app_apple_id);
    if (!Number.isSafeInteger(appAppleID) || appAppleID <= 0) {
      throw new Error("App Apple ID must be a positive integer.");
    }
    payload.app_apple_id = appAppleID;
  }
  await apiRequest(applicationAdminPath("/credentials/apple_app_store_server_api"), {
    method: "PUT",
    body: JSON.stringify({
      content_type: appleCredentialContentType,
      schema_version: 1,
      expected_revision: state.managedApplication.credential_revision,
      payload,
    }),
  });
}

async function saveGoogleCredential(form) {
  const data = Object.fromEntries(new FormData(form));
	const credentialValues = [
		data.google_client_email.trim(),
		data.google_private_key_id.trim(),
		data.google_private_key.trim(),
	];
	const configuredCredentialValues = credentialValues.filter(Boolean).length;
  const rtdnValues = [
    data.google_rtdn_subscription.trim(),
    data.google_rtdn_push_email.trim(),
    data.google_rtdn_audience.trim(),
  ];
  const configuredRTDNValues = rtdnValues.filter(Boolean).length;
  if (configuredRTDNValues > 0 && configuredRTDNValues < rtdnValues.length) {
    throw new Error("Complete all three RTDN fields or leave all of them empty.");
  }
	if (configuredCredentialValues === 0 && state.managedApplication.credential_configured) {
		if (configuredRTDNValues !== rtdnValues.length) {
			throw new Error("Complete all three RTDN fields to update the existing Google connection.");
		}
		await apiRequest(applicationAdminPath("/credentials/google_play_android_publisher/rtdn"), {
			method: "PUT",
			body: JSON.stringify({
				subscription: rtdnValues[0],
				push_service_account_email: rtdnValues[1],
				audience: rtdnValues[2],
				expected_revision: state.managedApplication.credential_revision,
			}),
		});
		return;
	}
	if (configuredCredentialValues !== credentialValues.length) {
		throw new Error("Complete all three service-account fields to create or rotate the Google connection.");
	}
  const payload = {
    client_email: data.google_client_email,
    private_key_id: data.google_private_key_id,
    private_key: data.google_private_key,
  };
  if (configuredRTDNValues === rtdnValues.length) {
    payload.rtdn = {
      subscription: rtdnValues[0],
      push_service_account_email: rtdnValues[1],
      audience: rtdnValues[2],
    };
  }
  await apiRequest(applicationAdminPath("/credentials/google_play_android_publisher"), {
    method: "PUT",
    body: JSON.stringify({
      content_type: googleCredentialContentType,
      schema_version: 1,
      expected_revision: state.managedApplication.credential_revision,
      payload,
    }),
  });
}

async function saveProviderCredential(form) {
  if (state.managedApplication.provider === "apple_app_store") {
    return saveAppleCredential(form);
  }
  if (state.managedApplication.provider === "google_play") {
    return saveGoogleCredential(form);
  }
  if (state.managedApplication.provider === "huawei_appgallery") {
    return saveHuaweiCredential(form);
  }
  throw new Error("This provider cannot be configured from the dashboard yet.");
}

function setFileStatus(id, message, tone = "") {
  const status = document.querySelector(`#${id}`);
  status.textContent = message;
  status.classList.toggle("success", tone === "success");
  status.classList.toggle("error", tone === "error");
}

function resetCredentialFileMessages() {
  setFileStatus("apple-private-key-file-status", "Select locally or paste the PEM value below.");
  setFileStatus("apple-root-files-status", "Select one or more files or paste PEM certificates below.");
  setFileStatus("google-service-account-file-status", "The selected file is parsed locally and is not stored as an uploaded file.");
}

function requireBoundedCredentialFile(file) {
  if (!file) throw new Error("Select a credential file first.");
  if (file.size <= 0 || file.size > maximumCredentialFileBytes) {
    throw new Error("Credential files must be between 1 byte and 1 MiB.");
  }
}

async function readCredentialText(file) {
  requireBoundedCredentialFile(file);
  return file.text();
}

function requirePrivateKeyPEM(value) {
  const key = value.trim();
  if (!key.includes("-----BEGIN PRIVATE KEY-----") || !key.includes("-----END PRIVATE KEY-----")) {
    throw new Error("The selected file is not a PKCS#8 PEM private key.");
  }
  return key;
}

function bytesToBase64(bytes) {
  let binary = "";
  for (let offset = 0; offset < bytes.length; offset += 8192) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + 8192));
  }
  return window.btoa(binary);
}

async function readAppleRootCertificate(file) {
  requireBoundedCredentialFile(file);
  const bytes = new Uint8Array(await file.arrayBuffer());
  const text = new TextDecoder().decode(bytes);
  const certificates = text.match(
    /-----BEGIN CERTIFICATE-----[\s\S]*?-----END CERTIFICATE-----/g,
  );
  if (certificates?.length) {
    return certificates.map((certificate) => certificate.trim()).join("\n");
  }
  const encoded = bytesToBase64(bytes);
  const lines = encoded.match(/.{1,64}/g) || [];
  return `-----BEGIN CERTIFICATE-----\n${lines.join("\n")}\n-----END CERTIFICATE-----`;
}

async function loadApplePrivateKeyFile(file) {
  const key = requirePrivateKeyPEM(await readCredentialText(file));
  elements.appleCredentialFields.querySelector("textarea[name='private_key']").value = key;
  setFileStatus("apple-private-key-file-status", "Private key loaded locally. It will be sent only when you save the Apple connection.", "success");
}

async function loadAppleRootFiles(files) {
  const selected = Array.from(files || []);
  if (!selected.length) throw new Error("Select at least one Apple root certificate.");
  const totalBytes = selected.reduce((total, file) => total + file.size, 0);
  if (totalBytes > maximumCredentialFileBytes) {
    throw new Error("The combined Apple root files must not exceed 1 MiB.");
  }
  const certificates = await Promise.all(selected.map(readAppleRootCertificate));
  elements.appleCredentialFields.querySelector("textarea[name='root_certificates']").value = certificates.join("\n");
  setFileStatus("apple-root-files-status", `${selected.length} Apple root certificate file${selected.length === 1 ? "" : "s"} loaded locally.`, "success");
}

async function loadGoogleServiceAccountFile(file) {
  const text = await readCredentialText(file);
  let credential;
  try {
    credential = JSON.parse(text);
  } catch (_) {
    throw new Error("The selected service-account file is not valid JSON.");
  }
  if (credential.type && credential.type !== "service_account") {
    throw new Error("The selected JSON is not a Google service-account credential.");
  }
  const clientEmail = String(credential.client_email || "").trim();
  const privateKeyID = String(credential.private_key_id || "").trim();
  const privateKey = requirePrivateKeyPEM(String(credential.private_key || ""));
  if (!clientEmail || !privateKeyID) {
    throw new Error("The service-account JSON is missing client_email or private_key_id.");
  }
  elements.googleCredentialFields.querySelector("input[name='google_client_email']").value = clientEmail;
  elements.googleCredentialFields.querySelector("input[name='google_private_key_id']").value = privateKeyID;
  elements.googleCredentialFields.querySelector("textarea[name='google_private_key']").value = privateKey;
  setFileStatus("google-service-account-file-status", "Service-account fields loaded locally. They will be sent only when you save the Google connection.", "success");
}

async function saveWebhook(form) {
  const data = Object.fromEntries(new FormData(form));
  await apiRequest(applicationAdminPath("/webhook"), {
    method: "PUT",
    body: JSON.stringify({
      url: data.url,
      signing_secret: data.signing_secret,
      expected_revision: state.managedApplication.webhook_revision,
    }),
  });
}

async function createApplicationKey() {
  return apiRequest("/v1/admin/api-keys", {
    method: "POST",
    body: JSON.stringify({
      role: "application",
      project_id: state.selectedProject,
      application_id: state.managedApplication.id,
    }),
  });
}

// revealKey presents a newly generated bearer once with role-specific language.
function revealKey(value, role) {
  if (elements.applicationDialog.open) closeApplicationManager();
  if (elements.apiKeyDialog.open) elements.apiKeyDialog.close();
  elements.revealedKeyTitle.textContent = role === "admin" ? "Admin key ready" : "Application key ready";
  elements.revealedKeyLabel.textContent = role === "admin" ? "Administrator bearer" : "Application bearer";
  elements.revealedKey.value = value;
  elements.copyMessage.textContent = "";
  elements.keyDialog.showModal();
}

// openAPIKeyManager opens the lifecycle workspace and refreshes authoritative metadata.
async function openAPIKeyManager() {
  state.pendingRevokeKey = null;
  elements.apiKeyDialog.showModal();
  await loadAPIKeys();
}

// loadAPIKeys refreshes bounded secret-free lifecycle metadata from the admin API.
async function loadAPIKeys(successMessage = "") {
  elements.apiKeyMessage.textContent = "Loading access keys…";
  elements.apiKeyMessage.className = "form-message working";
  elements.createAdminKey.disabled = true;
  try {
    const response = await apiRequest("/v1/admin/api-keys");
    state.apiKeys = response.api_keys || [];
    renderAPIKeys();
    setFormMessage(elements.apiKeyMessage, successMessage, successMessage ? "success" : "error");
  } catch (error) {
    if (error.status === 401) {
      if (elements.apiKeyDialog.open) elements.apiKeyDialog.close();
      logout();
      elements.authMessage.textContent = errorMessage(error);
      return;
    }
    setFormMessage(elements.apiKeyMessage, errorMessage(error));
  } finally {
    elements.createAdminKey.disabled = false;
  }
}

// renderAPIKeys draws role, scope, lifecycle, and safe rotation actions as an operational rail.
function renderAPIKeys() {
  elements.apiKeyList.replaceChildren();
  const activeAdmins = state.apiKeys.filter((key) => key.role === "admin" && !key.revoked_at).length;
  const activeApplications = state.apiKeys.filter((key) => key.role === "application" && !key.revoked_at).length;
  const revoked = state.apiKeys.filter((key) => key.revoked_at).length;
  setText("active-admin-keys", activeAdmins);
  setText("active-application-keys", activeApplications);
  setText("revoked-keys", revoked);
  if (!state.apiKeys.length) {
    elements.apiKeyList.append(node("p", "empty-state", "No stored keys yet. Create an administrator key before removing the bootstrap credential."));
    return;
  }
  for (const key of state.apiKeys) {
    const revokedKey = Boolean(key.revoked_at);
    const card = node("article", `api-key-card${revokedKey ? " revoked" : ""}`);
    const identity = node("div", "api-key-identity");
    const identityLine = node("div");
    identityLine.append(
      node("span", `status-badge ${revokedKey ? "revoked" : "active"}`, revokedKey ? "Revoked" : "Active"),
      node("code", "", key.id),
    );
    if (key.current) identityLine.append(node("span", "current-key-badge", "Current"));
    identity.append(identityLine, node("small", "", `Created ${formatTime(key.created_at)}${revokedKey ? ` · Revoked ${formatTime(key.revoked_at)}` : ""}`));

    const scope = node("div", "api-key-scope");
    if (key.role === "admin") {
      scope.append(node("strong", "", "Administrator"), node("small", "", "All control-plane projects"));
    } else {
      scope.append(node("strong", "", key.application_id || "Application"), node("small", "", key.project_id || "Unknown project"));
    }

    const revoke = node("button", "text-button", revokedKey ? "Revoked" : "Revoke");
    revoke.type = "button";
    const finalAdmin = key.role === "admin" && activeAdmins <= 1;
    revoke.disabled = revokedKey || key.current || finalAdmin;
    if (key.current) revoke.title = "Connect with a replacement key before revoking this session.";
    if (finalAdmin && !key.current) revoke.title = "Create another administrator key before revoking the final active administrator.";
    if (!revoke.disabled) revoke.addEventListener("click", () => openRevokeKeyConfirmation(key));
    card.append(identity, scope, revoke);
    elements.apiKeyList.append(card);
  }
}

// createAdministratorKey creates a replacement administrator bearer without revoking existing access.
async function createAdministratorKey() {
  elements.createAdminKey.disabled = true;
  setFormMessage(elements.apiKeyMessage, "Creating administrator key…", "working");
  try {
    const response = await apiRequest("/v1/admin/api-keys", {
      method: "POST",
      body: JSON.stringify({ role: "admin" }),
    });
    revealKey(response.key, "admin");
  } catch (error) {
    setFormMessage(elements.apiKeyMessage, errorMessage(error));
  } finally {
    elements.createAdminKey.disabled = false;
  }
}

// openRevokeKeyConfirmation prepares an explicit irreversible-action confirmation.
function openRevokeKeyConfirmation(key) {
  state.pendingRevokeKey = key;
  elements.revokeKeyIdentity.textContent = `${key.role === "admin" ? "Administrator" : "Application"} · ${key.id}`;
  elements.revokeKeyDialog.showModal();
}

// closeRevokeKeyConfirmation clears pending destructive-action state.
function closeRevokeKeyConfirmation() {
  state.pendingRevokeKey = null;
  elements.revokeKeyIdentity.textContent = "";
  if (elements.revokeKeyDialog.open) elements.revokeKeyDialog.close();
}

// revokePendingAPIKey performs one idempotent revocation and refreshes lifecycle metadata.
async function revokePendingAPIKey() {
  const key = state.pendingRevokeKey;
  if (!key) return;
  elements.confirmRevokeKey.disabled = true;
  try {
    await apiRequest(`/v1/admin/api-keys/${encodeURIComponent(key.id)}`, { method: "DELETE" });
    closeRevokeKeyConfirmation();
    await loadAPIKeys("Access key revoked.");
  } catch (error) {
    closeRevokeKeyConfirmation();
    setFormMessage(elements.apiKeyMessage, errorMessage(error));
  } finally {
    elements.confirmRevokeKey.disabled = false;
  }
}

function closeKeyDialog() {
  elements.revealedKey.value = "";
  elements.copyMessage.textContent = "";
  elements.keyDialog.close();
}

async function createCustomer(form) {
  const data = Object.fromEntries(new FormData(form));
  const project = encodeURIComponent(state.selectedProject);
  const customer = encodeURIComponent(data.customer_id);
  await apiRequest(`/v1/admin/projects/${project}/customers/${customer}`, {
    method: "PUT",
    body: JSON.stringify({ external_id: data.external_id }),
  });
}

function renderProducts(products) {
  const container = document.querySelector("#product-list");
  container.replaceChildren();
  setText("products-count", countLabel(products.length, "record"));
  if (!products.length) {
    container.append(node("p", "empty-state", "No catalog products found."));
    return;
  }
  for (const product of products) {
    const item = node("article", "stack-item");
    const identity = node("div");
    identity.append(node("strong", "", product.id), node("span", "kind-badge", productKindLabel(product.kind)));
    const tags = node("div", "entitlement-tags");
    for (const key of product.entitlement_keys) {
      tags.append(node("span", "", key));
    }
    item.append(
      identity,
      tags,
      node("span", "mapping-count", `${product.store_mapping_count} provider mapping`),
    );
    container.append(item);
  }
}

function renderQueues(queues) {
  const container = document.querySelector("#queue-list");
  container.replaceChildren();
  for (const queue of queues) {
    const item = node("article", "queue-item");
    item.append(
      node("strong", "", queueLabel(queue.name)),
      queueNumber("Pending", queue.pending),
      queueNumber("Completed", queue.completed),
      queueNumber("Failed", queue.failed, "failed"),
    );
    container.append(item);
  }
}

function queueNumber(label, value, extra = "") {
  const item = node("span", `queue-number ${extra}`.trim());
  item.append(node("b", "", value), document.createTextNode(label));
  return item;
}

function renderTransactions(transactions) {
  const body = document.querySelector("#transaction-rows");
  body.replaceChildren();
  if (!transactions.length) {
    renderEmptyTable("transaction-rows", 5, "No verified purchase observations yet.");
    return;
  }
  for (const transaction of transactions) {
    const row = document.createElement("tr");
    row.append(
      cellBadge(transaction.lifecycle_state),
      compoundCell(transaction.provider_product_id, transaction.product_id),
      compoundCell(transaction.customer_id, transaction.application_id),
      cellBadge(transaction.access, transaction.access_reason),
      textCell(formatTime(transaction.observed_at)),
    );
    body.append(row);
  }
}

function renderCustomers(customers) {
  const body = document.querySelector("#customer-rows");
  body.replaceChildren();
  if (!customers.length) {
    renderEmptyTable("customer-rows", 3, "No customers yet.");
    return;
  }
  for (const customer of customers) {
    const row = document.createElement("tr");
    row.append(
      compoundCell(customer.external_id, customer.id),
      textCell(`${customer.allowed_count}/${customer.entitlement_count} allowed`),
      textCell(customer.last_observed_at ? formatTime(customer.last_observed_at) : "—"),
    );
    body.append(row);
  }
}

function renderWebhooks(events) {
  const body = document.querySelector("#webhook-rows");
  body.replaceChildren();
  if (!events.length) {
    renderEmptyTable("webhook-rows", 3, "No webhook deliveries yet.");
    return;
  }
  for (const event of events) {
    const row = document.createElement("tr");
    row.append(
      compoundCell(event.event_type, event.id),
      cellBadge(event.status, event.error_code || ""),
      textCell(formatTime(event.delivered_at || event.failed_at || event.occurred_at)),
    );
    body.append(row);
  }
}

function renderEmptyTable(bodyID, columns, message) {
  const body = document.querySelector(`#${bodyID}`);
  body.replaceChildren();
  const row = document.createElement("tr");
  const cell = node("td", "empty-cell", message);
  cell.colSpan = columns;
  row.append(cell);
  body.append(row);
}

function compoundCell(primary, secondary) {
  const cell = document.createElement("td");
  cell.append(node("strong", "", primary), node("code", "", secondary));
  return cell;
}

function textCell(value) {
  return node("td", "", value);
}

function cellBadge(value, note = "") {
  const cell = document.createElement("td");
  cell.append(node("span", `status-badge ${statusClass(value)}`, value.replaceAll("_", " ")));
  if (note) {
    cell.append(document.createElement("br"), node("code", "", note.replaceAll("_", " ")));
  }
  return cell;
}

function statusClass(value) {
  return String(value).toLowerCase().replaceAll("_", "-");
}

function providerLabel(provider) {
  return {
    apple_app_store: "Apple App Store",
    google_play: "Google Play",
    huawei_appgallery: "Huawei AppGallery",
  }[provider] || provider.replaceAll("_", " ");
}

function productKindLabel(kind) {
  return kind === "non_consumable" ? "Lifetime" : kind.replaceAll("_", " ");
}

function queueLabel(name) {
  return {
    inbox: "Provider inbox",
    reconciliation: "Reconciliation",
    outbox: "Webhook outbox",
  }[name] || name;
}

function formatTime(value) {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.valueOf())) return "—";
  return new Intl.DateTimeFormat("en-GB", {
    dateStyle: "short",
    timeStyle: "short",
  }).format(date);
}

function sum(items, key) {
  return items.reduce((total, item) => total + Number(item[key] || 0), 0);
}

function countLabel(value, singular) {
  return `${value} ${singular}${Number(value) === 1 ? "" : "s"}`;
}

function node(tag, className = "", text = "") {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (text !== "") element.textContent = String(text);
  return element;
}

function setText(id, value) {
  document.querySelector(`#${id}`).textContent = String(value);
}

function setSignalWarning(name, warning) {
  document.querySelector(`[data-signal="${name}"]`).classList.toggle("warning", warning);
}

async function logout() {
  let logoutError = null;
  try {
    await fetch("/v1/admin/dashboard-session", {
      method: "DELETE",
      headers: { "X-IAPStack-Dashboard": "1" },
      cache: "no-store",
      credentials: "same-origin",
    });
  } catch (error) {
    logoutError = error;
  }
  state.projects = [];
  state.selectedProject = "";
  state.managedApplication = null;
  state.overview = null;
  state.apiKeys = [];
  state.pendingRevokeKey = null;
  elements.credentialForm.reset();
  elements.webhookForm.reset();
  elements.revealedKey.value = "";
  for (const dialog of [
    elements.applicationDialog, elements.apiKeyDialog, elements.revokeKeyDialog,
    elements.customerDialog, elements.keyDialog, elements.setupDialog,
  ]) {
    if (dialog.open) dialog.close();
  }
  elements.adminKey.value = "";
  showAuthenticated(false);
  if (logoutError) {
    elements.authMessage.textContent = "The browser session could not be cleared while the server was unavailable.";
  }
  elements.adminKey.focus();
}

async function createCatalog(form) {
  const data = Object.fromEntries(new FormData(form));
  const project = encodeURIComponent(data.project_id);
  const application = encodeURIComponent(data.application_id);
  const entitlement = encodeURIComponent(data.entitlement_id);
  const product = encodeURIComponent(data.product_id);
  const providerProduct = encodeURIComponent(data.provider_product_id);
  const mutations = [
    [`/v1/admin/projects/${project}`, {}],
    [`/v1/admin/projects/${project}/applications/${application}`, {
      provider: data.provider,
      environment: data.environment,
      provider_application_id: data.provider_application_id,
    }],
    [`/v1/admin/projects/${project}/entitlements/${entitlement}`, { key: data.entitlement_key }],
    [`/v1/admin/projects/${project}/products/${product}`, {
      kind: data.product_kind,
      entitlement_ids: [data.entitlement_id],
    }],
    [`/v1/admin/projects/${project}/applications/${application}/store-products/${providerProduct}`, {
      product_id: data.product_id,
    }],
  ];
  for (const [path, body] of mutations) {
    await apiRequest(path, { method: "PUT", body: JSON.stringify(body) });
  }
  return data.project_id;
}

function syncSetupProvider() {
  const provider = elements.setupProvider.value;
  const copy = {
    apple_app_store: {
      applicationLabel: "Apple bundle ID",
      applicationPlaceholder: "com.example.application",
      productLabel: "App Store product ID",
      productPlaceholder: "premium_monthly",
    },
    google_play: {
      applicationLabel: "Android package name",
      applicationPlaceholder: "com.example.application",
      productLabel: "Google Play product ID",
      productPlaceholder: "premium_monthly",
    },
    huawei_appgallery: {
      applicationLabel: "Huawei App ID",
      applicationPlaceholder: "123456789",
      productLabel: "Huawei product ID",
      productPlaceholder: "premium_yearly_huawei",
    },
  }[provider];
  elements.providerApplicationLabel.textContent = copy.applicationLabel;
  elements.providerApplicationID.placeholder = copy.applicationPlaceholder;
  elements.providerProductLabel.textContent = copy.productLabel;
  elements.providerProductID.placeholder = copy.productPlaceholder;
  const apple = provider === "apple_app_store";
  const testEnvironment = elements.setupForm.querySelector("select[name='environment'] option[value='test']");
  testEnvironment.disabled = apple;
  testEnvironment.hidden = apple;
  if (apple && testEnvironment.selected) {
    elements.setupForm.querySelector("select[name='environment']").value = "sandbox";
  }
}

elements.authForm.addEventListener("submit", (event) => {
  event.preventDefault();
  connect(elements.adminKey.value);
});

elements.refresh.addEventListener("click", () => {
  if (state.selectedProject) loadOverview(state.selectedProject);
});

elements.openAPIKeys.addEventListener("click", openAPIKeyManager);
document.querySelector("#close-api-keys").addEventListener("click", () => elements.apiKeyDialog.close());
elements.createAdminKey.addEventListener("click", createAdministratorKey);
document.querySelector("#close-revoke-key").addEventListener("click", closeRevokeKeyConfirmation);
document.querySelector("#cancel-revoke-key").addEventListener("click", closeRevokeKeyConfirmation);
elements.confirmRevokeKey.addEventListener("click", revokePendingAPIKey);
elements.revokeKeyDialog.addEventListener("close", () => {
  state.pendingRevokeKey = null;
  elements.revokeKeyIdentity.textContent = "";
});

elements.logout.addEventListener("click", logout);

document.querySelector("#open-setup").addEventListener("click", () => {
  elements.setupMessage.textContent = "";
  syncSetupProvider();
  elements.setupDialog.showModal();
  elements.setupForm.querySelector("input[name='project_id']").focus();
});

document.querySelector("#close-setup").addEventListener("click", () => elements.setupDialog.close());
document.querySelector("#cancel-setup").addEventListener("click", () => elements.setupDialog.close());
elements.setupProvider.addEventListener("change", syncSetupProvider);

elements.setupForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  elements.setupMessage.textContent = "Creating catalog…";
  setFormBusy(elements.setupForm, true);
  try {
    const projectID = await createCatalog(elements.setupForm);
    elements.setupForm.reset();
    syncSetupProvider();
    elements.setupDialog.close();
    await loadProjects(projectID);
    setGlobalMessage("Catalog setup created. Complete the provider credential and notification connection.", "success");
  } catch (error) {
    elements.setupMessage.textContent = errorMessage(error);
  } finally {
    setFormBusy(elements.setupForm, false);
  }
});

document.querySelector("#close-application").addEventListener("click", closeApplicationManager);

elements.applicationDialog.addEventListener("close", () => {
  elements.credentialForm.reset();
  resetCredentialFileMessages();
  elements.webhookForm.reset();
  elements.credentialMessage.textContent = "";
  elements.webhookMessage.textContent = "";
  elements.applicationKeyMessage.textContent = "";
  state.managedApplication = null;
});

document.querySelector("#apple-private-key-file").addEventListener("change", async (event) => {
  const target = elements.appleCredentialFields.querySelector("textarea[name='private_key']");
  target.value = "";
  try {
    await loadApplePrivateKeyFile(event.currentTarget.files?.[0]);
  } catch (error) {
    setFileStatus("apple-private-key-file-status", error.message, "error");
  }
});

document.querySelector("#apple-root-files").addEventListener("change", async (event) => {
  const target = elements.appleCredentialFields.querySelector("textarea[name='root_certificates']");
  target.value = "";
  try {
    await loadAppleRootFiles(event.currentTarget.files);
  } catch (error) {
    setFileStatus("apple-root-files-status", error.message, "error");
  }
});

document.querySelector("#google-service-account-file").addEventListener("change", async (event) => {
  for (const selector of [
    "input[name='google_client_email']",
    "input[name='google_private_key_id']",
    "textarea[name='google_private_key']",
  ]) {
    elements.googleCredentialFields.querySelector(selector).value = "";
  }
  try {
    await loadGoogleServiceAccountFile(event.currentTarget.files?.[0]);
  } catch (error) {
    setFileStatus("google-service-account-file-status", error.message, "error");
  }
});

elements.credentialForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  setFormBusy(elements.credentialForm, true);
  const provider = providerLabel(state.managedApplication.provider);
  setFormMessage(elements.credentialMessage, `Saving the protected ${provider} connection…`, "working");
  try {
    await saveProviderCredential(elements.credentialForm);
    elements.credentialForm.reset();
    resetCredentialFileMessages();
    await refreshManagedApplication();
    configureCredentialForm(state.managedApplication);
    setFormMessage(elements.credentialMessage, `${provider} connection saved.`, "success");
  } catch (error) {
    setFormMessage(elements.credentialMessage, errorMessage(error));
  } finally {
    setFormBusy(elements.credentialForm, false);
  }
});

elements.webhookForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  setFormBusy(elements.webhookForm, true);
  setFormMessage(elements.webhookMessage, "Saving the protected notification connection…", "working");
  try {
    await saveWebhook(elements.webhookForm);
    elements.webhookForm.reset();
    await refreshManagedApplication();
    setFormMessage(elements.webhookMessage, "Notification connection saved.", "success");
  } catch (error) {
    setFormMessage(elements.webhookMessage, errorMessage(error));
  } finally {
    setFormBusy(elements.webhookForm, false);
  }
});

elements.applicationKeyForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  setFormBusy(elements.applicationKeyForm, true);
  setFormMessage(elements.applicationKeyMessage, "Creating application key…", "working");
  try {
    const response = await createApplicationKey();
    revealKey(response.key, "application");
  } catch (error) {
    setFormMessage(elements.applicationKeyMessage, errorMessage(error));
  } finally {
    setFormBusy(elements.applicationKeyForm, false);
  }
});

document.querySelector("#open-customer").addEventListener("click", () => {
  if (!state.selectedProject) {
    setGlobalMessage("Create a project first.", "info");
    return;
  }
  elements.customerForm.reset();
  elements.customerMessage.textContent = "";
  elements.customerDialog.showModal();
  elements.customerForm.querySelector("input[name='customer_id']").focus();
});

document.querySelector("#close-customer").addEventListener("click", () => elements.customerDialog.close());
document.querySelector("#cancel-customer").addEventListener("click", () => elements.customerDialog.close());

elements.customerDialog.addEventListener("close", () => {
  elements.customerForm.reset();
  elements.customerMessage.textContent = "";
});

elements.customerForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  setFormBusy(elements.customerForm, true);
  setFormMessage(elements.customerMessage, "Adding customer…", "working");
  try {
    await createCustomer(elements.customerForm);
    elements.customerDialog.close();
    await loadProjects(state.selectedProject);
    setGlobalMessage("Customer added.", "success");
  } catch (error) {
    setFormMessage(elements.customerMessage, errorMessage(error));
  } finally {
    setFormBusy(elements.customerForm, false);
  }
});

document.querySelector("#copy-key").addEventListener("click", async () => {
  try {
    await navigator.clipboard.writeText(elements.revealedKey.value);
    setFormMessage(elements.copyMessage, "Access key copied to the clipboard.", "success");
  } catch (_) {
    elements.revealedKey.focus();
    elements.revealedKey.select();
    setFormMessage(elements.copyMessage, "Automatic copy was blocked. Copy the selected value manually.");
  }
});

document.querySelector("#close-key").addEventListener("click", closeKeyDialog);
document.querySelector("#done-key").addEventListener("click", closeKeyDialog);
elements.keyDialog.addEventListener("close", () => {
  elements.revealedKey.value = "";
  elements.copyMessage.textContent = "";
});

clearLegacyBearerStorage();
syncSetupProvider();
resumeDashboardSession();

async function resumeDashboardSession() {
  try {
    await loadProjects();
    showAuthenticated(true);
  } catch (error) {
    showAuthenticated(false);
    if (error.status !== 401) {
      elements.authMessage.textContent = errorMessage(error);
    }
  }
}
