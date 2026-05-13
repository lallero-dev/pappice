import {
  badge,
  debounce,
  defineComponents,
  el,
  fillOptions,
  fillSelect,
  formObject,
  labelize,
  relativeTime,
  splitList
} from "./components.js";

defineComponents();

const state = {
  issues: [],
  issueCounts: { all: 0, new: 0, assigned: 0, resolved: 0 },
  projects: [],
  users: [],
  members: [],
  webhooks: [],
  globalWebhooks: [],
  emailNotifications: [],
  deliveries: [],
  tokens: [],
  user: null,
  csrf: "",
  view: "issues",
  selectedProjectId: null,
  selectedId: null,
  meta: {
    statuses: [],
    priorities: [],
    roles: [],
    projectRoles: [],
    webhookEvents: []
  },
  filters: {
    q: "",
    status: "",
    assignee: ""
  }
};

const els = {
  connectionState: document.querySelector("#connectionState"),
  topNav: document.querySelector("#topNav"),
  issuesTab: document.querySelector("#issuesTab"),
  projectTab: document.querySelector("#projectTab"),
  adminTab: document.querySelector("#adminTab"),
  userLabel: document.querySelector("#userLabel"),
  logoutButton: document.querySelector("#logoutButton"),
  refreshButton: document.querySelector("#refreshButton"),
  newIssueButton: document.querySelector("#newIssueButton"),
  authView: document.querySelector("#authView"),
  setupForm: document.querySelector("#setupForm"),
  loginForm: document.querySelector("#loginForm"),
  appView: document.querySelector("#appView"),
  issueView: document.querySelector("#issueView"),
  adminView: document.querySelector("#adminView"),
  projectView: document.querySelector("#projectView"),
  issueList: document.querySelector("#issueList"),
  detailPane: document.querySelector("#detailPane"),
  searchInput: document.querySelector("#searchInput"),
  projectFilter: document.querySelector("#projectFilter"),
  assigneeFilter: document.querySelector("#assigneeFilter"),
  countAll: document.querySelector("#countAll"),
  countNew: document.querySelector("#countNew"),
  countAssigned: document.querySelector("#countAssigned"),
  countResolved: document.querySelector("#countResolved"),
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
  emailList: document.querySelector("#emailList"),
  deliveryList: document.querySelector("#deliveryList"),
  modalHost: document.querySelector("#modalHost")
};

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
  if (state.user?.role === "client") {
    window.location.assign("/support");
    return;
  }
  await enterApp();
}

async function enterApp() {
  showApp();
  await loadHealth();
  await loadProjects();
  await loadTokens();
  if (isAdmin()) {
    await loadAdmin();
  }
  if (canManageProject()) {
    await loadProjectAdmin();
  }
  setConnection("Ready");
}

function showAuth(mode) {
  state.user = null;
  state.csrf = "";
  els.authView.hidden = false;
  els.appView.hidden = true;
  els.topNav.hidden = true;
  els.userLabel.hidden = true;
  els.logoutButton.hidden = true;
  els.refreshButton.hidden = true;
  els.newIssueButton.hidden = true;
  els.setupForm.hidden = mode !== "setup";
  els.loginForm.hidden = mode !== "login";
  setConnection(mode === "setup" ? "Setup" : "Signed out");
}

function showApp() {
  els.authView.hidden = true;
  els.appView.hidden = false;
  els.topNav.hidden = false;
  els.userLabel.hidden = false;
  els.logoutButton.hidden = false;
  els.refreshButton.hidden = false;
  els.userLabel.textContent = `${state.user.display_name || state.user.username} / ${state.user.role}`;
  switchView("issues");
}

async function loadHealth() {
  const meta = await request("/api/health");
  state.meta.statuses = meta.statuses || [];
  state.meta.priorities = meta.priorities || [];
  state.meta.roles = meta.roles || [];
  state.meta.projectRoles = meta.project_roles || [];
  state.meta.webhookEvents = meta.webhook_events || [];
}

