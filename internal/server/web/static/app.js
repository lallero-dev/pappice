const state = {
  issues: [],
  users: [],
  webhooks: [],
  deliveries: [],
  tokens: [],
  commits: [],
  repo: null,
  user: null,
  view: "issues",
  meta: {
    statuses: [],
    severities: [],
    priorities: [],
    roles: [],
    webhookEvents: []
  },
  selectedId: null,
  filters: {
    q: "",
    status: "",
    project: "",
    assignee: ""
  }
};

const els = {
  connectionState: document.querySelector("#connectionState"),
  topNav: document.querySelector("#topNav"),
  issuesTab: document.querySelector("#issuesTab"),
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
  issueDialog: document.querySelector("#issueDialog"),
  issueForm: document.querySelector("#issueForm"),
  closeDialogButton: document.querySelector("#closeDialogButton"),
  cancelIssueButton: document.querySelector("#cancelIssueButton"),
  issueList: document.querySelector("#issueList"),
  detailPane: document.querySelector("#detailPane"),
  searchInput: document.querySelector("#searchInput"),
  statusFilter: document.querySelector("#statusFilter"),
  projectFilter: document.querySelector("#projectFilter"),
  assigneeFilter: document.querySelector("#assigneeFilter"),
  severitySelect: document.querySelector("#severitySelect"),
  prioritySelect: document.querySelector("#prioritySelect"),
  countAll: document.querySelector("#countAll"),
  countNew: document.querySelector("#countNew"),
  countAssigned: document.querySelector("#countAssigned"),
  countResolved: document.querySelector("#countResolved"),
  userForm: document.querySelector("#userForm"),
  newUserRole: document.querySelector("#newUserRole"),
  userList: document.querySelector("#userList"),
  tokenForm: document.querySelector("#tokenForm"),
  tokenResult: document.querySelector("#tokenResult"),
  tokenList: document.querySelector("#tokenList"),
  webhookForm: document.querySelector("#webhookForm"),
  webhookList: document.querySelector("#webhookList"),
  repoForm: document.querySelector("#repoForm"),
  scanRepoButton: document.querySelector("#scanRepoButton"),
  repoStatus: document.querySelector("#repoStatus"),
  commitList: document.querySelector("#commitList")
};

