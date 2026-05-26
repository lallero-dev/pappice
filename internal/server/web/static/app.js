import {
  badge,
  debounce,
  defineComponents,
  el,
  formObject,
  labelize,
  relativeTime,
  splitList
} from "./components.js";

defineComponents();

const state = {
  issues: [],
  issueCounts: { all: 0 },
  projects: [],
  users: [],
  members: [],
  webhooks: [],
  globalWebhooks: [],
  emailNotifications: [],
  auditEvents: [],
  maintenance: null,
  emailStats: null,
  emailEnabled: false,
  emailBatchDelaySeconds: 0,
  deliveries: [],
  tokens: [],
  branding: {
    name: "Pappice",
    subtitle: "customer support",
    mark: "P",
    color: "#5bb974"
  },
  user: null,
  accountLink: null,
  csrf: "",
  view: "issues",
  adminSection: "products",
  productSection: "members",
  selectedProjectId: null,
  selectedId: null,
  sort: {
    key: "created_at",
    dir: "desc"
  },
  meta: {
    statuses: [],
    priorities: [],
    roles: [],
    projectRoles: [],
    webhookEvents: [],
    uploads: {
      max_size_bytes: 10 * 1024 * 1024,
      max_files: 5,
      allowed_types: []
    }
  },
  filters: {
    q: "",
    statuses: ["new", "assigned"],
    assignee: ""
  },
  emailPage: {
    q: "",
    status: "",
    limit: 25,
    offset: 0,
    total: 0
  },
  auditPage: {
    q: "",
    limit: 25,
    offset: 0,
    total: 0
  }
};

const els = {
  appAlert: document.querySelector("#appAlert"),
  appAlertText: document.querySelector("#appAlertText"),
  appAlertClose: document.querySelector("#appAlertClose"),
  brandMark: document.querySelector("#brandMark"),
  brandName: document.querySelector("#brandName"),
  brandSubtitle: document.querySelector("#brandSubtitle"),
  topNav: document.querySelector("#topNav"),
  issuesTab: document.querySelector("#issuesTab"),
  projectTab: document.querySelector("#projectTab"),
  adminTab: document.querySelector("#adminTab"),
  profileMenu: document.querySelector("#profileMenu"),
  profileButton: document.querySelector("#profileButton"),
  profileAvatar: document.querySelector("#profileAvatar"),
  profileName: document.querySelector("#profileName"),
  profileRole: document.querySelector("#profileRole"),
  profileMenuName: document.querySelector("#profileMenuName"),
  profileEmail: document.querySelector("#profileEmail"),
  profilePopover: document.querySelector("#profilePopover"),
  profileEditButton: document.querySelector("#profileEditButton"),
  changePasswordButton: document.querySelector("#changePasswordButton"),
  logoutButton: document.querySelector("#logoutButton"),
  newIssueButton: document.querySelector("#newIssueButton"),
  authView: document.querySelector("#authView"),
  authError: document.querySelector("#authError"),
  setupForm: document.querySelector("#setupForm"),
  loginForm: document.querySelector("#loginForm"),
  accountLinkForm: document.querySelector("#accountLinkForm"),
  accountLinkTitle: document.querySelector("#accountLinkTitle"),
  accountLinkHelp: document.querySelector("#accountLinkHelp"),
  accountLinkUser: document.querySelector("#accountLinkUser"),
  accountLinkSubmit: document.querySelector("#accountLinkSubmit"),
  appView: document.querySelector("#appView"),
  issueView: document.querySelector("#issueView"),
  adminView: document.querySelector("#adminView"),
  adminSectionButtons: Array.from(document.querySelectorAll("[data-admin-section]")),
  adminSectionPanels: Array.from(document.querySelectorAll("[data-admin-panel]")),
  projectView: document.querySelector("#projectView"),
  productSectionButtons: Array.from(document.querySelectorAll("[data-product-section]")),
  productSectionPanels: Array.from(document.querySelectorAll("[data-product-panel]")),
  projectContextTitle: document.querySelector("#projectContextTitle"),
  projectContextMeta: document.querySelector("#projectContextMeta"),
  issueList: document.querySelector("#issueList"),
  searchInput: document.querySelector("#searchInput"),
  productFilter: document.querySelector("#productFilter"),
  assigneeFilter: document.querySelector("#assigneeFilter"),
  statusFilterList: document.querySelector("#statusFilterList"),
  addProjectButton: document.querySelector("#addProjectButton"),
  projectList: document.querySelector("#projectList"),
  addUserButton: document.querySelector("#addUserButton"),
  userList: document.querySelector("#userList"),
  createTokenButton: document.querySelector("#createTokenButton"),
  tokenResult: document.querySelector("#tokenResult"),
  tokenList: document.querySelector("#tokenList"),
  addMemberButton: document.querySelector("#addMemberButton"),
  memberList: document.querySelector("#memberList"),
  addWebhookButton: document.querySelector("#addWebhookButton"),
  webhookList: document.querySelector("#webhookList"),
  addGlobalWebhookButton: document.querySelector("#addGlobalWebhookButton"),
  globalWebhookList: document.querySelector("#globalWebhookList"),
  sendTestEmailButton: document.querySelector("#sendTestEmailButton"),
  emailSearchInput: document.querySelector("#emailSearchInput"),
  emailStatusFilter: document.querySelector("#emailStatusFilter"),
  emailOverview: document.querySelector("#emailOverview"),
  emailList: document.querySelector("#emailList"),
  emailPager: document.querySelector("#emailPager"),
  maintenanceOverview: document.querySelector("#maintenanceOverview"),
  auditSearchInput: document.querySelector("#auditSearchInput"),
  auditList: document.querySelector("#auditList"),
  auditPager: document.querySelector("#auditPager"),
  deliveryList: document.querySelector("#deliveryList"),
  modalHost: document.querySelector("#modalHost")
};

const shortDateFormatter = new Intl.DateTimeFormat(undefined, {
  day: "2-digit",
  hour: "2-digit",
  minute: "2-digit",
  month: "short"
});

const fullDateFormatter = new Intl.DateTimeFormat(undefined, {
  dateStyle: "medium",
  timeStyle: "short"
});

let appAlertTimer = 0;

