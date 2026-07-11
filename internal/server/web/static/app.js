import {
  defineComponents,
  el,
  formObject,
  labelize
} from "./components.js";
import {
  els,
  fullDateFormatter,
  router,
  state
} from "./state.js";
import { request } from "./api.js";
import {
  accountName,
  canAccessProductsView,
  canManageProduct,
  currentProduct,
  isAdmin,
  isCustomer
} from "./access.js";
import {
  clearAppAlert,
  showError,
  userMessage
} from "./ui.js";
import {
  initProducts,
  loadGlobalWebhooks,
  loadProductView,
  openProductModal,
  renderProductIndex
} from "./products.js";
import { initAdmin, loadAdmin, loadUsers } from "./admin.js";
import {
  applyTicketRouteSelection,
  closeSelectedTicket,
  closeTicketPopovers,
  defaultStatusFilters,
  initTickets,
  loadTickets,
  refreshTicketsSoon,
  renderAssigneeFilter,
  renderProductFilter,
  resetTickets,
  selectedTicket,
  setSelectedTicket,
  startTicketRefreshLoop,
  syncTicketMobileState,
  updateProductActions
} from "./tickets.js";

defineComponents();

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

async function boot() {
  initAdmin({ loadGlobalWebhooks, loadProducts, renderAssigneeFilter, syncRoute });
  initProducts({ loadProducts, loadUsers, setSelectedTicket, switchView, syncRoute });
  initTickets({ closeProfileMenu, loadProducts, openProductModal, switchView, syncRoute });
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
      el("strong", {}, accountName(user)),
      el("span", {}, user.email || ""),
      expiresAt ? el("span", {}, `Expires ${fullDateFormatter.format(new Date(expiresAt))}`) : el("span")
    );
  } else {
    els.accountLinkUser.hidden = true;
  }
}

async function enterApp() {
  showApp();
  if (!isCustomer()) {
    await loadUsers();
  } else {
    renderAssigneeFilter();
  }
  await loadProducts();
  await applyRoute();
  startTicketRefreshLoop();
}

function showAuth(mode) {
  resetTickets();
  state.user = null;
  state.csrf = "";
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
  const name = accountName(state.user);
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
  state.assignees = payload.assignees || [];
  if (state.ticketProductId && !state.products.some((product) => product.id === state.ticketProductId)) {
    state.ticketProductId = null;
  }
  if (state.productDetailId && !state.products.some((product) => product.id === state.productDetailId)) {
    state.productDetailId = null;
    state.productMode = "index";
  }
  renderProductFilter();
  renderAssigneeFilter();
  renderProductIndex();
  updateProductActions();
  await loadTickets();
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
  document.addEventListener("click", (event) => {
    if (!els.profileMenu.hidden && !els.profileMenu.contains(event.target)) closeProfileMenu();
  });
  document.addEventListener("keydown", handleGlobalKeydown);
  router.listen((route) => applyRoute(route).catch(showError));

  els.ticketsTab.addEventListener("click", () => switchView("tickets"));
  els.adminTab.addEventListener("click", () => switchView("admin"));
  els.modalHost.addEventListener("pappice-modal-error", (event) => showError(event.detail));
  document.addEventListener("pappice-auth-required", () => {
    showAuth("login");
    showAuthError("Your session expired. Sign in again.");
  });
  els.appAlertClose.addEventListener("click", clearAppAlert);
}

function handleGlobalKeydown(event) {
  if (event.key !== "Escape") return;
  if (hasOpenModalDialog()) return;
  if (!els.profilePopover.hidden) {
    closeProfileMenu();
    return;
  }
  if (!els.ticketFilterPopover.hidden || !els.ticketSortPopover.hidden) {
    closeTicketPopovers();
    return;
  }
  if (state.view !== "tickets" || !state.selectedId || els.appView.hidden) return;
  event.preventDefault();
  closeSelectedTicket();
}

function hasOpenModalDialog() {
  return Boolean(els.modalHost?.isOpen?.() || document.querySelector("dialog[open]"));
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
  if (view === "product" && options.load !== false) loadProductView().catch(showError);
  if (view === "tickets") refreshTicketsSoon();
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

boot().catch(showError);
