"use strict";

const huaweiCredentialContentType = "application/vnd.iapstack.huawei-credentials+json";
const appleCredentialContentType = "application/vnd.iapstack.apple-credentials+json";
const googleCredentialContentType = "application/vnd.iapstack.google-play-credentials+json";
const maximumCredentialFileBytes = 1024 * 1024;
const customerPageSize = 25;
const customerSearchMaxBytes = 512;
const customerSearchDelayMs = 350;
const state = {
  projects: [],
  selectedProject: "",
  managedApplication: null,
  overview: null,
  apiKeys: [],
  pendingRevokeKey: null,
  customerSearch: { query: "", nextToken: "", count: 0, sequence: 0, timer: 0 },
  customerDetail: null,
  customerDetailID: "",
  customerDetailSequence: 0,
  pendingRevokeGrant: null,
  grantIdempotencyKey: "",
  subscriptionProject: "",
  subscriptionEnvironment: "",
  subscriptionWindowDays: 30,
  subscriptionRequest: 0,
};

const subscriptionEnvironmentOrder = ["production", "sandbox", "test"];

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
  customerSearchForm: document.querySelector("#customer-search-form"),
  customerSearch: document.querySelector("#customer-search"),
  customersCount: document.querySelector("#customers-count"),
  customerMore: document.querySelector("#customer-more"),
  customerLoadMore: document.querySelector("#customer-load-more"),
  customerDetailDialog: document.querySelector("#customer-detail-dialog"),
  customerDetailBody: document.querySelector("#customer-detail-body"),
  customerDetailMessage: document.querySelector("#customer-detail-message"),
  grantDialog: document.querySelector("#grant-dialog"),
  grantForm: document.querySelector("#grant-form"),
  grantOption: document.querySelector("#grant-option"),
  grantApplication: document.querySelector("#grant-application"),
  grantDuration: document.querySelector("#grant-duration"),
  grantUntilLabel: document.querySelector("#grant-until-label"),
  grantEndsOn: document.querySelector("#grant-ends-on"),
  grantMessage: document.querySelector("#grant-message"),
  revokeGrantDialog: document.querySelector("#revoke-grant-dialog"),
  revokeGrantForm: document.querySelector("#revoke-grant-form"),
  revokeGrantIdentity: document.querySelector("#revoke-grant-identity"),
  revokeGrantMessage: document.querySelector("#revoke-grant-message"),
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
  if (projectID !== state.selectedProject) {
    elements.customerSearch.value = "";
    state.customerSearch.query = "";
  }
  elements.refresh.disabled = true;
  elements.refresh.setAttribute("aria-busy", "true");
  try {
    state.overview = await apiRequest(`/v1/admin/projects/${encodeURIComponent(projectID)}/overview`);
    state.selectedProject = projectID;
    renderProjectNavigation();
    renderOverview();
    elements.updatedAt.textContent = `Updated ${formatTime(new Date().toISOString())}`;
    // Not awaited: the subscription query is slower, and it must not hold the overview or the refresh button.
    loadSubscriptionMetrics(projectID);
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
  setGlobalMessage("Use New catalog to create your first store catalog.", "info");
  setText("metric-apps", "0");
  setText("metric-customers", "0");
  setText("metric-products", "0");
  setText("metric-pending", "0");
  renderAnalytics(emptyAnalytics());
  syncSubscriptionControls([]);
  renderEmptySubscriptions("Subscription health appears after you create a project.");
  for (const id of ["application-grid", "product-list", "queue-list"]) {
    const container = document.querySelector(`#${id}`);
    container.replaceChildren(node("p", "empty-state", "This section will populate after you create a project."));
  }
  renderEmptyTable("transaction-rows", 5, "No verifications yet.");
  renderEmptyTable("customer-rows", 3, "No customers yet.");
  renderEmptyTable("webhook-rows", 3, "No webhook events yet.");
  state.customerSearch.nextToken = "";
  elements.customerMore.hidden = true;
  elements.customersCount.textContent = "Newest first";
}