function formatSeconds(seconds) {
  const value = Number(seconds || 0);
  if (!Number.isFinite(value) || value <= 0) return "-";
  if (value < 60) return `${Math.round(value)}s`;
  const minutes = Math.round(value / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.round(minutes / 60);
  return `${hours}h`;
}

function normalizeBranding(input = {}) {
  const name = String(input.name || "").trim() || "Pappice";
  const subtitle = String(input.subtitle || "").trim() || "customer support";
  const mark = String(input.mark || "").trim() || name.slice(0, 1).toUpperCase() || "P";
  const color = isHexColor(input.color) ? input.color : "#5bb974";
  return {
    name,
    subtitle,
    mark: [...mark].slice(0, 3).join(""),
    color
  };
}

function applyBranding() {
  const branding = state.branding;
  els.brandName.textContent = branding.name;
  els.brandSubtitle.textContent = branding.subtitle;
  if (isDefaultPappiceBranding(branding)) {
    els.brandMark.classList.add("logo");
    els.brandMark.replaceChildren(el("img", { src: "/static/logo.svg", alt: "" }));
  } else {
    els.brandMark.classList.remove("logo");
    els.brandMark.textContent = branding.mark;
  }
  document.title = branding.name;
  document.documentElement.style.setProperty("--brand-color", branding.color);
  document.documentElement.style.setProperty("--brand-contrast", contrastColor(branding.color));
}

function isDefaultPappiceBranding(branding) {
  return branding.name === "Pappice" && branding.mark === "P";
}

function isHexColor(value) {
  return /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.test(String(value || "").trim());
}

function contrastColor(hex) {
  const expanded = hex.length === 4
    ? `#${hex[1]}${hex[1]}${hex[2]}${hex[2]}${hex[3]}${hex[3]}`
    : hex;
  const red = parseInt(expanded.slice(1, 3), 16);
  const green = parseInt(expanded.slice(3, 5), 16);
  const blue = parseInt(expanded.slice(5, 7), 16);
  const brightness = (red * 299 + green * 587 + blue * 114) / 1000;
  return brightness > 145 ? "#102417" : "#ffffff";
}

async function request(path, options = {}) {
  const method = String(options.method || "GET").toUpperCase();
  const bodyIsFormData = options.body instanceof FormData;
  const headers = { ...(options.headers || {}) };
  if (!bodyIsFormData && !headers["Content-Type"]) {
    headers["Content-Type"] = "application/json";
  }
  if (state.csrf && ["POST", "PATCH", "PUT", "DELETE"].includes(method)) {
    headers["X-Pappice-CSRF"] = state.csrf;
  }
  const response = await fetch(path, {
    credentials: "same-origin",
    headers,
    ...options
  });
  const text = await response.text();
  let payload = {};
  if (text) {
    try {
      payload = JSON.parse(text);
    } catch {
      payload = { error: text };
    }
  }
  if (!response.ok) {
    const error = new Error(payload.error || response.statusText || "Request failed");
    error.status = response.status;
    error.payload = payload;
    throw error;
  }
  return payload;
}

async function boot() {
  bindEvents();
  await loadHealth();
  const route = accountLinkRoute();
  if (route) {
    await loadAccountLinkRoute(route);
    return;
  }
  await loadSession();
}

async function loadSession() {
  const session = await request("/api/session");
  state.csrf = session.csrf_token || "";
  if (session.needs_setup) {
    showAuth("setup");
    return;
  }
  if (!session.authenticated) {
    showAuth("login");
    return;
  }
  state.user = session.user;
  await enterApp();
}

function accountLinkRoute() {
  const match = window.location.pathname.match(/^\/account\/(setup|reset)\/([^/]+)\/?$/);
  if (!match) return null;
  return {
    purpose: match[1],
    token: decodeURIComponent(match[2])
  };
}

function parseAppRoute() {
  const trailingSlash = window.location.pathname.length > 1 && window.location.pathname.endsWith("/");
  const parts = window.location.pathname.split("/").filter(Boolean).map(decodeRoutePart);
  if (parts.length === 0) return { view: "issues", normalize: true };
  switch (parts[0]) {
    case "tickets":
      return { view: "issues", normalize: trailingSlash || parts.length !== 1 };
    case "admin": {
      const section = validAdminSection(parts[1]) ? parts[1] : "products";
      return { view: "admin", section, normalize: trailingSlash || parts.length !== 2 || section !== parts[1] };
    }
    case "products": {
      const projectId = Number(parts[1] || 0);
      const section = validProductSection(parts[2]) ? parts[2] : "members";
      return {
        view: "project",
        projectId,
        section,
        normalize: trailingSlash || !Number.isInteger(projectId) || projectId < 1 || parts.length !== 3 || section !== parts[2]
      };
    }
    default:
      return { view: "issues", normalize: true };
  }
}

function decodeRoutePart(part) {
  try {
    return decodeURIComponent(part);
  } catch {
    return part;
  }
}

async function applyRouteFromPath() {
  if (!state.user) return;
  const route = parseAppRoute();
  if (route.view === "admin") {
    if (!isAdmin()) {
      switchView("issues", { updateRoute: false });
      updateRoutePath({ replace: true });
      return;
    }
    state.adminSection = route.section;
    switchView("admin", { updateRoute: false });
    updateRoutePath({ replace: route.normalize });
    return;
  }
  if (route.view === "project") {
    const project = currentProject(route.projectId);
    if (project) {
      state.selectedProjectId = project.id;
      state.productSection = route.section;
      renderProductFilter();
      updateProjectActions();
      await loadIssues();
      if (canManageProject()) {
        switchView("project", { updateRoute: false });
        updateRoutePath({ replace: route.normalize });
        return;
      }
    }
    switchView("issues", { updateRoute: false });
    updateRoutePath({ replace: true });
    return;
  }
  switchView("issues", { updateRoute: false });
  updateRoutePath({ replace: route.normalize });
}

function updateRoutePath({ replace = false } = {}) {
  if (!state.user) return;
  const nextPath = routePathForState();
  if (window.location.pathname === nextPath && window.location.search === "" && window.location.hash === "") return;
  if (replace) {
    window.history.replaceState(null, "", nextPath);
  } else {
    window.history.pushState(null, "", nextPath);
  }
}

function routePathForState() {
  if (state.view === "admin") return `/admin/${state.adminSection}`;
  if (state.view === "project" && state.selectedProjectId) return `/products/${state.selectedProjectId}/${state.productSection}`;
  return "/tickets";
}

async function loadAccountLinkRoute(route) {
  state.accountLink = { ...route, user: null };
  showAuth("account");
  renderAccountLinkForm({
    purpose: route.purpose,
    loading: true
  });
  try {
    const payload = await request(`/api/account-links/${encodeURIComponent(route.token)}`);
    state.accountLink = { ...route, user: payload.user, expiresAt: payload.expires_at };
    renderAccountLinkForm({
      purpose: payload.purpose || route.purpose,
      user: payload.user,
      expiresAt: payload.expires_at
    });
  } catch (error) {
    state.accountLink = null;
    renderAccountLinkForm({
      purpose: route.purpose,
      error: accountLinkErrorMessage(error)
    });
  }
}

function accountLinkErrorMessage(error) {
  const message = userMessage(error);
  if (message && message !== "The requested item was not found. Refresh the page and try again.") {
    return message;
  }
  return "This link is invalid or has expired. Ask an administrator for a new one.";
}

function renderAccountLinkForm({ purpose, user = null, expiresAt = "", loading = false, error = "" }) {
  const reset = purpose === "reset";
  els.accountLinkTitle.textContent = reset ? "Reset Password" : "Set Password";
  els.accountLinkSubmit.textContent = reset ? "Reset Password" : "Set Password";
  els.accountLinkHelp.textContent = error || (loading
    ? "Checking this one-time link..."
    : reset
      ? "Enter a new password for this Pappice account."
      : "Enter a password to finish account setup.");
  els.accountLinkHelp.classList.toggle("status-error", Boolean(error));
  els.accountLinkSubmit.disabled = loading || Boolean(error);
  els.accountLinkUser.replaceChildren();
  if (user) {
    els.accountLinkUser.hidden = false;
    els.accountLinkUser.append(
      el("strong", {}, user.display_name || user.username),
      el("span", {}, user.email || user.username),
      expiresAt ? el("span", {}, `Expires ${fullDateFormatter.format(new Date(expiresAt))}`) : el("span")
    );
  } else {
    els.accountLinkUser.hidden = true;
  }
}

async function enterApp() {
  showApp();
  await loadHealth();
  if (!isCustomer()) {
    await loadUsers();
  } else {
    renderAssigneeFilter();
  }
  await loadProjects();
  await applyRouteFromPath();
}

function showAuth(mode) {
  state.user = null;
  state.csrf = "";
  clearAppAlert();
  clearAuthError();
  els.authView.hidden = false;
  els.appView.hidden = true;
  els.topNav.hidden = true;
  els.adminTab.hidden = true;
  els.profileMenu.hidden = true;
  closeProfileMenu();
  els.newIssueButton.hidden = true;
  els.setupForm.hidden = mode !== "setup";
  els.loginForm.hidden = mode !== "login";
  els.accountLinkForm.hidden = mode !== "account";
}

function showApp() {
  clearAuthError();
  els.authView.hidden = true;
  els.appView.hidden = false;
  els.topNav.hidden = false;
  els.profileMenu.hidden = false;
  renderProfileMenu();
  switchView("issues", { updateRoute: false });
}

function renderProfileMenu() {
  const name = state.user?.display_name || state.user?.username || "";
  const role = labelize(state.user?.role || "");
  const email = state.user?.email || "";
  els.profileAvatar.textContent = (name || "P").slice(0, 1).toUpperCase();
  els.profileName.textContent = name;
  els.profileRole.textContent = role;
  els.profileMenuName.textContent = name;
  els.profileEmail.textContent = email || role;
}

function toggleProfileMenu() {
  const open = els.profilePopover.hidden;
  els.profilePopover.hidden = !open;
  els.profileButton.setAttribute("aria-expanded", String(open));
}

function closeProfileMenu() {
  els.profilePopover.hidden = true;
  els.profileButton?.setAttribute("aria-expanded", "false");
}

async function loadHealth() {
  const meta = await request("/api/health");
  state.meta.statuses = meta.statuses || [];
  state.meta.priorities = meta.priorities || [];
  state.meta.roles = meta.roles || [];
  state.meta.projectRoles = meta.project_roles || [];
  state.meta.webhookEvents = meta.webhook_events || [];
  state.meta.uploads = meta.uploads || state.meta.uploads;
  state.branding = normalizeBranding(meta.branding);
  applyBranding();
  state.filters.statuses = state.filters.statuses.filter((status) => state.meta.statuses.includes(status));
  if (state.filters.statuses.length === 0) state.filters.statuses = defaultStatusFilters();
}

async function loadProjects() {
  const payload = await request("/api/projects");
  state.projects = payload.projects || [];
  if (state.selectedProjectId && !state.projects.some((project) => project.id === state.selectedProjectId)) {
    state.selectedProjectId = null;
  }
  renderProductFilter();
  renderProjectList();
  updateProjectActions();
  await loadIssues();
}

async function loadIssues() {
  if (state.projects.length === 0) {
    state.issues = [];
    state.issueCounts = countIssues([]);
    renderIssuesView();
    return;
  }
  const params = new URLSearchParams();
  if (state.filters.q) params.set("q", state.filters.q);
  if (state.filters.assignee) params.set("assignee", state.filters.assignee);
  for (const status of state.filters.statuses) params.append("status", status);
  const projectID = state.selectedProjectId || null;
  if (projectID) params.set("project_id", String(projectID));
  const countParams = new URLSearchParams();
  if (projectID) countParams.set("project_id", String(projectID));
  const [payload, countsPayload] = await Promise.all([
    request(`/api/tickets?${params.toString()}`),
    request(`/api/tickets?${countParams.toString()}`)
  ]);
  if (projectID !== (state.selectedProjectId || null)) return;
  state.issues = payload.tickets || [];
  state.issueCounts = countIssues(countsPayload.tickets || []);
  if (state.selectedId && !state.issues.some((issue) => issue.id === state.selectedId)) {
    state.selectedId = state.issues[0]?.id || null;
  }
  if (!state.selectedId && state.issues.length > 0) {
    state.selectedId = state.issues[0].id;
  }
  renderIssuesView();
}

async function loadAdmin() {
  renderAdminSections();
  await loadAdminSection(state.adminSection);
}

async function loadAdminSection(section) {
  switch (section) {
    case "products":
      renderProjectList();
      return;
    case "accounts":
      await loadUsers();
      return;
    case "tokens":
      await loadTokens();
      return;
    case "webhooks":
      await loadGlobalWebhooks();
      return;
    case "email":
      await loadEmailNotifications();
      return;
    case "maintenance":
      await loadMaintenance();
      return;
    case "audit":
      await loadAuditEvents();
      return;
    default:
      state.adminSection = "products";
      renderAdminSections();
      renderProjectList();
  }
}

async function loadProjectAdmin() {
  renderProjectContext();
  renderProductSections();
  if (!state.selectedProjectId || !canManageProject()) return;
  await loadProductSection(state.productSection);
}

async function loadProductSection(section) {
  switch (validProductSection(section) ? section : "members") {
    case "members":
      await Promise.all([loadUsers(), loadMembers()]);
      return;
    case "webhooks":
      await loadProjectWebhooks();
      return;
    case "deliveries":
      await loadProjectDeliveries();
      return;
    default:
      state.productSection = "members";
      renderProductSections();
      await Promise.all([loadUsers(), loadMembers()]);
  }
}

async function loadUsers() {
  const payload = await request("/api/users");
  state.users = payload.users || [];
  renderAssigneeFilter();
  renderUsers();
}

async function loadTokens() {
  const payload = await request("/api/tokens");
  state.tokens = payload.tokens || [];
  renderTokens();
}

async function loadMembers() {
  const payload = await request(`/api/projects/${state.selectedProjectId}/members`);
  state.members = payload.members || [];
  renderMembers();
}

async function loadProjectWebhooks() {
  const payload = await request(`/api/projects/${state.selectedProjectId}/webhooks`);
  state.webhooks = payload.webhooks || [];
  renderWebhooks(els.webhookList, state.webhooks);
}

async function loadGlobalWebhooks() {
  const payload = await request("/api/webhooks");
  state.globalWebhooks = payload.webhooks || [];
  renderWebhooks(els.globalWebhookList, state.globalWebhooks);
}

async function loadEmailNotifications() {
  const params = new URLSearchParams({
    limit: String(state.emailPage.limit),
    offset: String(state.emailPage.offset)
  });
  if (state.emailPage.status) params.set("status", state.emailPage.status);
  if (state.emailPage.q) params.set("q", state.emailPage.q);
  const payload = await request(`/api/email-notifications?${params.toString()}`);
  state.emailNotifications = payload.notifications || [];
  state.emailPage.total = Number(payload.total || 0);
  state.emailPage.limit = Number(payload.limit || state.emailPage.limit);
  state.emailPage.offset = Number(payload.offset || 0);
  state.emailStats = payload.stats || null;
  state.emailEnabled = Boolean(payload.enabled);
  state.emailBatchDelaySeconds = Number(payload.batch_delay_seconds || 0);
  renderEmailNotifications();
}

async function loadAuditEvents() {
  const params = new URLSearchParams({
    limit: String(state.auditPage.limit),
    offset: String(state.auditPage.offset)
  });
  if (state.auditPage.q) params.set("q", state.auditPage.q);
  const payload = await request(`/api/audit-events?${params.toString()}`);
  state.auditEvents = payload.events || [];
  state.auditPage.total = Number(payload.total || 0);
  state.auditPage.limit = Number(payload.limit || state.auditPage.limit);
  state.auditPage.offset = Number(payload.offset || 0);
  renderAuditEvents();
}

async function loadMaintenance() {
  const payload = await request("/api/admin/maintenance");
  state.maintenance = payload;
  renderMaintenance();
}

async function loadProjectDeliveries() {
  const payload = await request(`/api/projects/${state.selectedProjectId}/webhook-deliveries`);
  state.deliveries = payload.deliveries || [];
  renderDeliveries();
}

function renderProductFilter() {
  els.productFilter.replaceChildren();
  if (state.projects.length === 0) {
    els.productFilter.append(new Option("No products", ""));
    els.productFilter.disabled = true;
    return;
  }
  els.productFilter.disabled = false;
  els.productFilter.append(new Option("All products", ""));
  for (const project of state.projects) {
    els.productFilter.append(new Option(`${project.key} / ${project.name}`, String(project.id)));
  }
  els.productFilter.value = state.selectedProjectId ? String(state.selectedProjectId) : "";
}

function updateProjectActions() {
  els.adminTab.hidden = !isAdmin();
  els.projectTab.hidden = !canManageProject();
  els.newIssueButton.hidden = !canCreateIssue();
  if (state.view === "admin" && !isAdmin()) switchView("issues");
  if (state.view === "project" && !canManageProject()) switchView("issues");
}

function renderIssuesView() {
  renderCounts();
  renderSortHeaders();
  renderIssueList();
}

function renderCounts() {
  const counts = state.issueCounts;
  els.statusFilterList.replaceChildren();
  for (const status of state.meta.statuses) {
    const active = state.filters.statuses.includes(status);
    const button = el("button", { className: "status-chip", type: "button", "data-filter-status": status }, [
      el("span", { className: "status-chip-label" }, labelize(status)),
      el("span", { className: "status-chip-count" }, String(counts[status] || 0))
    ]);
    button.classList.toggle("active", active);
    button.setAttribute("aria-pressed", String(active));
    button.addEventListener("click", () => {
      toggleStatusFilter(status);
      loadIssues().catch(showError);
    });
    els.statusFilterList.append(button);
  }
}

function defaultStatusFilters() {
  const defaults = ["new", "assigned"].filter((status) => state.meta.statuses.includes(status));
  return defaults.length > 0 ? defaults : state.meta.statuses.slice(0, 1);
}

function toggleStatusFilter(status) {
  const active = new Set(state.filters.statuses);
  if (active.has(status)) {
    if (active.size === 1) return;
    active.delete(status);
  } else {
    active.add(status);
  }
  state.filters.statuses = state.meta.statuses.filter((candidate) => active.has(candidate));
}

function renderAssigneeFilter() {
  const current = state.filters.assignee;
  const options = assigneeOptions(current);
  options[0] = { value: "", label: "Anyone" };
  els.assigneeFilter.replaceChildren();
  for (const option of options) {
    els.assigneeFilter.append(new Option(option.label, option.value));
  }
  els.assigneeFilter.value = current;
  els.assigneeFilter.disabled = options.length <= 1 && !current;
}

function countIssues(issues) {
  const counts = { all: issues.length };
  for (const status of state.meta.statuses) counts[status] = 0;
  for (const issue of issues) {
    counts[issue.status] = (counts[issue.status] || 0) + 1;
  }
  return counts;
}

function emptyState({ title, body, actionLabel = "", onAction = null }) {
  const node = el("div", { className: "empty-state" }, [
    el("h3", {}, title),
    el("p", {}, body)
  ]);
  const actions = emptyActions(actionLabel, onAction);
  if (actions) node.append(actions);
  return node;
}

function emptyInline({ title, body, actionLabel = "", onAction = null }) {
  const node = el("div", { className: "empty-inline" }, [
    el("h3", {}, title),
    el("p", {}, body)
  ]);
  const actions = emptyActions(actionLabel, onAction);
  if (actions) node.append(actions);
  return node;
}

function emptyActions(actionLabel, onAction) {
  if (!actionLabel || !onAction) return null;
  const button = el("button", { className: "ghost-button", type: "button" }, actionLabel);
  button.addEventListener("click", onAction);
  return el("div", { className: "empty-actions" }, [button]);
}

function hasActiveTicketFilters() {
  return Boolean(state.filters.q || state.filters.assignee || !sameStatuses(state.filters.statuses, defaultStatusFilters()));
}

function sameStatuses(left, right) {
  if (left.length !== right.length) return false;
  const values = new Set(left);
  return right.every((status) => values.has(status));
}

function clearTicketFilters() {
  state.filters.q = "";
  state.filters.assignee = "";
  state.filters.statuses = defaultStatusFilters();
  els.searchInput.value = "";
  els.assigneeFilter.value = "";
  renderCounts();
  loadIssues().catch(showError);
}

function renderIssueList() {
  els.issueList.replaceChildren();
  if (state.projects.length === 0) {
    els.issueList.append(emptyState({
      title: isAdmin() ? "Create a product to start" : "No products available",
      body: isAdmin()
        ? "Products group tickets by the customer, service, or team you support."
        : "Ask an administrator to add your account to a product before opening tickets.",
      actionLabel: isAdmin() ? "New Product" : "",
      onAction: isAdmin() ? openProjectModal : null
    }));
    return;
  }
  if (state.issues.length === 0) {
    if (hasActiveTicketFilters()) {
      els.issueList.append(emptyState({
        title: "No tickets match these filters",
        body: "Clear the filters to return to the default queue.",
        actionLabel: "Clear Filters",
        onAction: clearTicketFilters
      }));
      return;
    }
    els.issueList.append(emptyState({
      title: "No tickets yet",
      body: canCreateIssue()
        ? "Create the first ticket for this view."
        : "Tickets will appear here when customers or staff open them.",
      actionLabel: canCreateIssue() ? "New Ticket" : "",
      onAction: canCreateIssue() ? openIssueModal : null
    }));
    return;
  }
  for (const issue of sortedIssues()) {
    const row = document.createElement("button");
    row.type = "button";
    row.className = "issue-row";
    row.classList.toggle("active", issue.id === state.selectedId);
    row.addEventListener("click", () => {
      state.selectedId = issue.id;
      renderIssueList();
      openTicketDetail(issue);
    });
    row.append(
      issueDate("Created", issue.created_at),
      issueProduct(issue),
      issueTitle(issue),
      badge(issue.status, `status-${issue.status}`),
      badge(issue.priority, `priority-${issue.priority}`),
      issueDate("Updated", issue.updated_at)
    );
    els.issueList.append(row);
  }
}

function renderSortHeaders() {
  for (const button of document.querySelectorAll("[data-sort-key]")) {
    const active = button.dataset.sortKey === state.sort.key;
    button.classList.toggle("active", active);
    button.classList.toggle("desc", active && state.sort.dir === "desc");
    button.classList.toggle("asc", active && state.sort.dir === "asc");
    button.setAttribute("aria-sort", active ? (state.sort.dir === "desc" ? "descending" : "ascending") : "none");
  }
}

function setIssueSort(key) {
  if (state.sort.key === key) {
    state.sort.dir = state.sort.dir === "asc" ? "desc" : "asc";
  } else {
    state.sort.key = key;
    state.sort.dir = defaultSortDir(key);
  }
  renderIssuesView();
}

function defaultSortDir(key) {
  return ["created_at", "updated_at"].includes(key) ? "desc" : "asc";
}

function sortedIssues() {
  return [...state.issues].sort(compareIssues);
}

function compareIssues(a, b) {
  const direction = state.sort.dir === "desc" ? -1 : 1;
  let result = 0;
  switch (state.sort.key) {
    case "created_at":
    case "updated_at":
      result = compareTime(a[state.sort.key], b[state.sort.key]);
      break;
    case "product":
      result = compareText(issueProductLabel(a), issueProductLabel(b));
      break;
    case "status":
      result = compareOrdered(a.status, b.status, state.meta.statuses);
      break;
    case "priority":
      result = compareOrdered(a.priority, b.priority, state.meta.priorities);
      break;
    case "title":
    default:
      result = compareText(a.title, b.title);
      break;
  }
  if (result !== 0) return result * direction;
  result = compareTime(b.updated_at, a.updated_at);
  if (result !== 0) return result;
  return Number(a.id || 0) - Number(b.id || 0);
}

function compareTime(left, right) {
  return new Date(left).getTime() - new Date(right).getTime();
}

function compareText(left, right) {
  return String(left || "").localeCompare(String(right || ""), undefined, { numeric: true, sensitivity: "base" });
}

function compareOrdered(left, right, order) {
  const leftIndex = order.indexOf(left);
  const rightIndex = order.indexOf(right);
  return (leftIndex === -1 ? order.length : leftIndex) - (rightIndex === -1 ? order.length : rightIndex);
}

function issueDate(label, value) {
  const date = new Date(value);
  const valid = !Number.isNaN(date.getTime());
  const text = valid ? shortDateFormatter.format(date) : "";
  const title = valid ? fullDateFormatter.format(date) : "";
  return el("span", { className: "issue-date", title }, [
    el("span", { className: "issue-date-label" }, label),
    el("span", { className: "issue-date-value" }, text)
  ]);
}

function issueTitle(issue) {
  const wrap = el("span", { className: "issue-title" });
  const detail = issue.assignee
    ? `Assigned to ${issue.assignee}`
    : issue.requester
      ? `Requester ${issue.requester}`
      : "Unassigned";
  wrap.append(
    el("strong", {}, issue.title),
    el("span", {}, detail)
  );
  return wrap;
}

function issueProduct(issue) {
  const { key, name } = issueProductParts(issue);
  return el("span", { className: "issue-product" }, [
    el("strong", {}, key),
    el("span", {}, name)
  ]);
}

function issueProductParts(issue) {
  const project = currentProject(issue.project_id);
  return {
    key: issue.project_key || project?.key || issue.project || "Product",
    name: project?.name || ""
  };
}

function issueProductLabel(issue) {
  const { key, name } = issueProductParts(issue);
  return `${key} ${name}`.trim();
}

function openTicketDetail(issue = selectedIssue()) {
  if (!issue) return;
  state.selectedId = issue.id;
  openTicketModal(issue);
}

function openTicketModal(issue = null) {
  const creating = !issue;
  const creatableProjects = creating ? state.projects.filter((project) => canCreateIssue(project.id)) : [];
  const projectId = creating ? initialTicketProjectId(creatableProjects) : issue.project_id;
  if (creating && !projectId) {
    showError(new Error("Create a product before adding tickets"));
    return;
  }
  const editable = creating || canEditTicket(issue);
  const canComment = !creating && canCommentTicket(issue);
  const submittable = editable || canComment;
  els.modalHost.open({
    title: creating ? "New Ticket" : issue.key || `Ticket #${issue.id}`,
    size: creating ? "compact" : "wide",
    submitText: creating ? "Create Ticket" : editable ? "Save Changes" : "Send Reply",
    hideFooter: !submittable,
    content: ticketModalContent({
      issue,
      creating,
      editable,
      canComment,
      projectId,
      creatableProjects
    }),
    onSubmit: submittable ? async (data, form) => {
      if (creating) {
        const payload = ticketCreatePayload(data, projectId);
        const body = ticketCreateRequestBody(payload, form);
        const created = await request(`/api/projects/${payload.project_id}/tickets`, { method: "POST", body });
        state.selectedProjectId = payload.project_id;
        state.selectedId = created.id;
        await loadProjects();
        return;
      }
      await saveTicketChanges(issue, data, { editable, canComment }, form);
    } : null
  });
  if (submittable) bindTicketSaveState({ issue, creating, projectId, editable, canComment });
}

function ticketModalContent({ issue, creating, editable, canComment, projectId, creatableProjects }) {
  const wrap = el("div", { className: "ticket-detail" });
  wrap.classList.toggle("ticket-create", creating);
  const header = el("div", { className: "detail-header" });
  if (editable) {
    header.append(ticketTextField("Title", "title", issue?.title || "", {
      autocomplete: "off",
      className: "ticket-title-input",
      maxlength: 160,
      placeholder: "Brief summary",
      required: true
    }));
  } else {
    header.append(el("h3", {}, issue.title));
  }
  if (!creating) header.append(detailMeta(issue));

  const main = el("section", { className: "ticket-main" });
  if (editable) {
    main.append(ticketTextareaField("Description", "description", issue?.description || "", {
      className: "ticket-description-input",
      placeholder: "Describe the request, impact, and useful context.",
      rows: 6
    }));
    if (creating) {
      main.append(ticketAttachmentField("Attachments", "attachments"));
    } else if ((issue?.attachments || []).length > 0) {
      main.append(attachmentList(issue.attachments));
    }
  } else {
    main.append(
      el("h4", { className: "section-title" }, "Description"),
      el("div", { className: "description" }, issue.description || "No description."),
      attachmentList(issue.attachments || [])
    );
  }
  if (!creating) {
    main.append(
      el("h4", { className: "section-title" }, "Conversation"),
      comments(issue),
      canComment ? commentComposer(issue) : el("div")
    );
  }

  const side = el("aside", { className: "ticket-side" });
  if (creating) {
    side.append(sideSection("Product", ticketSelectControl("project_id", String(projectId), ticketProductOptions(creatableProjects), {
      ariaLabel: "Product",
      required: true
    })));
  }
  if (editable && !isCustomer()) {
    side.append(sideSection("Workflow", workflowEditor(issue || { assignee: "", priority: "normal", status: "new" }, { creating })));
  }
  if (!creating) {
    const requester = requesterBlock(issue);
    if (requester) {
      side.append(sideSection("Requester", requester));
    }
    const facts = [
      factBlock("Product", issue.project_key || issue.project || "Product"),
      factBlock("Created", relativeTime(issue.created_at)),
      factBlock("Updated", relativeTime(issue.updated_at))
    ];
    if (!canEditTicket(issue)) facts.splice(1, 0, factBlock("Assignee", issue.assignee || "Unassigned"));
    side.append(sideSection("Ticket", el("div", { className: "fact-list" }, facts)));
  }

  wrap.append(header, el("div", { className: "ticket-detail-grid" }, [main, side]));
  return wrap;
}

function initialTicketProjectId(creatableProjects) {
  if (state.selectedProjectId && creatableProjects.some((project) => project.id === state.selectedProjectId)) {
    return state.selectedProjectId;
  }
  return creatableProjects[0]?.id || null;
}

function ticketProductOptions(projects) {
  return projects.map((project) => ({ value: String(project.id), label: `${project.key} / ${project.name}` }));
}

function ticketTextField(label, name, value, options = {}) {
  const control = document.createElement("input");
  control.type = options.type || "text";
  control.name = name;
  control.value = value;
  applyTicketControlOptions(control, options);
  return ticketControlField(label, control);
}

function ticketTextareaField(label, name, value, options = {}) {
  const control = document.createElement("textarea");
  control.name = name;
  control.rows = options.rows || 4;
  control.value = value;
  applyTicketControlOptions(control, options);
  return ticketControlField(label, control);
}

function ticketSelectField(label, name, value, options, controlOptions = {}) {
  return ticketControlField(label, ticketSelectControl(name, value, options, controlOptions));
}

function ticketAttachmentField(label, name) {
  const control = document.createElement("input");
  control.type = "file";
  control.name = name;
  control.multiple = true;
  control.className = "attachment-input";
  control.dataset.ticketControl = "true";
  const allowed = state.meta.uploads?.allowed_types || [];
  if (allowed.length > 0 && !allowed.includes("*") && !allowed.includes("*/*")) {
    control.accept = allowed.join(",");
  }
  return ticketControlField(label, control);
}

function ticketSelectControl(name, value, options, controlOptions = {}) {
  const control = document.createElement("select");
  control.name = name;
  for (const option of options || []) {
    control.append(new Option(option.label, option.value));
  }
  control.value = value;
  applyTicketControlOptions(control, controlOptions);
  return control;
}

function ticketControlField(label, control) {
  return el("label", { className: "ticket-form-field" }, [
    el("span", {}, label),
    control
  ]);
}

function applyTicketControlOptions(control, options) {
  control.dataset.ticketControl = "true";
  if (options.className) control.className = options.className;
  if (options.required) control.required = true;
  if (options.maxlength) control.maxLength = options.maxlength;
  if (options.minlength) control.minLength = options.minlength;
  if (options.placeholder) control.placeholder = options.placeholder;
  if (options.autocomplete) control.autocomplete = options.autocomplete;
  if (options.ariaLabel) control.setAttribute("aria-label", options.ariaLabel);
}

function sideSection(title, content) {
  return el("section", { className: "side-section" }, [
    el("h4", { className: "section-title" }, title),
    content
  ]);
}

function detailMeta(issue) {
  const meta = el("div", { className: "detail-meta" });
  meta.append(
    badge(issue.status, `status-${issue.status}`),
    el("span", {}, issue.key || `#${issue.id}`),
    el("span", {}, `Requester ${issue.requester || "unknown"}`),
    el("span", {}, `Created ${relativeTime(issue.created_at)}`)
  );
  return meta;
}

function requesterBlock(issue) {
  if (!issue.requester_email && issue.source !== "portal") return null;
  const block = el("div", { className: "requester-block" });
  block.append(
    el("strong", {}, issue.requester_name || "Unknown"),
    issue.requester_email ? el("span", {}, issue.requester_email) : el("span", {}, issue.requester || "Requester"),
    badge(issue.source || "staff", "priority-normal")
  );
  return block;
}

function factBlock(label, value) {
  const block = el("div", { className: "fact-block" });
  block.append(el("span", { className: "fact-label" }, label), el("strong", {}, value));
  return block;
}

function renderAdminSections() {
  for (const button of els.adminSectionButtons) {
    const active = button.getAttribute("data-admin-section") === state.adminSection;
    button.classList.toggle("active", active);
    button.setAttribute("aria-current", active ? "page" : "false");
  }
  for (const panel of els.adminSectionPanels) {
    panel.hidden = panel.getAttribute("data-admin-panel") !== state.adminSection;
  }
}

async function switchAdminSection(section) {
  if (!isAdmin()) return;
  state.adminSection = validAdminSection(section) ? section : "products";
  renderAdminSections();
  if (state.view === "admin") updateRoutePath();
  await loadAdminSection(state.adminSection);
}

function validAdminSection(section) {
  return ["products", "accounts", "tokens", "webhooks", "email", "maintenance", "audit"].includes(section);
}

function renderProductSections() {
  for (const button of els.productSectionButtons) {
    const active = button.getAttribute("data-product-section") === state.productSection;
    button.classList.toggle("active", active);
    button.setAttribute("aria-current", active ? "page" : "false");
  }
  for (const panel of els.productSectionPanels) {
    panel.hidden = panel.getAttribute("data-product-panel") !== state.productSection;
  }
}

async function switchProductSection(section) {
  state.productSection = validProductSection(section) ? section : "members";
  renderProductSections();
  if (state.view === "project") updateRoutePath();
  if (state.selectedProjectId && canManageProject()) await loadProductSection(state.productSection);
}

function validProductSection(section) {
  return ["members", "webhooks", "deliveries"].includes(section);
}

function renderProjectList() {
  els.projectList.replaceChildren();
  if (state.projects.length === 0) {
    els.projectList.append(emptyInline({
      title: "No products",
      body: "Create a product before inviting customers.",
      actionLabel: "New Product",
      onAction: openProjectModal
    }));
    return;
  }
  for (const project of state.projects) {
    const row = el("div", { className: "admin-row" });
    const select = el("button", { className: "ghost-button", type: "button" }, "Open");
    select.addEventListener("click", async () => {
      state.selectedProjectId = project.id;
      state.productSection = "members";
      renderProductFilter();
      updateProjectActions();
      switchView(canManageProject(project.id) ? "project" : "issues");
      await loadIssues();
    });
    row.append(
      el("div", { className: "admin-row-main" }, `${project.key} / ${project.name}`),
      el("span", { className: "muted" }, project.role || ""),
      select
    );
    els.projectList.append(row);
  }
}

function renderProjectContext() {
  const project = currentProject();
  if (!els.projectContextTitle || !els.projectContextMeta) return;
  els.projectContextMeta.replaceChildren();

  if (!project) {
    els.projectContextTitle.textContent = "No product selected";
    els.projectContextMeta.append(el("span", { className: "muted" }, "Choose a product to manage members and integrations."));
    return;
  }

  els.projectContextTitle.textContent = project.name || project.key || "Product";
  els.projectContextMeta.append(
    el("span", { className: "project-key-pill" }, project.key || `#${project.id}`),
    el("span", { className: "muted" }, `${labelize(project.role || "owner")} access`)
  );
}

function renderUsers() {
  els.userList.replaceChildren();
  for (const user of state.users) {
    const row = el("div", { className: "admin-row" });
    const reset = el("button", { className: "ghost-button", type: "button" }, "Reset");
    reset.disabled = user.id === state.user.id || user.disabled;
    reset.addEventListener("click", () => resetUserPassword(user).catch(showError));
    const edit = el("button", { className: "ghost-button", type: "button" }, "Edit");
    edit.addEventListener("click", () => openUserModal(user));
    const remove = el("button", { className: "ghost-button", type: "button" }, "Delete");
    remove.disabled = user.id === state.user.id;
    remove.addEventListener("click", () => deleteUser(user.id).catch(showError));
    const label = user.email
      ? `${user.display_name || user.username} / ${user.username} / ${user.email}`
      : `${user.display_name || user.username} / ${user.username}`;

    row.append(
      el("div", { className: "admin-row-main" }, label),
      badge(user.role, "priority-normal"),
      el("span", { className: user.disabled || user.password_reset_required ? "muted" : "status-ok" }, user.disabled ? "Disabled" : user.password_reset_required ? "Password pending" : "Active"),
      reset,
      edit,
      remove
    );
    els.userList.append(row);
  }
}

function renderTokens() {
  els.tokenList.replaceChildren();
  if (state.tokens.length === 0) {
    els.tokenList.append(emptyInline({
      title: "No API tokens",
      body: "Create a token when an integration needs API access.",
      actionLabel: "Create Token",
      onAction: openTokenModal
    }));
    return;
  }
  for (const token of state.tokens) {
    const row = el("div", { className: "admin-row" });
    const remove = el("button", { className: "ghost-button", type: "button" }, "Delete");
    remove.addEventListener("click", () => deleteToken(token.id).catch(showError));
    row.append(
      el("div", { className: "admin-row-main" }, `${token.name} / ${token.prefix}`),
      el("span", { className: "muted" }, token.last_used_at ? `Used ${relativeTime(token.last_used_at)}` : "Never used"),
      remove
    );
    els.tokenList.append(row);
  }
}

function renderMembers() {
  els.memberList.replaceChildren();
  if (state.members.length === 0) {
    els.memberList.append(emptyInline({
      title: "No product members",
      body: "Add staff and customers who should access this product.",
      actionLabel: "Add Member",
      onAction: openMemberModal
    }));
    return;
  }
  for (const member of state.members) {
    const row = el("div", { className: "admin-row" });
    const remove = el("button", { className: "ghost-button", type: "button" }, "Remove");
    remove.addEventListener("click", () => deleteMember(member.user_id).catch(showError));
    row.append(
      el("div", { className: "admin-row-main" }, `${member.display_name || member.username} / ${member.username}`),
      badge(member.role, "priority-normal"),
      remove
    );
    els.memberList.append(row);
  }
}

function renderWebhooks(list, hooks) {
  list.replaceChildren();
  if (hooks.length === 0) {
    const global = list === els.globalWebhookList;
    list.append(emptyInline({
      title: "No webhooks",
      body: global ? "Global webhooks receive events from every product." : "Product webhooks only receive events for this product.",
      actionLabel: "New Webhook",
      onAction: () => openWebhookModal(global ? "global" : "project")
    }));
    return;
  }
  for (const hook of hooks) {
    const row = el("div", { className: "admin-row" });
    const test = el("button", { className: "ghost-button", type: "button" }, "Test");
    test.addEventListener("click", () => testWebhook(hook.id).catch(showError));
    const remove = el("button", { className: "ghost-button", type: "button" }, "Delete");
    remove.addEventListener("click", () => deleteWebhook(hook.id).catch(showError));
    row.append(
      el("div", { className: "admin-row-main" }, `${hook.name} / ${hook.url}`),
      el("span", { className: hook.enabled ? "status-ok" : "muted" }, hook.enabled ? "Enabled" : "Disabled"),
      el("span", { className: "muted" }, hook.last_status ? `HTTP ${hook.last_status}` : "No deliveries"),
      test,
      remove
    );
    list.append(row);
  }
}

function renderDeliveries() {
  els.deliveryList.replaceChildren();
  if (state.deliveries.length === 0) {
    els.deliveryList.append(emptyInline({
      title: "No deliveries",
      body: "Webhook delivery attempts will appear here after events are sent."
    }));
    return;
  }
  for (const delivery of state.deliveries) {
    const row = el("div", { className: "admin-row" });
    row.append(
      el("div", { className: "admin-row-main" }, `${delivery.event} / ${delivery.error || `HTTP ${delivery.status_code || 0}`}`),
      el("span", { className: "muted" }, `${delivery.duration_ms}ms`),
      el("span", { className: "muted" }, relativeTime(delivery.created_at))
    );
    els.deliveryList.append(row);
  }
}

function renderEmailNotifications() {
  if (els.emailSearchInput && els.emailSearchInput.value !== state.emailPage.q) {
    els.emailSearchInput.value = state.emailPage.q;
  }
  if (els.emailStatusFilter && els.emailStatusFilter.value !== state.emailPage.status) {
    els.emailStatusFilter.value = state.emailPage.status;
  }
  renderEmailOverview();
  els.sendTestEmailButton.disabled = !state.emailEnabled;
  els.emailList.replaceChildren();
  if (!state.emailEnabled && state.emailNotifications.length === 0) {
    els.emailList.append(emptyInline({
      title: "Email is not configured",
      body: "Set SMTP environment variables to send no-reply notifications."
    }));
    renderPager(els.emailPager, state.emailPage, (offset) => {
      state.emailPage.offset = offset;
      loadEmailNotifications().catch(showError);
    });
    return;
  }
  if (state.emailNotifications.length === 0) {
    els.emailList.append(emptyInline({
      title: "No email notifications",
      body: "Queued, sent, and failed email notifications will appear here."
    }));
    renderPager(els.emailPager, state.emailPage, (offset) => {
      state.emailPage.offset = offset;
      loadEmailNotifications().catch(showError);
    });
    return;
  }
  for (const notification of state.emailNotifications) {
    els.emailList.append(emailNotificationRow(notification));
  }
  renderPager(els.emailPager, state.emailPage, (offset) => {
    state.emailPage.offset = offset;
    loadEmailNotifications().catch(showError);
  });
}

function renderEmailOverview() {
  const stats = state.emailStats || {};
  els.emailOverview.replaceChildren(
    emailStat("Enabled", state.emailEnabled ? "Yes" : "No"),
    emailStat("Pending", stats.pending || 0),
    emailStat("Sending", stats.sending || 0),
    emailStat("Failed", stats.failed || 0),
    emailStat("Sent", stats.sent || 0),
    emailStat("Batch delay", formatSeconds(state.emailBatchDelaySeconds)),
    emailStat("Last sent", stats.last_sent_at ? relativeTime(stats.last_sent_at) : "-")
  );
}

function renderMaintenance() {
  if (!els.maintenanceOverview) return;
  const info = state.maintenance || {};
  const uploads = info.uploads || state.meta.uploads || {};
  const email = info.email || {};
  const backup = info.backup || {};
  els.maintenanceOverview.replaceChildren(
    maintenanceItem("Version", info.version || "dev"),
    maintenanceItem("Started", info.started_at ? relativeTime(info.started_at) : "-"),
    maintenanceItem("Database", info.database_path || "-"),
    maintenanceItem("Uploads", info.upload_path || "-"),
    maintenanceItem("Backups", backup.path || "-"),
    maintenanceItem("Last backup", backup.latest_at ? `${relativeTime(backup.latest_at)} / ${backup.latest_name || "latest"}` : "No backups found"),
    maintenanceItem("Upload limit", `${formatBytes(uploads.max_size_bytes || 0)} / ${uploads.max_files || 0} files`),
    maintenanceItem("Email", email.enabled ? "Enabled" : "Disabled"),
    maintenanceItem("Public URL", email.public_url || "-"),
    maintenanceItem("Email delay", formatSeconds(Number(email.batch_delay_seconds || 0)))
  );
}

function maintenanceItem(label, value) {
  return el("div", { className: "maintenance-item" }, [
    el("span", {}, label),
    el("strong", {}, String(value))
  ]);
}

function emailStat(label, value) {
  return el("div", { className: "email-stat" }, [
    el("span", {}, label),
    el("strong", {}, String(value))
  ]);
}

function emailNotificationRow(notification) {
  const row = el("div", { className: "admin-row email-row", role: "button", tabindex: "0" });
  row.addEventListener("click", () => openEmailNotificationModal(notification));
  row.addEventListener("keydown", (event) => {
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      openEmailNotificationModal(notification);
    }
  });
  const status = badge(notification.status, emailStatusClass(notification.status));
  const retry = el("button", { className: "ghost-button", type: "button" }, "Retry");
  retry.hidden = notification.status !== "failed";
  retry.addEventListener("click", (event) => {
    event.stopPropagation();
    retryEmailNotification(notification.id).catch(showError);
  });
  row.append(
    el("div", { className: "admin-row-main" }, [
      el("strong", {}, notification.subject || notification.event),
      el("span", {}, `${notification.event} / ${notification.recipient_email}`)
    ]),
    status,
    el("span", { className: "muted" }, notification.last_error || emailNotificationTime(notification)),
    retry
  );
  return row;
}

