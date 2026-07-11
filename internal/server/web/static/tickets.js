import { request } from "./api.js";
import { attachmentList, bindAttachmentDropZone, bindAttachmentPasteZone, selectedFiles, setAttachmentFiles, ticketAttachmentField } from "./attachments.js";
import { badge, debounce, el, labelize, relativeTime } from "./components.js";
import { richTextNodes } from "./rich-text.js";
import { DEFAULT_TICKET_STATUSES, TICKET_AUTOSAVE_DELAY_MS, TICKET_PAGE_SIZE, TICKET_REFRESH_INTERVAL_MS, TICKET_SORT_LABELS, els, fullDateFormatter, state } from "./state.js";
import { accountLabel, accountName, canAccessProductsView, canCommentTicket, canCreateTicket, canEditTicket, canUseAssigneeFilter, currentProduct, isAdmin, isCustomer, productDisplayName } from "./access.js";
import { confirmAction, emptyState, factBlock, selectOptions, showAppAlert, showError, sideSection } from "./ui.js";

let app = {};
let ticketLoadRequestID = 0;
let ticketDetailRequestID = 0;
let ticketRefreshTimer = 0;
let ticketRefreshInFlight = false;
const commentDrafts = new Map();

export function initTickets(options) {
  app = options;
  bindTicketEvents();
}

export function resetTickets() {
  stopTicketRefreshLoop();
  ticketLoadRequestID += 1;
  ticketDetailRequestID += 1;
  setSelectedTicket(null, { updateRoute: false });
  state.renderedTicketDetailId = null;
  commentDrafts.clear();
}

