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
import { createRouter } from "./router.js";

defineComponents();

const DEFAULT_ADMIN_SECTION = "accounts";
const ADMIN_SECTIONS = [DEFAULT_ADMIN_SECTION, "tokens", "webhooks", "email", "maintenance", "audit"];
const DEFAULT_PRODUCT_SECTION = "members";
const PRODUCT_SECTIONS = [DEFAULT_PRODUCT_SECTION, "webhooks", "deliveries"];
const DEFAULT_TICKET_STATUSES = ["new", "assigned"];
const TICKET_AUTOSAVE_DELAY_MS = 450;
const TICKET_SORT_LABELS = {
  updated_at: "Updated",
  created_at: "Created",
  priority: "Priority",
  status: "Status",
  title: "Title"
};
const router = createRouter({
  adminSections: ADMIN_SECTIONS,
  defaultAdminSection: DEFAULT_ADMIN_SECTION,
  productSections: PRODUCT_SECTIONS,
  defaultProductSection: DEFAULT_PRODUCT_SECTION
});

const state = {
  tickets: [],
  ticketCounts: { all: 0 },
  products: [],
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
  view: "tickets",
  adminSection: DEFAULT_ADMIN_SECTION,
  productSection: DEFAULT_PRODUCT_SECTION,
  productMode: "index",
  productDetailId: null,
  ticketProductId: null,
  selectedId: null,
  selectedTicket: null,
  renderedTicketDetailId: null,
  sort: {
    key: "updated_at",
    dir: "desc"
  },
  meta: {
    statuses: [],
    priorities: [],
    roles: [],
    productRoles: [],
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
    assignee: "",
    statusCustomized: false,
    unread: false
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
  ticketsTab: document.querySelector("#ticketsTab"),
  ticketsUnreadBadge: document.querySelector("#ticketsUnreadBadge"),
  productTab: document.querySelector("#productTab"),
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
  newTicketButton: document.querySelector("#newTicketButton"),
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
  ticketView: document.querySelector("#ticketView"),
  adminView: document.querySelector("#adminView"),
  adminSectionButtons: Array.from(document.querySelectorAll("[data-admin-section]")),
  adminSectionPanels: Array.from(document.querySelectorAll("[data-admin-panel]")),
  productView: document.querySelector("#productView"),
  productSectionButtons: Array.from(document.querySelectorAll("[data-product-section]")),
  productSectionPanels: Array.from(document.querySelectorAll("[data-product-panel]")),
  productIndexPanel: document.querySelector("#productIndexPanel"),
  productIndexList: document.querySelector("#productIndexList"),
  productDetailView: document.querySelector("#productDetailView"),
  productContextTitle: document.querySelector("#productContextTitle"),
  productContextMeta: document.querySelector("#productContextMeta"),
  deleteProductButton: document.querySelector("#deleteProductButton"),
  ticketList: document.querySelector("#ticketList"),
  ticketDetailPane: document.querySelector("#ticketDetailPane"),
  searchInput: document.querySelector("#searchInput"),
  ticketFilterButton: document.querySelector("#ticketFilterButton"),
  ticketFilterBadge: document.querySelector("#ticketFilterBadge"),
  ticketFilterPopover: document.querySelector("#ticketFilterPopover"),
  clearTicketFiltersButton: document.querySelector("#clearTicketFiltersButton"),
  productFilter: document.querySelector("#productFilter"),
  assigneeFilter: document.querySelector("#assigneeFilter"),
  unreadFilter: document.querySelector("#unreadFilter"),
  ticketSortButton: document.querySelector("#ticketSortButton"),
  ticketSortPopover: document.querySelector("#ticketSortPopover"),
  ticketSortLabel: document.querySelector("#ticketSortLabel"),
  ticketSortSelect: document.querySelector("#ticketSortSelect"),
  statusFilterList: document.querySelector("#statusFilterList"),
  addProductButton: document.querySelector("#addProductButton"),
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
  const route = router.accountLinkRoute();
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

async function applyRoute(route = router.current()) {
  if (!state.user) return;
  if (route.view === "admin") {
    if (!isAdmin()) {
      switchView("tickets", { updateRoute: false });
      syncRoute({ replace: true });
      return;
    }
    state.adminSection = route.adminSection;
    switchView("admin", { updateRoute: false });
    syncRoute({ replace: route.normalize });
    return;
  }
  if (route.view === "product") {
    if (route.productMode === "index") {
      if (canAccessProductsView()) {
        state.productMode = "index";
        state.productDetailId = null;
        switchView("product", { updateRoute: false });
        syncRoute({ replace: route.normalize });
        return;
      }
    } else {
      const product = currentProduct(route.productId);
      if (product && canManageProduct(product.id)) {
        state.productMode = "detail";
        state.productDetailId = product.id;
        state.productSection = route.productSection;
        updateProductActions();
        switchView("product", { updateRoute: false });
        syncRoute({ replace: route.normalize });
        return;
      }
    }
    switchView("tickets", { updateRoute: false });
    syncRoute({ replace: true });
    return;
  }
  switchView("tickets", { updateRoute: false });
  const selected = await applyTicketRouteSelection(route.ticketKey);
  syncRoute({ replace: route.normalize || (Boolean(route.ticketKey) && !selected) });
}

function syncRoute({ replace = false } = {}) {
  if (!state.user) return;
  router.navigate(routeForState(), { replace });
}

function routeForState() {
  const ticket = selectedTicket();
  return {
    view: state.view,
    adminSection: state.adminSection,
    productMode: state.productMode,
    productId: state.productDetailId,
    productSection: state.productSection,
    ticketKey: state.view === "tickets" ? ticket?.key || "" : ""
  };
}

async function applyTicketRouteSelection(key) {
  if (!key) {
    setSelectedTicket(null, { updateRoute: false });
    renderTicketsView();
    return true;
  }
  let ticket = state.tickets.find((item) => item.key === key);
  try {
    if (!ticket) ticket = await request(`/api/tickets/key/${encodeURIComponent(key)}`);
    setSelectedTicket(ticket, { updateRoute: false });
    return true;
  } catch (error) {
    setSelectedTicket(null, { updateRoute: false });
    showError(error);
    return false;
  } finally {
    renderTicketsView();
  }
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
  await loadProducts();
  await applyRoute();
}

function showAuth(mode) {
  state.user = null;
  state.csrf = "";
  state.selectedId = null;
  state.selectedTicket = null;
  syncTicketMobileState();
  document.body.classList.remove("app-mode");
  clearAppAlert();
  clearAuthError();
  els.authView.hidden = false;
  els.appView.hidden = true;
  els.topNav.hidden = true;
  els.adminTab.hidden = true;
  els.profileMenu.hidden = true;
  closeProfileMenu();
  els.newTicketButton.hidden = true;
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
  switchView("tickets", { updateRoute: false });
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

function toggleTicketPopover(popover, button) {
  if (!popover || !button) return;
  const open = popover.hidden;
  closeTicketPopovers();
  popover.hidden = !open;
  button.setAttribute("aria-expanded", String(open));
}

function closeTicketPopovers() {
  for (const [popover, button] of [
    [els.ticketFilterPopover, els.ticketFilterButton],
    [els.ticketSortPopover, els.ticketSortButton]
  ]) {
    if (!popover || !button) continue;
    popover.hidden = true;
    button.setAttribute("aria-expanded", "false");
  }
}

async function loadHealth() {
  const meta = await request("/api/health");
  state.meta.statuses = meta.statuses || [];
  state.meta.priorities = meta.priorities || [];
  state.meta.roles = meta.roles || [];
  state.meta.productRoles = meta.product_roles || [];
  state.meta.webhookEvents = meta.webhook_events || [];
  state.meta.uploads = meta.uploads || state.meta.uploads;
  state.branding = normalizeBranding(meta.branding);
  applyBranding();
  state.filters.statuses = state.filters.statuses.filter((status) => state.meta.statuses.includes(status));
  if (state.filters.statuses.length === 0) state.filters.statuses = defaultStatusFilters();
}

async function loadProducts() {
  const payload = await request("/api/products");
  state.products = payload.products || [];
  if (state.ticketProductId && !state.products.some((product) => product.id === state.ticketProductId)) {
    state.ticketProductId = null;
  }
  if (state.productDetailId && !state.products.some((product) => product.id === state.productDetailId)) {
    state.productDetailId = null;
    state.productMode = "index";
  }
  renderProductFilter();
  renderProductIndex();
  updateProductActions();
  await loadTickets();
}

async function loadTickets({ renderDetail = true } = {}) {
  if (state.products.length === 0) {
    state.tickets = [];
    state.ticketCounts = countTickets([]);
    renderTicketsUnreadBadge(0);
    renderTicketsView();
    return;
  }
  const params = new URLSearchParams();
  if (state.filters.q) params.set("q", state.filters.q);
  if (state.filters.assignee) params.set("assignee", state.filters.assignee);
  if (state.filters.unread) params.set("unread", "1");
  for (const status of state.filters.statuses) params.append("status", status);
  if (usesDefaultStatusView()) params.set("include_unread_outside_status", "1");
  const productID = state.ticketProductId || null;
  if (productID) params.set("product_id", String(productID));
  const countParams = new URLSearchParams();
  if (productID) countParams.set("product_id", String(productID));
  const unreadParams = new URLSearchParams({ unread: "1" });
  const [payload, countsPayload, unreadPayload] = await Promise.all([
    request(`/api/tickets?${params.toString()}`),
    request(`/api/tickets?${countParams.toString()}`),
    request(`/api/tickets?${unreadParams.toString()}`)
  ]);
  if (productID !== (state.ticketProductId || null)) return;
  state.tickets = payload.tickets || [];
  state.ticketCounts = countTickets(countsPayload.tickets || []);
  renderTicketsUnreadBadge((unreadPayload.tickets || []).length);
  const previousSelectedId = state.selectedId;
  const listedSelection = state.tickets.find((ticket) => ticket.id === state.selectedId);
  if (listedSelection) {
    state.selectedTicket = listedSelection;
  } else if (state.selectedId && !state.selectedTicket) {
    setSelectedTicket(null, { updateRoute: false });
  }
  if (renderDetail || state.selectedId !== previousSelectedId) {
    renderTicketsView();
  } else {
    renderCounts();
    renderSortHeaders();
    renderTicketList();
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

async function loadProductAdmin() {
  renderProductsView();
  if (state.productMode !== "detail" || !state.productDetailId || !canManageProduct(state.productDetailId)) return;
  await loadProductSection(state.productSection);
}

async function loadProductSection(section) {
  switch (validProductSection(section) ? section : DEFAULT_PRODUCT_SECTION) {
    case DEFAULT_PRODUCT_SECTION:
      await Promise.all([loadUsers(), loadMembers()]);
      return;
    case "webhooks":
      await loadProductWebhooks();
      return;
    case "deliveries":
      await loadProductDeliveries();
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
  const payload = await request(`/api/products/${state.productDetailId}/members`);
  state.members = payload.members || [];
  renderMembers();
}

async function loadProductWebhooks() {
  const payload = await request(`/api/products/${state.productDetailId}/webhooks`);
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

async function loadProductDeliveries() {
  const payload = await request(`/api/products/${state.productDetailId}/webhook-deliveries`);
  state.deliveries = payload.deliveries || [];
  renderDeliveries();
}

function renderProductFilter() {
  els.productFilter.replaceChildren();
  if (state.products.length === 0) {
    els.productFilter.append(new Option("No products", ""));
    els.productFilter.disabled = true;
    renderTicketFilterButton();
    return;
  }
  els.productFilter.disabled = false;
  els.productFilter.append(new Option("All products", ""));
  for (const product of state.products) {
    els.productFilter.append(new Option(productDisplayName(product), String(product.id)));
  }
  els.productFilter.value = state.ticketProductId ? String(state.ticketProductId) : "";
  renderTicketFilterButton();
}

function updateProductActions() {
  els.adminTab.hidden = !isAdmin();
  els.productTab.hidden = !canAccessProductsView();
  els.addProductButton.hidden = !isAdmin();
  els.newTicketButton.hidden = !canCreateTicket();
  if (state.view === "admin" && !isAdmin()) switchView("tickets");
  if (state.view === "product" && !canAccessProductsView()) switchView("tickets");
}

function renderTicketsView() {
  syncTicketMobileState();
  renderCounts();
  renderSortHeaders();
  renderTicketList();
  renderTicketDetail();
}

function renderCounts() {
  const counts = state.ticketCounts;
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
      loadTickets().catch(showError);
    });
    els.statusFilterList.append(button);
  }
  renderTicketFilterButton();
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
  state.filters.statusCustomized = true;
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
  renderTicketFilterButton();
}

function countTickets(tickets) {
  const counts = { all: tickets.length };
  for (const status of state.meta.statuses) counts[status] = 0;
  for (const ticket of tickets) {
    counts[ticket.status] = (counts[ticket.status] || 0) + 1;
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
  return Boolean(
    state.filters.q ||
    state.ticketProductId ||
    state.filters.assignee ||
    state.filters.unread ||
    state.filters.statusCustomized ||
    !sameStatuses(state.filters.statuses, defaultStatusFilters())
  );
}

function activeTicketFilterCount() {
  let count = 0;
  if (state.ticketProductId) count += 1;
  if (state.filters.assignee) count += 1;
  if (state.filters.unread) count += 1;
  if (state.filters.statusCustomized || !sameStatuses(state.filters.statuses, defaultStatusFilters())) count += 1;
  return count;
}

function renderTicketFilterButton() {
  if (!els.ticketFilterButton || !els.ticketFilterBadge) return;
  const count = activeTicketFilterCount();
  els.ticketFilterButton.classList.toggle("active", count > 0);
  els.ticketFilterBadge.hidden = count === 0;
  els.ticketFilterBadge.textContent = String(count);
  if (els.unreadFilter) els.unreadFilter.checked = state.filters.unread;
  if (els.clearTicketFiltersButton) els.clearTicketFiltersButton.disabled = !hasActiveTicketFilters();
}

function renderTicketsUnreadBadge(count) {
  if (!els.ticketsUnreadBadge) return;
  els.ticketsUnreadBadge.hidden = count === 0;
  els.ticketsUnreadBadge.textContent = String(count);
}

function sameStatuses(left, right) {
  if (left.length !== right.length) return false;
  const values = new Set(left);
  return right.every((status) => values.has(status));
}

function usesDefaultStatusView() {
  return !state.filters.statusCustomized && sameStatuses(state.filters.statuses, defaultStatusFilters());
}

function clearTicketFilters() {
  state.filters.q = "";
  state.filters.assignee = "";
  state.filters.statuses = defaultStatusFilters();
  state.filters.statusCustomized = false;
  state.filters.unread = false;
  state.ticketProductId = null;
  els.searchInput.value = "";
  els.productFilter.value = "";
  els.assigneeFilter.value = "";
  els.unreadFilter.checked = false;
  updateProductActions();
  renderCounts();
  loadTickets().catch(showError);
}

function renderTicketList() {
  els.ticketList.replaceChildren();
  if (state.products.length === 0) {
    els.ticketList.append(emptyState({
      title: isAdmin() ? "Create a product to start" : "No products available",
      body: isAdmin()
        ? "Products group tickets by the customer, service, or team you support."
        : "Ask an administrator to add your account to a product before opening tickets.",
      actionLabel: isAdmin() ? "New Product" : "",
      onAction: isAdmin() ? openProductModal : null
    }));
    return;
  }
  const tickets = sortedTickets();
  if (tickets.length === 0) {
    if (hasActiveTicketFilters()) {
      els.ticketList.append(emptyState({
        title: "No tickets match these filters",
        body: "Clear the filters to return to the default view.",
        actionLabel: "Clear Filters",
        onAction: clearTicketFilters
      }));
      return;
    }
    els.ticketList.append(emptyState({
      title: "No tickets yet",
      body: canCreateTicket()
        ? "Create the first ticket for this view."
        : "Tickets will appear here when customers or staff open them.",
      actionLabel: canCreateTicket() ? "New Ticket" : "",
      onAction: canCreateTicket() ? openTicketCreateModal : null
    }));
    return;
  }
  for (const ticket of tickets) {
    const row = document.createElement("button");
    row.type = "button";
    row.className = "ticket-row";
    row.classList.toggle("active", ticket.id === state.selectedId);
    row.classList.toggle("unread", Boolean(ticket.has_unread));
    row.addEventListener("click", () => {
      setSelectedTicket(state.selectedId === ticket.id ? null : ticket);
      renderTicketsView();
    });
    const product = ticketProductParts(ticket);
    const productLabel = el("span", { className: "ticket-row-product" }, product.name);
    if (ticket.has_unread) {
      productLabel.prepend(el("span", { className: "ticket-unread-dot", title: "Unread" }));
    }
    row.append(
      productLabel,
      el("span", { className: "ticket-row-time" }, relativeTime(ticket.updated_at)),
      el("span", { className: "ticket-row-title" }, ticket.title || "Untitled ticket"),
      el("span", { className: "ticket-row-footer" }, [
        badge(ticket.status, `status-${ticket.status}`),
        badge(ticket.priority, `priority-${ticket.priority}`),
        el("span", { className: "ticket-row-person" }, ticket.assignee || ticket.requester_name || ticket.requester || "Unassigned")
      ])
    );
    els.ticketList.append(row);
  }
}

function renderSortHeaders() {
  if (els.ticketSortSelect) {
    els.ticketSortSelect.value = `${state.sort.key}:${state.sort.dir}`;
  }
  if (els.ticketSortLabel) {
    els.ticketSortLabel.textContent = `By ${TICKET_SORT_LABELS[state.sort.key] || labelize(state.sort.key)}`;
  }
  for (const button of document.querySelectorAll("[data-sort-key]")) {
    const active = button.dataset.sortKey === state.sort.key;
    button.classList.toggle("active", active);
    button.classList.toggle("desc", active && state.sort.dir === "desc");
    button.classList.toggle("asc", active && state.sort.dir === "asc");
    button.setAttribute("aria-sort", active ? (state.sort.dir === "desc" ? "descending" : "ascending") : "none");
  }
  for (const button of document.querySelectorAll("[data-sort-dir]")) {
    const active = button.dataset.sortDir === state.sort.dir;
    button.classList.toggle("active", active);
    button.setAttribute("aria-pressed", String(active));
  }
}

function setTicketSortValue(value) {
  const [key, dir] = String(value || "").split(":");
  if (!key) return;
  state.sort.key = key;
  state.sort.dir = dir === "asc" ? "asc" : "desc";
  renderTicketsView();
}

function sortedTickets() {
  return [...state.tickets].sort(compareTickets);
}

function compareTickets(a, b) {
  const direction = state.sort.dir === "desc" ? -1 : 1;
  let result = 0;
  switch (state.sort.key) {
    case "created_at":
    case "updated_at":
      result = compareTime(a[state.sort.key], b[state.sort.key]);
      break;
    case "product":
      result = compareText(ticketProductLabel(a), ticketProductLabel(b));
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

function ticketProductParts(ticket) {
  const product = currentProduct(ticket.product_id);
  const key = ticket.product_key || product?.key || "";
  const name = ticket.product_name || productDisplayName(product) || ticket.product || key || "Product";
  return {
    key,
    name
  };
}

function ticketProductLabel(ticket) {
  return ticketProductParts(ticket).name;
}

function renderTicketDetail() {
  if (!els.ticketDetailPane) return;
  const ticket = selectedTicket();
  els.ticketDetailPane.replaceChildren();
  if (!ticket) {
    state.renderedTicketDetailId = null;
    els.ticketDetailPane.append(emptyState({
      title: "No ticket selected",
      body: state.tickets.length === 0
        ? "Tickets matching the current view will appear here."
        : "Select a ticket from the list to read the conversation."
    }));
    return;
  }

  const isNewlyOpenedTicket = state.renderedTicketDetailId !== ticket.id;
  const editable = canEditTicket(ticket);
  const canComment = canCommentTicket(ticket);
  const form = el("form", { className: "ticket-detail-form" });
  form.append(ticketDetailContent({
    ticket,
    editable,
    canComment
  }));

  form.addEventListener("submit", (event) => event.preventDefault());
  if (editable) bindTicketAutosave(form, ticket);
  if (canComment) bindCommentComposer(form, ticket);

  els.ticketDetailPane.append(form);
  state.renderedTicketDetailId = ticket.id;
  scrollTicketConversationToBottom(form, { smooth: !isNewlyOpenedTicket });
  markTicketRead(ticket).catch(showError);
}

async function markTicketRead(ticket) {
  if (!ticket?.id || !ticket.has_unread) return;
  const updated = await request(`/api/tickets/${ticket.id}/read`, { method: "POST" });
  setSelectedTicket(updated, { updateRoute: false });
  replaceTicket(updated);
  await loadTickets({ renderDetail: false });
}

function scrollTicketConversationToBottom(root, { smooth = false } = {}) {
  const conversation = root.querySelector(".conversation-stream");
  if (!conversation) return;
  const behavior = smooth && !prefersReducedMotion() ? "smooth" : "auto";
  const scroll = (mode = behavior) => {
    const top = Math.max(0, conversation.scrollHeight - conversation.clientHeight);
    if (mode === "smooth") {
      conversation.scrollTo({ top, behavior: "smooth" });
      return;
    }
    const previousBehavior = conversation.style.scrollBehavior;
    conversation.style.scrollBehavior = "auto";
    conversation.scrollTop = top;
    conversation.style.scrollBehavior = previousBehavior;
  };
  requestAnimationFrame(() => {
    scroll();
    requestAnimationFrame(() => scroll(behavior === "smooth" ? "smooth" : "auto"));
  });
  window.setTimeout(() => scroll("auto"), smooth ? 450 : 80);
  for (const image of conversation.querySelectorAll("img")) {
    if (!image.complete) image.addEventListener("load", () => scroll("auto"), { once: true });
  }
}

function prefersReducedMotion() {
  return window.matchMedia?.("(prefers-reduced-motion: reduce)")?.matches || false;
}

async function createTicketFromForm(data, form, fallbackProductId) {
  const payload = ticketCreatePayload(data, fallbackProductId);
  const body = ticketCreateRequestBody(payload, form);
  const created = await request(`/api/products/${payload.product_id}/tickets`, { method: "POST", body });
  state.ticketProductId = payload.product_id;
  const createdStatus = created.status || "new";
  if (createdStatus && !state.filters.statuses.includes(createdStatus)) {
    state.filters.statuses = [createdStatus];
  }
  setSelectedTicket(created);
  await loadProducts();
}

function ticketDetailContent({ ticket, editable, canComment }) {
  const wrap = el("div", { className: "ticket-detail" });
  const mobileHeader = ticketMobileHeader(ticket);
  const header = el("div", { className: "detail-header" });
  if (editable) {
    header.append(ticketTextField("", "title", ticket?.title || "", {
      autocomplete: "off",
      className: "ticket-title-input",
      maxlength: 160,
      placeholder: "Title",
      required: true
    }));
  } else {
    header.append(el("h3", {}, ticket.title));
  }

  const main = el("section", { className: "ticket-main" });
  const conversation = comments(ticket);
  const composer = canComment ? commentComposer(ticket) : el("div");
  main.append(conversation, composer);
  const attachmentInput = composer.querySelector(".attachment-input");
  if (attachmentInput) bindAttachmentDropZone(main, attachmentInput, "conversation-drop-active");

  const side = ticketSidePanel(ticket, editable);

  const conversationPanel = el("section", { className: "ticket-conversation-panel" }, [mobileHeader, header, main]);
  wrap.append(el("div", { className: "ticket-detail-grid" }, [conversationPanel, side]));
  return wrap;
}

function ticketMobileHeader(ticket) {
  const back = el("button", { className: "mobile-ticket-back", type: "button" }, "Back");
  back.addEventListener("click", () => closeSelectedTicket());
  const info = el("button", { className: "mobile-ticket-info", type: "button" }, "Info");
  info.addEventListener("click", () => openTicketInfoSheet(ticket));
  return el("div", { className: "ticket-mobile-header" }, [
    back,
    el("div", { className: "ticket-mobile-title" }, [
      el("strong", { title: ticket.title || "Untitled ticket" }, ticket.title || "Untitled ticket")
    ]),
    info
  ]);
}

function ticketSidePanel(ticket, editable) {
  const side = el("aside", { className: "ticket-side" });
  for (const section of ticketSideSections(ticket, editable)) side.append(section);
  return side;
}

function ticketSideSections(ticket, editable) {
  const sections = [];
  if (editable && !isCustomer()) {
    sections.push(sideSection("Workflow", workflowEditor(ticket || { assignee: "", priority: "normal", status: "new" })));
  }
  const requester = requesterBlock(ticket);
  if (requester) {
    sections.push(sideSection("Requester", requester));
  }
  const facts = [
    factBlock("Title", ticket.title || "Untitled ticket"),
    factBlock("Product", ticketProductLabel(ticket)),
    factBlock("Created", relativeTime(ticket.created_at)),
    factBlock("Updated", relativeTime(ticket.updated_at))
  ];
  if (!canEditTicket(ticket)) facts.splice(1, 0, factBlock("Assignee", ticket.assignee || "Unassigned"));
  sections.push(sideSection("Ticket", el("div", { className: "fact-list" }, facts)));
  if (isAdmin()) {
    sections.push(sideSection("Danger zone", ticketDangerActions(ticket)));
  }
  return sections;
}

function openTicketInfoSheet(ticket) {
  const current = selectedTicket() || ticket;
  if (!current) return;
  const editable = canEditTicket(current);
  const content = el("div", { className: "ticket-info-sheet" }, [
    ticketSidePanel(current, editable)
  ]);
  els.modalHost.open({
    title: "Ticket Info",
    content,
    hideFooter: true,
    size: "compact"
  });
  if (editable) bindTicketAutosave(els.modalHost.form, current);
}

function ticketDangerActions(ticket) {
  const remove = el("button", {
    className: "danger ticket-delete-button",
    "data-delete-ticket": "true",
    type: "button"
  }, "Delete Ticket");
  remove.addEventListener("click", () => deleteCurrentTicket(ticket, remove).catch(showError));
  return el("div", { className: "ticket-danger-actions" }, [
    el("p", {}, "Permanently remove this ticket and its conversation."),
    remove
  ]);
}

async function deleteCurrentTicket(ticket, button) {
  if (!ticket || !isAdmin()) return;
  const confirmed = await confirmSendAction({
    title: "Delete this ticket?",
    body: "This permanently removes the ticket, conversation, attachments, notifications, and delivery history.",
    confirmLabel: "Delete Ticket",
    danger: true,
    details: [
      ["Ticket", `${ticket.key || `#${ticket.id}`} / ${ticket.title || "Untitled ticket"}`],
      ["Product", ticketProductLabel(ticket)]
    ]
  });
  if (!confirmed) return;

  button.disabled = true;
  button.setAttribute("aria-busy", "true");
  try {
    await request(`/api/tickets/${ticket.id}`, { method: "DELETE" });
    state.tickets = state.tickets.filter((candidate) => candidate.id !== ticket.id);
    setSelectedTicket(null, { updateRoute: false });
    await loadTickets();
    syncRoute({ replace: true });
    showAppAlert(`Ticket ${ticket.key || `#${ticket.id}`} deleted.`);
  } finally {
    button.disabled = false;
    button.removeAttribute("aria-busy");
  }
}

function ticketProductOptions(products) {
  return products.map((product) => ({ value: String(product.id), label: productDisplayName(product) }));
}

function openTicketCreateModal() {
  const creatableProducts = state.products.filter((product) => canCreateTicket(product.id));
  if (creatableProducts.length === 0) {
    showError(new Error("Create a product before adding tickets"));
    return;
  }
  const content = el("div", { className: "ticket-create-modal" }, [
    ticketCreateFlow({
      ticket: { title: "", description: "", priority: "" },
      productId: null,
      creatableProducts
    })
  ]);
  let submitButton = null;
  els.modalHost.open({
    title: "New Ticket",
    content,
    submitText: "Create Ticket",
    size: "compact",
    onSubmit: async (data, form) => {
      const confirmed = await confirmTicketCreate(data, form);
      if (!confirmed) return false;
      await createTicketFromForm(data, form);
    }
  });
  submitButton = els.modalHost.shadowRoot?.querySelector("footer .primary");
  bindTicketCreateState({ root: content, submitButton });
  const attachmentInput = content.querySelector(".attachment-input");
  if (attachmentInput) {
    bindAttachmentDropZone(content, attachmentInput, "ticket-create-drop-active");
    bindAttachmentPasteZone(content, attachmentInput);
  }
}

function confirmTicketCreate(data, form) {
  const payload = ticketCreatePayload(data);
  const files = selectedTicketFiles(form);
  const product = currentProduct(payload.product_id);
  return confirmSendAction({
    title: "Create this ticket?",
    body: "The ticket will be opened and visible to the people who can access this product.",
    confirmLabel: "Create Ticket",
    stacked: true,
    details: [
      ["Product", product ? productDisplayName(product) : "Selected product"],
      ["Priority", labelize(payload.priority)],
      ["Attachments", files.length === 0 ? "None" : String(files.length)]
    ]
  });
}

function ticketCreateFlow({ ticket, productId, creatableProducts }) {
  const productOptions = [
    { value: "", label: "Choose product" },
    ...ticketProductOptions(creatableProducts)
  ];
  const priorityOptions = [
    { value: "", label: "Choose priority" },
    ...selectOptions(state.meta.priorities)
  ];
  const productValue = productId && creatableProducts.some((product) => product.id === productId) ? String(productId) : "";
  return el("div", { className: "ticket-create-flow" }, [
    ticketCreateStep("1", "Product", [
      ticketSelectField("", "product_id", productValue, productOptions, {
        ariaLabel: "Product",
        required: true
      })
    ]),
    ticketCreateStep("2", "Priority", [
      ticketSelectField("", "priority", ticket?.priority || "", priorityOptions, {
        ariaLabel: "Priority",
        required: true
      })
    ]),
    ticketCreateStep("3", "Ticket", [
      ticketTextField("", "title", ticket?.title || "", {
        autocomplete: "off",
        className: "ticket-title-input",
        maxlength: 160,
        placeholder: "Title",
        required: true
      }),
      ticketTextareaField("", "description", ticket?.description || "", {
        className: "ticket-description-input",
        placeholder: "Describe the request, impact, and useful context.",
        required: true,
        rows: 7
      }),
      ticketAttachmentField("", "attachments")
    ])
  ]);
}

function ticketCreateStep(number, title, content) {
  return el("section", { className: "ticket-create-step", "data-create-step": number }, [
    el("span", { className: "ticket-create-step-number" }, number),
    el("div", { className: "ticket-create-step-body" }, [
      el("div", { className: "ticket-create-step-head" }, [
        el("h4", {}, title)
      ]),
      el("div", { className: "ticket-create-step-content" }, content)
    ])
  ]);
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
    attachmentIcon()
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

function attachmentIcon() {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("class", "attachment-trigger-icon");
  svg.setAttribute("width", "100%");
  svg.setAttribute("height", "100%");
  svg.setAttribute("viewBox", "-8 0 32 32");
  svg.setAttribute("fill", "currentColor");
  svg.setAttribute("aria-hidden", "true");
  svg.setAttribute("focusable", "false");
  const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
  path.setAttribute("d", "m 228,157 v 20 c 0,3.313 -2.687,6 -6,6 -3.313,0 -6,-2.687 -6,-6 v -18 c 0,-2.209 1.791,-4 4,-4 2.209,0 4,1.791 4,4 v 18 c 0,1.104 -0.896,2 -2,2 -1.104,0 -2,-0.896 -2,-2 v -16 h -2 v 16 c 0,2.209 1.791,4 4,4 2.209,0 4,-1.791 4,-4 v -18 c 0,-3.313 -2.687,-6 -6,-6 -3.313,0 -6,2.687 -6,6 v 19 c 0.493,3.945 3.921,7 8,7 4.079,0 7.507,-3.055 8,-7 v -21 h -2");
  path.setAttribute("transform", "matrix(0.77810864 0.82467535 -0.81977676 0.78275824 -26.45409 -298.86851)");
  svg.append(path);
  return svg;
}

function renderAttachmentPreview(input, preview) {
  cleanupAttachmentPreview(preview);
  const files = Array.from(input.files || []);
  preview.classList.toggle("empty", files.length === 0);
  preview.replaceChildren();
  if (files.length === 0) return;
  const urls = [];
  files.forEach((file, index) => {
    const filename = file.name || "Attachment";
    const url = URL.createObjectURL(file);
    urls.push(url);
    const imagePreview = isStagedImageFile(file);
    const remove = el("button", { className: "attachment-remove", type: "button", "aria-label": `Remove ${file.name}` }, "x");
    remove.addEventListener("click", () => {
      const next = Array.from(input.files || []).filter((_, fileIndex) => fileIndex !== index);
      setAttachmentFiles(input, next);
    });
    const linkContent = [
      el("span", { className: "attachment-chip-label" }, filename)
    ];
    if (imagePreview) {
      linkContent.unshift(el("img", { className: "attachment-chip-thumb", src: url, alt: "" }));
    }
    preview.append(el("span", { className: imagePreview ? "attachment-preview-chip has-preview" : "attachment-preview-chip", title: `Download ${filename}` }, [
      el("a", { className: "attachment-chip-name", href: url, download: filename }, linkContent),
      remove
    ]));
  });
  preview.attachmentPreviewURLs = urls;
}

function isStagedImageFile(file) {
  return String(file?.type || "").toLowerCase().startsWith("image/");
}

function cleanupAttachmentPreview(preview) {
  for (const url of preview.attachmentPreviewURLs || []) {
    URL.revokeObjectURL(url);
  }
  preview.attachmentPreviewURLs = [];
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

function bindAttachmentPasteZone(target, input) {
  target.addEventListener("paste", (event) => {
    const files = clipboardAttachmentFiles(event.clipboardData);
    if (files.length === 0) return;
    event.preventDefault();
    appendAttachmentFiles(input, files);
  });
}

function clipboardAttachmentFiles(clipboardData) {
  const files = [];
  for (const item of Array.from(clipboardData?.items || [])) {
    if (item.kind !== "file") continue;
    const file = item.getAsFile();
    if (file) files.push(file);
  }
  if (files.length > 0) return files;
  return Array.from(clipboardData?.files || []);
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

function detailMeta(ticket) {
  const meta = el("div", { className: "detail-meta" });
  meta.append(
    badge(ticket.status, `status-${ticket.status}`),
    el("span", {}, ticket.key || `#${ticket.id}`),
    el("span", {}, `Requester ${ticket.requester || "unknown"}`),
    el("span", {}, `Created ${relativeTime(ticket.created_at)}`)
  );
  return meta;
}

function requesterBlock(ticket) {
  if (!ticket.requester_email && ticket.source !== "portal") return null;
  const block = el("div", { className: "requester-block" });
  block.append(
    el("strong", {}, ticket.requester_name || "Unknown"),
    ticket.requester_email ? el("span", {}, ticket.requester_email) : el("span", {}, ticket.requester || "Requester"),
    badge(ticket.source || "staff", "priority-normal")
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
  if (state.view === "admin") syncRoute();
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
  const showDetail = state.productMode === "detail" && state.productDetailId && canManageProduct(state.productDetailId);
  els.productIndexPanel.hidden = Boolean(showDetail);
  els.productDetailView.hidden = !showDetail;
  if (!showDetail) {
    state.productMode = "index";
    renderProductIndex();
    return;
  }
  renderProductContext();
  renderProductSections();
}

async function switchProductSection(section) {
  state.productSection = validProductSection(section) ? section : DEFAULT_PRODUCT_SECTION;
  renderProductSections();
  if (state.view === "product") syncRoute();
  if (state.productMode === "detail" && state.productDetailId && canManageProduct(state.productDetailId)) {
    await loadProductSection(state.productSection);
  }
}

function validProductSection(section) {
  return PRODUCT_SECTIONS.includes(section);
}

function renderProductIndex() {
  els.addProductButton.hidden = !isAdmin();
  els.productIndexList.replaceChildren();
  const products = manageableProducts();
  if (products.length === 0) {
    els.productIndexList.append(emptyInline({
      title: "No products",
      body: isAdmin()
        ? "Create a product before inviting customers."
        : "Ask an admin to grant owner access before managing product settings.",
      actionLabel: isAdmin() ? "New Product" : "",
      onAction: isAdmin() ? openProductModal : null
    }));
    return;
  }
  for (const product of products) {
    const row = el("div", { className: "admin-row product-index-row" });
    const open = el("button", {
      className: "ghost-button",
      type: "button",
      "data-product-open": String(product.id)
    }, "Open");
    open.addEventListener("click", () => openProductDetail(product.id).catch(showError));
    row.append(
      el("div", { className: "admin-row-main" }, [
        el("strong", {}, product.name || product.key || `Product ${product.id}`),
        el("span", {}, product.key || `#${product.id}`)
      ]),
      el("span", { className: "muted" }, `${labelize(product.role || "owner")} access`),
      open
    );
    els.productIndexList.append(row);
  }
}

function renderProductContext() {
  const product = currentProductDetail();
  if (!els.productContextTitle || !els.productContextMeta) return;
  els.productContextMeta.replaceChildren();
  if (els.deleteProductButton) els.deleteProductButton.hidden = true;

  if (!product) {
    els.productContextTitle.textContent = "No product selected";
    els.productContextMeta.append(el("span", { className: "muted" }, "Choose a product to manage members and integrations."));
    return;
  }

  els.productContextTitle.textContent = product.name || product.key || "Product";
  els.productContextMeta.append(
    el("span", { className: "product-key-pill" }, product.key || `#${product.id}`),
    el("span", { className: "muted" }, `${labelize(product.role || "owner")} access`)
  );
  if (els.deleteProductButton) {
    els.deleteProductButton.hidden = !isAdmin();
    els.deleteProductButton.dataset.productId = String(product.id);
  }
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
      onAction: () => openWebhookModal(global ? "global" : "product")
    }));
    return;
  }
  for (const hook of hooks) {
    const row = el("div", { className: "admin-row" });
    const edit = el("button", { className: "ghost-button", type: "button" }, "Edit");
    edit.addEventListener("click", () => openWebhookEditModal(hook, list === els.globalWebhookList ? "global" : "product"));
    const test = el("button", { className: "ghost-button", type: "button" }, "Test");
    test.addEventListener("click", () => testWebhook(hook.id).catch(showError));
    const remove = el("button", { className: "ghost-button", type: "button" }, "Delete");
    remove.addEventListener("click", () => deleteWebhook(hook.id).catch(showError));
    row.append(
      el("div", { className: "admin-row-main" }, `${hook.name} / ${hook.url}`),
      el("span", { className: hook.enabled ? "status-ok" : "muted" }, hook.enabled ? "Enabled" : "Disabled"),
      webhookLastStatus(hook),
      edit,
      test,
      remove
    );
    list.append(row);
  }
}

function webhookLastStatus(hook) {
  if (hook.last_error) {
    return el("span", { className: "status-error", title: hook.last_error }, "Failed");
  }
  if (hook.last_status) {
    const ok = Number(hook.last_status) >= 200 && Number(hook.last_status) <= 299;
    return el("span", { className: ok ? "status-ok" : "status-error" }, `HTTP ${hook.last_status}`);
  }
  return el("span", { className: "muted" }, "No deliveries");
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

function workflowEditor(ticket) {
  const controls = [
    ticketSelectField("Status", "status", ticket.status, selectOptions(state.meta.statuses), { required: true })
  ];
  controls.push(
    ticketSelectField("Priority", "priority", ticket.priority || "normal", selectOptions(state.meta.priorities), { required: true }),
    ticketSelectField("Assignee", "assignee", ticket.assignee || "", assigneeOptions(ticket.assignee))
  );
  const controlList = el("div", { className: "detail-controls" }, controls);
  return el("div", { className: "workflow-editor" }, [controlList]);
}

async function confirmTicketComment(ticket, composer) {
  const data = {
    body: composer.querySelector("[name='body']")?.value || "",
    visibility: composer.querySelector("[name='visibility']")?.value || "public"
  };
  const comment = ticketCommentPayload(ticket, data);
  const files = selectedCommentFiles(composer);
  if (!comment && files.length === 0) return false;
  const internal = String(data.visibility || "public") === "internal";
  return confirmSendAction({
    title: internal ? "Save this internal note?" : "Send this reply?",
    body: internal
      ? "The note will stay internal and customers will not see it."
      : "The reply will be added to the ticket conversation.",
    confirmLabel: internal ? "Save Note" : "Send Reply",
    details: [
      ["Ticket", ticket.title || "Selected ticket"],
      ["Visibility", internal ? "Internal note" : "Public reply"],
      ["Attachments", files.length === 0 ? "None" : String(files.length)]
    ]
  });
}

function confirmSendAction({ title, body, confirmLabel, details = [], danger = false, stacked = false }) {
  const host = stacked ? document.createElement("pappice-modal") : els.modalHost;
  if (!host) return Promise.resolve(window.confirm(body));
  if (stacked) document.body.append(host);
  return new Promise((resolve) => {
    let settled = false;
    let dialog = null;
    const finish = (value) => {
      if (settled) return;
      settled = true;
      resolve(value);
    };
    const handleClose = () => {
      finish(false);
      if (stacked) host.remove();
    };
    const detailList = details.length > 0
      ? el("dl", { className: "confirm-detail-list" }, details.flatMap(([label, value]) => [
        el("dt", {}, label),
        el("dd", {}, value)
      ]))
      : el("div");
    host.open({
      title,
      submitText: confirmLabel,
      submitClass: danger ? "danger" : "primary",
      content: el("div", { className: "send-confirm" }, [
        el("p", {}, body),
        detailList
      ]),
      onSubmit: async () => finish(true)
    });
    dialog = host.shadowRoot?.querySelector("dialog");
    dialog?.addEventListener("close", handleClose, { once: true });
  });
}

function ticketCreatePayload(data, fallbackProductId) {
  const payload = {
    description: String(data.description || "").trim(),
    priority: String(data.priority || "normal").trim() || "normal",
    product_id: Number(data.product_id || fallbackProductId),
    title: String(data.title || "").trim()
  };
  if (!isCustomer()) {
    const assignee = String(data.assignee || "").trim();
    if (assignee) payload.assignee = assignee;
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

function ticketUpdatePatch(ticket, data) {
  const patch = {};
  if (hasFormValue(data, "title")) {
    const title = String(data.title || "").trim();
    if (title && title !== (ticket.title || "")) patch.title = title;
  }
  if (hasFormValue(data, "description")) {
    const description = String(data.description || "").trim();
    if (description !== (ticket.description || "")) patch.description = description;
  }
  if (hasFormValue(data, "status")) {
    const status = String(data.status || "").trim();
    if (status && status !== ticket.status) patch.status = status;
  }
  if (hasFormValue(data, "priority")) {
    const priority = String(data.priority || "").trim();
    if (priority && priority !== ticket.priority) patch.priority = priority;
  }
  if (hasFormValue(data, "assignee")) {
    const assignee = String(data.assignee || "").trim();
    if (assignee !== (ticket.assignee || "")) patch.assignee = assignee;
  }
  return patch;
}

function hasFormValue(data, name) {
  return Object.prototype.hasOwnProperty.call(data, name);
}

function ticketCommentPayload(ticket, data) {
  const body = String(data.body || "").trim();
  if (!body) return null;
  return {
    body,
    visibility: canEditTicket(ticket) ? String(data.visibility || "public") : "public"
  };
}

function bindTicketAutosave(form, ticket) {
  let currentTicket = ticket;
  let saveQueue = Promise.resolve();
  const controls = Array.from(form.querySelectorAll("[name='title'], [name='status'], [name='priority'], [name='assignee']"));
  const save = () => {
    saveQueue = saveQueue.then(async () => {
      const data = Object.fromEntries(new FormData(form).entries());
      const patch = ticketUpdatePatch(currentTicket, data);
      if (Object.keys(patch).length === 0) return;
      const statusChanged = hasFormValue(patch, "status");
      const assigneeChanged = hasFormValue(patch, "assignee");
      try {
        const updated = await saveTicketPatch(currentTicket, patch);
        currentTicket = updated;
        if (statusChanged && updated.status && !state.filters.statuses.includes(updated.status)) {
          state.filters.statuses = [...state.filters.statuses, updated.status];
        }
        if (assigneeChanged && state.filters.assignee && updated.assignee !== state.filters.assignee) {
          state.filters.assignee = "";
          renderAssigneeFilter();
        }
        if (statusChanged || assigneeChanged) {
          await loadTickets({ renderDetail: false });
        } else {
          replaceTicket(updated);
          renderTicketList();
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

async function saveTicketPatch(ticket, patch) {
  const updated = await request(`/api/tickets/${ticket.id}`, { method: "PATCH", body: JSON.stringify(patch) });
  if (state.selectedId === updated.id) setSelectedTicket(updated, { updateRoute: false });
  return updated;
}

function replaceTicket(updated) {
  state.tickets = state.tickets.map((ticket) => ticket.id === updated.id ? updated : ticket);
  if (state.selectedId === updated.id) state.selectedTicket = updated;
}

function bindCommentComposer(form, ticket) {
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
    const confirmed = await confirmTicketComment(ticket, composer);
    if (!confirmed) {
      update();
      return;
    }
    sendButton.disabled = true;
    sendButton.setAttribute("aria-busy", "true");
    try {
      await sendTicketComment(ticket, composer);
    } catch (error) {
      showError(error);
      update();
    } finally {
      sendButton.removeAttribute("aria-busy");
    }
  });
  update();
}

async function sendTicketComment(ticket, composer) {
  const data = {
    body: composer.querySelector("[name='body']")?.value || "",
    visibility: composer.querySelector("[name='visibility']")?.value || "public"
  };
  const comment = ticketCommentPayload(ticket, data);
  const files = selectedCommentFiles(composer);
  if (!comment && files.length === 0) return;
  if (files.length > 0) {
    const body = new FormData();
    body.append("body", comment?.body || "");
    body.append("visibility", comment?.visibility || String(data.visibility || "public"));
    for (const file of files) body.append("attachments", file);
    await request(`/api/tickets/${ticket.id}/comments`, { method: "POST", body });
  } else {
    await request(`/api/tickets/${ticket.id}/comments`, { method: "POST", body: JSON.stringify(comment) });
  }
  const updated = await request(`/api/tickets/${ticket.id}`);
  setSelectedTicket(updated, { updateRoute: false });
  replaceTicket(updated);
  await loadTickets();
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

function bindTicketCreateState({ root, submitButton }) {
  const product = root.querySelector("[name='product_id']");
  const priority = root.querySelector("[name='priority']");
  const title = root.querySelector("[name='title']");
  const description = root.querySelector("[name='description']");
  const steps = Array.from(root.querySelectorAll("[data-create-step]"));
  const update = () => {
    const hasProduct = Boolean(product?.value);
    const hasPriority = Boolean(priority?.value);
    const hasTitle = String(title?.value || "").trim() !== "";
    const hasDescription = String(description?.value || "").trim() !== "";
    updateTicketCreateStep(steps[0], { enabled: true, active: !hasProduct, complete: hasProduct });
    updateTicketCreateStep(steps[1], { enabled: hasProduct, active: hasProduct && !hasPriority, complete: hasProduct && hasPriority });
    updateTicketCreateStep(steps[2], {
      enabled: hasProduct && hasPriority,
      active: hasProduct && hasPriority,
      complete: hasProduct && hasPriority && hasTitle && hasDescription
    });
    if (submitButton) submitButton.disabled = !(hasProduct && hasPriority && hasTitle && hasDescription);
  };
  root.querySelectorAll("[data-ticket-control]").forEach((control) => {
    control.addEventListener("input", update);
    control.addEventListener("change", update);
  });
  update();
}

function updateTicketCreateStep(step, { enabled, active, complete }) {
  if (!step) return;
  step.classList.toggle("disabled", !enabled);
  step.classList.toggle("active", enabled && active);
  step.classList.toggle("complete", complete);
  step.querySelectorAll("input, select, textarea, button").forEach((control) => {
    control.disabled = !enabled;
  });
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

function comments(ticket) {
  const list = el("div", { className: "comment-list conversation-stream" });
  const messages = conversationMessages(ticket);
  const unreadIndex = firstUnreadMessageIndex(ticket, messages);
  messages.forEach((message, index) => {
    if (index === unreadIndex) {
      list.append(unreadDivider());
    }
    const rowClasses = [
      "message-row",
      message.side === "current" ? "from-current" : "from-other",
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
  });
  return list;
}

function conversationMessages(ticket) {
  const opener = ticket.requester_name || ticket.requester || "Requester";
  const messages = [{
    author: opener,
    avatar: initials(opener),
    body: String(ticket.description || "").trim() || "No description.",
    label: `opened ${relativeTime(ticket.created_at)}`,
    visibility: "public",
    attachments: ticket.attachments || [],
    createdAt: ticket.created_at,
    side: isCurrentUserMessage(ticket, { author: opener, opening: true }) ? "current" : "other",
    className: "opening-message"
  }];
  for (const comment of ticket.comments || []) {
    const internal = comment.visibility === "internal";
    const author = comment.author || "Support";
    messages.push({
      author,
      avatar: initials(author),
      body: String(comment.body || "").trim() || "Attachment only",
      label: relativeTime(comment.created_at),
      visibility: internal ? "internal" : "public",
      attachments: comment.attachments || [],
      createdAt: comment.created_at,
      side: isCurrentUserMessage(ticket, comment) ? "current" : "other",
      className: internal ? "internal" : ""
    });
  }
  return messages;
}

function firstUnreadMessageIndex(ticket, messages) {
  if (!ticket.has_unread) return -1;
  const lastRead = timestampValue(ticket.last_read_at);
  return messages.findIndex((message) => {
    return message.side !== "current" && timestampValue(message.createdAt) > lastRead;
  });
}

function unreadDivider() {
  return el("div", {
    className: "conversation-unread-divider",
    role: "separator",
    "aria-label": "Unread messages"
  }, el("span", {}, "Unread"));
}

function timestampValue(value) {
  if (!value) return 0;
  const valueMs = Date.parse(value);
  return Number.isFinite(valueMs) ? valueMs : 0;
}

function initials(value) {
  const parts = String(value || "?").trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return "?";
  return parts.slice(0, 2).map((part) => part.slice(0, 1).toUpperCase()).join("");
}

function isCurrentUserMessage(ticket, entry) {
  const currentValues = currentUserAuthorValues();
  const author = normalizeAuthor(entry.author);
  if (author && currentValues.includes(author)) return true;
  if (!entry.opening) return false;
  const requesterValues = [
    ticket.requester,
    ticket.requester_name,
    ticket.requester_email,
    String(ticket.requester_email || "").split("@")[0]
  ].map(normalizeAuthor).filter(Boolean);
  return requesterValues.some((value) => currentValues.includes(value));
}

function currentUserAuthorValues() {
  const email = state.user?.email || "";
  return [
    state.user?.display_name,
    state.user?.username,
    email,
    String(email).split("@")[0]
  ].map(normalizeAuthor).filter(Boolean);
}

function normalizeAuthor(value) {
  return String(value || "").trim().toLowerCase();
}

function commentComposer(ticket) {
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
  if (canEditTicket(ticket)) {
    actions.unshift(commentVisibilityControl(visibility));
  }
  const entry = el("div", { className: "comment-entry" }, [
    body,
    el("div", { className: "comment-action-rail" }, actions)
  ]);
  wrap.append(entry, attachments);
  const attachmentInput = attachments.querySelector(".attachment-input");
  if (attachmentInput) {
    bindAttachmentDropZone(wrap, attachmentInput);
    bindAttachmentPasteZone(wrap, attachmentInput);
  }
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
  const previews = attachments.filter(isPreviewableImageAttachment);
  if (previews.length > 0) {
    list.append(el("div", { className: "attachment-preview-grid" }, previews.map(imageAttachmentPreview)));
  }
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

function isPreviewableImageAttachment(attachment) {
  return ["image/png", "image/jpeg", "image/gif", "image/webp"].includes(String(attachment.content_type || "").toLowerCase());
}

function imageAttachmentPreview(attachment) {
  const url = `/api/attachments/${attachment.id}`;
  const filename = attachment.filename || "Attached image";
  return el("a", {
    className: "attachment-image-link",
    href: url,
    download: filename,
    title: filename
  }, [
    el("img", {
      className: "attachment-image-preview",
      src: `${url}?preview=1`,
      alt: filename,
      loading: "lazy",
      decoding: "async"
    })
  ]);
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
  await request(`/api/products/${state.productDetailId}/members`, { method: "POST", body: JSON.stringify(input) });
  await loadProducts();
  if (state.productDetailId && canManageProduct(state.productDetailId)) await loadMembers();
}

async function deleteMember(userId) {
  await request(`/api/products/${state.productDetailId}/members/${userId}`, { method: "DELETE" });
  await loadProducts();
  if (state.productDetailId && canManageProduct(state.productDetailId)) await loadMembers();
}

async function deleteWebhook(id) {
  await request(`/api/webhooks/${id}`, { method: "DELETE" });
  await refreshWebhookLists();
}

async function refreshWebhookLists() {
  if (isAdmin()) await loadGlobalWebhooks();
  if (state.view === "product" && state.productDetailId && canManageProduct(state.productDetailId)) {
    await loadProductWebhooks();
  }
}

async function testWebhook(id) {
  const delivery = await request(`/api/webhooks/${id}/test`, { method: "POST" });
  await refreshWebhookLists();
  if (state.view === "product" && state.productDetailId && canManageProduct(state.productDetailId)) {
    await loadProductDeliveries();
  }
  openWebhookTestResult(delivery);
}

function openWebhookTestResult(delivery) {
  const ok = webhookDeliverySucceeded(delivery);
  const status = delivery.status_code ? `HTTP ${delivery.status_code}` : "No HTTP response";
  const message = ok
    ? `Receiver accepted the test delivery with ${status}.`
    : delivery.error || `Receiver rejected the test delivery with ${status}.`;
  const content = el("div", { className: "link-result" }, [
    el("p", {}, message),
    el("div", { className: "link-meta" }, [
      el("span", {}, ["Event: ", el("strong", {}, delivery.event || "webhook.test")]),
      el("span", {}, ["Status: ", el("strong", {}, status)]),
      el("span", {}, ["Duration: ", el("strong", {}, `${delivery.duration_ms || 0}ms`)])
    ])
  ]);
  els.modalHost.open({
    title: ok ? "Webhook Test Succeeded" : "Webhook Test Failed",
    content,
    hideFooter: true
  });
}

function webhookDeliverySucceeded(delivery) {
  const status = Number(delivery?.status_code || 0);
  return !delivery?.error && status >= 200 && status <= 299;
}

function selectOptions(values) {
  return values.map((value) => ({ value, label: labelize(value) }));
}

function openProductModal() {
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
      const product = await request("/api/products", { method: "POST", body: JSON.stringify(data) });
      await loadProducts();
      await openProductDetail(product.id);
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
      { name: "role", label: "Role", type: "select", options: selectOptions(state.meta.productRoles), value: "viewer" }
    ],
    onSubmit: async (data) => {
      await upsertMember({ user_id: Number(data.user_id), role: data.role });
    }
  });
}

function memberEditContent(member) {
  const role = el("select", { name: "role" });
  for (const option of selectOptions(state.meta.productRoles)) {
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
      { name: "secret", label: "Signing secret", placeholder: "Leave empty to generate one", autocomplete: "off" },
      { name: "events", label: "Events", placeholder: "ticket.created, ticket.updated, ticket.commented" },
      { name: "enabled", label: "Enabled", type: "checkbox", checked: true }
    ],
    onSubmit: async (data) => {
      const payload = { ...data, events: splitList(data.events), enabled: Boolean(data.enabled) };
      if (!String(payload.secret || "").trim()) delete payload.secret;
      let result;
      if (scope === "global") {
        result = await request("/api/webhooks", { method: "POST", body: JSON.stringify(payload) });
      } else {
        result = await request(`/api/products/${state.productDetailId}/webhooks`, { method: "POST", body: JSON.stringify(payload) });
      }
      await refreshWebhookLists();
      openWebhookSecretResult(result.webhook, result.secret, "created");
      return false;
    }
  });
}

function openWebhookEditModal(hook, scope) {
  const name = el("input", { name: "name", autocomplete: "off", required: "required", value: hook.name || "" });
  const url = el("input", { name: "url", autocomplete: "off", required: "required", value: hook.url || "" });
  const events = el("input", {
    name: "events",
    autocomplete: "off",
    placeholder: "ticket.created, ticket.updated, ticket.commented",
    value: (hook.events || []).join(", ")
  });
  const enabled = el("input", { name: "enabled", type: "checkbox" });
  enabled.checked = Boolean(hook.enabled);
  const rotate = el("button", { className: "ghost-button", type: "button" }, "Rotate Secret");
  const confirmArea = el("div", { className: "account-confirm-area" });
  const content = el("div", { className: "account-edit" }, [
    el("div", { className: "account-edit-grid" }, [
      accountField("Name", name),
      accountField("URL", url),
      accountField("Events", events),
      el("label", { className: "check" }, [enabled, "Enabled"])
    ]),
    el("section", { className: "account-manage" }, [
      el("div", { className: "account-manage-head" }, [
        el("strong", {}, "Signing Secret"),
        el("span", {}, hook.has_secret
          ? "Stored secrets are not shown again. Rotate when the receiver needs a new verification secret."
          : "This webhook has no signing secret.")
      ]),
      el("div", { className: "account-action-row" }, [rotate]),
      confirmArea
    ])
  ]);
  rotate.addEventListener("click", () => showAccountActionConfirm(confirmArea, {
    title: "Rotate webhook secret?",
    body: "Pappice will immediately sign future deliveries with the new secret. Update the receiver before relying on this webhook.",
    confirmLabel: "Rotate Secret",
    onConfirm: async () => {
      const result = await request(`/api/webhooks/${hook.id}/secret`, { method: "POST" });
      await refreshWebhookLists();
      openWebhookSecretResult(result.webhook, result.secret, "rotated");
    }
  }));
  els.modalHost.open({
    title: scope === "global" ? "Edit Global Webhook" : "Edit Product Webhook",
    submitText: "Save",
    content,
    onSubmit: async (data) => {
      await request(`/api/webhooks/${hook.id}`, {
        method: "PATCH",
        body: JSON.stringify({
          name: data.name,
          url: data.url,
          events: splitList(data.events),
          enabled: Boolean(data.enabled)
        })
      });
      await refreshWebhookLists();
    }
  });
}

function openWebhookSecretResult(hook, secret, action) {
  const secretInput = el("input", {
    readonly: "readonly",
    value: secret || ""
  });
  const copy = el("button", { type: "button" }, "Copy");
  copy.addEventListener("click", async () => {
    await copyText(secret || "");
    copy.textContent = "Copied";
    window.setTimeout(() => {
      copy.textContent = "Copy";
    }, 1200);
  });
  const content = el("div", { className: "link-result" }, [
    el("p", {}, `Webhook secret ${action}. Store it now; Pappice will not show this value again.`),
    el("div", { className: "copy-row" }, [secretInput, copy]),
    el("div", { className: "link-meta" }, [
      el("span", {}, ["Webhook: ", el("strong", {}, hook?.name || "")]),
      el("span", {}, ["Signature header: ", el("strong", {}, "X-Pappice-Signature")]),
      el("span", {}, ["Format: ", el("strong", {}, "sha256=<hmac>")])
    ])
  ]);
  els.modalHost.open({
    title: action === "rotated" ? "Webhook Secret Rotated" : "Webhook Secret Created",
    content,
    hideFooter: true
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
      router.navigate({ view: "tickets" }, { replace: true });
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
    closeTicketPopovers();
    toggleProfileMenu();
  });
  els.ticketFilterButton.addEventListener("click", (event) => {
    event.stopPropagation();
    closeProfileMenu();
    toggleTicketPopover(els.ticketFilterPopover, els.ticketFilterButton);
  });
  els.ticketSortButton.addEventListener("click", (event) => {
    event.stopPropagation();
    closeProfileMenu();
    toggleTicketPopover(els.ticketSortPopover, els.ticketSortButton);
  });
  els.ticketFilterPopover.addEventListener("click", (event) => event.stopPropagation());
  els.ticketSortPopover.addEventListener("click", (event) => event.stopPropagation());
  document.addEventListener("click", (event) => {
    if (!els.profileMenu.hidden && !els.profileMenu.contains(event.target)) closeProfileMenu();
    if (!event.target.closest?.(".toolbar-menu")) closeTicketPopovers();
  });
  document.addEventListener("keydown", handleGlobalKeydown);
  router.listen((route) => applyRoute(route).catch(showError));

  els.ticketsTab.addEventListener("click", () => switchView("tickets"));
  els.productTab.addEventListener("click", () => openProductsIndex());
  els.adminTab.addEventListener("click", () => switchView("admin"));
  for (const button of els.adminSectionButtons) {
    button.addEventListener("click", () => switchAdminSection(button.getAttribute("data-admin-section")).catch(showError));
  }
  for (const button of els.productSectionButtons) {
    button.addEventListener("click", () => switchProductSection(button.getAttribute("data-product-section")).catch(showError));
  }
  els.newTicketButton.addEventListener("click", () => openTicketCreateModal());
  els.modalHost.addEventListener("pappice-modal-error", (event) => showError(event.detail));

  els.searchInput.addEventListener("input", debounce(() => {
    state.filters.q = els.searchInput.value.trim();
    renderTicketFilterButton();
    loadTickets().catch(showError);
  }, 180));
  els.productFilter.addEventListener("change", async () => {
    state.ticketProductId = Number(els.productFilter.value) || null;
    setSelectedTicket(null);
    renderProductFilter();
    updateProductActions();
    await loadTickets();
  });
  els.assigneeFilter.addEventListener("change", () => {
    state.filters.assignee = els.assigneeFilter.value.trim();
    renderTicketFilterButton();
    loadTickets().catch(showError);
  });
  els.unreadFilter.addEventListener("change", () => {
    state.filters.unread = els.unreadFilter.checked;
    renderTicketFilterButton();
    loadTickets().catch(showError);
  });
  els.ticketSortSelect.addEventListener("change", () => setTicketSortValue(els.ticketSortSelect.value));
  els.clearTicketFiltersButton.addEventListener("click", () => {
    closeTicketPopovers();
    clearTicketFilters();
  });
  for (const button of document.querySelectorAll("[data-sort-key]")) {
    button.addEventListener("click", () => {
      const dir = button.dataset.sortKey === state.sort.key
        ? state.sort.dir
        : button.dataset.sortDefaultDir || state.sort.dir;
      setTicketSortValue(`${button.dataset.sortKey}:${dir}`);
      closeTicketPopovers();
    });
  }
  for (const button of document.querySelectorAll("[data-sort-dir]")) {
    button.addEventListener("click", () => {
      setTicketSortValue(`${state.sort.key}:${button.dataset.sortDir}`);
      closeTicketPopovers();
    });
  }
  els.emailSearchInput.addEventListener("input", runEmailSearch);
  els.emailStatusFilter.addEventListener("change", () => {
    state.emailPage.status = els.emailStatusFilter.value;
    state.emailPage.offset = 0;
    loadEmailNotifications().catch(showError);
  });
  els.auditSearchInput.addEventListener("input", runAuditSearch);
  els.addProductButton.addEventListener("click", () => openProductModal());
  els.deleteProductButton.addEventListener("click", () => {
    deleteCurrentProduct().catch(showError);
  });
  els.addUserButton.addEventListener("click", () => openUserModal());
  els.createTokenButton.addEventListener("click", () => openTokenModal());
  els.sendTestEmailButton.addEventListener("click", () => openTestEmailModal());
  els.addMemberButton.addEventListener("click", () => openMemberModal());
  els.addGlobalWebhookButton.addEventListener("click", () => openWebhookModal("global"));
  els.addWebhookButton.addEventListener("click", () => openWebhookModal("product"));
  els.appAlertClose.addEventListener("click", clearAppAlert);
}

function handleGlobalKeydown(event) {
  if (event.key !== "Escape") return;
  if (!els.profilePopover.hidden) {
    closeProfileMenu();
    return;
  }
  if (!els.ticketFilterPopover.hidden || !els.ticketSortPopover.hidden) {
    closeTicketPopovers();
    return;
  }
  if (els.modalHost?.isOpen?.()) return;
  if (state.view !== "tickets" || !state.selectedId || els.appView.hidden) return;
  event.preventDefault();
  closeSelectedTicket();
}

function closeSelectedTicket() {
  setSelectedTicket(null);
  renderTicketsView();
}

function switchView(view, options = {}) {
  if (view === "admin" && !isAdmin()) return;
  if (view === "product" && !canAccessProductsView()) return;
  state.view = view;
  els.ticketView.hidden = view !== "tickets";
  els.adminView.hidden = view !== "admin";
  els.productView.hidden = view !== "product";
  els.ticketsTab.classList.toggle("active", view === "tickets");
  els.adminTab.classList.toggle("active", view === "admin");
  els.productTab.classList.toggle("active", view === "product");
  syncTicketMobileState();
  if (options.updateRoute !== false) syncRoute({ replace: Boolean(options.replaceRoute) });
  if (view === "admin" && options.load !== false) loadAdmin().catch(showError);
  if (view === "product" && options.load !== false) loadProductAdmin().catch(showError);
}

function openProductsIndex() {
  state.productMode = "index";
  state.productDetailId = null;
  switchView("product");
}

async function deleteCurrentProduct() {
  const product = currentProductDetail();
  if (!product || !isAdmin()) return;
  const confirmed = await confirmSendAction({
    title: "Delete this product?",
    body: "This permanently removes the product, its tickets, members, webhooks, email history, and delivery history.",
    confirmLabel: "Delete Product",
    danger: true,
    details: [
      ["Product", `${product.key || `#${product.id}`} / ${product.name || product.key || "Product"}`],
      ["Tickets", "All tickets in this product will be deleted"]
    ]
  });
  if (!confirmed) return;

  if (els.deleteProductButton) els.deleteProductButton.disabled = true;
  try {
    await request(`/api/products/${product.id}`, { method: "DELETE" });
    if (state.ticketProductId === product.id) state.ticketProductId = null;
    setSelectedTicket(null, { updateRoute: false });
    state.productMode = "index";
    state.productDetailId = null;
    state.productSection = DEFAULT_PRODUCT_SECTION;
    await loadProducts();
    renderProductsView();
    syncRoute({ replace: true });
    showAppAlert("Product deleted.");
  } finally {
    if (els.deleteProductButton) els.deleteProductButton.disabled = false;
  }
}

async function openProductDetail(productId, section = DEFAULT_PRODUCT_SECTION) {
  if (!canManageProduct(productId)) return;
  state.productMode = "detail";
  state.productDetailId = productId;
  state.productSection = validProductSection(section) ? section : DEFAULT_PRODUCT_SECTION;
  switchView("product", { load: false });
  await loadProductAdmin();
}

async function refreshCurrent() {
  if (state.view === "admin") {
    await loadAdmin();
    return;
  }
  if (state.view === "product") {
    await loadProductAdmin();
    return;
  }
  await loadProducts();
}

function currentProduct(productId) {
  return state.products.find((product) => product.id === productId) || null;
}

function productDisplayName(product) {
  return product?.name || product?.key || (product?.id ? `Product ${product.id}` : "");
}

function currentProductDetail() {
  return currentProduct(state.productDetailId);
}

function selectedTicket() {
  return state.tickets.find((ticket) => ticket.id === state.selectedId) ||
    (state.selectedTicket?.id === state.selectedId ? state.selectedTicket : null);
}

function setSelectedTicket(ticket, { updateRoute = true } = {}) {
  state.selectedId = ticket?.id || null;
  state.selectedTicket = ticket || null;
  syncTicketMobileState();
  if (updateRoute) syncRoute();
}

function syncTicketMobileState() {
  const open = state.view === "tickets" && Boolean(state.selectedId);
  els.ticketView?.classList.toggle("has-selected-ticket", open);
  document.body.classList.toggle("ticket-detail-open", open);
}

function isAdmin() {
  return state.user?.role === "admin";
}

function isCustomer() {
  return state.user?.role === "customer";
}

function productRole(productId) {
  return currentProduct(productId)?.role || "";
}

function canManageProduct(productId) {
  return Boolean(productId) && !isCustomer() && (isAdmin() || productRole(productId) === "owner");
}

function manageableProducts() {
  return state.products.filter((product) => canManageProduct(product.id));
}

function canAccessProductsView() {
  return isAdmin() || manageableProducts().length > 0;
}

function canCreateTicket(productId = state.ticketProductId) {
  if (!productId) {
    return state.products.some((product) => canCreateTicket(product.id));
  }
  return isAdmin() || ["owner", "agent", "customer"].includes(productRole(productId));
}

function canCommentTicket(ticket = null) {
  return Boolean(ticket?.product_id) && canCreateTicket(ticket.product_id);
}

function canEditTicket(ticket = null) {
  const productId = ticket?.product_id || state.ticketProductId;
  return Boolean(productId) && !isCustomer() && (isAdmin() || ["owner", "agent"].includes(productRole(productId)));
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