function emailStatusClass(status) {
  if (status === "failed") return "priority-urgent";
  if (status === "sent") return "priority-low";
  return "priority-normal";
}

function emailNotificationTime(notification) {
  if (notification.status === "sent" && notification.sent_at) return `Sent ${relativeTime(notification.sent_at)}`;
  if (notification.status === "pending" && notification.next_attempt_at) {
    const nextAttempt = new Date(notification.next_attempt_at);
    if (!Number.isNaN(nextAttempt.getTime()) && nextAttempt.getTime() > Date.now()) {
      return `Sends ${shortDateFormatter.format(nextAttempt)}`;
    }
    return "Due now";
  }
  return relativeTime(notification.created_at);
}

function openEmailNotificationModal(notification) {
  const content = el("div", { className: "email-detail" }, [
    el("div", { className: "fact-list" }, [
      factBlock("Status", labelize(notification.status)),
      factBlock("Event", notification.event),
      factBlock("Recipient", notification.recipient_email),
      factBlock("Subject", notification.subject),
      factBlock("Created", relativeTime(notification.created_at)),
      factBlock("Attempts", notification.attempts || 0)
    ]),
    notification.last_error ? el("div", { className: "description" }, notification.last_error) : el("div"),
    el("h4", { className: "section-title" }, "Body"),
    el("pre", { className: "email-body" }, notification.body_text || "")
  ]);
  els.modalHost.open({
    title: "Email Notification",
    hideFooter: notification.status !== "failed",
    submitText: "Retry",
    content,
    onSubmit: notification.status === "failed" ? async () => {
      await retryEmailNotification(notification.id);
    } : null
  });
}