function bindTicketEvents() {
  els.ticketFilterButton.addEventListener("click", (event) => {
    event.stopPropagation();
    app.closeProfileMenu();
    toggleTicketPopover(els.ticketFilterPopover, els.ticketFilterButton);
  });
  els.ticketSortButton.addEventListener("click", (event) => {
    event.stopPropagation();
    app.closeProfileMenu();
    toggleTicketPopover(els.ticketSortPopover, els.ticketSortButton);
  });
  els.ticketFilterPopover.addEventListener("click", (event) => event.stopPropagation());
  els.ticketSortPopover.addEventListener("click", (event) => event.stopPropagation());
  els.ticketPopoverBackdrop.addEventListener("click", (event) => {
    event.preventDefault();
    event.stopPropagation();
    closeTicketPopovers();
  });
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") refreshTicketsSoon();
  });
  window.addEventListener("focus", refreshTicketsSoon);
  els.newTicketButton.addEventListener("click", openTicketCreateModal);
  document.addEventListener("pappice-attachment-limit", (event) => {
    showAppAlert(`You can attach up to ${event.detail.maxFiles} files.`);
  });

  els.searchInput.addEventListener("input", debounce(() => {
    state.filters.q = els.searchInput.value.trim();
    renderTicketFilterButton();
    loadTickets({ resetPage: true }).catch(showError);
  }, 180));
  els.productFilter.addEventListener("change", async () => {
    state.ticketProductId = Number(els.productFilter.value) || null;
    setSelectedTicket(null);
    renderProductFilter();
    renderAssigneeFilter();
    updateProductActions();
    await loadTickets({ resetPage: true });
  });
  els.assigneeFilter.addEventListener("change", () => {
    state.filters.assigneeUserId = els.assigneeFilter.value.trim();
    renderTicketFilterButton();
    loadTickets({ resetPage: true }).catch(showError);
  });
  els.unreadFilter.addEventListener("change", () => {
    state.filters.unread = els.unreadFilter.checked;
    renderTicketFilterButton();
    loadTickets({ resetPage: true }).catch(showError);
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
}

export async function applyTicketRouteSelection(key) {
  if (!key) {
    setSelectedTicket(null, { updateRoute: false });
    renderTicketsView();
    return true;
  }
  const summary = state.tickets.find((item) => item.key === key);
  const requestID = ++ticketDetailRequestID;
  try {
    const ticket = await request(summary
      ? `/api/tickets/${summary.id}`
      : `/api/tickets/key/${encodeURIComponent(key)}`);
    if (requestID !== ticketDetailRequestID) return false;
    setSelectedTicket(ticket, { updateRoute: false });
    return true;
  } catch (error) {
    if (requestID !== ticketDetailRequestID) return false;
    setSelectedTicket(null, { updateRoute: false });
    showError(error);
    return false;
  } finally {
    if (requestID === ticketDetailRequestID) renderTicketsView();
  }
}

function toggleTicketPopover(popover, button) {
  if (!popover || !button) return;
  const open = popover.hidden;
  closeTicketPopovers();
  popover.hidden = !open;
  button.setAttribute("aria-expanded", String(open));
  if (open && els.ticketPopoverBackdrop) {
    els.ticketPopoverBackdrop.hidden = false;
  }
}

export function closeTicketPopovers() {
  for (const [popover, button] of [
    [els.ticketFilterPopover, els.ticketFilterButton],
    [els.ticketSortPopover, els.ticketSortButton]
  ]) {
    if (!popover || !button) continue;
    popover.hidden = true;
    button.setAttribute("aria-expanded", "false");
  }
  if (els.ticketPopoverBackdrop) {
    els.ticketPopoverBackdrop.hidden = true;
  }
}

export async function loadTickets({ renderDetail = true, resetPage = false, offset = null } = {}) {
  const requestID = ++ticketLoadRequestID;
  if (state.products.length === 0) {
    state.tickets = [];
    state.ticketCounts = emptyTicketCounts();
    state.ticketPage = { limit: TICKET_PAGE_SIZE, offset: 0, hasMore: false };
    renderTicketsUnreadBadge(0);
    renderTicketsView();
    return { stale: false };
  }
  const params = new URLSearchParams();
  if (state.filters.q) params.set("q", state.filters.q);
  if (canUseAssigneeFilter() && state.filters.assigneeUserId) params.set("assignee_user_id", state.filters.assigneeUserId);
  if (state.filters.unread) params.set("unread", "1");
  for (const status of state.filters.statuses) params.append("status", status);
  if (usesDefaultStatusView()) params.set("include_unread_outside_status", "1");
  const productID = state.ticketProductId || null;
  if (productID) params.set("product_id", String(productID));
  params.set("sort", state.sort.key);
  params.set("direction", state.sort.dir);
  const requestedOffset = resetPage ? 0 : Math.max(0, offset ?? state.ticketPage.offset);
  params.set("limit", String(TICKET_PAGE_SIZE));
  params.set("offset", String(requestedOffset));
  const payload = await request(`/api/tickets?${params.toString()}`);
  if (requestID !== ticketLoadRequestID) return { stale: true };
  if (productID !== (state.ticketProductId || null)) return { stale: true };
  const tickets = payload.tickets || [];
  if (tickets.length === 0 && requestedOffset > 0) {
    return loadTickets({
      renderDetail,
      offset: Math.max(0, requestedOffset - TICKET_PAGE_SIZE)
    });
  }
  state.tickets = tickets;
  state.ticketCounts = { ...emptyTicketCounts(), ...(payload.counts || {}) };
  state.ticketPage = {
    limit: Number(payload.limit || TICKET_PAGE_SIZE),
    offset: Number(payload.offset ?? requestedOffset),
    hasMore: Boolean(payload.has_more)
  };
  renderTicketsUnreadBadge(Number(payload.unread_total || 0));
  const previousSelectedId = state.selectedId;
  if (state.selectedId && !state.selectedTicket) {
    setSelectedTicket(null, { updateRoute: false });
  }
  if (renderDetail || state.selectedId !== previousSelectedId) {
    renderTicketsView();
  } else {
    renderCounts();
    renderSortHeaders();
    renderTicketList();
  }
  return { stale: false };
}

export function startTicketRefreshLoop({ immediate = false } = {}) {
  stopTicketRefreshLoop();
  scheduleTicketRefresh(immediate ? 0 : TICKET_REFRESH_INTERVAL_MS);
}

export function stopTicketRefreshLoop() {
  window.clearTimeout(ticketRefreshTimer);
  ticketRefreshTimer = 0;
}

function scheduleTicketRefresh(delay = TICKET_REFRESH_INTERVAL_MS) {
  stopTicketRefreshLoop();
  if (!state.user) return;
  ticketRefreshTimer = window.setTimeout(() => {
    ticketRefreshTimer = 0;
    refreshTicketsInBackground()
      .catch(handleTicketRefreshError)
      .finally(() => scheduleTicketRefresh());
  }, delay);
}

export function refreshTicketsSoon() {
  if (canRefreshTicketsInBackground()) scheduleTicketRefresh(0);
}

function canRefreshTicketsInBackground() {
  return Boolean(state.user) &&
    state.products.length > 0 &&
    state.view === "tickets" &&
    !els.appView.hidden &&
    document.visibilityState !== "hidden";
}

async function refreshTicketsInBackground() {
  if (!canRefreshTicketsInBackground() || ticketRefreshInFlight) return;
  ticketRefreshInFlight = true;
  const selectedId = state.selectedId;
  const previousConversationRevision = ticketConversationRevision(selectedTicket());
  try {
    const refreshedList = await loadTickets({ renderDetail: false });
    if (!canRefreshTicketsInBackground() || refreshedList?.stale) return;
    if (selectedId && state.selectedId === selectedId) {
      await refreshSelectedTicket(selectedId, previousConversationRevision);
    }
  } finally {
    ticketRefreshInFlight = false;
  }
}

async function refreshSelectedTicket(ticketId, previousConversationRevision) {
  let updated;
  try {
    updated = await request(`/api/tickets/${ticketId}`);
  } catch (error) {
    if (error?.status === 404 && state.selectedId === ticketId) {
      setSelectedTicket(null, { updateRoute: false });
      await loadTickets();
      app.syncRoute({ replace: true });
      return;
    }
    throw error;
  }
  if (state.selectedId !== ticketId || state.view !== "tickets") return;
  applySelectedTicketRefresh(updated, previousConversationRevision);
}

function applySelectedTicketRefresh(updated, previousConversationRevision) {
  if (!updated?.id || state.selectedId !== updated.id || state.view !== "tickets") return;
  setSelectedTicket(updated, { updateRoute: false });
  replaceTicket(updated);
  if (ticketConversationRevision(updated) === previousConversationRevision) return;
  if (replaceTicketConversation(updated)) {
    markTicketRead(updated).catch(showError);
    return;
  }
  renderTicketDetail();
}

function replaceTicketConversation(ticket) {
  const form = els.ticketDetailPane?.querySelector(".ticket-detail-form");
  const currentConversation = form?.querySelector(".conversation-stream");
  if (!form || !currentConversation || state.renderedTicketDetailId !== ticket.id) return false;
  const followLatest = isConversationNearBottom(currentConversation);
  const previousScrollTop = currentConversation.scrollTop;
  const updatedConversation = comments(ticket);
  currentConversation.replaceWith(updatedConversation);
  if (followLatest) {
    scrollTicketConversationToBottom(form, { smooth: true });
  } else {
    updatedConversation.scrollTop = previousScrollTop;
  }
  return true;
}

function isConversationNearBottom(conversation) {
  return conversation.scrollHeight - conversation.scrollTop - conversation.clientHeight < 48;
}

function ticketConversationRevision(ticket) {
  if (!ticket) return "";
  return JSON.stringify([
    ticket.id,
    ticket.description || "",
    ticket.requester_user_id || 0,
    ticket.requester_name || "",
    ticket.requester_email || "",
    ticket.created_at || "",
    attachmentRevision(ticket.attachments),
    (ticket.comments || []).map((comment) => [
      comment.id,
      comment.author_user_id || 0,
      comment.author || "",
      comment.body || "",
      comment.visibility || "",
      comment.created_at || "",
      attachmentRevision(comment.attachments)
    ])
  ]);
}

function attachmentRevision(attachments = []) {
  return (attachments || []).map((attachment) => [
    attachment.id,
    attachment.filename || "",
    attachment.content_type || "",
    attachment.size_bytes || 0
  ]);
}

function handleTicketRefreshError(error) {
  if (error?.status === 401) {
    showError(error);
    return;
  }
  console.warn("Ticket refresh failed", error);
}

export function renderProductFilter() {
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

export function updateProductActions() {
  els.adminTab.hidden = !isAdmin();
  els.productTab.hidden = !canAccessProductsView();
  els.addProductButton.hidden = !isAdmin();
  els.newTicketButton.hidden = !canCreateTicket();
  if (state.view === "admin" && !isAdmin()) app.switchView("tickets");
  if (state.view === "product" && !canAccessProductsView()) app.switchView("tickets");
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
      loadTickets({ resetPage: true }).catch(showError);
    });
    els.statusFilterList.append(button);
  }
  renderTicketFilterButton();
}

