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

const DEFAULT_ADMIN_SECTION = "accounts";
const ADMIN_SECTIONS = [DEFAULT_ADMIN_SECTION, "tokens", "webhooks", "email", "maintenance", "audit"];
const DEFAULT_PRODUCT_SECTION = "members";
const PRODUCT_SECTIONS = [DEFAULT_PRODUCT_SECTION, "webhooks", "deliveries"];
const DEFAULT_TICKET_STATUSES = ["new", "assigned"];
const DRAFT_TICKET_ID = "draft";
const TICKET_AUTOSAVE_DELAY_MS = 450;

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
  adminSection: DEFAULT_ADMIN_SECTION,
  productSection: DEFAULT_PRODUCT_SECTION,
  productMode: "index",
  productDetailId: null,
  ticketProjectId: null,
  ticketDraft: null,
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
    statuses: [...DEFAULT_TICKET_STATUSES],
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
  productIndexPanel: document.querySelector("#productIndexPanel"),
  productIndexList: document.querySelector("#productIndexList"),
  productDetailView: document.querySelector("#productDetailView"),
  projectContextTitle: document.querySelector("#projectContextTitle"),
  projectContextMeta: document.querySelector("#projectContextMeta"),
  issueList: document.querySelector("#issueList"),
  ticketDetailPane: document.querySelector("#ticketDetailPane"),
  searchInput: document.querySelector("#searchInput"),
  productFilter: document.querySelector("#productFilter"),
  assigneeFilter: document.querySelector("#assigneeFilter"),
  issueSortSelect: document.querySelector("#issueSortSelect"),
  statusFilterList: document.querySelector("#statusFilterList"),
  addProjectButton: document.querySelector("#addProjectButton"),
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
      const section = validAdminSection(parts[1]) ? parts[1] : DEFAULT_ADMIN_SECTION;
      return { view: "admin", section, normalize: trailingSlash || parts.length !== 2 || section !== parts[1] };
    }
    case "products": {
      if (parts.length === 1) {
        return { view: "project", mode: "index", normalize: trailingSlash };
      }
      const projectId = Number(parts[1] || 0);
      const section = validProductSection(parts[2]) ? parts[2] : DEFAULT_PRODUCT_SECTION;
      return {
        view: "project",
        mode: "detail",
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
    if (route.mode === "index") {
      if (canAccessProductsView()) {
        state.productMode = "index";
        state.productDetailId = null;
        switchView("project", { updateRoute: false });
        updateRoutePath({ replace: route.normalize });
        return;
      }
    } else {
      const project = currentProject(route.projectId);
      if (project && canManageProject(project.id)) {
        state.productMode = "detail";
        state.productDetailId = project.id;
        state.productSection = route.section;
        updateProjectActions();
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
  if (state.view === "project" && state.productMode === "detail" && state.productDetailId) {
    return `/products/${state.productDetailId}/${state.productSection}`;
  }
  if (state.view === "project") return "/products";
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
  state.selectedId = null;
  state.ticketDraft = null;
  document.body.classList.remove("app-mode");
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
  if (mode === "login") {
    els.loginForm.insertBefore(els.authError, els.loginForm.children[1] || null);
  } else if (mode === "setup") {
    els.setupForm.insertBefore(els.authError, els.setupForm.children[1] || null);
  } else {
    els.accountLinkForm.insertBefore(els.authError, els.accountLinkForm.children[1] || null);
  }
}

function showApp() {
  document.body.classList.add("app-mode");
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
  if (state.ticketProjectId && !state.projects.some((project) => project.id === state.ticketProjectId)) {
    state.ticketProjectId = null;
  }
  if (state.productDetailId && !state.projects.some((project) => project.id === state.productDetailId)) {
    state.productDetailId = null;
    state.productMode = "index";
  }
  if (state.ticketDraft && !state.projects.some((project) => project.id === state.ticketDraft.project_id)) {
    discardTicketDraft({ render: false });
  }
  renderProductFilter();
  renderProductIndex();
  updateProjectActions();
  await loadIssues();
}

async function loadIssues({ renderDetail = true } = {}) {
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
  const projectID = state.ticketProjectId || null;
  if (projectID) params.set("project_id", String(projectID));
  const countParams = new URLSearchParams();
  if (projectID) countParams.set("project_id", String(projectID));
  const [payload, countsPayload] = await Promise.all([
    request(`/api/tickets?${params.toString()}`),
    request(`/api/tickets?${countParams.toString()}`)
  ]);
  if (projectID !== (state.ticketProjectId || null)) return;
  state.issues = payload.tickets || [];
  state.issueCounts = countIssues(countsPayload.tickets || []);
  const previousSelectedId = state.selectedId;
  const draftSelected = state.selectedId === DRAFT_TICKET_ID && state.ticketDraft;
  if (state.selectedId && !draftSelected && !state.issues.some((issue) => issue.id === state.selectedId)) {
    state.selectedId = null;
  }
  if (renderDetail || state.selectedId !== previousSelectedId) {
    renderIssuesView();
  } else {
    renderCounts();
    renderSortHeaders();
    renderIssueList();
  }
}

async function loadAdmin() {
  renderAdminSections();
  await loadAdminSection(state.adminSection);
}

async function loadAdminSection(section) {
  switch (section) {
    case DEFAULT_ADMIN_SECTION:
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
      state.adminSection = DEFAULT_ADMIN_SECTION;
      renderAdminSections();
      await loadUsers();
  }
}

async function loadProjectAdmin() {
  renderProductsView();
  if (state.productMode !== "detail" || !state.productDetailId || !canManageProject(state.productDetailId)) return;
  await loadProductSection(state.productSection);
}

async function loadProductSection(section) {
  switch (validProductSection(section) ? section : DEFAULT_PRODUCT_SECTION) {
    case DEFAULT_PRODUCT_SECTION:
      await Promise.all([loadUsers(), loadMembers()]);
      return;
    case "webhooks":
      await loadProjectWebhooks();
      return;
    case "deliveries":
      await loadProjectDeliveries();
      return;
    default:
      state.productSection = DEFAULT_PRODUCT_SECTION;
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
  const payload = await request(`/api/projects/${state.productDetailId}/members`);
  state.members = payload.members || [];
  renderMembers();
}

async function loadProjectWebhooks() {
  const payload = await request(`/api/projects/${state.productDetailId}/webhooks`);
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
  const payload = await request(`/api/projects/${state.productDetailId}/webhook-deliveries`);
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
  els.productFilter.value = state.ticketProjectId ? String(state.ticketProjectId) : "";
}

function updateProjectActions() {
  els.adminTab.hidden = !isAdmin();
  els.projectTab.hidden = !canAccessProductsView();
  els.addProjectButton.hidden = !isAdmin();
  els.newIssueButton.hidden = !canCreateIssue();
  if (state.view === "admin" && !isAdmin()) switchView("issues");
  if (state.view === "project" && !canAccessProductsView()) switchView("issues");
}

function renderIssuesView() {
  renderCounts();
  renderSortHeaders();
  renderIssueList();
  renderTicketDetail();
}

function renderCounts() {
  const counts = state.issueCounts;
  els.statusFilterList.replaceChildren();
  for (const status of state.meta.statuses) {
    const active = state.filters.statuses.includes(status);
    const button = el("button", { className: "status-folder", type: "button", "data-filter-status": status }, [
      el("span", { className: "status-folder-label" }, labelize(status)),
      el("span", { className: "status-folder-count" }, String(counts[status] || 0))
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
  const defaults = DEFAULT_TICKET_STATUSES.filter((status) => state.meta.statuses.includes(status));
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
  const draft = state.ticketDraft;
  const issues = sortedIssues();
  if (issues.length === 0 && !draft) {
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
      onAction: canCreateIssue() ? openTicketDraft : null
    }));
    return;
  }
  for (const issue of draft ? [draft, ...issues] : issues) {
    const row = document.createElement("button");
    row.type = "button";
    row.className = "issue-row";
    row.classList.toggle("draft", issue.id === DRAFT_TICKET_ID);
    row.classList.toggle("active", issue.id === state.selectedId);
    row.addEventListener("click", () => {
      state.selectedId = state.selectedId === issue.id ? null : issue.id;
      renderIssuesView();
    });
    const product = issueProductParts(issue);
    row.append(
      el("span", { className: "issue-row-product" }, product.key),
      el("span", { className: "issue-row-time" }, relativeTime(issue.updated_at)),
      el("span", { className: "issue-row-title" }, issue.title || (issue.id === DRAFT_TICKET_ID ? "Untitled draft" : "Untitled ticket")),
      el("span", { className: "issue-row-summary" }, ticketSummary(issue)),
      el("span", { className: "issue-row-footer" }, [
        badge(issue.status, `status-${issue.status}`),
        badge(issue.priority, `priority-${issue.priority}`),
        el("span", {}, issue.assignee || issue.requester || "Unassigned")
      ])
    );
    els.issueList.append(row);
  }
}

function ticketSummary(issue) {
  if (issue.id === DRAFT_TICKET_ID) return issue.description || "Draft ticket";
  const latest = [...(issue.comments || [])].reverse().find((comment) => String(comment.body || "").trim() !== "");
  return latest?.body || issue.description || "No description.";
}

function renderSortHeaders() {
  if (els.issueSortSelect) {
    els.issueSortSelect.value = `${state.sort.key}:${state.sort.dir}`;
  }
  for (const button of document.querySelectorAll("[data-sort-key]")) {
    const active = button.dataset.sortKey === state.sort.key;
    button.classList.toggle("active", active);
    button.classList.toggle("desc", active && state.sort.dir === "desc");
    button.classList.toggle("asc", active && state.sort.dir === "asc");
    button.setAttribute("aria-sort", active ? (state.sort.dir === "desc" ? "descending" : "ascending") : "none");
  }
}

function setIssueSortValue(value) {
  const [key, dir] = String(value || "").split(":");
  if (!key) return;
  state.sort.key = key;
  state.sort.dir = dir === "asc" ? "asc" : "desc";
  renderIssuesView();
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

function renderTicketDetail() {
  if (!els.ticketDetailPane) return;
  const issue = selectedIssue();
  els.ticketDetailPane.replaceChildren();
  if (!issue) {
    els.ticketDetailPane.append(emptyState({
      title: "No ticket selected",
      body: state.issues.length === 0
        ? "Tickets matching the current view will appear here."
        : "Select a ticket from the list to read the conversation."
    }));
    return;
  }

  const creating = issue.id === DRAFT_TICKET_ID;
  const editable = creating || canEditTicket(issue);
  const canComment = !creating && canCommentTicket(issue);
  const form = el("form", { className: "ticket-detail-form" });
  const creatableProjects = creating ? state.projects.filter((project) => canCreateIssue(project.id)) : [];
  form.append(ticketDetailContent({
    issue,
    creating,
    editable,
    canComment,
    projectId: issue.project_id,
    creatableProjects
  }));

  if (creating) {
    const submit = el("button", { className: "primary-button", type: "submit" }, "Create Ticket");
    const actions = [submit];
    const discard = el("button", { className: "ghost-button", type: "button" }, "Discard");
    discard.addEventListener("click", () => discardTicketDraft());
    actions.unshift(discard);
    form.append(el("div", { className: "ticket-savebar" }, actions));
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      submit.disabled = true;
      submit.setAttribute("aria-busy", "true");
      try {
        const data = Object.fromEntries(new FormData(form).entries());
        await createTicketFromDraft(data, form, issue.project_id);
      } catch (error) {
        showError(error);
      } finally {
        submit.disabled = false;
        submit.removeAttribute("aria-busy");
      }
    });
    bindTicketSaveState({ projectId: issue.project_id, form, submitButton: submit });
    bindDraftState(form);
  } else {
    form.addEventListener("submit", (event) => event.preventDefault());
    if (editable) bindTicketAutosave(form, issue);
    if (canComment) bindCommentComposer(form, issue);
  }

  els.ticketDetailPane.append(form);
}

function bindDraftState(form) {
  const update = () => {
    if (!state.ticketDraft) return;
    const data = Object.fromEntries(new FormData(form).entries());
    const projectId = Number(data.project_id || state.ticketDraft.project_id);
    const project = currentProject(projectId);
    state.ticketDraft = {
      ...state.ticketDraft,
      project_id: projectId,
      project_key: project?.key || state.ticketDraft.project_key,
      project: project?.key || state.ticketDraft.project,
      title: String(data.title || "").trim(),
      description: String(data.description || "").trim(),
      priority: String(data.priority || state.ticketDraft.priority || "normal").trim() || "normal",
      assignee: String(data.assignee || "").trim(),
      updated_at: new Date().toISOString()
    };
    renderIssueList();
  };
  form.querySelectorAll("[data-ticket-control]").forEach((control) => {
    control.addEventListener("input", update);
    control.addEventListener("change", update);
  });
  update();
}

function openTicketDraft() {
  const creatableProjects = state.projects.filter((project) => canCreateIssue(project.id));
  const projectId = initialTicketProjectId(creatableProjects);
  if (!projectId) {
    showError(new Error("Create a product before adding tickets"));
    return;
  }
  if (!state.ticketDraft) {
    const project = currentProject(projectId);
    const now = new Date().toISOString();
    state.ticketDraft = {
      id: DRAFT_TICKET_ID,
      project_id: projectId,
      project_key: project?.key || "",
      project: project?.key || "",
      title: "",
      description: "",
      status: "new",
      priority: "normal",
      assignee: "",
      requester: state.user?.display_name || state.user?.username || "",
      requester_name: state.user?.display_name || state.user?.username || "",
      requester_email: state.user?.email || "",
      source: isCustomer() ? "portal" : "staff",
      comments: [],
      attachments: [],
      created_at: now,
      updated_at: now
    };
  }
  state.selectedId = DRAFT_TICKET_ID;
  renderIssuesView();
}

function discardTicketDraft({ render = true } = {}) {
  state.ticketDraft = null;
  if (state.selectedId === DRAFT_TICKET_ID) {
    state.selectedId = null;
  }
  if (render) renderIssuesView();
}

async function createTicketFromDraft(data, form, fallbackProjectId) {
  const payload = ticketCreatePayload(data, fallbackProjectId);
  const body = ticketCreateRequestBody(payload, form);
  const created = await request(`/api/projects/${payload.project_id}/tickets`, { method: "POST", body });
  state.ticketDraft = null;
  state.ticketProjectId = payload.project_id;
  const createdStatus = created.status || "new";
  if (createdStatus && !state.filters.statuses.includes(createdStatus)) {
    state.filters.statuses = [createdStatus];
  }
  state.selectedId = created.id;
  await loadProjects();
}

function ticketDetailContent({ issue, creating, editable, canComment, projectId, creatableProjects }) {
  const wrap = el("div", { className: "ticket-detail" });
  wrap.classList.toggle("ticket-create", creating);
  const header = el("div", { className: "detail-header" });
  if (editable) {
    header.append(ticketTextField("", "title", issue?.title || "", {
      autocomplete: "off",
      className: "ticket-title-input",
      maxlength: 160,
      placeholder: "Brief summary",
      required: true
    }));
  } else {
    header.append(el("h3", {}, issue.title));
  }

  const main = el("section", { className: "ticket-main" });
  if (creating) {
    main.append(ticketTextareaField("Message", "description", issue?.description || "", {
      className: "ticket-description-input",
      placeholder: "Describe the request, impact, and useful context.",
      rows: 6
    }));
    main.append(ticketAttachmentField("Attachments", "attachments"));
  }
  if (!creating) {
    const conversation = comments(issue);
    const composer = canComment ? commentComposer(issue) : el("div");
    main.append(conversation, composer);
    const attachmentInput = composer.querySelector(".attachment-input");
    if (attachmentInput) bindAttachmentDropZone(main, attachmentInput, "conversation-drop-active");
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

  const conversationPanel = el("section", { className: "ticket-conversation-panel" }, [header, main]);
  wrap.append(el("div", { className: "ticket-detail-grid" }, [conversationPanel, side]));
  return wrap;
}

function initialTicketProjectId(creatableProjects) {
  if (state.ticketProjectId && creatableProjects.some((project) => project.id === state.ticketProjectId)) {
    return state.ticketProjectId;
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
  const preview = el("div", { className: "attachment-preview empty" });
  const trigger = el("button", { className: "attachment-trigger", type: "button" }, [
    el("span", { className: "attachment-trigger-icon", "aria-hidden": "true" })
  ]);
  trigger.setAttribute("aria-label", "Attach files");
  trigger.title = "Attach files";
  const picker = el("div", { className: "attachment-picker" }, [
    control,
    trigger,
    preview
  ]);
  let filesBeforeBrowse = null;
  let syncingFiles = false;
  trigger.addEventListener("click", () => {
    filesBeforeBrowse = Array.from(control.files || []);
    control.click();
  });
  control.addEventListener("change", () => {
    if (syncingFiles) {
      renderAttachmentPreview(control, preview);
      return;
    }
    if (filesBeforeBrowse) {
      const previous = filesBeforeBrowse;
      filesBeforeBrowse = null;
      if (previous.length > 0) {
        syncingFiles = true;
        setAttachmentFiles(control, mergeAttachmentFiles(previous, Array.from(control.files || [])));
        syncingFiles = false;
        return;
      }
    }
    renderAttachmentPreview(control, preview);
  });
  bindAttachmentDropZone(picker, control, "dragging");
  return el("div", { className: "ticket-form-field attachment-field" }, [
    el("span", {}, label),
    picker
  ]);
}

function renderAttachmentPreview(input, preview) {
  const files = Array.from(input.files || []);
  preview.classList.toggle("empty", files.length === 0);
  preview.replaceChildren();
  if (files.length === 0) return;
  files.forEach((file, index) => {
    const remove = el("button", { className: "attachment-remove", type: "button", "aria-label": `Remove ${file.name}` }, "x");
    remove.addEventListener("click", () => {
      const next = Array.from(input.files || []).filter((_, fileIndex) => fileIndex !== index);
      setAttachmentFiles(input, next);
    });
    preview.append(el("span", { className: "attachment-preview-chip", title: file.name || "Attachment" }, [
      el("span", { className: "attachment-chip-name" }, file.name || "Attachment"),
      remove
    ]));
  });
}

function mergeAttachmentFiles(...groups) {
  const seen = new Set();
  const files = [];
  for (const file of groups.flat()) {
    const key = [file.name, file.size, file.lastModified, file.type].join("\0");
    if (seen.has(key)) continue;
    seen.add(key);
    files.push(file);
  }
  return files;
}

function appendAttachmentFiles(input, files) {
  setAttachmentFiles(input, mergeAttachmentFiles(Array.from(input.files || []), Array.from(files || [])));
}

const handledAttachmentDropEvents = new WeakSet();

function bindAttachmentDropZone(target, input, activeClass = "attachment-drop-active") {
  let cleanupTimer = 0;
  const hasFiles = (event) => Array.from(event.dataTransfer?.types || []).includes("Files");
  const deactivate = () => {
    window.clearTimeout(cleanupTimer);
    cleanupTimer = 0;
    target.classList.remove(activeClass);
  };
  const scheduleCleanup = () => {
    window.clearTimeout(cleanupTimer);
    cleanupTimer = window.setTimeout(deactivate, 5000);
  };
  const activate = (event) => {
    if (!hasFiles(event)) return false;
    event.preventDefault();
    target.classList.add(activeClass);
    scheduleCleanup();
    return true;
  };
  target.addEventListener("dragenter", (event) => {
    activate(event);
  });
  target.addEventListener("dragover", (event) => {
    if (!activate(event)) return;
    event.dataTransfer.dropEffect = "copy";
  });
  target.addEventListener("dragleave", (event) => {
    if (!hasFiles(event)) return;
    event.preventDefault();
    if (event.relatedTarget && target.contains(event.relatedTarget)) return;
    deactivate();
  });
  target.addEventListener("drop", (event) => {
    if (!hasFiles(event)) return;
    event.preventDefault();
    deactivate();
    if (handledAttachmentDropEvents.has(event)) return;
    handledAttachmentDropEvents.add(event);
    if (event.dataTransfer?.files?.length) appendAttachmentFiles(input, event.dataTransfer.files);
  });
}

function setAttachmentFiles(input, files) {
  const maxFiles = Number(state.meta.uploads?.max_files || 0);
  const selected = maxFiles > 0 ? files.slice(0, maxFiles) : files;
  const transfer = new DataTransfer();
  for (const file of selected) transfer.items.add(file);
  input.files = transfer.files;
  input.dispatchEvent(new Event("change", { bubbles: true }));
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
  state.adminSection = validAdminSection(section) ? section : DEFAULT_ADMIN_SECTION;
  renderAdminSections();
  if (state.view === "admin") updateRoutePath();
  await loadAdminSection(state.adminSection);
}

function validAdminSection(section) {
  return ADMIN_SECTIONS.includes(section);
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

function renderProductsView() {
  const showDetail = state.productMode === "detail" && state.productDetailId && canManageProject(state.productDetailId);
  els.productIndexPanel.hidden = Boolean(showDetail);
  els.productDetailView.hidden = !showDetail;
  if (!showDetail) {
    state.productMode = "index";
    renderProductIndex();
    return;
  }
  renderProjectContext();
  renderProductSections();
}

async function switchProductSection(section) {
  state.productSection = validProductSection(section) ? section : DEFAULT_PRODUCT_SECTION;
  renderProductSections();
  if (state.view === "project") updateRoutePath();
  if (state.productMode === "detail" && state.productDetailId && canManageProject(state.productDetailId)) {
    await loadProductSection(state.productSection);
  }
}

function validProductSection(section) {
  return PRODUCT_SECTIONS.includes(section);
}

function renderProductIndex() {
  els.addProjectButton.hidden = !isAdmin();
  els.productIndexList.replaceChildren();
  const products = manageableProjects();
  if (products.length === 0) {
    els.productIndexList.append(emptyInline({
      title: "No products",
      body: isAdmin()
        ? "Create a product before inviting customers."
        : "Ask an admin to grant owner access before managing product settings.",
      actionLabel: isAdmin() ? "New Product" : "",
      onAction: isAdmin() ? openProjectModal : null
    }));
    return;
  }
  for (const project of products) {
    const row = el("div", { className: "admin-row product-index-row" });
    const open = el("button", {
      className: "ghost-button",
      type: "button",
      "data-product-open": String(project.id)
    }, "Open");
    open.addEventListener("click", () => openProductDetail(project.id).catch(showError));
    row.append(
      el("div", { className: "admin-row-main" }, [
        el("strong", {}, project.name || project.key || `Product ${project.id}`),
        el("span", {}, project.key || `#${project.id}`)
      ]),
      el("span", { className: "muted" }, `${labelize(project.role || "owner")} access`),
      open
    );
    els.productIndexList.append(row);
  }
}

function renderProjectContext() {
  const project = currentProductDetail();
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
    const edit = el("button", { className: "ghost-button", type: "button" }, "Edit");
    edit.addEventListener("click", () => openUserModal(user));
    const label = user.email
      ? `${user.display_name || user.username} / ${user.username} / ${user.email}`
      : `${user.display_name || user.username} / ${user.username}`;

    row.append(
      el("div", { className: "admin-row-main" }, label),
      badge(user.role, "priority-normal"),
      el("span", { className: user.disabled || user.password_reset_required ? "muted" : "status-ok" }, user.disabled ? "Disabled" : user.password_reset_required ? "Password pending" : "Active"),
      edit
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
    const row = el("div", { className: "admin-row", "data-member-user": String(member.user_id) });
    const edit = el("button", { className: "ghost-button", type: "button" }, "Edit");
    edit.addEventListener("click", () => openMemberModal(member));
    row.append(
      el("div", { className: "admin-row-main" }, `${member.display_name || member.username} / ${member.username}`),
      badge(member.role, "priority-normal"),
      edit
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
  const patch = {};
  if (hasFormValue(data, "title")) {
    const title = String(data.title || "").trim();
    if (title && title !== (issue.title || "")) patch.title = title;
  }
  if (hasFormValue(data, "description")) {
    const description = String(data.description || "").trim();
    if (description !== (issue.description || "")) patch.description = description;
  }
  if (hasFormValue(data, "status")) {
    const status = String(data.status || "").trim();
    if (status && status !== issue.status) patch.status = status;
  }
  if (hasFormValue(data, "priority")) {
    const priority = String(data.priority || "").trim();
    if (priority && priority !== issue.priority) patch.priority = priority;
  }
  if (hasFormValue(data, "assignee")) {
    const assignee = String(data.assignee || "").trim();
    if (assignee !== (issue.assignee || "")) patch.assignee = assignee;
  }
  return patch;
}

function hasFormValue(data, name) {
  return Object.prototype.hasOwnProperty.call(data, name);
}

function ticketCommentPayload(issue, data) {
  const body = String(data.body || "").trim();
  if (!body) return null;
  return {
    body,
    visibility: canEditTicket(issue) ? String(data.visibility || "public") : "public"
  };
}

function bindTicketAutosave(form, issue) {
  let currentIssue = issue;
  let saveQueue = Promise.resolve();
  const controls = Array.from(form.querySelectorAll("[name='title'], [name='status'], [name='priority'], [name='assignee']"));
  const save = () => {
    saveQueue = saveQueue.then(async () => {
      const data = Object.fromEntries(new FormData(form).entries());
      const patch = ticketUpdatePatch(currentIssue, data);
      if (Object.keys(patch).length === 0) return;
      const statusChanged = hasFormValue(patch, "status");
      const assigneeChanged = hasFormValue(patch, "assignee");
      try {
        const updated = await saveTicketPatch(currentIssue, patch);
        currentIssue = updated;
        if (statusChanged && updated.status && !state.filters.statuses.includes(updated.status)) {
          state.filters.statuses = [...state.filters.statuses, updated.status];
        }
        if (assigneeChanged && state.filters.assignee && updated.assignee !== state.filters.assignee) {
          state.filters.assignee = "";
          renderAssigneeFilter();
        }
        if (statusChanged || assigneeChanged) {
          await loadIssues({ renderDetail: false });
        } else {
          replaceIssue(updated);
          renderIssueList();
        }
      } catch (error) {
        showError(error);
      }
    });
    saveQueue = saveQueue.catch(() => {});
  };
  const debouncedSave = debounce(save, TICKET_AUTOSAVE_DELAY_MS);
  for (const control of controls) {
    if (control.tagName === "INPUT" || control.tagName === "TEXTAREA") {
      control.addEventListener("input", () => debouncedSave(control));
      control.addEventListener("change", () => save(control));
    } else {
      control.addEventListener("change", () => save(control));
    }
  }
}

async function saveTicketPatch(issue, patch) {
  return request(`/api/tickets/${issue.id}`, { method: "PATCH", body: JSON.stringify(patch) });
}

function replaceIssue(updated) {
  state.issues = state.issues.map((issue) => issue.id === updated.id ? updated : issue);
}

function bindCommentComposer(form, issue) {
  const composer = form.querySelector(".comment-form");
  if (!composer) return;
  const sendButton = composer.querySelector("[data-comment-send]");
  const body = composer.querySelector("[name='body']");
  const update = () => {
    sendButton.disabled = String(body.value || "").trim() === "" && selectedCommentFiles(composer).length === 0;
  };
  body.addEventListener("input", update);
  composer.querySelectorAll("input[type='file']").forEach((input) => input.addEventListener("change", update));
  form.addEventListener("submit", async (event) => {
    if (event.submitter !== sendButton) return;
    event.preventDefault();
    sendButton.disabled = true;
    sendButton.setAttribute("aria-busy", "true");
    try {
      await sendTicketComment(issue, composer);
    } catch (error) {
      showError(error);
      update();
    } finally {
      sendButton.removeAttribute("aria-busy");
    }
  });
  update();
}

async function sendTicketComment(issue, composer) {
  const data = {
    body: composer.querySelector("[name='body']")?.value || "",
    visibility: composer.querySelector("[name='visibility']")?.value || "public"
  };
  const comment = ticketCommentPayload(issue, data);
  const files = selectedCommentFiles(composer);
  if (!comment && files.length === 0) return;
  if (files.length > 0) {
    const body = new FormData();
    body.append("body", comment?.body || "");
    body.append("visibility", comment?.visibility || String(data.visibility || "public"));
    for (const file of files) body.append("attachments", file);
    await request(`/api/tickets/${issue.id}/comments`, { method: "POST", body });
  } else {
    await request(`/api/tickets/${issue.id}/comments`, { method: "POST", body: JSON.stringify(comment) });
  }
  await loadIssues();
}

function selectedTicketFiles(form) {
  if (!form) return [];
  const files = [];
  form.querySelectorAll("input[type='file']").forEach((input) => {
    files.push(...Array.from(input.files || []));
  });
  return files;
}

function selectedCommentFiles(composer) {
  if (!composer) return [];
  const files = [];
  composer.querySelectorAll("input[type='file']").forEach((input) => {
    files.push(...Array.from(input.files || []));
  });
  return files;
}

function bindTicketSaveState({ projectId, form, submitButton }) {
  const update = () => {
    const data = Object.fromEntries(new FormData(form).entries());
    const hasTitle = String(data.title || "").trim() !== "";
    submitButton.disabled = !hasTitle || !Number(data.project_id || projectId);
  };
  form.querySelectorAll("[data-ticket-control]").forEach((control) => {
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
  const list = el("div", { className: "comment-list conversation-stream" });
  for (const message of conversationMessages(issue)) {
    const rowClasses = [
      "message-row",
      message.side === "customer" ? "from-customer" : "from-support",
      message.className ? `message-${message.className}` : ""
    ].filter(Boolean).join(" ");
    const row = el("div", { className: rowClasses });
    const avatar = el("span", { className: "message-avatar", title: message.author }, message.avatar);
    const item = el("article", { className: `comment message-bubble ${message.className}` });
    item.append(
      el("div", { className: "comment-head message-head" }, [
        el("span", { className: "message-head-main" }, [
          el("strong", {}, message.author)
        ]),
        el("span", { className: "comment-time" }, message.label),
        message.visibility === "internal" ? el("span", { className: "message-flag internal-flag" }, "Internal") : el("span")
      ]),
      el("p", {}, message.body),
      attachmentList(message.attachments || [])
    );
    row.append(avatar, item);
    list.append(row);
  }
  return list;
}

function conversationMessages(issue) {
  const opener = issue.requester_name || issue.requester || "Requester";
  const messages = [{
    author: opener,
    avatar: initials(opener),
    body: String(issue.description || "").trim() || "No description.",
    label: `opened ${relativeTime(issue.created_at)}`,
    visibility: "public",
    attachments: issue.attachments || [],
    side: isRequesterSide(issue, { author: opener }) ? "customer" : "support",
    className: "opening-message"
  }];
  for (const comment of issue.comments || []) {
    const internal = comment.visibility === "internal";
    const author = comment.author || "Support";
    messages.push({
      author,
      avatar: initials(author),
      body: String(comment.body || "").trim() || "Attachment only",
      label: relativeTime(comment.created_at),
      visibility: internal ? "internal" : "public",
      attachments: comment.attachments || [],
      side: !internal && isRequesterSide(issue, comment) ? "customer" : "support",
      className: internal ? "internal" : ""
    });
  }
  return messages;
}

function initials(value) {
  const parts = String(value || "?").trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return "?";
  return parts.slice(0, 2).map((part) => part.slice(0, 1).toUpperCase()).join("");
}

function isRequesterSide(issue, entry) {
  if (issue.source === "portal" && !entry.author) return true;
  const author = normalizeAuthor(entry.author);
  if (!author) return issue.source === "portal";
  const requesterValues = [
    issue.requester,
    issue.requester_name,
    issue.requester_email,
    String(issue.requester_email || "").split("@")[0]
  ].map(normalizeAuthor).filter(Boolean);
  return requesterValues.includes(author);
}

function normalizeAuthor(value) {
  return String(value || "").trim().toLowerCase();
}

function commentComposer(issue) {
  const wrap = el("div", { className: "comment-form" });
  const body = document.createElement("textarea");
  body.name = "body";
  body.rows = 3;
  body.className = "comment-input";
  body.dataset.ticketControl = "true";
  body.placeholder = "Write a reply";
  const send = el("button", {
    className: "comment-send-button",
    type: "submit",
    "aria-label": "Send reply",
    "data-comment-send": "true"
  }, el("span", { className: "send-icon", "aria-hidden": "true" }));
  const visibility = document.createElement("select");
  visibility.name = "visibility";
  visibility.dataset.ticketControl = "true";
  visibility.setAttribute("aria-label", "Reply visibility");
  visibility.append(new Option("Public reply", "public"), new Option("Internal note", "internal"));
  visibility.value = "public";
  const attachments = ticketAttachmentField("Attachments", "attachments");
  const actions = [send];
  if (canEditTicket(issue)) {
    actions.unshift(commentVisibilityControl(visibility));
  }
  const entry = el("div", { className: "comment-entry" }, [
    body,
    el("div", { className: "comment-action-rail" }, actions)
  ]);
  wrap.append(entry, attachments);
  const attachmentInput = attachments.querySelector(".attachment-input");
  if (attachmentInput) bindAttachmentDropZone(wrap, attachmentInput);
  return wrap;
}

function commentVisibilityControl(select) {
  const control = el("label", { className: "comment-visibility-control" }, [
    el("span", { className: "visibility-icon", "aria-hidden": "true" }),
    select
  ]);
  const update = () => {
    const internal = select.value === "internal";
    control.classList.toggle("visibility-public", !internal);
    control.classList.toggle("visibility-internal", internal);
    const label = internal ? "Internal note" : "Public reply";
    control.title = label;
    select.title = label;
  };
  select.addEventListener("change", update);
  update();
  return control;
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
  await request(`/api/projects/${state.productDetailId}/members`, { method: "POST", body: JSON.stringify(input) });
  await loadProjects();
  if (state.productDetailId && canManageProject(state.productDetailId)) await loadMembers();
}

async function deleteMember(userId) {
  await request(`/api/projects/${state.productDetailId}/members/${userId}`, { method: "DELETE" });
  await loadProjects();
  if (state.productDetailId && canManageProject(state.productDetailId)) await loadMembers();
}

async function deleteWebhook(id) {
  await request(`/api/webhooks/${id}`, { method: "DELETE" });
  if (isAdmin()) await loadGlobalWebhooks();
  if (state.view === "project" && state.productDetailId && canManageProject(state.productDetailId)) await loadProjectWebhooks();
}

async function testWebhook(id) {
  const delivery = await request(`/api/webhooks/${id}/test`, { method: "POST" });
  console.info(delivery.error ? "Webhook test failed" : "Webhook test sent", delivery);
  if (isAdmin()) await loadGlobalWebhooks();
  if (state.view === "project" && state.productDetailId && canManageProject(state.productDetailId)) {
    await Promise.all([loadProjectWebhooks(), loadProjectDeliveries()]);
  }
}

function selectOptions(values) {
  return values.map((value) => ({ value, label: labelize(value) }));
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
      const project = await request("/api/projects", { method: "POST", body: JSON.stringify(data) });
      await loadProjects();
      await openProductDetail(project.id);
    }
  });
}

function openUserModal(user = null) {
  const editing = Boolean(user);
  if (editing) {
    els.modalHost.open({
      title: `Edit ${user.username}`,
      submitText: "Save",
      content: accountEditContent(user),
      onSubmit: async (data) => {
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
    return;
  }

  const fields = [
    { group: [
      { name: "username", label: "Username", required: true, maxlength: 48, autocomplete: "off" },
      { name: "display_name", label: "Display name", autocomplete: "off" }
    ] },
    { name: "email", label: "Email", type: "email", autocomplete: "email" },
    { name: "role", label: "Role", type: "select", options: selectOptions(state.meta.roles), value: "staff" }
  ];

  els.modalHost.open({
    title: "New Account",
    submitText: "Create & Send Setup",
    values: { role: "staff" },
    fields,
    onSubmit: async (data) => {
      const created = await request("/api/users", { method: "POST", body: JSON.stringify(data) });
      await loadUsers();
      window.setTimeout(() => openAccountLinkResult(created, "setup"), 0);
    }
  });
}

function accountEditContent(user) {
  const self = user.id === state.user.id;
  const displayName = el("input", {
    name: "display_name",
    autocomplete: "off",
    value: user.display_name || ""
  });
  const email = el("input", {
    name: "email",
    type: "email",
    autocomplete: "email",
    value: user.email || ""
  });
  const role = el("select", { name: "role" });
  for (const option of selectOptions(state.meta.roles)) {
    role.append(new Option(option.label, option.value));
  }
  role.value = user.role || "staff";
  role.disabled = self;

  const disabled = el("input", { name: "disabled", type: "checkbox" });
  disabled.checked = Boolean(user.disabled);
  disabled.disabled = self;

  const reset = el("button", {
    className: "ghost",
    type: "button",
    "data-account-action": "reset"
  }, "Send reset link");
  reset.disabled = self || user.disabled;
  const remove = el("button", {
    className: "danger",
    type: "button",
    "data-account-action": "delete"
  }, "Delete account");
  remove.disabled = self;
  const confirmArea = el("div", { className: "account-confirm-area" });
  const content = el("div", { className: "account-edit" }, [
    el("div", { className: "account-edit-grid" }, [
      accountField("Display name", displayName),
      accountField("Email", email),
      accountField("Role", role),
      el("label", { className: "check" }, [disabled, "Disabled"])
    ]),
    el("section", { className: "account-manage" }, [
      el("div", { className: "account-manage-head" }, [
        el("strong", {}, "Account access"),
        el("span", {}, self ? "You cannot reset or delete your own account." : "Use these actions only when the account owner cannot sign in or no longer needs access.")
      ]),
      el("div", { className: "account-action-row" }, [reset, remove]),
      confirmArea
    ])
  ]);

  reset.addEventListener("click", () => showAccountActionConfirm(confirmArea, {
    title: "Send a password reset link?",
    body: `${user.display_name || user.username} will receive a one-time link if email is configured. Existing sessions are not changed.`,
    confirmLabel: "Send reset link",
    onConfirm: () => resetUserPassword(user)
  }));
  remove.addEventListener("click", () => showAccountActionConfirm(confirmArea, {
    title: "Delete this account?",
    body: `This permanently removes ${user.display_name || user.username}. This cannot be undone.`,
    confirmLabel: "Delete account",
    danger: true,
    onConfirm: async () => {
      await deleteUser(user.id);
      els.modalHost.close();
    }
  }));
  return content;
}

function accountField(label, control) {
  return el("label", {}, [label, control]);
}

function showAccountActionConfirm(container, { title, body, confirmLabel, danger = false, onConfirm }) {
  const confirm = el("button", { className: danger ? "danger" : "primary", type: "button" }, confirmLabel);
  const cancel = el("button", { className: "ghost", type: "button" }, "Cancel");
  cancel.addEventListener("click", () => container.replaceChildren());
  confirm.addEventListener("click", async () => {
    confirm.disabled = true;
    confirm.setAttribute("aria-busy", "true");
    try {
      await onConfirm();
    } catch (error) {
      showError(error);
      confirm.disabled = false;
      confirm.removeAttribute("aria-busy");
    }
  });
  container.replaceChildren(el("div", { className: danger ? "account-confirm danger-zone" : "account-confirm" }, [
    el("strong", {}, title),
    el("p", {}, body),
    el("div", { className: "account-confirm-actions" }, [cancel, confirm])
  ]));
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

function openMemberModal(member = null) {
  if (member) {
    els.modalHost.open({
      title: `Edit ${member.display_name || member.username}`,
      submitText: "Save",
      content: memberEditContent(member),
      onSubmit: async (data) => {
        await upsertMember({ user_id: member.user_id, role: data.role });
      }
    });
    return;
  }
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

function memberEditContent(member) {
  const role = el("select", { name: "role" });
  for (const option of selectOptions(state.meta.projectRoles)) {
    role.append(new Option(option.label, option.value));
  }
  role.value = member.role || "viewer";
  const remove = el("button", {
    className: "danger",
    type: "button",
    "data-member-action": "delete"
  }, "Remove member");
  const confirmArea = el("div", { className: "account-confirm-area" });
  const content = el("div", { className: "account-edit" }, [
    el("div", { className: "account-edit-grid" }, [
      accountField("Account", el("input", {
        disabled: "disabled",
        value: `${member.display_name || member.username} / ${member.username}`
      })),
      accountField("Role", role)
    ]),
    el("section", { className: "account-manage" }, [
      el("div", { className: "account-manage-head" }, [
        el("strong", {}, "Membership"),
        el("span", {}, "Remove this account only when it should no longer access this product.")
      ]),
      el("div", { className: "account-action-row" }, [remove]),
      confirmArea
    ])
  ]);

  remove.addEventListener("click", () => showAccountActionConfirm(confirmArea, {
    title: "Remove this member?",
    body: `${member.display_name || member.username} will lose access to this product. Existing tickets and comments are kept.`,
    confirmLabel: "Remove member",
    danger: true,
    onConfirm: async () => {
      await deleteMember(member.user_id);
      els.modalHost.close();
    }
  }));
  return content;
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
      await request(`/api/projects/${state.productDetailId}/webhooks`, { method: "POST", body: JSON.stringify(payload) });
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
  document.addEventListener("keydown", handleGlobalKeydown);
  window.addEventListener("popstate", () => applyRouteFromPath().catch(showError));

  els.issuesTab.addEventListener("click", () => switchView("issues"));
  els.projectTab.addEventListener("click", () => openProductsIndex());
  els.adminTab.addEventListener("click", () => switchView("admin"));
  for (const button of els.adminSectionButtons) {
    button.addEventListener("click", () => switchAdminSection(button.getAttribute("data-admin-section")).catch(showError));
  }
  for (const button of els.productSectionButtons) {
    button.addEventListener("click", () => switchProductSection(button.getAttribute("data-product-section")).catch(showError));
  }
  els.newIssueButton.addEventListener("click", () => openTicketDraft());
  els.modalHost.addEventListener("pappice-modal-error", (event) => showError(event.detail));

  els.searchInput.addEventListener("input", debounce(() => {
    state.filters.q = els.searchInput.value.trim();
    loadIssues().catch(showError);
  }, 180));
  els.productFilter.addEventListener("change", async () => {
    state.ticketProjectId = Number(els.productFilter.value) || null;
    state.selectedId = state.ticketDraft ? DRAFT_TICKET_ID : null;
    renderProductFilter();
    updateProjectActions();
    await loadIssues();
  });
  els.assigneeFilter.addEventListener("change", () => {
    state.filters.assignee = els.assigneeFilter.value.trim();
    loadIssues().catch(showError);
  });
  els.issueSortSelect.addEventListener("change", () => setIssueSortValue(els.issueSortSelect.value));
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

function handleGlobalKeydown(event) {
  if (event.key !== "Escape") return;
  if (!els.profilePopover.hidden) {
    closeProfileMenu();
    return;
  }
  if (els.modalHost?.isOpen?.()) return;
  if (state.view !== "issues" || !state.selectedId || els.appView.hidden) return;
  event.preventDefault();
  closeSelectedTicket();
}

function closeSelectedTicket() {
  state.selectedId = null;
  renderIssuesView();
}

function switchView(view, options = {}) {
  if (view === "admin" && !isAdmin()) return;
  if (view === "project" && !canAccessProductsView()) return;
  state.view = view;
  els.issueView.hidden = view !== "issues";
  els.adminView.hidden = view !== "admin";
  els.projectView.hidden = view !== "project";
  els.issuesTab.classList.toggle("active", view === "issues");
  els.adminTab.classList.toggle("active", view === "admin");
  els.projectTab.classList.toggle("active", view === "project");
  if (options.updateRoute !== false) updateRoutePath({ replace: Boolean(options.replaceRoute) });
  if (view === "admin" && options.load !== false) loadAdmin().catch(showError);
  if (view === "project" && options.load !== false) loadProjectAdmin().catch(showError);
}

function openProductsIndex() {
  state.productMode = "index";
  state.productDetailId = null;
  switchView("project");
}

async function openProductDetail(projectId, section = DEFAULT_PRODUCT_SECTION) {
  if (!canManageProject(projectId)) return;
  state.productMode = "detail";
  state.productDetailId = projectId;
  state.productSection = validProductSection(section) ? section : DEFAULT_PRODUCT_SECTION;
  switchView("project", { load: false });
  await loadProjectAdmin();
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

function currentProject(projectId) {
  return state.projects.find((project) => project.id === projectId) || null;
}

function currentProductDetail() {
  return currentProject(state.productDetailId);
}

function selectedIssue() {
  if (state.selectedId === DRAFT_TICKET_ID) return state.ticketDraft;
  return state.issues.find((issue) => issue.id === state.selectedId) || null;
}

function isAdmin() {
  return state.user?.role === "admin";
}

function isCustomer() {
  return state.user?.role === "customer";
}

function projectRole(projectId) {
  return currentProject(projectId)?.role || "";
}

function canManageProject(projectId) {
  return Boolean(projectId) && !isCustomer() && (isAdmin() || projectRole(projectId) === "owner");
}

function manageableProjects() {
  return state.projects.filter((project) => canManageProject(project.id));
}

function canAccessProductsView() {
  return isAdmin() || manageableProjects().length > 0;
}

function canCreateIssue(projectId = state.ticketProjectId) {
  if (!projectId) {
    return state.projects.some((project) => canCreateIssue(project.id));
  }
  return isAdmin() || ["owner", "agent", "customer"].includes(projectRole(projectId));
}

function canCommentTicket(issue = null) {
  return Boolean(issue?.project_id) && canCreateIssue(issue.project_id);
}

function canEditTicket(issue = null) {
  const projectId = issue?.project_id || state.ticketProjectId;
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