function renderAuditEvents() {
  if (els.auditSearchInput && els.auditSearchInput.value !== state.auditPage.q) {
    els.auditSearchInput.value = state.auditPage.q;
  }
  els.auditList.replaceChildren();
  if (state.auditEvents.length === 0) {
    els.auditList.append(emptyInline({
      title: "No audit events",
      body: "Security and admin actions will appear here."
    }));
    renderPager(els.auditPager, state.auditPage, (offset) => {
      state.auditPage.offset = offset;
      loadAuditEvents().catch(showError);
    });
    return;
  }
  for (const event of state.auditEvents) {
    const row = el("div", { className: "admin-row" });
    row.append(
      el("div", { className: "admin-row-main" }, [
        el("strong", {}, labelize(event.action)),
        el("span", {}, `${event.actor_username || "system"} / ${event.target_type}${event.target_name ? ` / ${event.target_name}` : ""}`)
      ]),
      el("span", { className: "muted" }, relativeTime(event.created_at))
    );
    els.auditList.append(row);
  }
  renderPager(els.auditPager, state.auditPage, (offset) => {
    state.auditPage.offset = offset;
    loadAuditEvents().catch(showError);
  });
}

function renderPager(container, page, onChange) {
  if (!container) return;
  const total = Number(page.total || 0);
  const limit = Number(page.limit || 25);
  const offset = Number(page.offset || 0);
  const start = total === 0 ? 0 : Math.min(offset + 1, total);
  const end = Math.min(offset + limit, total);
  const previous = el("button", { className: "ghost-button", type: "button" }, "Previous");
  const next = el("button", { className: "ghost-button", type: "button" }, "Next");
  previous.disabled = offset <= 0;
  next.disabled = offset + limit >= total;
  previous.addEventListener("click", () => onChange(Math.max(0, offset - limit)));
  next.addEventListener("click", () => onChange(offset + limit));
  container.replaceChildren(
    el("span", { className: "pager-summary" }, `${start}-${end} of ${total}`),
    previous,
    next
  );
}