export function defaultStatusFilters() {
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

export function renderAssigneeFilter() {
  const section = els.assigneeFilter?.closest(".popover-section");
  if (!canUseAssigneeFilter()) {
    state.filters.assigneeUserId = "";
    if (section) section.hidden = true;
    if (els.assigneeFilter) els.assigneeFilter.replaceChildren(new Option("Anyone", ""));
    renderTicketFilterButton();
    return;
  }
  if (section) section.hidden = false;
  const current = state.filters.assigneeUserId;
  const options = assigneeOptions(state.ticketProductId, current);
  options[0] = { value: "", label: "Anyone" };
  els.assigneeFilter.replaceChildren();
  for (const option of options) {
    els.assigneeFilter.append(new Option(option.label, option.value));
  }
  els.assigneeFilter.value = current;
  els.assigneeFilter.disabled = options.length <= 1 && !current;
  renderTicketFilterButton();
}

function emptyTicketCounts() {
  const counts = { all: 0 };
  for (const status of state.meta.statuses) counts[status] = 0;
  return counts;
}

function hasActiveTicketFilters() {
  return Boolean(
    state.filters.q ||
    state.ticketProductId ||
    (canUseAssigneeFilter() && state.filters.assigneeUserId) ||
    state.filters.unread ||
    state.filters.statusCustomized ||
    !sameStatuses(state.filters.statuses, defaultStatusFilters())
  );
}

function activeTicketFilterCount() {
  let count = 0;
  if (state.ticketProductId) count += 1;
  if (canUseAssigneeFilter() && state.filters.assigneeUserId) count += 1;
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
  state.filters.assigneeUserId = "";
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
  loadTickets({ resetPage: true }).catch(showError);
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
      onAction: isAdmin() ? app.openProductModal : null
    }));
    return;
  }
  const tickets = state.tickets;
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
    row.addEventListener("click", () => toggleTicketSelection(ticket).catch(showError));
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
        el("span", { className: "ticket-row-person" }, ticketPersonLabel(ticket))
      ])
    );
    els.ticketList.append(row);
  }
  if (state.ticketPage.offset > 0 || state.ticketPage.hasMore) {
    els.ticketList.append(ticketPagination());
  }
}