async function loadProjects() {
  const payload = await request("/api/projects");
  state.projects = payload.projects || [];
  if (state.selectedProjectId && !state.projects.some((project) => project.id === state.selectedProjectId)) {
    state.selectedProjectId = null;
  }
  renderProjectSelectors();
  renderProjectList();
  updateProjectActions();
  await loadIssues();
}

async function loadIssues() {
  if (state.projects.length === 0) {
    state.issues = [];
    state.issueCounts = { all: 0, new: 0, assigned: 0, resolved: 0 };
    renderIssuesView();
    return;
  }
  setConnection("Loading");
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(state.filters)) {
    if (value) params.set(key, value);
  }
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
  setConnection("Ready");
}

async function loadAdmin() {
  await Promise.all([loadUsers(), loadGlobalWebhooks(), loadEmailNotifications()]);
}

async function loadProjectAdmin() {
  if (!state.selectedProjectId || !canManageProject()) return;
  await Promise.all([loadUsers(), loadMembers(), loadProjectWebhooks(), loadProjectDeliveries()]);
}

async function loadUsers() {
  const payload = await request("/api/users");
  state.users = payload.users || [];
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
  renderEmailNotifications(payload.enabled);
}

async function loadProjectDeliveries() {
  const payload = await request(`/api/projects/${state.selectedProjectId}/webhook-deliveries`);
  state.deliveries = payload.deliveries || [];
  renderDeliveries();
}

function renderProjectSelectors() {
  const options = state.projects.map((project) => ({ value: String(project.id), label: `${project.key} / ${project.name}` }));
  fillSelect(els.projectFilter, options, state.projects.length === 0 ? "No products" : "All products");
  els.projectFilter.value = state.selectedProjectId ? String(state.selectedProjectId) : "";
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
  renderIssueList();
  renderDetail();
}

function renderCounts() {
  const counts = state.issueCounts;
  els.countAll.textContent = counts.all;
  els.countNew.textContent = counts.new;
  els.countAssigned.textContent = counts.assigned;
  els.countResolved.textContent = counts.resolved;
  document.querySelectorAll("[data-filter-status]").forEach((button) => {
    button.classList.toggle("active", button.dataset.filterStatus === state.filters.status);
  });
}

function countIssues(issues) {
  const counts = { all: issues.length, new: 0, assigned: 0, resolved: 0 };
  for (const issue of issues) {
    if (issue.status === "new") counts.new++;
    if (issue.status === "assigned") counts.assigned++;
    if (issue.status === "resolved") counts.resolved++;
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
  for (const issue of state.issues) {
    const row = document.createElement("button");
    row.type = "button";
    row.className = "issue-row";
    row.classList.toggle("active", issue.id === state.selectedId);
    row.addEventListener("click", () => {
      state.selectedId = issue.id;
      renderIssueList();
      renderDetail();
    });
    row.append(
      el("span", { className: "issue-id" }, issue.key || `#${issue.number || issue.id}`),
      issueTitle(issue),
      badge(issue.status, `status-${issue.status}`),
      badge(issue.priority, `priority-${issue.priority}`),
      el("span", {}, relativeTime(issue.updated_at))
    );
    els.issueList.append(row);
  }
}

function issueTitle(issue) {
  const wrap = el("span", { className: "issue-title" });
  wrap.append(
    el("strong", {}, issue.title),
    el("span", {}, `${issue.project_key || issue.project || "Product"}${issue.assignee ? ` / ${issue.assignee}` : ""}`)
  );
  return wrap;
}

function renderDetail() {
  els.detailPane.replaceChildren();
  const issue = selectedIssue();
  if (!issue) {
    els.detailPane.append(el("div", { className: "empty-state" }, "No ticket selected."));
    return;
  }
  const header = el("div", { className: "detail-header" });
  header.append(el("h1", {}, issue.title), detailMeta(issue));

  const controls = el("div", { className: "detail-controls" });
  controls.append(
    selectControl("Status", issue.status, state.meta.statuses, (value) => patchIssue(issue.id, { status: value })),
    selectControl("Priority", issue.priority, state.meta.priorities, (value) => patchIssue(issue.id, { priority: value })),
    textControl("Assignee", issue.assignee || "", (value) => patchIssue(issue.id, { assignee: value }))
  );
  if (!canEditIssue()) {
    controls.querySelectorAll("input, select").forEach((field) => {
      field.disabled = true;
    });
  }
  const blocks = [
    header,
    controls,
    el("div", { className: "description" }, issue.description || "No description."),
    tagRow(issue.tags || []),
    comments(issue),
    canCommentIssue() ? commentForm(issue) : el("div")
  ];
  const requester = requesterBlock(issue);
  if (requester) blocks.splice(2, 0, requester);
  els.detailPane.append(...blocks);
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
    el("strong", {}, "Requester"),
    el("span", {}, `${issue.requester_name || "Unknown"}${issue.requester_email ? ` / ${issue.requester_email}` : ""}`),
    badge(issue.source || "staff", "priority-normal")
  );
  return block;
}