function workflowEditor(issue, { creating = false } = {}) {
  const controls = [];
  if (!creating) {
    controls.push(ticketSelectField("Status", "status", issue.status, selectOptions(state.meta.statuses), { required: true }));
  }
  controls.push(
    ticketSelectField("Priority", "priority", issue.priority || "normal", selectOptions(state.meta.priorities), { required: true }),
    ticketSelectField("Assignee", "assignee", issue.assignee || "", assigneeOptions(issue.assignee))
  );
  const controlList = el("div", { className: "detail-controls" }, controls);
  return el("div", { className: "workflow-editor" }, [controlList]);
}

function ticketCreatePayload(data, fallbackProjectId) {
  const payload = {
    description: String(data.description || "").trim(),
    project_id: Number(data.project_id || fallbackProjectId),
    title: String(data.title || "").trim()
  };
  if (!isCustomer()) {
    payload.assignee = String(data.assignee || "").trim();
    payload.priority = String(data.priority || "normal").trim() || "normal";
  }
  return payload;
}

function ticketCreateRequestBody(payload, form) {
  const files = selectedTicketFiles(form);
  if (files.length === 0) return JSON.stringify(payload);
  const body = new FormData();
  for (const [key, value] of Object.entries(payload)) {
    if (value !== undefined && value !== null) body.append(key, value);
  }
  for (const file of files) body.append("attachments", file);
  return body;
}