function ticketPagination() {
  const { limit, offset, hasMore } = state.ticketPage;
  const previous = el("button", { className: "ghost-button", type: "button" }, "Previous");
  const next = el("button", { className: "ghost-button", type: "button" }, "Next");
  previous.disabled = offset === 0;
  next.disabled = !hasMore;
  const goToPage = async (nextOffset, button) => {
    previous.disabled = true;
    next.disabled = true;
    button.setAttribute("aria-busy", "true");
    try {
      await loadTickets({ renderDetail: false, offset: nextOffset });
      els.ticketList.scrollTo({ top: 0, behavior: prefersReducedMotion() ? "auto" : "smooth" });
    } catch (error) {
      showError(error);
      renderTicketList();
    }
  };
  previous.addEventListener("click", () => goToPage(Math.max(0, offset - limit), previous));
  next.addEventListener("click", () => goToPage(offset + limit, next));
  return el("div", { className: "ticket-pagination" }, [
    previous,
    el("span", { className: "ticket-page-number" }, `Page ${Math.floor(offset / limit) + 1}`),
    next
  ]);
}

async function toggleTicketSelection(summary) {
  if (state.selectedId === summary.id) {
    closeSelectedTicket();
    return;
  }
  const requestID = ++ticketDetailRequestID;
  const ticket = await request(`/api/tickets/${summary.id}`);
  if (requestID !== ticketDetailRequestID) return;
  setSelectedTicket(ticket);
  renderTicketsView();
}

