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
  emailStats: null,
  emailEnabled: false,
  emailBatchDelaySeconds: 0,
  deliveries: [],
  tokens: [],
  user: null,
  accountLink: null,
  csrf: "",
  view: "issues",
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
    webhookEvents: []
  },
  filters: {
    q: "",
    statuses: ["new", "assigned"],
    assignee: ""
  }
};

const els = {
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
  projectView: document.querySelector("#projectView"),
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
  emailOverview: document.querySelector("#emailOverview"),
  emailList: document.querySelector("#emailList"),
  auditList: document.querySelector("#auditList"),
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

function formatSeconds(seconds) {
  const value = Number(seconds || 0);
  if (!Number.isFinite(value) || value <= 0) return "-";
  if (value < 60) return `${Math.round(value)}s`;
  const minutes = Math.round(value / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.round(minutes / 60);
  return `${hours}h`;
}

async function request(path, options = {}) {
  const method = String(options.method || "GET").toUpperCase();
  const headers = {
    "Content-Type": "application/json",
    ...options.headers
  };
  if (state.csrf && ["POST", "PATCH", "PUT", "DELETE"].includes(method)) {
    headers["X-Pemmece-CSRF"] = state.csrf;
  }
  const response = await fetch(path, {
    credentials: "same-origin",
    headers,
    ...options
  });
  const text = await response.text();
  const payload = text ? JSON.parse(text) : {};
  if (!response.ok) {
    const error = new Error(payload.error || "Request failed");
    error.status = response.status;
    error.payload = payload;
    throw error;
  }
  return payload;
}

async function boot() {
  bindEvents();
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
      error: "This link is invalid or has expired. Contact an administrator for a new one."
    });
  }
}