function ticketUpdatePatch(issue, data) {
  const next = {
    title: String(data.title || "").trim(),
    description: String(data.description || "").trim(),
    status: String(data.status || "").trim(),
    priority: String(data.priority || "").trim(),
    assignee: String(data.assignee || "").trim()
  };
  const patch = {};
  if (next.title && next.title !== (issue.title || "")) patch.title = next.title;
  if (next.description !== (issue.description || "")) patch.description = next.description;
  if (next.status && next.status !== issue.status) patch.status = next.status;
  if (next.priority && next.priority !== issue.priority) patch.priority = next.priority;
  if (next.assignee !== (issue.assignee || "")) patch.assignee = next.assignee;
  return patch;
}

function ticketCommentPayload(issue, data) {
  const body = String(data.body || "").trim();
  if (!body) return null;
  return {
    body,
    visibility: canEditTicket(issue) ? String(data.visibility || "public") : "public"
  };
}

async function saveTicketChanges(issue, data, { editable, canComment }, form) {
  const patch = editable ? ticketUpdatePatch(issue, data) : {};
  const comment = canComment ? ticketCommentPayload(issue, data) : null;
  const files = selectedTicketFiles(form);
  if (Object.keys(patch).length === 0 && !comment && files.length === 0) return;
  if (files.length > 0) {
    const body = ticketUpdateRequestBody(patch, comment, data, files);
    const path = editable ? `/api/tickets/${issue.id}` : `/api/tickets/${issue.id}/comments`;
    await request(path, { method: editable ? "PATCH" : "POST", body });
  } else if (editable) {
    await request(`/api/tickets/${issue.id}`, { method: "PATCH", body: JSON.stringify({ ...patch, comment }) });
  } else if (comment) {
    await request(`/api/tickets/${issue.id}/comments`, { method: "POST", body: JSON.stringify(comment) });
  }
  await loadIssues();
}

function ticketUpdateRequestBody(patch, comment, data, files) {
  const body = new FormData();
  for (const [key, value] of Object.entries(patch)) {
    body.append(key, value);
  }
  if (comment || files.length > 0) {
    body.append("body", comment?.body || "");
    body.append("visibility", comment?.visibility || String(data.visibility || "public"));
  }
  for (const file of files) body.append("attachments", file);
  return body;
}

function selectedTicketFiles(form) {
  if (!form) return [];
  const files = [];
  form.querySelectorAll("input[type='file']").forEach((input) => {
    files.push(...Array.from(input.files || []));
  });
  return files;
}

function bindTicketSaveState({ issue, creating, projectId, editable, canComment }) {
  const update = () => {
    const data = Object.fromEntries(new FormData(els.modalHost.form).entries());
    const hasTitle = String(data.title || "").trim() !== "";
    if (creating) {
      els.modalHost.submitButton.disabled = !hasTitle || !Number(data.project_id || projectId);
      return;
    }
    const hasTicketChanges = editable && Object.keys(ticketUpdatePatch(issue, data)).length > 0;
    const hasFiles = selectedTicketFiles(els.modalHost.form).length > 0;
    const hasComment = canComment && (String(data.body || "").trim() !== "" || hasFiles);
    els.modalHost.submitButton.disabled = (editable && !hasTitle) || (!hasTicketChanges && !hasComment);
  };
  els.modalHost.bodyNode.querySelectorAll("[data-ticket-control]").forEach((control) => {
    control.addEventListener("input", update);
    control.addEventListener("change", update);
  });
  update();
}

function assigneeOptions(current = "") {
  const options = [{ value: "", label: "Unassigned" }];
  const seen = new Set([""]);
  for (const user of state.users) {
    if (user.disabled || !["admin", "staff"].includes(user.role)) continue;
    if (seen.has(user.username)) continue;
    seen.add(user.username);
    options.push({
      value: user.username,
      label: `${user.display_name || user.username} / ${user.username}`
    });
  }
  if (current && !seen.has(current)) {
    options.push({ value: current, label: `${current} / current` });
  }
  return options;
}

function comments(issue) {
  const list = el("div", { className: "comment-list" });
  if ((issue.comments || []).length === 0) {
    list.append(el("p", { className: "muted" }, "No replies yet."));
    return list;
  }
  for (const comment of issue.comments || []) {
    const item = el("div", { className: "comment" });
    item.classList.toggle("internal", comment.visibility === "internal");
    const nodes = [
      el("div", { className: "comment-head" }, [
        el("strong", {}, comment.author),
        el("span", { className: "comment-time" }, relativeTime(comment.created_at)),
        comment.visibility === "internal" ? badge("internal", "priority-normal") : el("span")
      ])
    ];
    if (String(comment.body || "").trim() !== "") {
      nodes.push(el("p", {}, comment.body));
    }
    nodes.push(attachmentList(comment.attachments || []));
    item.append(...nodes);
    list.append(item);
  }
  return list;
}

