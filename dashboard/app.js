"use strict";

const sessionKey = "iapstack.dashboard.admin";
const huaweiCredentialContentType = "application/vnd.iapstack.huawei-credentials+json";
const state = {
  adminKey: "",
  projects: [],
  selectedProject: "",
  managedApplication: null,
  overview: null,
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
  logout: document.querySelector("#logout"),
  setupDialog: document.querySelector("#setup-dialog"),
  setupForm: document.querySelector("#setup-form"),
  setupMessage: document.querySelector("#setup-message"),
  applicationDialog: document.querySelector("#application-dialog"),
  credentialForm: document.querySelector("#credential-form"),
  credentialMessage: document.querySelector("#credential-message"),
  webhookForm: document.querySelector("#webhook-form"),
  webhookMessage: document.querySelector("#webhook-message"),
  applicationKeyForm: document.querySelector("#application-key-form"),
  applicationKeyMessage: document.querySelector("#application-key-message"),
  customerDialog: document.querySelector("#customer-dialog"),
  customerForm: document.querySelector("#customer-form"),
  customerMessage: document.querySelector("#customer-message"),
  keyDialog: document.querySelector("#key-dialog"),
  revealedKey: document.querySelector("#revealed-key"),
  copyMessage: document.querySelector("#copy-message"),
};

function sessionRead() {
  try {
    return window.sessionStorage.getItem(sessionKey) || "";
  } catch (_) {
    return "";
  }
}

function sessionWrite(value) {
  try {
    if (value) {
      window.sessionStorage.setItem(sessionKey, value);
    } else {
      window.sessionStorage.removeItem(sessionKey);
    }
  } catch (_) {
    // Private browsing may disable storage; the in-memory session still works.
  }
}