async function request(path, options = {}) {
  const response = await fetch(path, {
    credentials: "same-origin",
    headers: {
      "Content-Type": "application/json",
      ...options.headers
    },
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
  const session = await request("/api/me");
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

async function enterApp() {
  showApp();
  await loadHealth();
  await loadIssues();
  await loadTokens();
  if (isAdmin()) {
    await loadAdmin();
  }
  setConnection("Ready");
}

function showAuth(mode) {
  state.user = null;
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
  els.adminTab.hidden = !isAdmin();
  els.userLabel.hidden = false;
  els.logoutButton.hidden = false;
  els.refreshButton.hidden = false;
  els.newIssueButton.hidden = !canWriteIssues();
  els.userLabel.textContent = `${state.user.display_name || state.user.username} / ${state.user.role}`;
  switchView("issues");
}

async function loadHealth() {
  const meta = await request("/api/health");
  state.meta.statuses = meta.statuses || [];
  state.meta.severities = meta.severities || [];
  state.meta.priorities = meta.priorities || [];
  state.meta.roles = meta.roles || [];
  state.meta.webhookEvents = meta.webhook_events || [];
  fillOptions(els.statusFilter, state.meta.statuses, "All statuses");
  fillOptions(els.severitySelect, state.meta.severities);
  fillOptions(els.prioritySelect, state.meta.priorities);
  fillOptions(els.newUserRole, state.meta.roles);
  els.newUserRole.value = "reporter";
  els.severitySelect.value = "minor";
  els.prioritySelect.value = "normal";
}

async function loadIssues() {
  setConnection("Loading");
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(state.filters)) {
    if (value) params.set(key, value);
  }
  const payload = await request(`/api/issues?${params.toString()}`);
  state.issues = payload.issues || [];
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
  await Promise.all([loadUsers(), loadWebhooks(), loadRepo(), loadDeliveries()]);
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

async function loadWebhooks() {
  const payload = await request("/api/webhooks");
  state.webhooks = payload.webhooks || [];
  renderWebhooks();
}

async function loadDeliveries() {
  const payload = await request("/api/webhook-deliveries");
  state.deliveries = payload.deliveries || [];
}

async function loadRepo() {
  const payload = await request("/api/repo");
  state.repo = payload.repo;
  state.commits = payload.commits || [];
  renderRepo();
}

function renderIssuesView() {
  renderCounts();
  renderProjects();
  renderIssueList();
  renderDetail();
}

function renderCounts() {
  const counts = {
    all: state.issues.length,
    new: 0,
    assigned: 0,
    resolved: 0
  };
  for (const issue of state.issues) {
    if (issue.status === "new") counts.new++;
    if (issue.status === "assigned") counts.assigned++;
    if (issue.status === "resolved") counts.resolved++;
  }
  els.countAll.textContent = counts.all;
  els.countNew.textContent = counts.new;
  els.countAssigned.textContent = counts.assigned;
  els.countResolved.textContent = counts.resolved;

  document.querySelectorAll("[data-filter-status]").forEach((button) => {
    button.classList.toggle("active", button.dataset.filterStatus === state.filters.status);
  });
}

function renderProjects() {
  const existing = els.projectFilter.value;
  const projects = [...new Set(state.issues.map((issue) => issue.project).filter(Boolean))].sort();
  fillOptions(els.projectFilter, projects, "All projects");
  els.projectFilter.value = projects.includes(existing) ? existing : state.filters.project;
}

function renderIssueList() {
  els.issueList.replaceChildren();
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
      el("span", { className: "issue-id" }, `#${issue.id}`),
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
  const commitText = issue.commits?.length ? ` / ${issue.commits.length} commit${issue.commits.length === 1 ? "" : "s"}` : "";
  wrap.append(
    el("strong", {}, issue.title),
    el("span", {}, `${issue.project || "Inbox"}${issue.assignee ? ` / ${issue.assignee}` : ""}${commitText}`)
  );
  return wrap;
}

function renderDetail() {
  els.detailPane.replaceChildren();
  const issue = selectedIssue();
  if (!issue) {
    els.detailPane.append(el("div", { className: "empty-state" }, "No issue selected."));
    return;
  }

  const header = el("div", { className: "detail-header" });
  header.append(el("h1", {}, issue.title), detailMeta(issue));

  const controls = el("div", { className: "detail-controls" });
  controls.append(
    selectControl("Status", issue.status, state.meta.statuses, (value) => patchIssue(issue.id, { status: value })),
    selectControl("Priority", issue.priority, state.meta.priorities, (value) => patchIssue(issue.id, { priority: value })),
    selectControl("Severity", issue.severity, state.meta.severities, (value) => patchIssue(issue.id, { severity: value })),
    textControl("Assignee", issue.assignee || "", (value) => patchIssue(issue.id, { assignee: value }))
  );

  if (!canWriteIssues()) {
    controls.querySelectorAll("input, select").forEach((field) => {
      field.disabled = true;
    });
  }

  els.detailPane.append(
    header,
    controls,
    el("div", { className: "description" }, issue.description || "No description."),
    tagRow(issue.tags || []),
    commitBlock(issue.commits || []),
    comments(issue),
    canWriteIssues() ? commentForm(issue) : el("div")
  );
}

function detailMeta(issue) {
  const meta = el("div", { className: "detail-meta" });
  meta.append(
    badge(issue.status, `status-${issue.status}`),
    badge(issue.severity, `severity-${issue.severity}`),
    el("span", {}, `Project ${issue.project || "Inbox"}`),
    el("span", {}, `Reporter ${issue.reporter || "unknown"}`),
    el("span", {}, `Created ${relativeTime(issue.created_at)}`)
  );
  return meta;
}

function commitBlock(commits) {
  const block = el("div", { className: "commit-block" });
  block.append(el("h3", {}, "Commits"));
  if (commits.length === 0) {
    block.append(el("p", { className: "muted" }, "No linked commits."));
    return block;
  }
  for (const commit of commits) {
    const item = el("div", { className: "commit-item" });
    item.append(
      el("code", {}, commit.short_hash || commit.hash.slice(0, 8)),
      el("span", {}, commit.subject),
      el("small", {}, `${commit.author || "unknown"} / ${relativeTime(commit.date)}`)
    );
    block.append(item);
  }
  return block;
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
  for (const tag of tags) {
    row.append(el("span", { className: "tag" }, tag));
  }
  return row;
}

function comments(issue) {
  const list = el("div", { className: "comment-list" });
  for (const comment of issue.comments || []) {
    const item = el("div", { className: "comment" });
    item.append(
      el("strong", {}, `${comment.author} / ${relativeTime(comment.created_at)}`),
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
  const submit = el("button", { className: "ghost-button", type: "submit" }, "Add Comment");
  form.append(body, submit);
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    await addComment(issue.id, { body: body.value });
    form.reset();
  });
  return form;
}

function renderUsers() {
  els.userList.replaceChildren();
  for (const user of state.users) {
    const row = el("div", { className: "admin-row" });
    const role = document.createElement("select");
    fillOptions(role, state.meta.roles);
    role.value = user.role;
    role.disabled = user.id === state.user.id;
    role.addEventListener("change", () => updateUser(user.id, { role: role.value }).catch(showError));

    const disabled = document.createElement("input");
    disabled.type = "checkbox";
    disabled.checked = user.disabled;
    disabled.disabled = user.id === state.user.id;
    disabled.addEventListener("change", () => updateUser(user.id, { disabled: disabled.checked }).catch(showError));

    const remove = el("button", { className: "ghost-button", type: "button" }, "Delete");
    remove.disabled = user.id === state.user.id;
    remove.addEventListener("click", () => deleteUser(user.id).catch(showError));

    row.append(
      el("div", { className: "admin-row-main" }, `${user.display_name || user.username} / ${user.username}`),
      role,
      labelWrap("Disabled", disabled),
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

function renderWebhooks() {
  els.webhookList.replaceChildren();
  for (const hook of state.webhooks) {
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
    els.webhookList.append(row);
  }
}

function renderRepo() {
  if (!state.repo) return;
  els.repoForm.elements.path.value = state.repo.path || "";
  els.repoForm.elements.scan_limit.value = state.repo.scan_limit || 200;
  const scanned = state.repo.last_scanned_at ? `Last scan ${relativeTime(state.repo.last_scanned_at)}` : "Not scanned";
  els.repoStatus.textContent = state.repo.last_error ? `${scanned}. ${state.repo.last_error}` : scanned;

  els.commitList.replaceChildren();
  const commits = state.commits.slice(0, 30);
  if (commits.length === 0) {
    els.commitList.append(el("div", { className: "empty-inline" }, "No linked commits."));
    return;
  }
  for (const commit of commits) {
    const row = el("div", { className: "admin-row" });
    row.append(
      el("code", {}, `#${commit.issue_id} ${commit.short_hash || commit.hash.slice(0, 8)}`),
      el("div", { className: "admin-row-main" }, commit.subject),
      el("span", { className: "muted" }, commit.author || "unknown")
    );
    els.commitList.append(row);
  }
}

async function patchIssue(id, patch) {
  await request(`/api/issues/${id}`, {
    method: "PATCH",
    body: JSON.stringify(patch)
  });
  await loadIssues();
}

async function addComment(id, comment) {
  await request(`/api/issues/${id}/comments`, {
    method: "POST",
    body: JSON.stringify(comment)
  });
  await loadIssues();
}

async function updateUser(id, patch) {
  await request(`/api/users/${id}`, {
    method: "PATCH",
    body: JSON.stringify(patch)
  });
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

async function deleteWebhook(id) {
  await request(`/api/webhooks/${id}`, { method: "DELETE" });
  await loadWebhooks();
}

async function testWebhook(id) {
  const delivery = await request(`/api/webhooks/${id}/test`, { method: "POST" });
  setConnection(delivery.error ? "Hook error" : "Hook sent");
  await loadWebhooks();
}

async function scanRepo() {
  setConnection("Scanning");
  try {
    const payload = await request("/api/repo/scan", { method: "POST" });
    state.repo = payload.repo;
    state.commits = payload.commits || [];
    renderRepo();
    await loadIssues();
    setConnection("Ready");
  } catch (error) {
    if (error.payload?.repo) {
      state.repo = error.payload.repo;
      renderRepo();
    }
    throw error;
  }
}

function bindEvents() {
  els.setupForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const form = new FormData(els.setupForm);
    await request("/api/setup", {
      method: "POST",
      body: JSON.stringify(formObject(form))
    });
    els.setupForm.reset();
    await loadSession();
  });

  els.loginForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const form = new FormData(els.loginForm);
    await request("/api/login", {
      method: "POST",
      body: JSON.stringify(formObject(form))
    });
    els.loginForm.reset();
    await loadSession();
  });

  els.logoutButton.addEventListener("click", async () => {
    await request("/api/logout", { method: "POST" });
    showAuth("login");
  });

  els.issuesTab.addEventListener("click", () => switchView("issues"));
  els.adminTab.addEventListener("click", () => switchView("admin"));
  els.refreshButton.addEventListener("click", () => refreshCurrent().catch(showError));
  els.newIssueButton.addEventListener("click", () => els.issueDialog.showModal());
  els.closeDialogButton.addEventListener("click", () => els.issueDialog.close());
  els.cancelIssueButton.addEventListener("click", () => els.issueDialog.close());

  els.issueForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const form = new FormData(els.issueForm);
    const issue = {
      title: form.get("title"),
      description: form.get("description"),
      project: form.get("project"),
      assignee: form.get("assignee"),
      severity: form.get("severity"),
      priority: form.get("priority"),
      tags: splitList(form.get("tags"))
    };
    const created = await request("/api/issues", {
      method: "POST",
      body: JSON.stringify(issue)
    });
    state.selectedId = created.id;
    els.issueForm.reset();
    els.severitySelect.value = "minor";
    els.prioritySelect.value = "normal";
    els.issueDialog.close();
    await loadIssues();
  });

  els.searchInput.addEventListener("input", debounce(() => {
    state.filters.q = els.searchInput.value.trim();
    loadIssues().catch(showError);
  }, 180));

  els.statusFilter.addEventListener("change", () => {
    state.filters.status = els.statusFilter.value;
    loadIssues().catch(showError);
  });

  els.projectFilter.addEventListener("change", () => {
    state.filters.project = els.projectFilter.value;
    loadIssues().catch(showError);
  });

  els.assigneeFilter.addEventListener("input", debounce(() => {
    state.filters.assignee = els.assigneeFilter.value.trim();
    loadIssues().catch(showError);
  }, 220));

  document.querySelectorAll("[data-filter-status]").forEach((button) => {
    button.addEventListener("click", () => {
      state.filters.status = button.dataset.filterStatus;
      els.statusFilter.value = state.filters.status;
      loadIssues().catch(showError);
    });
  });

  els.userForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const form = new FormData(els.userForm);
    await request("/api/users", {
      method: "POST",
      body: JSON.stringify(formObject(form))
    });
    els.userForm.reset();
    els.newUserRole.value = "reporter";
    await loadUsers();
  });

  els.tokenForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const form = new FormData(els.tokenForm);
    const payload = await request("/api/tokens", {
      method: "POST",
      body: JSON.stringify(formObject(form))
    });
    els.tokenResult.textContent = payload.value;
    els.tokenForm.reset();
    await loadTokens();
  });

  els.webhookForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const form = new FormData(els.webhookForm);
    await request("/api/webhooks", {
      method: "POST",
      body: JSON.stringify({
        name: form.get("name"),
        url: form.get("url"),
        events: splitList(form.get("events")),
        enabled: form.get("enabled") === "on"
      })
    });
    els.webhookForm.reset();
    els.webhookForm.elements.enabled.checked = true;
    await loadWebhooks();
  });

  els.repoForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const form = new FormData(els.repoForm);
    const payload = await request("/api/repo", {
      method: "PATCH",
      body: JSON.stringify({
        path: String(form.get("path") || ""),
        scan_limit: Number(form.get("scan_limit") || 200)
      })
    });
    state.repo = payload.repo;
    renderRepo();
  });

  els.scanRepoButton.addEventListener("click", () => scanRepo().catch(showError));
}