function renderProjectList() {
  els.projectList.replaceChildren();
  for (const project of state.projects) {
    const row = el("div", { className: "admin-row" });
    const select = el("button", { className: "ghost-button", type: "button" }, "Open");
    select.addEventListener("click", async () => {
      state.selectedProjectId = project.id;
      renderProjectSelectors();
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
      el("span", { className: user.disabled ? "muted" : "status-ok" }, user.disabled ? "Disabled" : "Active"),
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

function renderEmailNotifications(enabled) {
  els.emailList.replaceChildren();
  if (!enabled && state.emailNotifications.length === 0) {
    els.emailList.append(el("div", { className: "empty-inline" }, "Email is not configured."));
    return;
  }
  if (state.emailNotifications.length === 0) {
    els.emailList.append(el("div", { className: "empty-inline" }, "No email notifications."));
    return;
  }
  for (const notification of state.emailNotifications) {
    const row = el("div", { className: "admin-row" });
    row.append(
      el("div", { className: "admin-row-main" }, `${notification.event} / ${notification.recipient_email}`),
      badge(notification.status, notification.status === "failed" ? "priority-urgent" : "priority-normal"),
      el("span", { className: "muted" }, notification.last_error || relativeTime(notification.created_at))
    );
    els.emailList.append(row);
  }
}

function selectControl(label, value, options, onChange) {
  const wrap = el("label", {}, label);
  const select = document.createElement("select");
  fillOptions(select, options);
  select.value = value;
  select.addEventListener("change", () => onChange(select.value).catch(showError));
  wrap.append(select);
  return wrap;
}

function textControl(label, value, onChange) {
  const wrap = el("label", {}, label);
  const input = document.createElement("input");
  input.value = value;
  input.autocomplete = "off";
  input.addEventListener("change", () => onChange(input.value).catch(showError));
  wrap.append(input);
  return wrap;
}

function tagRow(tags) {
  const row = el("div", { className: "tag-row" });
  if (tags.length === 0) {
    row.append(el("span", { className: "tag" }, "untagged"));
    return row;
  }
  for (const tag of tags) row.append(el("span", { className: "tag" }, tag));
  return row;
}

function comments(issue) {
  const list = el("div", { className: "comment-list" });
  for (const comment of issue.comments || []) {
    const item = el("div", { className: "comment" });
    item.classList.toggle("internal", comment.visibility === "internal");
    item.append(
      el("strong", {}, `${comment.author} / ${relativeTime(comment.created_at)}`),
      comment.visibility === "internal" ? badge("internal", "priority-normal") : el("span"),
      el("p", {}, comment.body)
    );
    list.append(item);
  }
  return list;
}

function commentForm(issue) {
  const form = el("form", { className: "comment-form" });
  const body = document.createElement("textarea");
  body.name = "body";
  body.rows = 3;
  body.placeholder = "comment";
  const visibility = document.createElement("select");
  visibility.name = "visibility";
  visibility.append(new Option("Public reply", "public"), new Option("Internal note", "internal"));
  if (!canEditIssue()) {
    visibility.value = "public";
    visibility.disabled = true;
  }
  const submit = el("button", { className: "ghost-button", type: "submit" }, "Add Comment");
  form.append(body, visibility, submit);
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    await addComment(issue.id, { body: body.value, visibility: visibility.value || "public" });
    form.reset();
    visibility.value = "public";
  });
  return form;
}

async function patchIssue(id, patch) {
  await request(`/api/tickets/${id}`, { method: "PATCH", body: JSON.stringify(patch) });
  await loadIssues();
}

async function addComment(id, comment) {
  await request(`/api/tickets/${id}/comments`, { method: "POST", body: JSON.stringify(comment) });
  await loadIssues();
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
  setConnection(delivery.error ? "Hook error" : "Hook sent");
  if (isAdmin()) await loadGlobalWebhooks();
  if (canManageProject()) await Promise.all([loadProjectWebhooks(), loadProjectDeliveries()]);
}

function selectOptions(values) {
  return values.map((value) => ({ value, label: labelize(value) }));
}

function openIssueModal() {
  const creatableProjects = state.projects.filter((project) => canCreateIssue(project.id));
  const projects = creatableProjects.map((project) => ({ value: String(project.id), label: `${project.key} / ${project.name}` }));
  const projectId = state.selectedProjectId && creatableProjects.some((project) => project.id === state.selectedProjectId)
    ? state.selectedProjectId
    : creatableProjects[0]?.id;
  if (!projectId) {
    showError(new Error("Create a product before adding tickets"));
    return;
  }
  els.modalHost.open({
    title: "New Ticket",
    submitText: "Create",
    values: {
      project_id: String(projectId),
      priority: "normal"
    },
    fields: [
      { name: "title", label: "Title", required: true, maxlength: 160, autocomplete: "off" },
      { name: "description", label: "Description", type: "textarea", rows: 5 },
      { group: [
        { name: "project_id", label: "Product", type: "select", options: projects, required: true },
        { name: "assignee", label: "Assignee", autocomplete: "off" }
      ] },
      { group: [
        { name: "priority", label: "Priority", type: "select", options: selectOptions(state.meta.priorities) }
      ] },
      { name: "tags", label: "Tags", autocomplete: "off", placeholder: "ui, regression" }
    ],
    onSubmit: async (data) => {
      const targetProjectId = Number(data.project_id || projectId);
      const issue = {
        title: data.title,
        description: data.description,
        project_id: targetProjectId,
        assignee: data.assignee,
        priority: data.priority,
        tags: splitList(data.tags)
      };
      const created = await request(`/api/projects/${targetProjectId}/tickets`, { method: "POST", body: JSON.stringify(issue) });
      state.selectedProjectId = targetProjectId;
      state.selectedId = created.id;
      await loadProjects();
    }
  });
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
  } : { role: "user" };
  const fields = editing ? [
    { group: [
      { name: "display_name", label: "Display name", autocomplete: "off" },
      { name: "email", label: "Email", type: "email", autocomplete: "email" }
    ] },
    { group: [
      { name: "password", label: "New password", type: "password", minlength: 8, autocomplete: "new-password" },
      { name: "role", label: "Role", type: "select", options: selectOptions(state.meta.roles), disabled: user.id === state.user.id }
    ] },
    { name: "disabled", label: "Disabled", type: "checkbox", disabled: user.id === state.user.id }
  ] : [
    { group: [
      { name: "username", label: "Username", required: true, maxlength: 48, autocomplete: "off" },
      { name: "display_name", label: "Display name", autocomplete: "off" }
    ] },
    { name: "email", label: "Email", type: "email", autocomplete: "email" },
    { group: [
      { name: "password", label: "Password", type: "password", required: true, minlength: 8 },
      { name: "role", label: "Role", type: "select", options: selectOptions(state.meta.roles), value: "user" }
    ] }
  ];

  els.modalHost.open({
    title: editing ? `Edit ${user.username}` : "New User",
    submitText: editing ? "Save" : "Create",
    values,
    fields,
    onSubmit: async (data) => {
      if (!editing) {
        await request("/api/users", { method: "POST", body: JSON.stringify(data) });
        await loadUsers();
        return;
      }
      const patch = {
        display_name: data.display_name || "",
        email: data.email || ""
      };
      if (data.password) patch.password = data.password;
      if (user.id !== state.user.id) {
        patch.role = data.role;
        patch.disabled = Boolean(data.disabled);
      }
      await updateUser(user.id, patch);
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

function openMemberModal() {
  const users = state.users
    .filter((user) => !user.disabled)
    .map((user) => ({ value: String(user.id), label: `${user.display_name || user.username} / ${user.username}` }));
  els.modalHost.open({
    title: "Add Product Member",
    submitText: "Save",
    fields: [
      { name: "user_id", label: "User", type: "select", options: users },
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

  els.logoutButton.addEventListener("click", async () => {
    await request("/api/logout", { method: "POST" });
    showAuth("login");
  });

  els.issuesTab.addEventListener("click", () => switchView("issues"));
  els.projectTab.addEventListener("click", () => switchView("project"));
  els.adminTab.addEventListener("click", () => switchView("admin"));
  els.refreshButton.addEventListener("click", () => refreshCurrent().catch(showError));
  els.newIssueButton.addEventListener("click", () => openIssueModal());
  els.modalHost.addEventListener("pm-modal-error", (event) => showError(event.detail));

  els.projectFilter.addEventListener("change", async () => {
    state.selectedProjectId = Number(els.projectFilter.value) || null;
    state.selectedId = null;
    renderProjectSelectors();
    updateProjectActions();
    await loadIssues();
    if (canManageProject()) await loadProjectAdmin();
  });

  els.searchInput.addEventListener("input", debounce(() => {
    state.filters.q = els.searchInput.value.trim();
    loadIssues().catch(showError);
  }, 180));
  els.assigneeFilter.addEventListener("input", debounce(() => {
    state.filters.assignee = els.assigneeFilter.value.trim();
    loadIssues().catch(showError);
  }, 220));
  document.querySelectorAll("[data-filter-status]").forEach((button) => {
    button.addEventListener("click", () => {
      state.filters.status = button.dataset.filterStatus;
      loadIssues().catch(showError);
    });
  });

  els.addProjectButton.addEventListener("click", () => openProjectModal());
  els.addUserButton.addEventListener("click", () => openUserModal());
  els.createTokenButton.addEventListener("click", () => openTokenModal());
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

function projectRole(projectId = state.selectedProjectId) {
  return currentProject(projectId)?.role || "";
}

function canManageProject(projectId = state.selectedProjectId) {
  return Boolean(projectId) && (isAdmin() || projectRole(projectId) === "owner");
}

function canCreateIssue(projectId = state.selectedProjectId) {
  if (!projectId) {
    return state.projects.some((project) => canCreateIssue(project.id));
  }
  return isAdmin() || ["owner", "agent", "customer"].includes(projectRole(projectId));
}

function canCommentIssue() {
  return canCreateIssue(selectedIssue()?.project_id);
}

function canEditIssue() {
  const projectId = selectedIssue()?.project_id || state.selectedProjectId;
  return Boolean(projectId) && (isAdmin() || ["owner", "agent"].includes(projectRole(projectId)));
}

function setConnection(text) {
  els.connectionState.textContent = text;
}

function showError(error) {
  setConnection("Error");
  console.error(error);
  if (error.status === 401) showAuth("login");
}

boot().catch(showError);