async function apiRequest(path, options = {}) {
  const headers = new Headers(options.headers || {});
  headers.set("Authorization", `Bearer ${state.adminKey}`);
  if (options.body) {
    headers.set("Content-Type", "application/json");
  }
  const response = await fetch(path, {
    ...options,
    headers,
    cache: "no-store",
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

function errorMessage(error) {
  const suffix = error.requestID ? ` · Request ${error.requestID}` : "";
  if (error.status === 401) {
    return `Admin anahtarı geçersiz veya yetkisiz.${suffix}`;
  }
  if (error.status === 503) {
    return `Veritabanı şu anda kullanılamıyor.${suffix}`;
  }
  return `${error.message || "İstek tamamlanamadı."}${suffix}`;
}

function setFormMessage(element, message, tone = "error") {
  element.textContent = message;
  element.classList.toggle("success", tone === "success");
  element.classList.toggle("working", tone === "working");
}

async function connect(adminKey) {
  state.adminKey = adminKey.trim();
  elements.authMessage.textContent = "";
  try {
    await loadProjects();
    sessionWrite(state.adminKey);
    showAuthenticated(true);
  } catch (error) {
    state.adminKey = "";
    sessionWrite("");
    elements.authMessage.textContent = errorMessage(error);
    showAuthenticated(false);
  }
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

async function loadOverview(projectID) {
  elements.globalMessage.textContent = "";
  elements.refresh.disabled = true;
  try {
    state.overview = await apiRequest(`/v1/admin/projects/${encodeURIComponent(projectID)}/overview`);
    state.selectedProject = projectID;
    renderProjectNavigation();
    renderOverview();
    elements.updatedAt.textContent = `Güncellendi ${formatTime(new Date().toISOString())}`;
  } catch (error) {
    if (error.status === 401) {
      logout();
      elements.authMessage.textContent = errorMessage(error);
      return;
    }
    elements.globalMessage.textContent = errorMessage(error);
  } finally {
    elements.refresh.disabled = false;
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
      node("small", "", `${project.application_count} app · ${project.customer_count} müşteri`),
    );
    button.addEventListener("click", () => loadOverview(project.id));
    elements.projectNav.append(button);
  }
  if (!state.projects.length) {
    elements.projectNav.append(node("p", "empty-state", "Henüz proje yok. + ile ilk kataloğu oluşturun."));
  }
}

function renderEmptyWorkspace() {
  state.overview = null;
  elements.projectTitle.textContent = "İlk projeyi oluşturun";
  elements.globalMessage.textContent = "Sol menüdeki + düğmesi Huawei katalog iskeletini oluşturur.";
  setText("metric-apps", "0");
  setText("metric-customers", "0");
  setText("metric-products", "0");
  setText("metric-pending", "0");
  for (const id of ["application-grid", "product-list", "queue-list"]) {
    const container = document.querySelector(`#${id}`);
    container.replaceChildren(node("p", "empty-state", "Bu bölüm proje oluşturulduğunda dolacak."));
  }
  renderEmptyTable("transaction-rows", 5, "Henüz doğrulama yok.");
  renderEmptyTable("customer-rows", 3, "Henüz müşteri yok.");
  renderEmptyTable("webhook-rows", 3, "Henüz webhook olayı yok.");
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
  setText("metric-apps-note", `${configured} tam yapılandırılmış`);
  setText("metric-customers", overview.project.customer_count);
  setText("metric-customers-note", `${allowed} aktif entitlement`);
  setText("metric-products", overview.project.product_count);
  setText("metric-products-note", `${sum(overview.products, "store_mapping_count")} store eşlemesi`);
  setText("metric-pending", pending);
  setText("metric-pending-note", failed ? `${failed} terminal hata` : "Terminal hata yok");

  setText("signal-store", `${configured}/${overview.applications.length} hazır`);
  setText("signal-verify", `${overview.recent_transactions.length} son kayıt`);
  setText("signal-access", `${allowed} izin`);
  setText("signal-deliver", `${delivered} teslim`);
  setSignalWarning("store", configured !== overview.applications.length);
  setSignalWarning("verify", failed > 0);
  setSignalWarning("access", overview.recent_transactions.some((item) => item.access === "unresolved"));
  setSignalWarning("deliver", overview.recent_webhook_events.some((event) => event.status === "failed"));

  renderApplications(overview.applications);
  renderProducts(overview.products);
  renderQueues(overview.queues);
  renderTransactions(overview.recent_transactions);
  renderCustomers(overview.customers);
  renderWebhooks(overview.recent_webhook_events);
}

function renderApplications(applications) {
  const container = document.querySelector("#application-grid");
  container.replaceChildren();
  setText("apps-count", `${applications.length} kayıt`);
  if (!applications.length) {
    container.append(node("p", "empty-state", "Bu projede uygulama yok."));
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
      configRow("Provider credential", application.credential_configured ? "Hazır" : "Eksik", application.credential_configured),
      configRow("Webhook", application.webhook_configured ? "Hazır" : "Eksik", application.webhook_configured),
    );
    const actions = node("div", "application-actions");
    const manage = node("button", "secondary-button", "Bağlantıları yönet");
    manage.type = "button";
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
  elements.webhookForm.reset();
  elements.credentialMessage.textContent = "";
  elements.webhookMessage.textContent = "";
  elements.applicationKeyMessage.textContent = "";
  renderCommissioningStatus();
  elements.applicationDialog.showModal();
}

function renderCommissioningStatus() {
  const application = state.managedApplication;
  if (!application) return;
  setText("application-dialog-title", application.id);
  setText("application-dialog-meta", `${providerLabel(application.provider)} · ${application.environment} · ${application.provider_application_id}`);
  setText("credential-revision", application.credential_revision ? `Revision ${application.credential_revision}` : "Yeni");
  setText("webhook-revision", application.webhook_revision ? `Revision ${application.webhook_revision}` : "Yeni");
  setText("credential-step-status", application.credential_configured ? `Revision ${application.credential_revision}` : "Eksik");
  setText("webhook-step-status", application.webhook_configured ? `Revision ${application.webhook_revision}` : "Eksik");
  document.querySelector("#credential-step").classList.toggle("complete", application.credential_configured);
  document.querySelector("#webhook-step").classList.toggle("complete", application.webhook_configured);
}

function closeApplicationManager() {
  elements.credentialForm.reset();
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

function revealApplicationKey(value) {
  closeApplicationManager();
  elements.revealedKey.value = value;
  elements.copyMessage.textContent = "";
  elements.keyDialog.showModal();
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
  setText("products-count", `${products.length} kayıt`);
  if (!products.length) {
    container.append(node("p", "empty-state", "Katalog ürünü bulunmuyor."));
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
      queueNumber("Bekleyen", queue.pending),
      queueNumber("Tamam", queue.completed),
      queueNumber("Hata", queue.failed, "failed"),
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
    renderEmptyTable("transaction-rows", 5, "Henüz doğrulanmış purchase observation yok.");
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
    renderEmptyTable("customer-rows", 3, "Henüz müşteri yok.");
    return;
  }
  for (const customer of customers) {
    const row = document.createElement("tr");
    row.append(
      compoundCell(customer.external_id, customer.id),
      textCell(`${customer.allowed_count}/${customer.entitlement_count} izinli`),
      textCell(customer.last_observed_at ? formatTime(customer.last_observed_at) : "—"),
    );
    body.append(row);
  }
}

function renderWebhooks(events) {
  const body = document.querySelector("#webhook-rows");
  body.replaceChildren();
  if (!events.length) {
    renderEmptyTable("webhook-rows", 3, "Henüz webhook teslimatı yok.");
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
  return provider === "huawei_appgallery" ? "Huawei AppGallery" : provider.replaceAll("_", " ");
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
  return new Intl.DateTimeFormat("tr-TR", {
    dateStyle: "short",
    timeStyle: "short",
  }).format(date);
}

function sum(items, key) {
  return items.reduce((total, item) => total + Number(item[key] || 0), 0);
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

function logout() {
  state.adminKey = "";
  state.projects = [];
  state.selectedProject = "";
  state.managedApplication = null;
  state.overview = null;
  elements.credentialForm.reset();
  elements.webhookForm.reset();
  elements.revealedKey.value = "";
  for (const dialog of [elements.applicationDialog, elements.customerDialog, elements.keyDialog, elements.setupDialog]) {
    if (dialog.open) dialog.close();
  }
  sessionWrite("");
  elements.adminKey.value = "";
  showAuthenticated(false);
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
      provider: "huawei_appgallery",
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

elements.authForm.addEventListener("submit", (event) => {
  event.preventDefault();
  connect(elements.adminKey.value);
});

elements.refresh.addEventListener("click", () => {
  if (state.selectedProject) loadOverview(state.selectedProject);
});

elements.logout.addEventListener("click", logout);

document.querySelector("#open-setup").addEventListener("click", () => {
  elements.setupMessage.textContent = "";
  elements.setupDialog.showModal();
});

document.querySelector("#close-setup").addEventListener("click", () => elements.setupDialog.close());
document.querySelector("#cancel-setup").addEventListener("click", () => elements.setupDialog.close());

elements.setupForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  elements.setupMessage.textContent = "Katalog oluşturuluyor…";
  const submit = elements.setupForm.querySelector("button[type='submit']");
  submit.disabled = true;
  try {
    const projectID = await createCatalog(elements.setupForm);
    elements.setupForm.reset();
    elements.setupDialog.close();
    await loadProjects(projectID);
    elements.globalMessage.textContent = "Katalog iskeleti oluşturuldu. Provider credential ve webhook yapılandırmasını tamamlayın.";
  } catch (error) {
    elements.setupMessage.textContent = errorMessage(error);
  } finally {
    submit.disabled = false;
  }
});

document.querySelector("#close-application").addEventListener("click", closeApplicationManager);

elements.applicationDialog.addEventListener("close", () => {
  elements.credentialForm.reset();
  elements.webhookForm.reset();
  elements.credentialMessage.textContent = "";
  elements.webhookMessage.textContent = "";
  elements.applicationKeyMessage.textContent = "";
  state.managedApplication = null;
});

elements.credentialForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  const submit = elements.credentialForm.querySelector("button[type='submit']");
  submit.disabled = true;
  setFormMessage(elements.credentialMessage, "Huawei bağlantısı korunarak kaydediliyor…", "working");
  try {
    await saveHuaweiCredential(elements.credentialForm);
    elements.credentialForm.reset();
    await refreshManagedApplication();
    setFormMessage(elements.credentialMessage, "Huawei bağlantısı kaydedildi.", "success");
  } catch (error) {
    setFormMessage(elements.credentialMessage, errorMessage(error));
  } finally {
    submit.disabled = false;
  }
});

elements.webhookForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  const submit = elements.webhookForm.querySelector("button[type='submit']");
  submit.disabled = true;
  setFormMessage(elements.webhookMessage, "Bildirim bağlantısı korunarak kaydediliyor…", "working");
  try {
    await saveWebhook(elements.webhookForm);
    elements.webhookForm.reset();
    await refreshManagedApplication();
    setFormMessage(elements.webhookMessage, "Bildirim bağlantısı kaydedildi.", "success");
  } catch (error) {
    setFormMessage(elements.webhookMessage, errorMessage(error));
  } finally {
    submit.disabled = false;
  }
});

elements.applicationKeyForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  const submit = elements.applicationKeyForm.querySelector("button[type='submit']");
  submit.disabled = true;
  setFormMessage(elements.applicationKeyMessage, "Application key oluşturuluyor…", "working");
  try {
    const response = await createApplicationKey();
    revealApplicationKey(response.key);
  } catch (error) {
    setFormMessage(elements.applicationKeyMessage, errorMessage(error));
  } finally {
    submit.disabled = false;
  }
});