function ticketPersonLabel(ticket) {
  if (ticket.assignee_user_id) {
    const assignee = state.assignees.find((candidate) =>
      candidate.product_id === ticket.product_id && candidate.user_id === ticket.assignee_user_id
    );
    return assignee ? accountName(assignee) : ticket.assignee_email || "Unavailable assignee";
  }
  return ticket.requester_name || ticket.requester_email || "Unassigned";
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
  renderSortHeaders();
  loadTickets({ resetPage: true }).catch(showError);
}

function ticketProductParts(ticket) {
  const product = currentProduct(ticket.product_id);
  const key = ticket.product_key || product?.key || "";
  const name = ticket.product_name || productDisplayName(product) || key || "Product";
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
  if (state.selectedId === ticket.id) setSelectedTicket(updated, { updateRoute: false });
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
  await app.loadProducts();
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
    sections.push(sideSection("Workflow", workflowEditor(ticket || { assignee_user_id: 0, priority: "normal", status: "new" })));
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
  if (!canEditTicket(ticket) && !isCustomer()) facts.splice(1, 0, factBlock("Assignee", ticket.assignee_email || "Unassigned"));
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
  const confirmed = await confirmAction({
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
    clearCommentDraft(ticket);
    state.tickets = state.tickets.filter((candidate) => candidate.id !== ticket.id);
    setSelectedTicket(null, { updateRoute: false });
    await loadTickets();
    app.syncRoute({ replace: true });
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
  const files = selectedFiles(form);
  const product = currentProduct(payload.product_id);
  return confirmAction({
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

function requesterBlock(ticket) {
  if (ticket.source !== "portal") return null;
  const block = el("div", { className: "requester-block" });
  block.append(
    el("strong", {}, ticket.requester_name || "Unknown"),
    el("span", {}, ticket.requester_email || "Deleted account")
  );
  return block;
}

function workflowEditor(ticket) {
  const controls = [
    ticketSelectField("Status", "status", ticket.status, selectOptions(state.meta.statuses), { required: true })
  ];
  controls.push(
    ticketSelectField("Priority", "priority", ticket.priority || "normal", selectOptions(state.meta.priorities), { required: true }),
    ticketSelectField("Assignee", "assignee_user_id", String(ticket.assignee_user_id || ""), assigneeOptions(ticket.product_id, ticket.assignee_user_id))
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
  const files = selectedFiles(composer);
  if (!comment && files.length === 0) return false;
  const internal = String(data.visibility || "public") === "internal";
  return confirmAction({
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

function ticketCreatePayload(data, fallbackProductId) {
  return {
    description: String(data.description || "").trim(),
    priority: String(data.priority || "normal").trim() || "normal",
    product_id: Number(data.product_id || fallbackProductId),
    title: String(data.title || "").trim()
  };
}

function ticketCreateRequestBody(payload, form) {
  const files = selectedFiles(form);
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
  if (hasFormValue(data, "assignee_user_id")) {
    const assigneeUserId = Number(data.assignee_user_id || 0);
    if (assigneeUserId !== Number(ticket.assignee_user_id || 0)) patch.assignee_user_id = assigneeUserId;
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
  const controls = Array.from(form.querySelectorAll("[name='title'], [name='status'], [name='priority'], [name='assignee_user_id']"));
  const save = () => {
    saveQueue = saveQueue.then(async () => {
      const data = Object.fromEntries(new FormData(form).entries());
      const patch = ticketUpdatePatch(currentTicket, data);
      if (Object.keys(patch).length === 0) return;
      const statusChanged = hasFormValue(patch, "status");
      const assigneeChanged = hasFormValue(patch, "assignee_user_id");
      try {
        const updated = await saveTicketPatch(currentTicket, patch);
        currentTicket = updated;
        if (statusChanged && updated.status && !state.filters.statuses.includes(updated.status)) {
          state.filters.statuses = [...state.filters.statuses, updated.status];
        }
        if (assigneeChanged && state.filters.assigneeUserId && String(updated.assignee_user_id || "") !== state.filters.assigneeUserId) {
          state.filters.assigneeUserId = "";
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
  state.tickets = state.tickets.map((ticket) => {
    if (ticket.id !== updated.id) return ticket;
    const summary = { ...ticket };
    for (const key of Object.keys(summary)) {
      if (Object.hasOwn(updated, key)) summary[key] = updated[key];
    }
    return summary;
  });
  if (state.selectedId === updated.id) state.selectedTicket = updated;
}

function bindCommentComposer(form, ticket) {
  const composer = form.querySelector(".comment-form");
  if (!composer) return;
  const sendButton = composer.querySelector("[data-comment-send]");
  const body = composer.querySelector("[name='body']");
  const visibility = composer.querySelector("[name='visibility']");
  const update = () => {
    sendButton.disabled = String(body.value || "").trim() === "" && selectedFiles(composer).length === 0;
  };
  body.addEventListener("input", () => {
    saveCommentDraft(ticket, composer);
    update();
  });
  visibility?.addEventListener("change", () => saveCommentDraft(ticket, composer));
  composer.querySelectorAll("input[type='file']").forEach((input) => input.addEventListener("change", () => {
    saveCommentDraft(ticket, composer);
    update();
  }));
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
  const files = selectedFiles(composer);
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
  clearCommentDraft(ticket);
  setSelectedTicket(updated, { updateRoute: false });
  replaceTicket(updated);
  await loadTickets();
}

function commentDraftKey(ticket) {
  return ticket?.id ? String(ticket.id) : "";
}

function commentDraft(ticket) {
  const key = commentDraftKey(ticket);
  return key ? commentDrafts.get(key) || null : null;
}

function saveCommentDraft(ticket, composer) {
  const key = commentDraftKey(ticket);
  if (!key) return;
  const body = composer.querySelector("[name='body']")?.value || "";
  const visibility = composer.querySelector("[name='visibility']")?.value || "public";
  const files = selectedFiles(composer);
  if (String(body).trim() === "" && visibility === "public" && files.length === 0) {
    commentDrafts.delete(key);
    return;
  }
  commentDrafts.set(key, { body, visibility, files });
}

function clearCommentDraft(ticket) {
  const key = commentDraftKey(ticket);
  if (key) commentDrafts.delete(key);
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

function assigneeOptions(productID = null, current = 0) {
  const options = [{ value: "", label: "Unassigned" }];
  const seen = new Set([""]);
  for (const member of state.assignees) {
    if (productID && member.product_id !== productID) continue;
    const userID = String(member.user_id || "");
    if (!userID || seen.has(userID)) continue;
    seen.add(userID);
    options.push({
      value: userID,
      label: accountLabel(member)
    });
  }
  const currentID = String(current || "");
  if (currentID && !seen.has(currentID)) {
    options.push({ value: currentID, label: "Unavailable assignee" });
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
      el("p", {}, richTextNodes(message.body)),
      attachmentList(message.attachments || [])
    );
    row.append(avatar, item);
    list.append(row);
  });
  return list;
}

function conversationMessages(ticket) {
  const opener = ticket.requester_name || ticket.requester_email || "Deleted account";
  const messages = [{
    author: opener,
    avatar: initials(opener),
    body: String(ticket.description || "").trim() || "No description.",
    label: conversationTimestamp(ticket.created_at),
    visibility: "public",
    attachments: ticket.attachments || [],
    createdAt: ticket.created_at,
    side: isCurrentUserMessage(ticket, { opening: true }) ? "current" : "other",
    className: "opening-message"
  }];
  for (const comment of ticket.comments || []) {
    const internal = comment.visibility === "internal";
    const author = comment.author || "Support";
    messages.push({
      author,
      avatar: initials(author),
      body: String(comment.body || "").trim() || "Attachment only",
      label: conversationTimestamp(comment.created_at),
      visibility: internal ? "internal" : "public",
      attachments: comment.attachments || [],
      createdAt: comment.created_at,
      side: isCurrentUserMessage(ticket, comment) ? "current" : "other",
      className: internal ? "internal" : ""
    });
  }
  return messages;
}

function conversationTimestamp(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return fullDateFormatter.format(date);
}

function firstUnreadMessageIndex(ticket, messages) {
  const selectedBoundary = state.selectedId === ticket.id ? state.selectedUnreadBoundary : null;
  if (!ticket.has_unread && selectedBoundary === null) return -1;
  const lastRead = timestampValue(selectedBoundary ?? ticket.last_read_at);
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
  const userID = Number(state.user?.id || 0);
  return userID > 0 && userID === Number(entry.opening ? ticket.requester_user_id : entry.author_user_id);
}

function commentComposer(ticket) {
  const wrap = el("div", { className: "comment-form" });
  const draft = commentDraft(ticket);
  const body = document.createElement("textarea");
  body.name = "body";
  body.rows = 3;
  body.className = "comment-input";
  body.dataset.ticketControl = "true";
  body.placeholder = "Write a reply";
  body.value = draft?.body || "";
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
  visibility.value = draft?.visibility || "public";
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
    if (draft?.files?.length) setAttachmentFiles(attachmentInput, draft.files);
    bindAttachmentDropZone(wrap, attachmentInput);
    bindAttachmentPasteZone(wrap, attachmentInput);
  }
  return wrap;
}

function commentVisibilityControl(select) {
  const icon = el("span", { className: "visibility-icon", "aria-hidden": "true" });
  const control = el("label", { className: "comment-visibility-control" }, [
    icon,
    select
  ]);
  const update = () => {
    const internal = select.value === "internal";
    control.classList.toggle("visibility-public", !internal);
    control.classList.toggle("visibility-internal", internal);
    const label = internal ? "Internal note" : "Public reply";
    icon.replaceChildren(visibilityIcon(internal ? "internal" : "public"));
    control.title = label;
    select.title = label;
  };
  select.addEventListener("change", update);
  update();
  return control;
}

function visibilityIcon(type) {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("fill", "none");
  svg.setAttribute("focusable", "false");
  if (type === "internal") {
    svg.append(
      svgPath("M7 10V8a5 5 0 0 1 10 0v2", { "stroke-linecap": "round" }),
      svgPath("M6.5 10h11a1.5 1.5 0 0 1 1.5 1.5v7A1.5 1.5 0 0 1 17.5 20h-11A1.5 1.5 0 0 1 5 18.5v-7A1.5 1.5 0 0 1 6.5 10Z"),
      svgPath("M12 14v2", { "stroke-linecap": "round" })
    );
    return svg;
  }
  svg.append(
    svgPath("M5 6.8A3.8 3.8 0 0 1 8.8 3h6.4A3.8 3.8 0 0 1 19 6.8v4.4a3.8 3.8 0 0 1-3.8 3.8h-3.6L7 18v-3.2a3.8 3.8 0 0 1-2-3.4V6.8Z", { "stroke-linejoin": "round" }),
    svgPath("M8.5 8h7M8.5 11.5H13", { "stroke-linecap": "round" })
  );
  return svg;
}

function svgPath(d, attrs = {}) {
  const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
  path.setAttribute("d", d);
  path.setAttribute("stroke", "currentColor");
  path.setAttribute("stroke-width", "2");
  path.setAttribute("stroke-linejoin", "round");
  for (const [name, value] of Object.entries(attrs)) path.setAttribute(name, value);
  return path;
}

export function closeSelectedTicket() {
  ticketDetailRequestID += 1;
  setSelectedTicket(null);
  renderTicketsView();
}

export function selectedTicket() {
  return state.selectedTicket?.id === state.selectedId ? state.selectedTicket : null;
}

export function setSelectedTicket(ticket, { updateRoute = true } = {}) {
  const previousId = state.selectedId;
  state.selectedId = ticket?.id || null;
  state.selectedTicket = ticket || null;
  if (!ticket || ticket.id !== previousId) {
    state.selectedUnreadBoundary = ticket?.has_unread ? ticket.last_read_at || "" : null;
  }
  syncTicketMobileState();
  if (updateRoute) app.syncRoute();
}

export function syncTicketMobileState() {
  const open = state.view === "tickets" && Boolean(state.selectedId);
  els.ticketView?.classList.toggle("has-selected-ticket", open);
  document.body.classList.toggle("ticket-detail-open", open);
}