function switchView(view) {
  if (view === "admin" && !isAdmin()) return;
  state.view = view;
  els.issueView.hidden = view !== "issues";
  els.adminView.hidden = view !== "admin";
  els.issuesTab.classList.toggle("active", view === "issues");
  els.adminTab.classList.toggle("active", view === "admin");
}

async function refreshCurrent() {
  if (state.view === "admin") {
    await loadAdmin();
    await loadTokens();
    return;
  }
  await loadIssues();
}

function selectedIssue() {
  return state.issues.find((issue) => issue.id === state.selectedId) || null;
}

function isAdmin() {
  return state.user?.role === "admin";
}

function canWriteIssues() {
  return ["admin", "developer", "reporter"].includes(state.user?.role);
}

function fillOptions(select, values, emptyLabel) {
  select.replaceChildren();
  if (emptyLabel) {
    select.append(new Option(emptyLabel, ""));
  }
  for (const value of values) {
    select.append(new Option(labelize(value), value));
  }
}

function badge(value, className) {
  return el("span", { className: `badge ${className}` }, labelize(value));
}

function labelize(value) {
  return String(value || "").replace(/_/g, " ").replace(/\b\w/g, (char) => char.toUpperCase());
}

function relativeTime(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const seconds = Math.round((Date.now() - date.getTime()) / 1000);
  if (seconds < 60) return "now";
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.round(minutes / 60);
  if (hours < 48) return `${hours}h`;
  const days = Math.round(hours / 24);
  if (days < 30) return `${days}d`;
  return date.toLocaleDateString();
}

function setConnection(text) {
  els.connectionState.textContent = text;
}

function showError(error) {
  setConnection("Error");
  console.error(error);
  if (error.status === 401) {
    showAuth("login");
  }
}

function debounce(fn, wait) {
  let timeout = 0;
  return (...args) => {
    window.clearTimeout(timeout);
    timeout = window.setTimeout(() => fn(...args), wait);
  };
}

function formObject(form) {
  return Object.fromEntries(form.entries());
}

function splitList(value) {
  return String(value || "")
    .split(/[,\s]+/)
    .map((item) => item.trim())
    .filter(Boolean);
}

function labelWrap(text, input) {
  const label = el("label", { className: "check-row" }, text);
  label.prepend(input);
  return label;
}

function el(tag, props = {}, text) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(props)) {
    if (key === "className") {
      node.className = value;
    } else if (key === "type") {
      node.type = value;
    } else {
      node.setAttribute(key, value);
    }
  }
  if (text !== undefined) {
    node.textContent = text;
  }
  return node;
}

boot().catch(showError);