function commentComposer(issue) {
  const wrap = el("div", { className: "comment-form" });
  const body = document.createElement("textarea");
  body.name = "body";
  body.rows = 3;
  body.className = "comment-input";
  body.dataset.ticketControl = "true";
  body.placeholder = "Write a reply";
  const visibility = document.createElement("select");
  visibility.name = "visibility";
  visibility.dataset.ticketControl = "true";
  visibility.append(new Option("Public reply", "public"), new Option("Internal note", "internal"));
  visibility.value = "public";
  const attachments = ticketAttachmentField("Attachments", "attachments");
  if (canEditTicket(issue)) {
    wrap.append(body, attachments, el("div", { className: "comment-actions" }, [visibility]));
    return wrap;
  }
  wrap.append(body, attachments);
  return wrap;
}

function attachmentList(attachments) {
  if (!attachments || attachments.length === 0) return el("div", { className: "attachment-list empty" });
  const list = el("div", { className: "attachment-list" });
  for (const attachment of attachments) {
    list.append(el("a", {
      className: "attachment-link",
      href: `/api/attachments/${attachment.id}`,
      download: attachment.filename
    }, [
      el("span", { className: "attachment-name" }, attachment.filename || "Attachment"),
      el("span", { className: "attachment-size" }, formatBytes(attachment.size_bytes || 0))
    ]));
  }
  return list;
}

function formatBytes(value) {
  const bytes = Number(value || 0);
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

async function updateUser(id, patch) {
  await request(`/api/users/${id}`, { method: "PATCH", body: JSON.stringify(patch) });
  await loadUsers();
}

async function deleteUser(id) {
  await request(`/api/users/${id}`, { method: "DELETE" });
  await loadUsers();
}

async function deleteToken(id) {
  await request(`/api/tokens/${id}`, { method: "DELETE" });
  await loadTokens();
}

async function upsertMember(input) {
  await request(`/api/projects/${state.selectedProjectId}/members`, { method: "POST", body: JSON.stringify(input) });
  await loadMembers();
}

async function deleteMember(userId) {
  await request(`/api/projects/${state.selectedProjectId}/members/${userId}`, { method: "DELETE" });
  await loadMembers();
}

async function deleteWebhook(id) {
  await request(`/api/webhooks/${id}`, { method: "DELETE" });
  if (isAdmin()) await loadGlobalWebhooks();
  if (canManageProject()) await loadProjectWebhooks();
}

async function testWebhook(id) {
  const delivery = await request(`/api/webhooks/${id}/test`, { method: "POST" });
  console.info(delivery.error ? "Webhook test failed" : "Webhook test sent", delivery);
  if (isAdmin()) await loadGlobalWebhooks();
  if (canManageProject()) await Promise.all([loadProjectWebhooks(), loadProjectDeliveries()]);
}

function selectOptions(values) {
  return values.map((value) => ({ value, label: labelize(value) }));
}

function openIssueModal() {
  openTicketModal();
}

function openProjectModal() {
  els.modalHost.open({
    title: "New Product",
    submitText: "Create",
    fields: [
      { group: [
        { name: "key", label: "Key", required: true, maxlength: 16, placeholder: "OPS", autocomplete: "off" },
        { name: "name", label: "Name", required: true, placeholder: "Operations", autocomplete: "off" }
      ] },
      { name: "description", label: "Description", type: "textarea", rows: 3 }
    ],
    onSubmit: async (data) => {
      await request("/api/projects", { method: "POST", body: JSON.stringify(data) });
      await loadProjects();
    }
  });
}

function openUserModal(user = null) {
  const editing = Boolean(user);
  const values = editing ? {
    display_name: user.display_name || "",
    email: user.email || "",
    role: user.role,
    disabled: Boolean(user.disabled)
  } : { role: "staff" };
  const fields = editing ? [
    { group: [
      { name: "display_name", label: "Display name", autocomplete: "off" },
      { name: "email", label: "Email", type: "email", autocomplete: "email" }
    ] },
    { group: [
      { name: "role", label: "Role", type: "select", options: selectOptions(state.meta.roles), disabled: user.id === state.user.id }
    ] },
    { name: "disabled", label: "Disabled", type: "checkbox", disabled: user.id === state.user.id }
  ] : [
    { group: [
      { name: "username", label: "Username", required: true, maxlength: 48, autocomplete: "off" },
      { name: "display_name", label: "Display name", autocomplete: "off" }
    ] },
    { name: "email", label: "Email", type: "email", autocomplete: "email" },
    { name: "role", label: "Role", type: "select", options: selectOptions(state.meta.roles), value: "staff" }
  ];

  els.modalHost.open({
    title: editing ? `Edit ${user.username}` : "New Account",
    submitText: editing ? "Save" : "Create & Send Setup",
    values,
    fields,
    onSubmit: async (data) => {
      if (!editing) {
        const created = await request("/api/users", { method: "POST", body: JSON.stringify(data) });
        await loadUsers();
        window.setTimeout(() => openAccountLinkResult(created, "setup"), 0);
        return;
      }
      const patch = {
        display_name: data.display_name || "",
        email: data.email || ""
      };
      if (user.id !== state.user.id) {
        patch.role = data.role;
        patch.disabled = Boolean(data.disabled);
      }
      await updateUser(user.id, patch);
    }
  });
}

async function resetUserPassword(user) {
  const payload = await request(`/api/users/${user.id}/password-reset`, { method: "POST" });
  await loadUsers();
  openAccountLinkResult(payload, "reset");
}

function openAccountLinkResult(payload, purpose = "setup") {
  const link = payload.account_link || {};
  const userLabel = payload.display_name || payload.username || "Account";
  const title = purpose === "reset" ? `Password Reset for ${userLabel}` : `Setup Link for ${userLabel}`;
  const linkInput = el("input", {
    readonly: "readonly",
    value: link.url || ""
  });
  const copy = el("button", { type: "button" }, "Copy");
  copy.addEventListener("click", async () => {
    await copyText(link.url || "");
    copy.textContent = "Copied";
    window.setTimeout(() => {
      copy.textContent = "Copy";
    }, 1200);
  });
  const hasEmail = Boolean(payload.email);
  const statusText = !hasEmail
    ? "This account has no email address. Share this one-time link manually."
    : link.email_enabled
    ? link.email_queued
      ? "Pappice queued the email. Keep this link as a fallback for manual delivery."
      : "Email is configured, but the message could not be queued. Use the link below."
    : "Email is not configured. Share this one-time link manually.";
  const content = el("div", { className: "link-result" }, [
    el("p", {}, statusText),
    el("div", { className: "copy-row" }, [linkInput, copy]),
    el("div", { className: "link-meta" }, [
      el("span", {}, ["Account: ", el("strong", {}, userLabel)]),
      el("span", {}, ["Username: ", el("strong", {}, payload.username || "")]),
      link.expires_at ? el("span", {}, ["Expires: ", el("strong", {}, fullDateFormatter.format(new Date(link.expires_at)))]) : el("span")
    ])
  ]);
  els.modalHost.open({
    title,
    content,
    hideFooter: true
  });
}

async function copyText(value) {
  if (!value) return;
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(value);
    return;
  }
  const area = document.createElement("textarea");
  area.value = value;
  area.style.position = "fixed";
  area.style.opacity = "0";
  document.body.append(area);
  area.select();
  document.execCommand("copy");
  area.remove();
}

function openProfileModal() {
  els.modalHost.open({
    title: "Profile",
    submitText: "Save",
    values: {
      display_name: state.user?.display_name || "",
      email: state.user?.email || ""
    },
    fields: [
      { name: "display_name", label: "Display name", autocomplete: "name" },
      { name: "email", label: "Email", type: "email", autocomplete: "email" }
    ],
    onSubmit: async (data) => {
      state.user = await request("/api/me", {
        method: "PATCH",
        body: JSON.stringify({
          display_name: data.display_name || "",
          email: data.email || ""
        })
      });
      renderProfileMenu();
      if (!isCustomer()) await loadUsers();
    }
  });
}

function openPasswordModal() {
  els.modalHost.open({
    title: "Change Password",
    submitText: "Update Password",
    fields: [
      { name: "current_password", label: "Current password", type: "password", required: true, autocomplete: "current-password" },
      { name: "new_password", label: "New password", type: "password", required: true, minlength: 8, autocomplete: "new-password" },
      { name: "confirm_password", label: "Confirm new password", type: "password", required: true, minlength: 8, autocomplete: "new-password" }
    ],
    onSubmit: async (data) => {
      if (data.new_password !== data.confirm_password) {
        throw new Error("New passwords do not match");
      }
      const payload = await request("/api/me/password", {
        method: "POST",
        body: JSON.stringify({
          current_password: data.current_password,
          new_password: data.new_password
        })
      });
      state.user = payload.user || state.user;
      state.csrf = payload.csrf_token || state.csrf;
      renderProfileMenu();
    }
  });
}

function openTokenModal() {
  els.modalHost.open({
    title: "Create API Token",
    submitText: "Create",
    fields: [{ name: "name", label: "Name", placeholder: "CLI", autocomplete: "off" }],
    onSubmit: async (data) => {
      const payload = await request("/api/tokens", { method: "POST", body: JSON.stringify(data) });
      els.tokenResult.textContent = payload.value;
      await loadTokens();
    }
  });
}

function openTestEmailModal() {
  els.modalHost.open({
    title: "Send Test Email",
    submitText: "Queue Test",
    fields: [
      { name: "email", label: "Recipient", type: "email", value: state.user?.email || "", required: true, autocomplete: "email" }
    ],
    onSubmit: async (data) => {
      await request("/api/email-notifications/test", { method: "POST", body: JSON.stringify({ email: data.email }) });
      await loadEmailNotifications();
    }
  });
}

async function retryEmailNotification(id) {
  await request(`/api/email-notifications/${id}/retry`, { method: "POST" });
  await loadEmailNotifications();
}

function openMemberModal() {
  const users = state.users
    .filter((user) => !user.disabled)
    .map((user) => ({ value: String(user.id), label: `${user.display_name || user.username} / ${user.username}` }));
  els.modalHost.open({
    title: "Add Product Member",
    submitText: "Save",
    fields: [
      { name: "user_id", label: "Account", type: "select", options: users },
      { name: "role", label: "Role", type: "select", options: selectOptions(state.meta.projectRoles), value: "viewer" }
    ],
    onSubmit: async (data) => {
      await upsertMember({ user_id: Number(data.user_id), role: data.role });
    }
  });
}