function renderOverview() {
  const overview = state.overview;
  const pending = sum(overview.queues, "pending");
  const failed = sum(overview.queues, "failed");
  const verifyQueues = overview.queues.filter((queue) => queue.name !== "outbox");
  const outbox = overview.queues.find((queue) => queue.name === "outbox") || {};
  const failedVerifyJobs = sum(verifyQueues, "failed");
  const configured = overview.applications.filter(
    (application) => application.credential_configured && application.webhook_configured,
  ).length;
  const allowed = sum(overview.customers, "allowed_count");
  const delivered = Number(outbox.completed || 0);

  elements.projectTitle.textContent = overview.project.id;
  setText("metric-apps", formatNumber(overview.project.application_count));
  setText("metric-apps-note", `${configured} fully configured`);
  setText("metric-customers", formatNumber(overview.project.customer_count));
  setText("metric-customers-note", countLabel(allowed, "active entitlement"));
  setText("metric-products", formatNumber(overview.project.product_count));
  setText("metric-products-note", countLabel(sum(overview.products, "store_mapping_count"), "store mapping"));
  setText("metric-pending", formatNumber(pending));
  setText("metric-pending-note", failed ? countLabel(failed, "terminal failure") : "No terminal failures");

  renderAnalytics(overview.analytics);

  const unconfigured = overview.applications.length - configured;
  const failedDeliveries = Number(outbox.failed || 0);
  const unresolvedAccess = Number(overview.analytics.unresolved_count || 0);
  setText(
    "signal-store",
    `${configured}/${overview.applications.length} ready${unconfigured ? ` · ${unconfigured} ${unconfigured === 1 ? "needs" : "need"} setup` : ""}`,
  );
  setText(
    "signal-verify",
    `${countLabel(overview.analytics.verified_count, "verified event")}${failedVerifyJobs ? ` · ${countLabel(failedVerifyJobs, "failed job")}` : ""}`,
  );
  setText(
    "signal-access",
    `${formatNumber(overview.analytics.active_entitlement_count)} active${unresolvedAccess ? ` · ${formatNumber(unresolvedAccess)} unresolved` : ""}`,
  );
  setText(
    "signal-deliver",
    `${formatNumber(delivered)} delivered${failedDeliveries ? ` · ${formatNumber(failedDeliveries)} failed` : ""}`,
  );
  setSignalWarning("store", configured !== overview.applications.length);
  setSignalWarning("verify", failedVerifyJobs > 0);
  setSignalWarning("access", overview.analytics.unresolved_count > 0);
  setSignalWarning("deliver", failedDeliveries > 0);

  renderApplications(overview.applications);
  renderProducts(overview.products);
  renderQueues(overview.queues);
  renderTransactions(overview.recent_transactions);
  renderWebhooks(overview.recent_webhook_events);
  loadCustomerPage();
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
    denied_count: 0,
    unresolved_count: 0,
    active_entitlement_count: 0,
    reversed_count: 0,
    daily_activity: dailyActivity,
    revenue: {
      status: "no_applications",
      recognized_minor_units: 0,
      currency: null,
      amounts: [],
      uncovered_providers: [],
      zero_filled_application_ids: [],
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
  const dailyActivity = analytics.daily_activity || [];
  const summary = summarizeActivity(dailyActivity);
  const verifiedTotal = Number(analytics.verified_count || 0);
  const deniedTotal = Number(analytics.denied_count || 0);
  const peak = summary.peak;
  setText("analytics-verified", formatNumber(verifiedTotal));
  setText("analytics-denied-rate", verifiedTotal ? `${((deniedTotal / verifiedTotal) * 100).toFixed(1)}%` : "—");
  setText("analytics-peak", peak ? `${formatNumber(peak)}/day` : "—");
  setText("analytics-reversed", formatNumber(analytics.reversed_count));
  renderRevenue(analytics.revenue || emptyAnalytics().revenue);
  renderSandboxValue(analytics.sandbox_value || emptyAnalytics().sandbox_value);
  renderActivityChart(dailyActivity, summary);
}

function summarizeActivity(activity) {
  const summary = { verified: 0, denied: 0, unresolved: 0, peak: 0 };
  for (const day of activity) {
    const verified = Number(day.verified || 0);
    summary.verified += verified;
    summary.denied += Number(day.denied || 0);
    summary.unresolved += Number(day.unresolved || 0);
    summary.peak = Math.max(summary.peak, verified);
  }
  return summary;
}

function renderRevenue(revenue) {
  const badge = document.querySelector("#revenue-status");
  const number = document.querySelector(".revenue-number");
  badge.className = "revenue-badge";
  number.classList.remove("revenue-lines");
  setText("revenue-production-apps", formatNumber(revenue.production_application_count));
  setText("revenue-test-apps", formatNumber(revenue.test_application_count));

  if (revenue.status === "reports_imported") {
    badge.textContent = "Reports imported";
    badge.classList.add("reports-imported");
    number.classList.add("revenue-lines");
    setText("revenue-eyebrow", "Store-reported proceeds");
    setText("revenue-value", formatImportedProceeds(revenue.amounts));
    setText("revenue-currency", "not converted");
    setText(
      "revenue-note",
      "Imported store proceeds for the periods shown. Currencies are not converted. Tax is not calculated.",
    );
    return;
  }

  if (revenue.status === "store_reports_required") {
    badge.textContent = "Reports required";
    badge.classList.add("reports-required");
    const coveredAmounts = Array.isArray(revenue.amounts) ? revenue.amounts : [];
    if (coveredAmounts.length === 0) {
      setText("revenue-eyebrow", "Not calculated");
      setText("revenue-value", "—");
      setText("revenue-currency", "");
      setText("revenue-note", storeReportsRequiredNote(revenue));
      return;
    }
    number.classList.add("revenue-lines");
    setText("revenue-eyebrow", "Covered store proceeds");
    setText("revenue-value", formatImportedProceeds(coveredAmounts));
    setText("revenue-currency", "not converted");
    setText("revenue-note", coveredProviderProceedsNote(revenue));
    return;
  }

  if (revenue.status === "no_applications") {
    badge.textContent = "No applications";
    badge.classList.add("no-applications");
    setText("revenue-eyebrow", "Authoritative total");
    setText("revenue-value", "0");
    setText("revenue-currency", "real revenue");
    setText("revenue-note", "Add an application to start verification activity.");
    return;
  }

  badge.textContent = "Sandbox only";
  setText("revenue-eyebrow", "Authoritative total");
  setText("revenue-value", formatRevenue(revenue.recognized_minor_units, revenue.currency));
  setText("revenue-currency", revenue.currency || "real revenue");
  setText("revenue-note", "Test purchases never create real store revenue.");
}

function formatImportedProceeds(amounts) {
  if (!Array.isArray(amounts) || amounts.length === 0) return "—";
  return amounts.map((amount) => {
    const provider = proceedsProviderName(amount.provider);
    return `${provider} · ${amount.period_start} – ${amount.period_end} · ${amount.amount_decimal} ${amount.currency}`;
  }).join("\n");
}

function proceedsProviderName(provider) {
  switch (provider) {
    case "apple_app_store":
      return "Apple";
    case "google_play":
      return "Google Play";
    case "huawei_appgallery":
      return "Huawei AppGallery";
    default:
      return "Store";
  }
}

function coveredProviderProceedsNote(revenue) {
  const lines = [
    "Store-reported proceeds for providers with an accepted report. Providers without a report are not included, so this is not a complete project total. Purchase prices are not revenue.",
  ];
  const providers = Array.isArray(revenue.uncovered_providers) ? revenue.uncovered_providers : [];
  for (const provider of providers) {
    lines.push(uncoveredProviderSentence(provider));
  }
  const zeroFilled = zeroFilledApplicationSentence(revenue);
  if (zeroFilled) lines.push(zeroFilled);
  return lines.join(" ");
}

function zeroFilledApplicationSentence(revenue) {
  const ids = Array.isArray(revenue.zero_filled_application_ids) ? revenue.zero_filled_application_ids : [];
  if (!ids.length) return "";
  return `These production applications were absent from a report that included another application of the same provider and are counted as zero: ${ids.join(", ")}. The report does not name a store account, so this can mean no sales or a separate account whose report was not imported.`;
}

function storeReportsRequiredNote(revenue) {
  const lines = ["Official store financial reports are required. Purchase prices are not revenue."];
  const providers = Array.isArray(revenue.uncovered_providers) ? revenue.uncovered_providers : [];
  for (const provider of providers) {
    lines.push(uncoveredProviderSentence(provider));
  }
  return lines.join(" ");
}

function uncoveredProviderSentence(provider) {
  switch (provider) {
    case "google_play":
      return "Google Play has no accepted earnings report.";
    case "apple_app_store":
      return "Apple has no accepted financial report.";
    case "huawei_appgallery":
      return "Huawei AppGallery has no accepted settlement report.";
    default:
      return "A production store has no accepted financial report.";
  }
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

function renderActivityChart(activity, summary = summarizeActivity(activity)) {
  const chart = document.querySelector("#activity-chart");
  chart.replaceChildren();
  const { width, height } = chart.viewBox.baseVal;
  const top = 10;
  const right = 8;
  const bottom = 30;
  const left = 34;
  const plotWidth = width - left - right;
  const plotHeight = height - top - bottom;
  const baseline = top + plotHeight;
  const maximum = niceAxisMaximum(Math.max(1, summary.peak));
  const { verified, denied, unresolved } = summary;
  chart.setAttribute(
    "aria-label",
    `${verified} verified events in ${activity.length || 30} days: ${denied} denied and ${unresolved} unresolved.`,
  );
  document.querySelector("#legend-unresolved").hidden = unresolved === 0;

  // Every axis maximum above 1 is even, so the middle tick is a whole number at exactly half height.
  const ticks = maximum > 1 ? [maximum, maximum / 2, 0] : [maximum, 0];
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

function niceAxisMaximum(value) {
  const magnitude = 10 ** Math.floor(Math.log10(value));
  // Prefer the tightest round ceiling whose half is still a whole number, so the midline tick stays exact.
  for (const step of [1, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10]) {
    const candidate = Math.round(step * magnitude);
    if (candidate >= value && (candidate % 2 === 0 || candidate === 1)) return candidate;
  }
  return 10 * magnitude;
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

function subscriptionEnvironments(applications) {
  const present = new Set(applications.map((application) => application.environment));
  return subscriptionEnvironmentOrder.filter((environment) => present.has(environment));
}

function environmentLabel(environment) {
  return { production: "Production", sandbox: "Sandbox", test: "Test" }[environment] || environment;
}

function syncSubscriptionControls(environments) {
  const select = document.querySelector("#subscription-environment");
  select.replaceChildren();
  for (const environment of environments) {
    const option = node("option", "", environmentLabel(environment));
    option.value = environment;
    select.append(option);
  }
  if (!environments.length) select.append(node("option", "", "No applications"));
  select.disabled = environments.length === 0;
  if (environments.length) select.value = state.subscriptionEnvironment;
  for (const button of document.querySelectorAll(".segmented-control [data-window-days]")) {
    button.setAttribute("aria-pressed", String(Number(button.dataset.windowDays) === state.subscriptionWindowDays));
  }
}

async function loadSubscriptionMetrics(projectID) {
  const environments = subscriptionEnvironments(state.overview?.applications || []);
  const projectChanged = state.subscriptionProject !== projectID;
  if (projectChanged || !environments.includes(state.subscriptionEnvironment)) {
    state.subscriptionProject = projectID;
    state.subscriptionEnvironment = environments[0] || "";
  }
  syncSubscriptionControls(environments);
  if (projectChanged && environments.length) {
    renderEmptySubscriptions("Loading subscription metrics for this project…");
  }
  if (!environments.length) {
    renderEmptySubscriptions("This project has no applications, so it has no subscriptions to count.");
    return;
  }
  const request = ++state.subscriptionRequest;
  const panel = document.querySelector(".subscription-panel");
  panel.setAttribute("aria-busy", "true");
  setText("subscription-message", "");
  const query = new URLSearchParams({
    environment: state.subscriptionEnvironment,
    window_days: String(state.subscriptionWindowDays),
  });
  try {
    const metrics = await apiRequest(
      `/v1/admin/projects/${encodeURIComponent(projectID)}/subscription-metrics?${query.toString()}`,
    );
    if (request !== state.subscriptionRequest) return;
    renderSubscriptionMetrics(metrics);
  } catch (error) {
    if (request !== state.subscriptionRequest) return;
    if (error.status === 401) {
      logout();
      elements.authMessage.textContent = errorMessage(error);
      return;
    }
    renderEmptySubscriptions("Subscription metrics could not be loaded.");
    setText("subscription-message", errorMessage(error));
  } finally {
    if (request === state.subscriptionRequest) panel.removeAttribute("aria-busy");
  }
}

function renderEmptySubscriptions(message) {
  for (const id of ["subscriptions-active", "subscriptions-new", "subscriptions-renewals", "subscriptions-churned"]) {
    setText(id, "0");
  }
  setText("subscriptions-active-note", "—");
  setText("subscriptions-churn-rate", "—");
  setText("subscriptions-renewals-note", "New store transactions in a subscription");
  renderSubscriptionChart([], 0);
  document.querySelector("#subscription-churn-reasons").replaceChildren();
  const trials = document.querySelector("#subscription-trials");
  trials.replaceChildren(node("p", "empty-state", message));
  renderEmptyTable("subscription-rows", 6, message);
}

function renderSubscriptionMetrics(metrics) {
  const trials = Array.isArray(metrics.trials) ? metrics.trials : [];
  const trialsRecorded = trials.some((trial) => trial.availability === "available");
  setText("subscriptions-active", formatNumber(metrics.active_at_end));
  setText(
    "subscriptions-active-note",
    trialsRecorded
      ? `${countLabel(metrics.in_trial_at_end, "subscription")} in a free trial · ${formatNumber(metrics.active_at_start)} at the start`
      : `${formatNumber(metrics.active_at_start)} at the start · trial state not recorded`,
  );
  setText("subscriptions-new", formatNumber(metrics.new_subscriptions));
  setText("subscriptions-renewals", formatNumber(metrics.renewals));
  setText(
    "subscriptions-renewals-note",
    `Same plan · ${countLabel(metrics.plan_changes, "plan change")} and ${formatNumber(metrics.resubscriptions)} resubscribed not included`,
  );
  setText("subscriptions-churned", formatNumber(metrics.churn.churned));
  setText(
    "subscriptions-churn-rate",
    metrics.churn.rate === null || metrics.churn.rate === undefined
      ? "No subscription was active in this window"
      : `${formatPercent(metrics.churn.rate)} churn rate · of ${formatNumber(metrics.active_during_window)} active in the window`,
  );
  setText(
    "subscription-chart-caption",
    `${environmentLabel(metrics.environment)} · UTC · ${metrics.window_days} days · today at ${formatTime(metrics.measured_at)}`,
  );
  renderSubscriptionChart(metrics.daily_active || [], metrics.window_days);
  renderSubscriptionChurn(metrics.churn);
  renderSubscriptionTrials(trials);
  renderSubscriptionRows(metrics.breakdown || [], metrics.breakdown_truncated);
}

function renderSubscriptionChart(daily, windowDays) {
  const chart = document.querySelector("#subscription-chart");
  chart.replaceChildren();
  const width = 760;
  const top = 10;
  const right = 8;
  const bottom = 30;
  const left = 34;
  const plotWidth = width - left - right;
  const plotHeight = 200 - top - bottom;
  const baseline = top + plotHeight;
  const peak = Math.max(0, ...daily.map((day) => Number(day.active || 0)));
  const maximum = niceAxisMaximum(Math.max(1, peak));
  const first = daily.length ? Number(daily[0].active || 0) : 0;
  const last = daily.length ? Number(daily[daily.length - 1].active || 0) : 0;
  chart.setAttribute(
    "aria-label",
    daily.length
      ? `Active subscriptions over ${windowDays} days: ${first} at the end of the first day, ${last} now, peak ${peak}.`
      : "No subscription activity in this window",
  );

  for (const tick of [...new Set([maximum, Math.ceil(maximum / 2), 0])]) {
    const y = baseline - (tick / maximum) * plotHeight;
    chart.append(
      svgNode("line", { class: "chart-grid-line", x1: left, x2: width - right, y1: y, y2: y }),
      svgNode("text", { class: "chart-axis-label", x: left - 7, y: y + 3, "text-anchor": "end" }, tick),
    );
  }
  if (!peak) {
    chart.append(svgNode(
      "text",
      { class: "chart-empty-label", x: left + plotWidth / 2, y: top + plotHeight / 2, "text-anchor": "middle" },
      "No active subscriptions in this window",
    ));
  }

  const slotWidth = daily.length ? plotWidth / daily.length : plotWidth;
  const barWidth = Math.max(2, Math.min(14, slotWidth * 0.62));
  const labelEvery = daily.length > 45 ? 14 : 7;
  daily.forEach((day, index) => {
    const x = left + index * slotWidth + (slotWidth - barWidth) / 2;
    const value = Number(day.active || 0);
    if (value) {
      const height = (value / maximum) * plotHeight;
      const bar = svgNode("rect", {
        class: "chart-bar chart-bar-allowed", x, y: baseline - height, width: barWidth, height, rx: 2,
      });
      bar.append(svgNode("title", {}, `${formatChartDate(day.date)}: ${formatNumber(value)} active`));
      chart.append(bar);
    }
    const finalDay = index === daily.length - 1;
    const clearOfFinalLabel = index <= daily.length - 1 - Math.ceil(labelEvery / 2);
    const periodicTick = daily.length <= 10 || (index % labelEvery === 0 && clearOfFinalLabel);
    if (index === 0 || finalDay || periodicTick) {
      chart.append(svgNode(
        "text",
        { class: "chart-axis-label", x: x + barWidth / 2, y: baseline + 19, "text-anchor": "middle" },
        formatChartDate(day.date),
      ));
    }
  });
}

function renderSubscriptionChurn(churn) {
  const list = document.querySelector("#subscription-churn-reasons");
  list.replaceChildren();
  const rows = [
    ["Renewal turned off", churn.voluntary],
    ["Payment problem", churn.billing],
    ["Refunded or revoked", churn.reversed],
    ["Reason not recorded", churn.unclassified],
  ];
  for (const [label, value] of rows) {
    list.append(subscriptionListRow(label, value));
  }
  const excluded = node("div", "subscription-list-note");
  excluded.append(node("dt", "", "Not counted as churn"));
  excluded.append(node(
    "dd",
    "",
    `${formatNumber(churn.replaced)} replaced · ${formatNumber(churn.paused)} paused · ${formatNumber(churn.awaiting_evidence)} awaiting store evidence`,
  ));
  list.append(excluded);
}

function subscriptionListRow(label, value) {
  const row = node("div");
  row.append(node("dt", "", label), node("dd", "", formatNumber(value)));
  return row;
}

function renderSubscriptionTrials(trials) {
  const container = document.querySelector("#subscription-trials");
  container.replaceChildren();
  if (!trials.length) {
    container.append(node("p", "empty-state", "No applications in this environment."));
    return;
  }
  for (const trial of trials) {
    const item = node("article", "subscription-trial");
    item.append(node("strong", "", providerLabel(trial.provider)));
    if (trial.availability !== "available") {
      item.classList.add("not-available");
      item.append(
        node("span", "", `Not available for ${providerLabel(trial.provider)}`),
        node("small", "", "Its stored purchase evidence does not mark a free trial, so no trial is counted."),
      );
      container.append(item);
      continue;
    }
    const decided = Number(trial.converted || 0) + Number(trial.not_converted || 0);
    const conversion = trial.conversion_rate === null || trial.conversion_rate === undefined
      ? "no ended trial to convert yet"
      : `${formatNumber(trial.converted)} of ${formatNumber(decided)} ended trials converted (${formatPercent(trial.conversion_rate)})`;
    const outcome = Number(trial.outcome_not_recorded || 0)
      ? ` · ${countLabel(trial.outcome_not_recorded, "outcome")} not recorded`
      : "";
    item.append(
      node("span", "", `${countLabel(trial.started, "trial")} started · ${conversion}${outcome}`),
      node(
        "small",
        "",
        `${formatNumber(trial.with_offer_phase)} of ${countLabel(trial.active_during_window, "subscription")} carry a recorded offer phase.`,
      ),
    );
    container.append(item);
  }
}

function renderSubscriptionRows(breakdown, truncated) {
  const body = document.querySelector("#subscription-rows");
  body.replaceChildren();
  if (!breakdown.length) {
    renderEmptyTable("subscription-rows", 6, "No subscription was active in this window.");
    return;
  }
  for (const row of breakdown) {
    const tableRow = document.createElement("tr");
    tableRow.append(
      compoundCell(row.product_id, row.application_id),
      textCell(providerLabel(row.provider)),
      textCell(formatNumber(row.active_at_end)),
      textCell(formatNumber(row.new_subscriptions)),
      textCell(formatNumber(row.renewals)),
      textCell(formatNumber(row.churned)),
    );
    body.append(tableRow);
  }
  if (truncated) {
    const note = document.createElement("tr");
    const cell = node("td", "empty-cell", "Showing the first 200 products. Totals above include every product.");
    cell.colSpan = 6;
    note.append(cell);
    body.append(note);
  }
}

function formatPercent(ratio) {
  return `${(Number(ratio) * 100).toFixed(1)}%`;
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

// formatQueueCount keeps large counts inside the fixed queue columns ("123,456" becomes "123.5K").
function formatQueueCount(value) {
  const number = Number(value || 0);
  if (Math.abs(number) < 100000) return formatNumber(number);
  return new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 }).format(number);
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
  const rtdnValues = [
    data.google_rtdn_subscription.trim(),
    data.google_rtdn_push_email.trim(),
    data.google_rtdn_audience.trim(),
  ];
  const configuredRTDNValues = rtdnValues.filter(Boolean).length;
  if (configuredRTDNValues > 0 && configuredRTDNValues < rtdnValues.length) {
    throw new Error("Complete all three RTDN fields or leave all of them empty.");
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

// loadAPIKeys refreshes secret-free lifecycle metadata, following pages so older keys stay revocable.
async function loadAPIKeys(successMessage = "") {
  elements.apiKeyMessage.textContent = "Loading access keys…";
  elements.apiKeyMessage.className = "form-message working";
  elements.createAdminKey.disabled = true;
  try {
    const keys = [];
    const seenTokens = new Set();
    let pageToken = "";
    const maxPages = 50;
    for (let page = 0; page < maxPages; page += 1) {
      const path = pageToken
        ? `/v1/admin/api-keys?page_token=${encodeURIComponent(pageToken)}`
        : "/v1/admin/api-keys";
      const response = await apiRequest(path);
      keys.push(...(response.api_keys || []));
      const nextToken = response.next_page_token || "";
      if (!nextToken || seenTokens.has(nextToken)) {
        pageToken = "";
        break;
      }
      seenTokens.add(nextToken);
      pageToken = nextToken;
    }
    state.apiKeys = keys;
    renderAPIKeys();
    if (pageToken) {
      setFormMessage(elements.apiKeyMessage, "Older keys remain beyond the loaded pages.", "error");
    } else {
      setFormMessage(elements.apiKeyMessage, successMessage, successMessage ? "success" : "error");
    }
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
      node("span", "mapping-count", countLabel(product.store_mapping_count, "provider mapping")),
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
      queueNumber("Failed", queue.failed, Number(queue.failed) > 0 ? "failed" : ""),
    );
    container.append(item);
  }
}

function queueNumber(label, value, extra = "") {
  const item = node("span", `queue-number ${extra}`.trim());
  const count = node("b", "", formatQueueCount(value));
  count.title = formatNumber(value);
  item.append(count, document.createTextNode(label));
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

// customerSearchQuery normalizes the search field the same way the server trims it.
function customerSearchQuery() {
  return elements.customerSearch.value.trim();
}

// loadCustomerPage lists newest customers, or searches them, and appends a later page when asked.
async function loadCustomerPage(append = false) {
  if (!state.selectedProject) return;
  const query = append ? state.customerSearch.query : customerSearchQuery();
  if (new TextEncoder().encode(query).length > customerSearchMaxBytes) {
    renderCustomerMessage("Search text is too long.", "Use at most 512 bytes.");
    return;
  }
  const sequence = state.customerSearch.sequence + 1;
  state.customerSearch.sequence = sequence;
  const params = new URLSearchParams({ limit: String(customerPageSize) });
  if (query) params.set("query", query);
  if (append && state.customerSearch.nextToken) params.set("page_token", state.customerSearch.nextToken);
  const project = encodeURIComponent(state.selectedProject);
  elements.customerLoadMore.disabled = true;
  elements.customersCount.textContent = query ? "Searching…" : elements.customersCount.textContent;
  try {
    const page = await apiRequest(`/v1/admin/projects/${project}/customers?${params}`);
    if (sequence !== state.customerSearch.sequence) return;
    state.customerSearch.query = query;
    state.customerSearch.nextToken = page.next_page_token || "";
    state.customerSearch.count = (append ? state.customerSearch.count : 0) + (page.customers || []).length;
    renderCustomers(page.customers || [], { append, query });
  } catch (error) {
    if (sequence !== state.customerSearch.sequence) return;
    if (error.status === 401) {
      logout();
      elements.authMessage.textContent = errorMessage(error);
      return;
    }
    state.customerSearch.nextToken = "";
    renderCustomerMessage("Customers could not be loaded.", errorMessage(error));
  } finally {
    if (sequence === state.customerSearch.sequence) elements.customerLoadMore.disabled = false;
  }
}

// renderCustomers draws one page of customer rows, each opening the customer detail.
function renderCustomers(customers, { append = false, query = "" } = {}) {
  const body = document.querySelector("#customer-rows");
  if (!append) body.replaceChildren();
  const more = Boolean(state.customerSearch.nextToken);
  elements.customerMore.hidden = !more;
  if (!append && !customers.length) {
    elements.customersCount.textContent = query ? "No matches" : "Newest first";
    if (query) {
      renderCustomerMessage(
        `No customer matches “${query}”.`,
        "Search covers external IDs, customer IDs, aliases, and store transaction IDs. Prefixes match external and customer IDs only.",
      );
    } else {
      renderEmptyTable("customer-rows", 3, "No customers yet.");
    }
    return;
  }
  const count = state.customerSearch.count;
  elements.customersCount.textContent = query
    ? `${formatNumber(count)}${more ? "+" : ""} ${count === 1 && !more ? "match" : "matches"}`
    : `Newest first · ${formatNumber(count)}${more ? "+" : ""} shown`;
  for (const customer of customers) {
    const row = document.createElement("tr");
    const identity = document.createElement("td");
    const open = node("button", "row-link");
    open.type = "button";
    open.setAttribute("aria-label", `Open customer ${customer.external_id}`);
    open.append(node("strong", "", customer.external_id), node("code", "", customerIdentityDetail(customer)));
    open.addEventListener("click", () => openCustomerDetail(customer.id));
    identity.append(open);
    if (customer.match) identity.append(node("span", "match-badge", customerMatchLabel(customer.match)));
    row.append(
      identity,
      textCell(`${customer.allowed_count}/${customer.entitlement_count} allowed`),
      textCell(customer.last_observed_at ? formatTime(customer.last_observed_at) : "—"),
    );
    body.append(row);
  }
}

// renderCustomerMessage replaces the customer rows with a two-line empty or error state.
function renderCustomerMessage(title, detail) {
  const body = document.querySelector("#customer-rows");
  body.replaceChildren();
  const row = document.createElement("tr");
  const cell = node("td", "empty-cell");
  cell.colSpan = 3;
  cell.append(node("strong", "", title), node("span", "", detail));
  row.append(cell);
  body.append(row);
  elements.customerMore.hidden = true;
}

function customerIdentityDetail(customer) {
  const details = [customer.id];
  if (customer.merged_into_customer_id) {
    details.push(`merged into ${customer.merged_into_customer_id}`);
  }
  if (customer.alias_count) {
    details.push(`${formatNumber(customer.alias_count)} ${Number(customer.alias_count) === 1 ? "alias" : "aliases"}`);
  }
  return details.join(" · ");
}

// customerMatchLabel names why a customer matched the search text.
function customerMatchLabel(match) {
  return {
    external_id: "External ID",
    customer_id: "Customer ID",
    alias: "Alias",
    transaction_id: "Transaction ID",
    original_transaction_id: "Original transaction ID",
    external_id_prefix: "External ID prefix",
    customer_id_prefix: "Customer ID prefix",
  }[match] || match.replaceAll("_", " ");
}

// openCustomerDetail shows the customer dialog at once and loads its authoritative detail.
async function openCustomerDetail(customerID) {
  state.customerDetailID = customerID;
  state.customerDetail = null;
  setText("customer-detail-title", "Customer");
  setText("customer-detail-meta", customerID);
  elements.customerDetailBody.replaceChildren(node("p", "empty-state", "Loading customer…"));
  setFormMessage(elements.customerDetailMessage, "");
  if (!elements.customerDetailDialog.open) elements.customerDetailDialog.showModal();
  await loadCustomerDetail(customerID);
}

// loadCustomerDetail refreshes the open customer dialog from the admin API.
async function loadCustomerDetail(customerID, successMessage = "") {
  const sequence = state.customerDetailSequence + 1;
  state.customerDetailSequence = sequence;
  const project = encodeURIComponent(state.selectedProject);
  try {
    const detail = await apiRequest(`/v1/admin/projects/${project}/customers/${encodeURIComponent(customerID)}`);
    if (sequence !== state.customerDetailSequence || !elements.customerDetailDialog.open) return;
    state.customerDetail = detail;
    renderCustomerDetail(detail);
    setFormMessage(elements.customerDetailMessage, successMessage, successMessage ? "success" : "error");
  } catch (error) {
    if (sequence !== state.customerDetailSequence) return;
    if (error.status === 401) {
      closeCustomerDetail();
      logout();
      elements.authMessage.textContent = errorMessage(error);
      return;
    }
    elements.customerDetailBody.replaceChildren();
    setFormMessage(
      elements.customerDetailMessage,
      error.status === 404 ? "This customer no longer exists in the project." : errorMessage(error),
    );
  }
}

function closeCustomerDetail() {
  state.customerDetail = null;
  state.customerDetailID = "";
  elements.customerDetailBody.replaceChildren();
  setFormMessage(elements.customerDetailMessage, "");
  if (elements.customerDetailDialog.open) elements.customerDetailDialog.close();
}

// renderCustomerDetail draws identity, access sources, manual grants, and the timeline.
function renderCustomerDetail(detail) {
  setText("customer-detail-title", detail.customer.external_id);
  setText("customer-detail-meta", detail.customer.id);
  elements.customerDetailBody.replaceChildren(
    customerIdentitySection(detail),
    customerAccessSection(detail),
    customerGrantsSection(detail),
    customerTimelineSection(detail),
  );
}

// detailSection creates one titled section with optional heading actions.
function detailSection(title, count = "", ...actions) {
  const section = node("section", "detail-section");
  const heading = node("div", "detail-heading");
  const titleRow = node("div");
  titleRow.append(node("h3", "", title));
  if (count) titleRow.append(node("span", "panel-count", count));
  heading.append(titleRow, ...actions);
  section.append(heading);
  return section;
}

function customerIdentitySection(detail) {
  const section = detailSection("Identity");
  const vitals = node("dl", "detail-vitals");
  const merged = detail.customer.merged_into_customer_id;
  vitals.append(
    detailVital("External ID", detail.customer.external_id, true),
    detailVital("Customer ID", detail.customer.id, true),
    detailVital("Created", formatTime(detail.customer.created_at)),
    detailVital("Identity", merged ? "Merged" : "Canonical"),
  );
  section.append(vitals);
  if (merged) {
    const note = node("div", "detail-note");
    const open = node("button", "text-button", "Open canonical customer");
    open.type = "button";
    open.addEventListener("click", () => openCustomerDetail(merged));
    note.append(
      node("span", "", "Merged into "),
      node("code", "", merged),
      node("span", "", ". Access, grants, and the timeline below belong to the canonical customer."),
      open,
    );
    section.append(note);
  }
  const list = node("ul", "detail-list");
  for (const alias of detail.aliases) {
    const item = node("li");
    item.append(
      node("span", "source-badge", "Alias"),
      node("code", "", alias.external_id),
      node("small", "", `Added ${formatTime(alias.created_at)} by ${alias.actor_role} key ${alias.actor_key_id}`),
    );
    list.append(item);
  }
  for (const merge of detail.merges) {
    const item = node("li");
    item.append(
      node("span", "source-badge", "Merge"),
      node("code", "", `${merge.source_external_id} → ${merge.canonical_customer_id}`),
      node("small", "", `Merged ${formatTime(merge.created_at)} by admin key ${merge.actor_key_id}`),
    );
    list.append(item);
  }
  if (!detail.aliases.length && !detail.merges.length) {
    list.append(node("li", "detail-empty", "No aliases or merges."));
  }
  section.append(list);
  return section;
}

function detailVital(label, value, identifier = false) {
  const item = node("div");
  item.append(node("dt", "", label), node("dd", identifier ? "identifier" : "", value));
  return item;
}

function customerAccessSection(detail) {
  const grant = node("button", "secondary-button compact-button", "Grant access");
  grant.type = "button";
  grant.disabled = !detail.grant_options.some((option) => option.application_ids.length);
  if (grant.disabled) grant.title = "Map a product that grants an entitlement to an application first.";
  grant.addEventListener("click", openGrantDialog);
  const section = detailSection("Access", countLabel(detail.entitlements.length, "entitlement"), grant);
  if (!detail.entitlements.length) {
    section.append(node("p", "detail-empty", "No entitlement projections yet."));
    return section;
  }
  const wrap = node("div", "detail-table");
  const table = document.createElement("table");
  const head = document.createElement("thead");
  const headRow = document.createElement("tr");
  for (const label of ["Entitlement", "Access", "Source", "Ends"]) headRow.append(node("th", "", label));
  head.append(headRow);
  const body = document.createElement("tbody");
  for (const entitlement of detail.entitlements) {
    const row = document.createElement("tr");
    const source = document.createElement("td");
    source.append(node("span", `source-badge ${entitlement.source}`, sourceLabel(entitlement.source)), node("code", "", entitlement.source_id));
    row.append(
      compoundCell(entitlement.key, entitlement.entitlement_id),
      cellBadge(entitlement.access, entitlement.reason),
      source,
      textCell(entitlement.effective_ends_at ? formatTime(entitlement.effective_ends_at) : "No end"),
    );
    body.append(row);
  }
  table.append(head, body);
  wrap.append(table);
  section.append(wrap);
  return section;
}

// sourceLabel names an access source so store evidence is never confused with other sources.
function sourceLabel(source) {
  return {
    store_observation: "Store verification",
    import: "Import",
    manual: "Manual grant",
  }[source] || String(source || "").replaceAll("_", " ");
}

function customerGrantsSection(detail) {
  const section = detailSection("Manual grants", countLabel(detail.manual_grants.length, "grant"));
  const list = node("ul", "detail-list grant-list");
  for (const grant of detail.manual_grants) {
    const item = node("li", `grant-item ${grant.status}`);
    const summary = node("div", "grant-summary");
    const title = node("div");
    title.append(node("strong", "", grant.entitlement_key), node("span", `status-badge ${statusClass(grant.status)}`, grant.status));
    summary.append(
      title,
      node("p", "grant-reason", grant.reason),
      node("small", "", `${formatTime(grant.effective_starts_at)} to ${formatTime(grant.effective_ends_at)} · ${grant.application_id} · by key ${grant.actor_key_id}`),
    );
    if (grant.revoked_at) {
      summary.append(node("small", "", `Revoked ${formatTime(grant.revoked_at)} by key ${grant.revoked_by_key_id}: ${grant.revocation_reason}`));
    }
    item.append(summary);
    if (grant.status === "active") {
      const revoke = node("button", "text-button revoke-grant-button", "Revoke");
      revoke.type = "button";
      revoke.setAttribute("aria-label", `Revoke manual grant of ${grant.entitlement_key}`);
      revoke.addEventListener("click", () => openRevokeGrant(grant));
      item.append(revoke);
    }
    list.append(item);
  }
  if (!detail.manual_grants.length) {
    list.append(node("li", "detail-empty", "No manual grants. Store purchases and imports appear under Access."));
  }
  section.append(list);
  return section;
}

function customerTimelineSection(detail) {
  const section = detailSection(
    "Timeline",
    detail.timeline_truncated ? `Latest ${formatNumber(detail.timeline.length)} · older records exist` : countLabel(detail.timeline.length, "event"),
  );
  const list = node("ol", "timeline");
  for (const event of detail.timeline) list.append(timelineEntry(event));
  if (!detail.timeline.length) list.append(node("li", "detail-empty", "No stored records yet."));
  section.append(list);
  return section;
}

// timelineEntry renders one stored record with text labels for every state.
function timelineEntry(event) {
  const item = node("li", `timeline-item ${event.kind.replaceAll("_", "-")}`);
  const time = node("time", "", formatTime(event.occurred_at));
  time.dateTime = event.occurred_at;
  const content = node("div", "timeline-content");
  const title = node("div", "timeline-title");
  const lines = [];
  const identifiers = [];
  let badge = "";
  let badgeNote = "";
  switch (event.kind) {
    case "customer_created":
      title.append(node("strong", "", "Customer created"));
      identifiers.push(event.external_id, event.customer_id);
      break;
    case "alias_added":
      title.append(node("strong", "", "Alias added"));
      identifiers.push(event.external_id);
      lines.push(`By ${event.actor_role || "admin"} key ${event.actor_key_id}`);
      break;
    case "customer_merged":
      title.append(node("strong", "", "Customer merged"));
      lines.push(`${event.external_id} merged into this customer by admin key ${event.actor_key_id}`);
      identifiers.push(event.related_customer_id, event.source_id);
      break;
    case "purchase_observed":
      title.append(node("strong", "", "Store observation"));
      badge = event.lifecycle_state;
      lines.push(
        event.previous_lifecycle_state && event.previous_lifecycle_state !== event.lifecycle_state
          ? `Lifecycle ${event.previous_lifecycle_state.replaceAll("_", " ")} to ${event.lifecycle_state.replaceAll("_", " ")}`
          : `Lifecycle ${event.lifecycle_state.replaceAll("_", " ")}`,
        `Access ${event.access} · ${String(event.access_reason || "").replaceAll("_", " ")}${event.effective_ends_at ? ` · ends ${formatTime(event.effective_ends_at)}` : ""}`,
      );
      identifiers.push(event.provider_product_id, event.application_id, event.source_id);
      break;
    case "import_grant_recorded":
      title.append(node("strong", "", "Import grant"));
      badge = event.access;
      badgeNote = event.access_reason;
      lines.push(`${event.entitlement_key} from ${event.origin_store}. Imported history, not store verification.`);
      identifiers.push(event.source_id);
      break;
    case "manual_grant_created":
      title.append(node("strong", "", "Manual grant"));
      lines.push(`${event.entitlement_key} until ${formatTime(event.effective_ends_at)} · by key ${event.actor_key_id}`, `Reason: ${event.reason}`);
      identifiers.push(event.source_id);
      break;
    case "manual_grant_revoked":
      title.append(node("strong", "", "Manual grant revoked"));
      badge = "revoked";
      lines.push(`${event.entitlement_key} · by key ${event.actor_key_id}`, `Reason: ${event.reason}`);
      identifiers.push(event.source_id);
      break;
    case "entitlement_changed":
      title.append(node("strong", "", "Entitlement changed"));
      badge = event.access;
      badgeNote = event.access_reason;
      lines.push(`${event.entitlement_key}${event.version ? ` · version ${event.version}` : ""} · source ${sourceLabel(event.source).toLowerCase()}`);
      lines.push(event.status ? `Webhook ${event.status}` : "No webhook record");
      identifiers.push(event.source_id);
      break;
    case "webhook_delivered":
    case "webhook_failed":
      title.append(node("strong", "", event.kind === "webhook_delivered" ? "Webhook delivered" : "Webhook failed"));
      badge = event.kind === "webhook_delivered" ? "delivered" : "failed";
      badgeNote = event.error_code || "";
      lines.push(`${event.entitlement_key}${event.version ? ` · version ${event.version}` : ""} to ${event.application_id}`);
      identifiers.push(event.source_id);
      break;
    case "reconciliation":
      title.append(node("strong", "", "Store re-check"));
      badge = event.status;
      badgeNote = event.error_code || "";
      lines.push(`Reconciliation job for ${event.application_id}`);
      identifiers.push(event.source_id);
      break;
    default:
      title.append(node("strong", "", event.kind.replaceAll("_", " ")));
  }
  if (badge) {
    title.append(node("span", `status-badge ${statusClass(badge)}`, String(badge).replaceAll("_", " ")));
    if (badgeNote) title.append(node("small", "", String(badgeNote).replaceAll("_", " ")));
  }
  content.append(title);
  for (const line of lines) content.append(node("p", "", line));
  const ids = identifiers.filter(Boolean);
  if (ids.length) content.append(node("code", "", ids.join(" · ")));
  item.append(time, content);
  return item;
}

// openGrantDialog prepares a manual grant for the canonical customer of the open detail.
function openGrantDialog() {
  const detail = state.customerDetail;
  if (!detail) return;
  elements.grantForm.reset();
  setFormMessage(elements.grantMessage, "");
  state.grantIdempotencyKey = newIdempotencyKey();
  elements.grantOption.replaceChildren();
  detail.grant_options.forEach((option, index) => {
    if (!option.application_ids.length) return;
    const label = `${option.entitlement_key} · via ${option.product_id} (${productKindLabel(option.product_kind)})`;
    const item = node("option", "", label);
    item.value = String(index);
    elements.grantOption.append(item);
  });
  syncGrantApplications();
  syncGrantDuration();
  const tomorrow = new Date(Date.now() + 86400000);
  elements.grantEndsOn.min = localDateValue(tomorrow);
  elements.grantDialog.showModal();
  elements.grantOption.focus();
}

// syncGrantApplications lists only the applications that map the chosen product; the server refuses others.
function syncGrantApplications() {
  const option = state.customerDetail?.grant_options[Number(elements.grantOption.value || 0)];
  const providers = new Map((state.overview?.applications || []).map((application) => [application.id, application.provider]));
  elements.grantApplication.replaceChildren();
  for (const applicationID of option?.application_ids || []) {
    const provider = providers.get(applicationID);
    const item = node("option", "", provider ? `${applicationID} · ${providerLabel(provider)}` : applicationID);
    item.value = applicationID;
    elements.grantApplication.append(item);
  }
}

function syncGrantDuration() {
  const custom = elements.grantDuration.value === "date";
  elements.grantUntilLabel.hidden = !custom;
  elements.grantEndsOn.required = custom;
  elements.grantEndsOn.disabled = !custom;
}

function localDateValue(date) {
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  return `${date.getFullYear()}-${month}-${day}`;
}

function newIdempotencyKey() {
  if (window.crypto?.randomUUID) return `dashboard-${window.crypto.randomUUID()}`;
  const bytes = new Uint8Array(16);
  window.crypto.getRandomValues(bytes);
  return `dashboard-${Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("")}`;
}

// submitManualGrant sends one audited grant; the stored idempotency key makes a resubmit safe.
async function submitManualGrant() {
  const detail = state.customerDetail;
  const option = detail?.grant_options[Number(elements.grantOption.value)];
  if (!detail || !option) return;
  const data = Object.fromEntries(new FormData(elements.grantForm));
  const body = {
    idempotency_key: state.grantIdempotencyKey,
    entitlement_id: option.entitlement_id,
    product_id: option.product_id,
    application_id: data.application_id,
    reason: String(data.reason || "").trim(),
  };
  if (elements.grantDuration.value === "date") {
    const endsAt = new Date(`${data.ends_on}T23:59:59`);
    if (Number.isNaN(endsAt.valueOf()) || endsAt <= new Date()) {
      throw Object.assign(new Error("Choose a date in the future."), { status: 400 });
    }
    body.ends_at = endsAt.toISOString();
  } else {
    body.duration_days = Number(elements.grantDuration.value);
  }
  const project = encodeURIComponent(state.selectedProject);
  const customer = encodeURIComponent(detail.canonical_customer_id);
  return apiRequest(`/v1/admin/projects/${project}/customers/${customer}/manual-grants`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

function openRevokeGrant(grant) {
  state.pendingRevokeGrant = grant;
  elements.revokeGrantForm.reset();
  setFormMessage(elements.revokeGrantMessage, "");
  elements.revokeGrantIdentity.textContent = `${grant.entitlement_key} · ${grant.id}`;
  elements.revokeGrantDialog.showModal();
  elements.revokeGrantForm.querySelector("input[name='reason']").focus();
}

function closeRevokeGrant() {
  state.pendingRevokeGrant = null;
  elements.revokeGrantIdentity.textContent = "";
  if (elements.revokeGrantDialog.open) elements.revokeGrantDialog.close();
}

// manualGrantOutcome explains what a grant or revocation changed without implying store evidence.
function manualGrantOutcome(result, action) {
  if (result.replay) return `This ${action} was already recorded. Nothing changed.`;
  if (!result.projections_changed) {
    return action === "grant"
      ? "Grant recorded. Another source already gives equal or longer access, so current access did not change."
      : "Revocation recorded. Current access did not change.";
  }
  return action === "grant"
    ? `Access granted. ${countLabel(result.webhooks_enqueued, "webhook")} queued.`
    : `Grant revoked. ${countLabel(result.webhooks_enqueued, "webhook")} queued.`;
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
  return `${formatNumber(value)} ${singular}${Number(value) === 1 ? "" : "s"}`;
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
  state.customerDetail = null;
  state.customerDetailID = "";
  state.pendingRevokeGrant = null;
  window.clearTimeout(state.customerSearch.timer);
  state.customerSearch = { query: "", nextToken: "", count: 0, sequence: state.customerSearch.sequence + 1, timer: 0 };
  elements.customerSearch.value = "";
  elements.credentialForm.reset();
  elements.webhookForm.reset();
  elements.revealedKey.value = "";
  for (const dialog of [
    elements.revokeGrantDialog, elements.grantDialog, elements.customerDetailDialog,
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

function openSetupDialog() {
  elements.setupMessage.textContent = "";
  syncSetupProvider();
  elements.setupDialog.showModal();
  elements.setupForm.querySelector("input[name='project_id']").focus();
}

document.querySelector("#open-setup").addEventListener("click", openSetupDialog);
document.querySelector("#open-setup-primary").addEventListener("click", openSetupDialog);

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

elements.customerSearchForm.addEventListener("submit", (event) => {
  event.preventDefault();
  window.clearTimeout(state.customerSearch.timer);
  loadCustomerPage();
});

elements.customerSearch.addEventListener("input", () => {
  window.clearTimeout(state.customerSearch.timer);
  state.customerSearch.timer = window.setTimeout(() => {
    if (customerSearchQuery() !== state.customerSearch.query) loadCustomerPage();
  }, customerSearchDelayMs);
});

elements.customerLoadMore.addEventListener("click", () => loadCustomerPage(true));

document.querySelector("#close-customer-detail").addEventListener("click", closeCustomerDetail);
elements.customerDetailDialog.addEventListener("close", () => {
  state.customerDetail = null;
  state.customerDetailID = "";
  elements.customerDetailBody.replaceChildren();
});

elements.grantOption.addEventListener("change", syncGrantApplications);
elements.grantDuration.addEventListener("change", syncGrantDuration);
// A changed request is a new grant, so it must not reuse the replay key of the request it replaces.
elements.grantForm.addEventListener("input", () => {
  if (elements.grantDialog.open) state.grantIdempotencyKey = newIdempotencyKey();
});
document.querySelector("#close-grant").addEventListener("click", () => elements.grantDialog.close());
document.querySelector("#cancel-grant").addEventListener("click", () => elements.grantDialog.close());
elements.grantDialog.addEventListener("close", () => {
  elements.grantForm.reset();
  setFormMessage(elements.grantMessage, "");
  state.grantIdempotencyKey = "";
});

elements.grantForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  setFormBusy(elements.grantForm, true);
  setFormMessage(elements.grantMessage, "Recording grant…", "working");
  try {
    const result = await submitManualGrant();
    const customerID = state.customerDetailID;
    elements.grantDialog.close();
    await loadCustomerDetail(customerID, manualGrantOutcome(result, "grant"));
    loadOverview(state.selectedProject);
  } catch (error) {
    setFormMessage(
      elements.grantMessage,
      error.status === 403 ? "Manual grants need a stored admin key. Sign in with one instead of the bootstrap key." : errorMessage(error),
    );
  } finally {
    setFormBusy(elements.grantForm, false);
  }
});

document.querySelector("#close-revoke-grant").addEventListener("click", closeRevokeGrant);
document.querySelector("#cancel-revoke-grant").addEventListener("click", closeRevokeGrant);
elements.revokeGrantDialog.addEventListener("close", () => {
  state.pendingRevokeGrant = null;
  elements.revokeGrantIdentity.textContent = "";
  setFormMessage(elements.revokeGrantMessage, "");
});

elements.revokeGrantForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  const grant = state.pendingRevokeGrant;
  const detail = state.customerDetail;
  if (!grant || !detail) return;
  setFormBusy(elements.revokeGrantForm, true);
  setFormMessage(elements.revokeGrantMessage, "Revoking grant…", "working");
  try {
    const reason = String(new FormData(elements.revokeGrantForm).get("reason") || "").trim();
    const project = encodeURIComponent(state.selectedProject);
    const customer = encodeURIComponent(detail.canonical_customer_id);
    const result = await apiRequest(
      `/v1/admin/projects/${project}/customers/${customer}/manual-grants/${encodeURIComponent(grant.id)}:revoke`,
      { method: "POST", body: JSON.stringify({ reason }) },
    );
    const customerID = state.customerDetailID;
    closeRevokeGrant();
    await loadCustomerDetail(customerID, manualGrantOutcome(result, "revocation"));
    loadOverview(state.selectedProject);
  } catch (error) {
    setFormMessage(
      elements.revokeGrantMessage,
      error.status === 403 ? "Revoking needs a stored admin key. Sign in with one instead of the bootstrap key." : errorMessage(error),
    );
  } finally {
    setFormBusy(elements.revokeGrantForm, false);
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

document.querySelector("#subscription-environment").addEventListener("change", (event) => {
  state.subscriptionEnvironment = event.currentTarget.value;
  if (state.selectedProject) loadSubscriptionMetrics(state.selectedProject);
});

for (const button of document.querySelectorAll(".segmented-control [data-window-days]")) {
  button.addEventListener("click", () => {
    state.subscriptionWindowDays = Number(button.dataset.windowDays);
    if (state.selectedProject) {
      loadSubscriptionMetrics(state.selectedProject);
    } else {
      syncSubscriptionControls([]);
    }
  });
}

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