document.querySelector("#open-customer").addEventListener("click", () => {
  if (!state.selectedProject) {
    elements.globalMessage.textContent = "Önce bir proje oluşturun.";
    return;
  }
  elements.customerForm.reset();
  elements.customerMessage.textContent = "";
  elements.customerDialog.showModal();
});

document.querySelector("#close-customer").addEventListener("click", () => elements.customerDialog.close());
document.querySelector("#cancel-customer").addEventListener("click", () => elements.customerDialog.close());

elements.customerDialog.addEventListener("close", () => {
  elements.customerForm.reset();
  elements.customerMessage.textContent = "";
});

elements.customerForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  const submit = elements.customerForm.querySelector("button[type='submit']");
  submit.disabled = true;
  setFormMessage(elements.customerMessage, "Müşteri ekleniyor…", "working");
  try {
    await createCustomer(elements.customerForm);
    elements.customerDialog.close();
    await loadProjects(state.selectedProject);
    elements.globalMessage.textContent = "Müşteri eklendi.";
  } catch (error) {
    setFormMessage(elements.customerMessage, errorMessage(error));
  } finally {
    submit.disabled = false;
  }
});

document.querySelector("#copy-key").addEventListener("click", async () => {
  try {
    await navigator.clipboard.writeText(elements.revealedKey.value);
    setFormMessage(elements.copyMessage, "Application key panoya kopyalandı.", "success");
  } catch (_) {
    elements.revealedKey.focus();
    elements.revealedKey.select();
    setFormMessage(elements.copyMessage, "Otomatik kopyalama engellendi; seçili değeri manuel kopyalayın.");
  }
});

document.querySelector("#close-key").addEventListener("click", closeKeyDialog);
document.querySelector("#done-key").addEventListener("click", closeKeyDialog);
elements.keyDialog.addEventListener("close", () => {
  elements.revealedKey.value = "";
  elements.copyMessage.textContent = "";
});

const savedKey = sessionRead();
if (savedKey) {
  connect(savedKey);
} else {
  showAuthenticated(false);
}