function openWebhookModal(scope) {
  els.modalHost.open({
    title: scope === "global" ? "New Global Webhook" : "New Product Webhook",
    submitText: "Create",
    fields: [
      { group: [
        { name: "name", label: "Name", required: true, autocomplete: "off" },
        { name: "url", label: "URL", required: true, placeholder: "https://example.test/hook", autocomplete: "off" }
      ] },
      { name: "events", label: "Events", placeholder: "ticket.created, ticket.updated, ticket.commented" },
      { name: "enabled", label: "Enabled", type: "checkbox", checked: true }
    ],
    onSubmit: async (data) => {
      const payload = { ...data, events: splitList(data.events), enabled: Boolean(data.enabled) };
      if (scope === "global") {
        await request("/api/webhooks", { method: "POST", body: JSON.stringify(payload) });
        await loadGlobalWebhooks();
        return;
      }
      await request(`/api/projects/${state.selectedProjectId}/webhooks`, { method: "POST", body: JSON.stringify(payload) });
      await loadProjectWebhooks();
    }
  });
}

async function submitAuthForm(form, action) {
  clearAuthError();
  const submit = form.querySelector("button[type='submit']");
  if (submit) submit.disabled = true;
  try {
    await action();
  } catch (error) {
    showAuthError(error);
  } finally {
    if (submit) submit.disabled = false;
  }
}

function bindEvents() {
  const runEmailSearch = debounce(() => {
    state.emailPage.q = els.emailSearchInput.value.trim();
    state.emailPage.offset = 0;
    loadEmailNotifications().catch(showError);
  }, 250);
  const runAuditSearch = debounce(() => {
    state.auditPage.q = els.auditSearchInput.value.trim();
    state.auditPage.offset = 0;
    loadAuditEvents().catch(showError);
  }, 250);

  els.setupForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    await submitAuthForm(els.setupForm, async () => {
      const form = new FormData(els.setupForm);
      const payload = await request("/api/setup", { method: "POST", body: JSON.stringify(formObject(form)) });
      state.csrf = payload.csrf_token || "";
      els.setupForm.reset();
      await loadSession();
    });
  });

  els.loginForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    await submitAuthForm(els.loginForm, async () => {
      const form = new FormData(els.loginForm);
      const payload = await request("/api/login", { method: "POST", body: JSON.stringify(formObject(form)) });
      state.csrf = payload.csrf_token || "";
      els.loginForm.reset();
      await loadSession();
    });
  });

  els.accountLinkForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (!state.accountLink?.token) return;
    await submitAuthForm(els.accountLinkForm, async () => {
      const form = new FormData(els.accountLinkForm);
      const payload = await request(`/api/account-links/${encodeURIComponent(state.accountLink.token)}`, {
        method: "POST",
        body: JSON.stringify(formObject(form))
      });
      state.csrf = payload.csrf_token || "";
      state.user = payload.user || null;
      state.accountLink = null;
      els.accountLinkForm.reset();
      window.history.replaceState(null, "", "/");
      await enterApp();
    });
  });

  els.logoutButton.addEventListener("click", async () => {
    closeProfileMenu();
    try {
      await request("/api/logout", { method: "POST" });
      showAuth("login");
    } catch (error) {
      showError(error);
    }
  });
  els.profileEditButton.addEventListener("click", () => {
    closeProfileMenu();
    openProfileModal();
  });
  els.changePasswordButton.addEventListener("click", () => {
    closeProfileMenu();
    openPasswordModal();
  });
  els.profileButton.addEventListener("click", (event) => {
    event.stopPropagation();
    toggleProfileMenu();
  });
  document.addEventListener("click", (event) => {
    if (!els.profileMenu.hidden && !els.profileMenu.contains(event.target)) closeProfileMenu();
  });
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") closeProfileMenu();
  });
  window.addEventListener("popstate", () => applyRouteFromPath().catch(showError));

  els.issuesTab.addEventListener("click", () => switchView("issues"));
  els.projectTab.addEventListener("click", () => switchView("project"));
  els.adminTab.addEventListener("click", () => switchView("admin"));
  for (const button of els.adminSectionButtons) {
    button.addEventListener("click", () => switchAdminSection(button.getAttribute("data-admin-section")).catch(showError));
  }
  for (const button of els.productSectionButtons) {
    button.addEventListener("click", () => switchProductSection(button.getAttribute("data-product-section")).catch(showError));
  }
  els.newIssueButton.addEventListener("click", () => openIssueModal());
  els.modalHost.addEventListener("pappice-modal-error", (event) => showError(event.detail));
  document.querySelectorAll("[data-sort-key]").forEach((button) => {
    button.addEventListener("click", () => setIssueSort(button.dataset.sortKey));
  });

  els.searchInput.addEventListener("input", debounce(() => {
    state.filters.q = els.searchInput.value.trim();
    loadIssues().catch(showError);
  }, 180));
  els.productFilter.addEventListener("change", async () => {
    state.selectedProjectId = Number(els.productFilter.value) || null;
    state.selectedId = null;
    renderProductFilter();
    updateProjectActions();
    await loadIssues();
    if (canManageProject()) await loadProjectAdmin();
  });
  els.assigneeFilter.addEventListener("change", () => {
    state.filters.assignee = els.assigneeFilter.value.trim();
    loadIssues().catch(showError);
  });
  els.emailSearchInput.addEventListener("input", runEmailSearch);
  els.emailStatusFilter.addEventListener("change", () => {
    state.emailPage.status = els.emailStatusFilter.value;
    state.emailPage.offset = 0;
    loadEmailNotifications().catch(showError);
  });
  els.auditSearchInput.addEventListener("input", runAuditSearch);
  els.addProjectButton.addEventListener("click", () => openProjectModal());
  els.addUserButton.addEventListener("click", () => openUserModal());
  els.createTokenButton.addEventListener("click", () => openTokenModal());
  els.sendTestEmailButton.addEventListener("click", () => openTestEmailModal());
  els.addMemberButton.addEventListener("click", () => openMemberModal());
  els.addGlobalWebhookButton.addEventListener("click", () => openWebhookModal("global"));
  els.addWebhookButton.addEventListener("click", () => openWebhookModal("project"));
  els.appAlertClose.addEventListener("click", clearAppAlert);
}

function switchView(view, options = {}) {
  if (view === "admin" && !isAdmin()) return;
  if (view === "project" && !canManageProject()) return;
  state.view = view;
  els.issueView.hidden = view !== "issues";
  els.adminView.hidden = view !== "admin";
  els.projectView.hidden = view !== "project";
  els.issuesTab.classList.toggle("active", view === "issues");
  els.adminTab.classList.toggle("active", view === "admin");
  els.projectTab.classList.toggle("active", view === "project");
  if (options.updateRoute !== false) updateRoutePath({ replace: Boolean(options.replaceRoute) });
  if (view === "admin") loadAdmin().catch(showError);
  if (view === "project") {
    renderProjectContext();
    loadProjectAdmin().catch(showError);
  }
}

async function refreshCurrent() {
  if (state.view === "admin") {
    await loadAdmin();
    return;
  }
  if (state.view === "project") {
    await loadProjectAdmin();
    return;
  }
  await loadProjects();
}

function currentProject(projectId = state.selectedProjectId) {
  return state.projects.find((project) => project.id === projectId) || null;
}

function selectedIssue() {
  return state.issues.find((issue) => issue.id === state.selectedId) || null;
}

function isAdmin() {
  return state.user?.role === "admin";
}

function isCustomer() {
  return state.user?.role === "customer";
}

function projectRole(projectId = state.selectedProjectId) {
  return currentProject(projectId)?.role || "";
}

function canManageProject(projectId = state.selectedProjectId) {
  return Boolean(projectId) && !isCustomer() && (isAdmin() || projectRole(projectId) === "owner");
}

function canCreateIssue(projectId = state.selectedProjectId) {
  if (!projectId) {
    return state.projects.some((project) => canCreateIssue(project.id));
  }
  return isAdmin() || ["owner", "agent", "customer"].includes(projectRole(projectId));
}

function canCommentTicket(issue = null) {
  return Boolean(issue?.project_id) && canCreateIssue(issue.project_id);
}

function canEditTicket(issue = null) {
  const projectId = issue?.project_id || state.selectedProjectId;
  return Boolean(projectId) && !isCustomer() && (isAdmin() || ["owner", "agent"].includes(projectRole(projectId)));
}

function showAuthError(error) {
  const message = userMessage(error);
  els.authError.textContent = message;
  els.authError.hidden = false;
}

function clearAuthError() {
  els.authError.textContent = "";
  els.authError.hidden = true;
}

function showAppAlert(message) {
  window.clearTimeout(appAlertTimer);
  els.appAlertText.textContent = message;
  els.appAlert.hidden = false;
  appAlertTimer = window.setTimeout(clearAppAlert, 8000);
}

function clearAppAlert() {
  window.clearTimeout(appAlertTimer);
  appAlertTimer = 0;
  els.appAlert.hidden = true;
  els.appAlertText.textContent = "";
}

function userMessage(error) {
  if (typeof error === "string") return error;
  const raw = String(error?.message || "Request failed").trim();
  if (!raw || raw === "Failed to fetch") {
    return "Pappice could not be reached. Check the connection and try again.";
  }
  if (raw.startsWith("validation failed: ")) {
    return raw.replace("validation failed: ", "");
  }
  if (error?.status === 403) return raw || "You do not have permission to do that.";
  if (error?.status === 404) return raw && raw !== "not found" ? raw : "The requested item was not found. Refresh the page and try again.";
  if (error?.status === 409) return raw || "This action conflicts with the current state.";
  if (error?.status === 429) return raw || "Too many attempts. Try again later.";
  if (error?.status >= 500) return "Pappice hit an internal error. Try again, then check the server logs if it persists.";
  return raw;
}

function showError(error) {
  console.error(error);
  if (error.status === 401) {
    showAuth("login");
    showAuthError("Your session expired. Sign in again.");
    return;
  }
  showAppAlert(userMessage(error));
}

boot().catch(showError);