function renderAccountLinkForm({ purpose, user = null, expiresAt = "", loading = false, error = "" }) {
  const reset = purpose === "reset";
  els.accountLinkTitle.textContent = reset ? "Reset Password" : "Set Password";
  els.accountLinkSubmit.textContent = reset ? "Reset Password" : "Set Password";
  els.accountLinkHelp.textContent = error || (loading
    ? "Checking this one-time link..."
    : reset
      ? "Enter a new password for this Pemmece account."
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
  if (!isCustomer()) {
    await loadTokens();
  }
  if (isAdmin()) {
    await loadAdmin();
  }
  if (canManageProject()) {
    await loadProjectAdmin();
  }
}

function showAuth(mode) {
  state.user = null;
  state.csrf = "";
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
  els.authView.hidden = true;
  els.appView.hidden = false;
  els.topNav.hidden = false;
  els.profileMenu.hidden = false;
  renderProfileMenu();
  switchView("issues");
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
  await Promise.all([loadUsers(), loadGlobalWebhooks(), loadEmailNotifications(), loadAuditEvents()]);
}

async function loadProjectAdmin() {
  if (!state.selectedProjectId || !canManageProject()) return;
  await Promise.all([loadUsers(), loadMembers(), loadProjectWebhooks(), loadProjectDeliveries()]);
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
  const payload = await request("/api/email-notifications");
  state.emailNotifications = payload.notifications || [];
  state.emailStats = payload.stats || null;
  state.emailEnabled = Boolean(payload.enabled);
  state.emailBatchDelaySeconds = Number(payload.batch_delay_seconds || 0);
  renderEmailNotifications();
}

async function loadAuditEvents() {
  const payload = await request("/api/audit-events");
  state.auditEvents = payload.events || [];
  renderAuditEvents();
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

function renderIssueList() {
  els.issueList.replaceChildren();
  if (state.projects.length === 0) {
    els.issueList.append(el("div", { className: "empty-state" }, "No products yet."));
    return;
  }
  if (state.issues.length === 0) {
    els.issueList.append(document.querySelector("#emptyTemplate").content.cloneNode(true));
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
    onSubmit: submittable ? async (data) => {
      if (creating) {
        const payload = ticketCreatePayload(data, projectId);
        const created = await request(`/api/projects/${payload.project_id}/tickets`, {
          method: "POST",
          body: JSON.stringify(payload)
        });
        state.selectedProjectId = payload.project_id;
        state.selectedId = created.id;
        await loadProjects();
        return;
      }
      await saveTicketChanges(issue, data, { editable, canComment });
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
  } else {
    main.append(
      el("h4", { className: "section-title" }, "Description"),
      el("div", { className: "description" }, issue.description || "No description.")
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

function renderProjectList() {
  els.projectList.replaceChildren();
  for (const project of state.projects) {
    const row = el("div", { className: "admin-row" });
    const select = el("button", { className: "ghost-button", type: "button" }, "Open");
    select.addEventListener("click", async () => {
      state.selectedProjectId = project.id;
      renderProductFilter();
      updateProjectActions();
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
    els.deliveryList.append(el("div", { className: "empty-inline" }, "No deliveries."));
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
  renderEmailOverview();
  els.sendTestEmailButton.disabled = !state.emailEnabled;
  els.emailList.replaceChildren();
  if (!state.emailEnabled && state.emailNotifications.length === 0) {
    els.emailList.append(el("div", { className: "empty-inline" }, "Email is not configured."));
    return;
  }
  if (state.emailNotifications.length === 0) {
    els.emailList.append(el("div", { className: "empty-inline" }, "No email notifications."));
    return;
  }
  for (const notification of state.emailNotifications) {
    els.emailList.append(emailNotificationRow(notification));
  }
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
  els.auditList.replaceChildren();
  if (state.auditEvents.length === 0) {
    els.auditList.append(el("div", { className: "empty-inline" }, "No audit events."));
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

async function saveTicketChanges(issue, data, { editable, canComment }) {
  const patch = editable ? ticketUpdatePatch(issue, data) : {};
  const comment = canComment ? ticketCommentPayload(issue, data) : null;
  if (Object.keys(patch).length === 0 && !comment) return;
  if (editable) {
    await request(`/api/tickets/${issue.id}`, { method: "PATCH", body: JSON.stringify({ ...patch, comment }) });
  } else if (comment) {
    await request(`/api/tickets/${issue.id}/comments`, { method: "POST", body: JSON.stringify(comment) });
  }
  await loadIssues();
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
    const hasComment = canComment && String(data.body || "").trim() !== "";
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
    item.append(
      el("div", { className: "comment-head" }, [
        el("strong", {}, comment.author),
        el("span", { className: "comment-time" }, relativeTime(comment.created_at)),
        comment.visibility === "internal" ? badge("internal", "priority-normal") : el("span")
      ]),
      el("p", {}, comment.body)
    );
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
  if (canEditTicket(issue)) {
    wrap.append(body, el("div", { className: "comment-actions" }, [visibility]));
    return wrap;
  }
  wrap.append(body);
  return wrap;
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
      ? "Pemmece queued the email. Keep this link as a fallback for manual delivery."
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

function bindEvents() {
  els.setupForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const form = new FormData(els.setupForm);
    const payload = await request("/api/setup", { method: "POST", body: JSON.stringify(formObject(form)) });
    state.csrf = payload.csrf_token || "";
    els.setupForm.reset();
    await loadSession();
  });

  els.loginForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const form = new FormData(els.loginForm);
    const payload = await request("/api/login", { method: "POST", body: JSON.stringify(formObject(form)) });
    state.csrf = payload.csrf_token || "";
    els.loginForm.reset();
    await loadSession();
  });

  els.accountLinkForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (!state.accountLink?.token) return;
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

  els.logoutButton.addEventListener("click", async () => {
    closeProfileMenu();
    await request("/api/logout", { method: "POST" });
    showAuth("login");
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

  els.issuesTab.addEventListener("click", () => switchView("issues"));
  els.projectTab.addEventListener("click", () => switchView("project"));
  els.adminTab.addEventListener("click", () => switchView("admin"));
  els.newIssueButton.addEventListener("click", () => openIssueModal());
  els.modalHost.addEventListener("pm-modal-error", (event) => showError(event.detail));
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
  els.addProjectButton.addEventListener("click", () => openProjectModal());
  els.addUserButton.addEventListener("click", () => openUserModal());
  els.createTokenButton.addEventListener("click", () => openTokenModal());
  els.sendTestEmailButton.addEventListener("click", () => openTestEmailModal());
  els.addMemberButton.addEventListener("click", () => openMemberModal());
  els.addGlobalWebhookButton.addEventListener("click", () => openWebhookModal("global"));
  els.addWebhookButton.addEventListener("click", () => openWebhookModal("project"));
}

function switchView(view) {
  if (view === "admin" && !isAdmin()) return;
  if (view === "project" && !canManageProject()) return;
  state.view = view;
  els.issueView.hidden = view !== "issues";
  els.adminView.hidden = view !== "admin";
  els.projectView.hidden = view !== "project";
  els.issuesTab.classList.toggle("active", view === "issues");
  els.adminTab.classList.toggle("active", view === "admin");
  els.projectTab.classList.toggle("active", view === "project");
  if (view === "project") loadProjectAdmin().catch(showError);
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

function showError(error) {
  console.error(error);
  if (error.status === 401) showAuth("login");
}

boot().catch(showError);
